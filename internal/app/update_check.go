package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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

var releaseVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

func newerRelease(candidate, current string) bool {
	a, b := releaseVersion.FindStringSubmatch(candidate), releaseVersion.FindStringSubmatch(current)
	if a == nil || b == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, e1 := strconv.ParseUint(a[i], 10, 64)
		y, e2 := strconv.ParseUint(b[i], 10, 64)
		if e1 != nil || e2 != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return false
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
	req.Header.Set("User-Agent", userAgent)
	token := strings.TrimSpace(os.Getenv("GH_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if token == "" {
		authCtx, authCancel := context.WithTimeout(ctx, 2*time.Second)
		output, e := exec.CommandContext(authCtx, "gh", "auth", "token").Output()
		authCancel()
		if e == nil {
			token = strings.TrimSpace(string(output))
		}
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
		return fail(fmt.Sprintf("GitHub 返回 HTTP %d。私有仓库需要服务端 gh 登录或 GH_TOKEN 的仓库读取权限；也可能触发访问限额。", res.StatusCode))
	}
	if res.StatusCode != 200 {
		return fail(fmt.Sprintf("检查版本失败：GitHub HTTP %d", res.StatusCode))
	}
	var entries []Obj
	if json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&entries) != nil {
		return fail("GitHub 版本数据无效，请稍后重试。")
	}
	history := []Obj{}
	latest := Obj{}
	for _, entry := range entries {
		if flag(entry["draft"]) {
			continue
		}
		tag := str(entry["tag_name"])
		history = append(history, Obj{"tag": tag, "notes": entry["body"], "publishedAt": entry["published_at"], "prerelease": flag(entry["prerelease"])})
		if !flag(entry["prerelease"]) && releaseVersion.MatchString(tag) && (str(latest["tag_name"]) == "" || newerRelease(tag, str(latest["tag_name"]))) {
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
			if json.NewDecoder(io.LimitReader(latestRes.Body, 4<<20)).Decode(&latest) != nil || !releaseVersion.MatchString(str(latest["tag_name"])) {
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
