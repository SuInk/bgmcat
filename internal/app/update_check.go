package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const updateRepository = "SuInk/bgmcat"
const manualUpdateReason = "当前版本尚不支持应用内自动安装。请根据部署方式使用完整安装包、安装脚本或重新构建升级。"

func (a *App) updateState() Obj {
	s, _ := a.Store.Get("updates", "status")
	if s == nil {
		s = Obj{"kind": "unsupported", "latestVersion": "", "latestPublishedAt": nil, "checkedAt": nil, "lastError": "", "updateAvailable": false}
	}
	s["supported"] = false
	s["unsupportedReason"] = manualUpdateReason
	s["currentVersion"], s["buildType"], s["platform"], s["arch"] = Version, "source", runtime.GOOS, runtime.GOARCH
	s["channel"] = a.updateChannel()
	s["busy"], s["source"], s["downloadedVersion"] = false, nil, ""
	s["progress"] = Obj{"phase": "idle", "done": 0, "total": 0, "percent": 0}
	s["failureCount"], s["circuitBroken"] = 0, false
	// A previously checked version can differ after a manual upgrade.
	if str(s["latestVersion"]) != "" {
		s["updateAvailable"] = newerRelease(str(s["latestVersion"]), Version)
		if str(s["kind"]) != "error" {
			if flag(s["updateAvailable"]) {
				s["kind"] = "available"
			} else {
				s["kind"] = "up_to_date"
			}
		}
	}
	return s
}

// releaseVersion accepts stable tags (v0.2.1) and prereleases such as
// v0.2.2-canary.3 or v0.3.0-beta.1.
var releaseVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$`)

// Update channels: stable only sees normal releases; canary also sees every
// prerelease (canary builds published from main, plus manual beta/rc tags).
const (
	updateChannelStable = "stable"
	updateChannelCanary = "canary"
)

func normalizeUpdateChannel(v any) string {
	if str(v) == updateChannelCanary {
		return updateChannelCanary
	}
	return updateChannelStable
}

func (a *App) updateChannel() string { return normalizeUpdateChannel(a.Settings()["updateChannel"]) }

// buildChannel reports which channel the running binary was published on.
func buildChannel(version string) string {
	if m := releaseVersion.FindStringSubmatch(version); m != nil && m[4] != "" {
		if strings.HasPrefix(m[4], "canary.") {
			return updateChannelCanary
		}
		return "beta"
	}
	return updateChannelStable
}

// compareReleases orders versions by semantic-version precedence: the core
// number first, then a prerelease sorts before its release, and prerelease
// identifiers compare numerically when both are numbers. ok is false when
// either side is not a release version.
func compareReleases(a, b string) (cmp int, ok bool) {
	x, y := releaseVersion.FindStringSubmatch(a), releaseVersion.FindStringSubmatch(b)
	if x == nil || y == nil {
		return 0, false
	}
	for i := 1; i <= 3; i++ {
		p, e1 := strconv.ParseUint(x[i], 10, 64)
		q, e2 := strconv.ParseUint(y[i], 10, 64)
		if e1 != nil || e2 != nil {
			return 0, false
		}
		if p != q {
			if p > q {
				return 1, true
			}
			return -1, true
		}
	}
	switch {
	case x[4] == y[4]:
		return 0, true
	case x[4] == "":
		return 1, true
	case y[4] == "":
		return -1, true
	}
	l, r := strings.Split(x[4], "."), strings.Split(y[4], ".")
	for i := 0; i < len(l) && i < len(r); i++ {
		if l[i] == r[i] {
			continue
		}
		p, e1 := strconv.ParseUint(l[i], 10, 64)
		q, e2 := strconv.ParseUint(r[i], 10, 64)
		switch {
		case e1 == nil && e2 == nil:
			if p > q {
				return 1, true
			}
			return -1, true
		case e1 == nil: // numeric identifiers sort before alphanumeric ones
			return -1, true
		case e2 == nil:
			return 1, true
		case l[i] > r[i]:
			return 1, true
		default:
			return -1, true
		}
	}
	switch {
	case len(l) > len(r):
		return 1, true
	case len(l) < len(r):
		return -1, true
	}
	return 0, true
}

func newerRelease(candidate, current string) bool {
	c, ok := compareReleases(candidate, current)
	return ok && c > 0
}

func (a *App) checkRelease(ctx context.Context) Obj {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	state := a.updateState()
	state["checkedAt"] = time.Now().UTC().Format(time.RFC3339)
	fail := func(message string) Obj {
		state["kind"], state["lastError"] = "error", message
		_ = a.Store.Put("updates", "status", state)
		return state
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+updateRepository+"/releases?per_page=30", nil)
	if err != nil {
		return fail("无法创建版本检查请求")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "BangumiCat/"+Version)
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := *a.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		return fail("无法连接 GitHub，请检查服务端网络后重试。")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 404 {
		return fail(fmt.Sprintf("GitHub 返回 HTTP %d（仓库暂未发布公开 Release，或触发了 GitHub 访问频率限制）。", res.StatusCode))
	}
	if res.StatusCode != 200 {
		return fail(fmt.Sprintf("检查版本失败：GitHub HTTP %d", res.StatusCode))
	}
	var entries []Obj
	if json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&entries) != nil {
		return fail("GitHub 版本数据无效，请稍后重试。")
	}
	channel := a.updateChannel()
	history := []Obj{}
	latest := Obj{}
	for _, entry := range entries {
		if flag(entry["draft"]) {
			continue
		}
		tag := str(entry["tag_name"])
		history = append(history, Obj{"tag": tag, "notes": entry["body"], "publishedAt": entry["published_at"], "prerelease": flag(entry["prerelease"])})
		eligible := !flag(entry["prerelease"]) || channel == updateChannelCanary
		if eligible && releaseVersion.MatchString(tag) && (str(latest["tag_name"]) == "" || newerRelease(tag, str(latest["tag_name"]))) {
			latest = entry
		}
	}
	// If recent history contains only prereleases, the stable release can be older
	// than this page. GitHub's latest endpoint excludes drafts and prereleases.
	if len(entries) >= 30 && str(latest["tag_name"]) == "" {
		latestReq := req.Clone(ctx)
		latestReq.URL.Path = "/repos/" + updateRepository + "/releases/latest"
		latestReq.URL.RawQuery = ""
		latestRes, e := client.Do(latestReq)
		if e != nil {
			return fail("无法读取稳定版信息，请检查网络后重试。")
		}
		defer latestRes.Body.Close()
		if latestRes.StatusCode == 200 {
			if json.NewDecoder(io.LimitReader(latestRes.Body, 4<<20)).Decode(&latest) != nil || buildChannel(str(latest["tag_name"])) != updateChannelStable {
				return fail("稳定版信息无效，请查看发布页面。")
			}
		} else if latestRes.StatusCode != 404 {
			return fail(fmt.Sprintf("读取稳定版失败：GitHub HTTP %d", latestRes.StatusCode))
		}
	}
	state["latestVersion"], state["latestPublishedAt"], state["lastError"] = str(latest["tag_name"]), latest["published_at"], ""
	state["updateAvailable"] = newerRelease(str(latest["tag_name"]), Version)
	state["kind"] = "up_to_date"
	if latest["tag_name"] == nil {
		state["kind"] = "no_release"
	} else if flag(state["updateAvailable"]) {
		state["kind"] = "available"
	}
	_ = a.Store.Put("updates", "history", Obj{"items": history})
	if err := a.Store.Put("updates", "status", state); err != nil {
		return fail("版本检查完成，但本地状态保存失败。")
	}
	return state
}
