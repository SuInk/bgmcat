package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func authRequest(a *App, method, path string, payload any, cookie *http.Cookie, ua, remoteAddr string) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&b).Encode(payload)
	}
	r := httptest.NewRequest(method, path, &b)
	if ua != "" {
		r.Header.Set("User-Agent", ua)
	}
	if remoteAddr != "" {
		r.RemoteAddr = remoteAddr
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}

func findCookie(res *http.Response, name string) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder) Obj {
	t.Helper()
	var env Obj
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode json (%d): %v body=%s", w.Code, err, w.Body.String())
	}
	return env
}

func TestWebUIAuthBootstrapLoginSessionsAndPasswordRotation(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Config{
		DataDir: dir,
		Web:     fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.authFailDelay = 0
	defer a.Close()

	boot := a.BootstrapResult()
	if !boot.Created || !strings.HasPrefix(boot.Username, "bgmcat#") || len(boot.GeneratedPassword) < 16 {
		t.Fatalf("unexpected bootstrap result: %+v", boot)
	}

	// 二次 Bootstrap 不得覆盖已有凭据
	second, err := a.BootstrapAdmin("admin2", "override-password")
	if err != nil || second.Created || second.Username != boot.Username || second.GeneratedPassword != "" {
		t.Fatalf("second bootstrap overwrote credentials: %+v err=%v", second, err)
	}

	// 未登录访问 /api/auth/status 不泄漏管理员用户名
	w := authRequest(a, "GET", "/api/auth/status", nil, nil, "", "198.51.100.10:12345")
	if w.Code != 200 {
		t.Fatalf("status code=%d body=%s", w.Code, w.Body.String())
	}
	statusData := object(decodeEnvelope(t, w)["data"])
	if !flag(statusData["authRequired"]) || flag(statusData["authenticated"]) || str(statusData["username"]) != "" {
		t.Fatalf("unexpected unauth status: %+v", statusData)
	}

	// 错误密码返回 401
	w = authRequest(a, "POST", "/api/auth/login", Obj{"username": boot.Username, "password": "wrong-password"}, nil, "Mozilla/5.0 (Macintosh; Intel Mac OS X)", "198.51.100.10:12345")
	if w.Code != 401 {
		t.Fatalf("expected 401 on wrong password, got %d", w.Code)
	}

	// 正确账号密码登录签发 HttpOnly Cookie
	w = authRequest(a, "POST", "/api/auth/login", Obj{"username": boot.Username, "password": boot.GeneratedPassword}, nil, "Mozilla/5.0 (Macintosh; Intel Mac OS X)", "198.51.100.10:12345")
	if w.Code != 200 {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	macCookie := findCookie(w.Result(), authSessionCookieName)
	if macCookie == nil || !macCookie.HttpOnly || macCookie.Value == "" {
		t.Fatalf("expected HttpOnly session cookie, got %+v", macCookie)
	}

	// 凭 Cookie 可直接读取受保护接口和 /api/auth/status
	w = authRequest(a, "GET", "/api/settings", nil, macCookie, "Mozilla/5.0 (Macintosh; Intel Mac OS X)", "198.51.100.10:12345")
	if w.Code != 200 {
		t.Fatalf("cookie auth failed on /api/settings: %d %s", w.Code, w.Body.String())
	}
	w = authRequest(a, "GET", "/api/auth/status", nil, macCookie, "Mozilla/5.0 (Macintosh; Intel Mac OS X)", "198.51.100.10:12345")
	statusData = object(decodeEnvelope(t, w)["data"])
	if !flag(statusData["authenticated"]) || str(statusData["username"]) != boot.Username {
		t.Fatalf("expected authenticated status with username, got %+v", statusData)
	}

	// 另一台设备登录，检查会话列表与踢其他设备
	w2 := authRequest(a, "POST", "/api/auth/login", Obj{"username": boot.Username, "password": boot.GeneratedPassword}, nil, "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0)", "198.51.100.22:54321")
	if w2.Code != 200 {
		t.Fatalf("second device login failed: %d", w2.Code)
	}
	iphoneCookie := findCookie(w2.Result(), authSessionCookieName)

	w = authRequest(a, "GET", "/api/auth/sessions", nil, macCookie, "", "198.51.100.10:12345")
	sessions := list(decodeEnvelope(t, w)["data"])
	if len(sessions) != 2 || !flag(object(sessions[0])["current"]) || str(object(sessions[0])["deviceName"]) != "macOS 浏览器" {
		t.Fatalf("unexpected sessions list: %+v", sessions)
	}

	// 修改账号与密码：未登录请求被拒绝 -> 已登录状态下无需旧密码即可更新为 admin + 新密码 -> 旧设备立即失效，当前设备获得新 Cookie
	w = authRequest(a, "POST", "/api/auth/password", Obj{
		"newUsername": "admin",
		"newPassword": "new-strong-pass-123",
	}, nil, "", "198.51.100.10:12345")
	if w.Code != 401 {
		t.Fatalf("expected 401 on unauthenticated password change, got %d", w.Code)
	}

	w = authRequest(a, "POST", "/api/auth/password", Obj{
		"newUsername": "admin",
		"newPassword": "new-strong-pass-123",
	}, macCookie, "Mozilla/5.0 (Macintosh; Intel Mac OS X)", "198.51.100.10:12345")
	if w.Code != 200 {
		t.Fatalf("password change failed: %d %s", w.Code, w.Body.String())
	}
	rotatedCookie := findCookie(w.Result(), authSessionCookieName)
	if rotatedCookie == nil || rotatedCookie.Value == macCookie.Value {
		t.Fatalf("expected rotated session cookie, got %+v", rotatedCookie)
	}
	if authRequest(a, "POST", "/api/auth/login", Obj{"username": "admin", "password": "new-strong-pass-123"}, nil, "Mozilla/5.0", "198.51.100.10:12345").Code != 200 {
		t.Fatal("login with newly updated password should succeed")
	}

	// 旧 Cookie 与另一台设备的 Cookie 均立即失效
	if authRequest(a, "GET", "/api/settings", nil, macCookie, "", "198.51.100.10:12345").Code != 401 {
		t.Fatal("old cookie should be invalidated after password change")
	}
	if authRequest(a, "GET", "/api/settings", nil, iphoneCookie, "", "198.51.100.22:54321").Code != 401 {
		t.Fatal("other device cookie should be invalidated after password change")
	}
	if authRequest(a, "GET", "/api/settings", nil, rotatedCookie, "", "198.51.100.10:12345").Code != 200 {
		t.Fatal("rotated cookie should remain valid")
	}

	// 退出登录清除当前会话与 Cookie
	w = authRequest(a, "POST", "/api/auth/logout", Obj{}, rotatedCookie, "", "198.51.100.10:12345")
	if w.Code != 200 {
		t.Fatalf("logout failed: %d", w.Code)
	}
	if authRequest(a, "GET", "/api/settings", nil, rotatedCookie, "", "198.51.100.10:12345").Code != 401 {
		t.Fatal("logged out cookie should be rejected")
	}
}

func TestAuthThrottleAndPasswordReset(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Config{
		DataDir:       dir,
		AdminUsername: "diana",
		AdminPassword: "correct-password-888",
		Web:           fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.authFailDelay = 0
	nowTime := time.Now()
	a.throttle.now = func() time.Time { return nowTime }

	// 前 5 次输错返回 401，第 6 次输错进入 30 秒指数退避锁定期，第 7 次请求直接 429 + Retry-After
	for i := 1; i <= 6; i++ {
		w := authRequest(a, "POST", "/api/auth/login", Obj{"username": "diana", "password": "wrong"}, nil, "", "203.0.113.9:4000")
		if w.Code != 401 {
			t.Fatalf("attempt %d: want 401, got %d", i, w.Code)
		}
	}
	blocked := authRequest(a, "POST", "/api/auth/login", Obj{"username": "diana", "password": "correct-password-888"}, nil, "", "203.0.113.9:4000")
	if blocked.Code != 429 {
		t.Fatalf("expected 429 during lockout, got %d body=%s", blocked.Code, blocked.Body.String())
	}
	retrySecs, _ := strconv.Atoi(blocked.Header().Get("Retry-After"))
	if retrySecs < 25 || retrySecs > 30 {
		t.Fatalf("unexpected Retry-After=%q", blocked.Header().Get("Retry-After"))
	}

	// 时间推进 31 秒后锁定期结束，正确密码可正常登录
	nowTime = nowTime.Add(31 * time.Second)
	okResp := authRequest(a, "POST", "/api/auth/login", Obj{"username": "diana", "password": "correct-password-888"}, nil, "", "203.0.113.9:4000")
	if okResp.Code != 200 {
		t.Fatalf("expected login success after lockout expired, got %d", okResp.Code)
	}
	sessionCookie := findCookie(okResp.Result(), authSessionCookieName)
	a.Close()

	// CLI ResetDataDirPassword 保留已有用户名、生成新随机密码并清空所有旧会话
	resetRes, err := ResetDataDirPassword(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if resetRes.Username != "diana" || len(resetRes.GeneratedPassword) < 16 {
		t.Fatalf("unexpected reset result: %+v", resetRes)
	}

	restarted, err := New(Config{
		DataDir: dir,
		Web:     fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted.authFailDelay = 0
	defer restarted.Close()

	if authRequest(restarted, "GET", "/api/settings", nil, sessionCookie, "", "203.0.113.9:4000").Code != 401 {
		t.Fatal("reset-password must revoke all prior sessions")
	}
	if authRequest(restarted, "POST", "/api/auth/login", Obj{"username": "diana", "password": resetRes.GeneratedPassword}, nil, "", "203.0.113.9:4000").Code != 200 {
		t.Fatal("expected login with newly reset password to succeed")
	}
}
