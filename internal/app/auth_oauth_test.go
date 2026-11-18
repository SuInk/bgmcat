package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

func newAuthOAuthTestApp(t *testing.T) *App {
	t.Helper()
	a, err := New(Config{
		DataDir:   t.TempDir(),
		Addr:      "127.0.0.1:0",
		PublicURL: "http://127.0.0.1:3001",
		Web:       fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("UI")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.authFailDelay = 0
	t.Cleanup(func() {
		_ = a.Close()
	})
	return a
}

func TestOAuthDynamicRedirectURIBehindTunnel(t *testing.T) {
	a := newAuthOAuthTestApp(t)

	var oidcSrv *httptest.Server
	oidcSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(Obj{
				"authorization_endpoint": oidcSrv.URL + "/authorize",
				"token_endpoint":         oidcSrv.URL + "/token",
				"userinfo_endpoint":      oidcSrv.URL + "/userinfo",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer oidcSrv.Close()

	if _, err := a.Store.Patch("config", "settings", Obj{
		"githubOAuthEnabled":  true,
		"githubClientId":      "gh-cid",
		"googleOAuthEnabled":  true,
		"googleAuthClientId":  "goog-cid",
		"googleAllowedEmails": []any{"alice@example.com"},
		"oidcEnabled":         true,
		"oidcIssuerUrl":       oidcSrv.URL,
		"oidcClientId":        "oidc-cid",
		"oidcAllowedUsers":    []any{"*"},
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		provider   string
		cookieName string
	}{
		{provider: "github", cookieName: "github_state"},
		{provider: "google", cookieName: "google_auth_state"},
		{provider: "oidc", cookieName: "oidc_auth_state"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/"+tc.provider+"/start", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		req.Host = "127.0.0.1:3001"
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Host", "mobile-cat.trycloudflare.com")
		req.Header.Set("CF-Connecting-IP", "198.51.100.8")

		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusFound {
			t.Fatalf("%s start status=%d body=%s", tc.provider, w.Code, w.Body.String())
		}
		loc := w.Header().Get("Location")
		parsedLoc, err := url.Parse(loc)
		if err != nil {
			t.Fatalf("%s invalid Location %q: %v", tc.provider, loc, err)
		}
		wantRedirect := "https://mobile-cat.trycloudflare.com/api/auth/" + tc.provider + "/callback"
		if got := parsedLoc.Query().Get("redirect_uri"); got != wantRedirect {
			t.Fatalf("%s redirect_uri=%q, want %q", tc.provider, got, wantRedirect)
		}
		c := findCookie(w.Result(), tc.cookieName)
		if c == nil || !c.Secure {
			t.Fatalf("%s state cookie=%+v, expected Secure=true", tc.provider, c)
		}
	}

	// 验证经本地 cloudflared 转发、带 CF-Connecting-IP + Origin + X-Forwarded-Host 的 POST 不会被 CORS 拦截为 403
	exReq := httptest.NewRequest(http.MethodPost, "/api/auth/oauth/exchange", strings.NewReader(`{"code":"missing"}`))
	exReq.RemoteAddr = "127.0.0.1:54321"
	exReq.Host = "127.0.0.1:3001"
	exReq.Header.Set("Content-Type", "application/json")
	exReq.Header.Set("Origin", "https://mobile-cat.trycloudflare.com")
	exReq.Header.Set("X-Forwarded-Host", "mobile-cat.trycloudflare.com")
	exReq.Header.Set("X-Forwarded-Proto", "https")
	exReq.Header.Set("CF-Connecting-IP", "198.51.100.8")
	exW := httptest.NewRecorder()
	a.Handler().ServeHTTP(exW, exReq)
	if exW.Code == http.StatusForbidden {
		t.Fatalf("expected tunneled Origin not to fail CORS 403, body=%s", exW.Body.String())
	}
}

func TestGoogleOAuthFlowAndAllowlist(t *testing.T) {
	a := newAuthOAuthTestApp(t)

	currentEmail := "alice@example.com"
	mockGoogle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if r.Form.Get("code") == "" || r.Form.Get("code_verifier") == "" {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(Obj{"access_token": "goog-access-1"})
		case "/oauth2/v2/userinfo":
			_ = json.NewEncoder(w).Encode(Obj{
				"id":             "gid-1001",
				"email":          currentEmail,
				"verified_email": true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockGoogle.Close()

	mockURL, _ := url.Parse(mockGoogle.URL)
	baseTransport := http.DefaultTransport
	a.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "oauth2.googleapis.com" || r.URL.Host == "www.googleapis.com" {
				cloned := r.Clone(r.Context())
				cloned.URL.Scheme = mockURL.Scheme
				cloned.URL.Host = mockURL.Host
				return baseTransport.RoundTrip(cloned)
			}
			return baseTransport.RoundTrip(r)
		}),
	}

	if _, err := a.Store.Patch("config", "settings", Obj{
		"googleOAuthEnabled":     true,
		"googleAuthClientId":     "g-cid",
		"googleAuthClientSecret": "g-sec",
		"googleAllowedEmails":    []any{"alice@example.com"},
	}); err != nil {
		t.Fatal(err)
	}

	// Case A: 白名单邮箱允许登录
	startW := authRequest(a, http.MethodGet, "/api/auth/google/start", nil, nil, "Mozilla/5.0 (iPhone)", "127.0.0.1:4000")
	if startW.Code != http.StatusFound {
		t.Fatalf("google start=%d body=%s", startW.Code, startW.Body.String())
	}
	stateCookie := findCookie(startW.Result(), "google_auth_state")
	locURL, _ := url.Parse(startW.Header().Get("Location"))
	state := locURL.Query().Get("state")

	cbW := authRequest(a, http.MethodGet, "/api/auth/google/callback?code=goog-code-ok&state="+url.QueryEscape(state), nil, stateCookie, "Mozilla/5.0 (iPhone)", "127.0.0.1:4000")
	if cbW.Code != http.StatusFound {
		t.Fatalf("google callback=%d body=%s", cbW.Code, cbW.Body.String())
	}
	cbLoc, _ := url.Parse(cbW.Header().Get("Location"))
	loginCode := cbLoc.Query().Get("login")
	if loginCode == "" {
		t.Fatalf("expected /?login=code redirect, got %q", cbW.Header().Get("Location"))
	}

	exW := authRequest(a, http.MethodPost, "/api/auth/oauth/exchange", Obj{"code": loginCode}, nil, "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)", "198.51.100.22:5000")
	if exW.Code != http.StatusOK {
		t.Fatalf("oauth exchange=%d body=%s", exW.Code, exW.Body.String())
	}
	exData := object(decodeEnvelope(t, exW)["data"])
	if str(exData["method"]) != "google" || str(exData["login"]) != "alice@example.com" {
		t.Fatalf("unexpected exchange data: %+v", exData)
	}
	sessCookie := findCookie(exW.Result(), authSessionCookieName)
	if sessCookie == nil || sessCookie.Value == "" {
		t.Fatal("expected bgmcat_session cookie after Google exchange")
	}

	sessW := authRequest(a, http.MethodGet, "/api/auth/sessions", nil, sessCookie, "", "198.51.100.22:5000")
	if sessW.Code != http.StatusOK {
		t.Fatalf("sessions=%d body=%s", sessW.Code, sessW.Body.String())
	}
	sessions := list(decodeEnvelope(t, sessW)["data"])
	if len(sessions) == 0 {
		t.Fatal("expected at least 1 session")
	}
	s0 := object(sessions[0])
	if str(s0["authMethod"]) != "google" || str(s0["githubLogin"]) != "alice@example.com" {
		t.Fatalf("unexpected session metadata: %+v", s0)
	}

	// Case B: 非白名单邮箱被拒绝并跳转到 /?authError=...
	currentEmail = "intruder@example.com"
	startW2 := authRequest(a, http.MethodGet, "/api/auth/google/start", nil, nil, "", "127.0.0.1:4000")
	stateCookie2 := findCookie(startW2.Result(), "google_auth_state")
	locURL2, _ := url.Parse(startW2.Header().Get("Location"))
	cbW2 := authRequest(a, http.MethodGet, "/api/auth/google/callback?code=goog-code-bad&state="+url.QueryEscape(locURL2.Query().Get("state")), nil, stateCookie2, "", "127.0.0.1:4000")
	if cbW2.Code != http.StatusFound {
		t.Fatalf("disallowed callback status=%d", cbW2.Code)
	}
	cbLoc2, _ := url.Parse(cbW2.Header().Get("Location"))
	if !strings.Contains(cbLoc2.Query().Get("authError"), "不在允许名单") {
		t.Fatalf("expected authError redirect, got %q", cbW2.Header().Get("Location"))
	}
}

func TestOIDCDiscoveryAndPKCEFlow(t *testing.T) {
	a := newAuthOAuthTestApp(t)

	var expectedChallenge string
	var idpSrv *httptest.Server
	idpSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(Obj{
				"authorization_endpoint": idpSrv.URL + "/oauth2/auth",
				"token_endpoint":         idpSrv.URL + "/oauth2/token",
				"userinfo_endpoint":      idpSrv.URL + "/oauth2/userinfo",
			})
		case "/oauth2/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			verifier := r.Form.Get("code_verifier")
			sum := sha256.Sum256([]byte(verifier))
			gotChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
			if expectedChallenge == "" || gotChallenge != expectedChallenge {
				http.Error(w, `{"error":"invalid_grant","error_description":"PKCE mismatch"}`, http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(Obj{"access_token": "oidc-access-1"})
		case "/oauth2/userinfo":
			if r.Header.Get("Authorization") != "Bearer oidc-access-1" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(Obj{
				"sub":                "sub-42",
				"preferred_username": "linuxdo_cat",
				"email":              "cat@linux.do",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer idpSrv.Close()

	if _, err := a.Store.Patch("config", "settings", Obj{
		"oidcEnabled":      true,
		"oidcDisplayName":  "Linux.do",
		"oidcIssuerUrl":    idpSrv.URL,
		"oidcClientId":     "oidc-cid",
		"oidcClientSecret": "",
		"oidcAllowedUsers": []any{"cat@linux.do"},
	}); err != nil {
		t.Fatal(err)
	}

	startW := authRequest(a, http.MethodGet, "/api/auth/oidc/start", nil, nil, "Android Chrome", "127.0.0.1:4100")
	if startW.Code != http.StatusFound {
		t.Fatalf("oidc start=%d body=%s", startW.Code, startW.Body.String())
	}
	stateCookie := findCookie(startW.Result(), "oidc_auth_state")
	locURL, _ := url.Parse(startW.Header().Get("Location"))
	expectedChallenge = locURL.Query().Get("code_challenge")
	if locURL.Query().Get("code_challenge_method") != "S256" || expectedChallenge == "" {
		t.Fatalf("missing PKCE S256 params in %q", startW.Header().Get("Location"))
	}

	cbW := authRequest(a, http.MethodGet, "/api/auth/oidc/callback?code=oidc-code-1&state="+url.QueryEscape(locURL.Query().Get("state")), nil, stateCookie, "Android Chrome", "127.0.0.1:4100")
	if cbW.Code != http.StatusFound {
		t.Fatalf("oidc callback=%d body=%s", cbW.Code, cbW.Body.String())
	}
	cbLoc, _ := url.Parse(cbW.Header().Get("Location"))
	loginCode := cbLoc.Query().Get("login")
	if loginCode == "" {
		t.Fatalf("expected login code redirect, got %q", cbW.Header().Get("Location"))
	}

	exW := authRequest(a, http.MethodPost, "/api/auth/oauth/exchange", Obj{"code": loginCode}, nil, "Android Chrome", "198.51.100.33:5000")
	if exW.Code != http.StatusOK {
		t.Fatalf("oidc exchange=%d body=%s", exW.Code, exW.Body.String())
	}
	sessCookie := findCookie(exW.Result(), authSessionCookieName)
	statusW := authRequest(a, http.MethodGet, "/api/auth/status", nil, sessCookie, "Android Chrome", "198.51.100.33:5000")
	statusData := object(decodeEnvelope(t, statusW)["data"])
	if !flag(statusData["authenticated"]) || str(statusData["authMethod"]) != "oidc" {
		t.Fatalf("expected authenticated oidc status, got %+v", statusData)
	}
}

func TestDianaFederatedAuthPasswordAndPairing(t *testing.T) {
	a := newAuthOAuthTestApp(t)

	var logoutCalls atomic.Int32
	var pairPolls atomic.Int32
	dianaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/owner/status":
			_ = json.NewEncoder(w).Encode(Obj{"available": true})
		case "/api/auth/login":
			var body Obj
			_ = json.NewDecoder(r.Body).Decode(&body)
			if str(body["username"]) == "diana_owner" && str(body["password"]) == "secret-pw" {
				http.SetCookie(w, &http.Cookie{Name: "diana_session", Value: "temp-login", Path: "/"})
				_ = json.NewEncoder(w).Encode(Obj{"ok": true})
			} else {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(Obj{"ok": false, "error": "Diana 账号或密码错误"})
			}
		case "/api/auth/logout":
			if strings.Contains(r.Header.Get("Cookie"), "diana_session=temp-") {
				logoutCalls.Add(1)
			}
			_ = json.NewEncoder(w).Encode(Obj{"ok": true})
		case "/api/auth/owner/pair":
			_ = json.NewEncoder(w).Encode(Obj{
				"code":               "654321",
				"poll_token":         "poll-xyz",
				"expires_in_seconds": 120,
			})
		case "/api/auth/owner/pair/status":
			var body Obj
			_ = json.NewDecoder(r.Body).Decode(&body)
			if str(body["poll_token"]) != "poll-xyz" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if pairPolls.Add(1) == 1 {
				_ = json.NewEncoder(w).Encode(Obj{
					"approved":           false,
					"expired":            false,
					"expires_in_seconds": 118,
				})
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "diana_session", Value: "temp-pair", Path: "/"})
			_ = json.NewEncoder(w).Encode(Obj{"approved": true})
		case "/api/auth/owner/pair/claim":
			var body Obj
			_ = json.NewDecoder(r.Body).Decode(&body)
			if str(body["code"]) != "777888" {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(Obj{"ok": false, "error": "验证码无效或已过期"})
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "diana_session", Value: "temp-claim", Path: "/"})
			_ = json.NewEncoder(w).Encode(Obj{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer dianaSrv.Close()

	if _, err := a.Store.Patch("config", "settings", Obj{
		"dianaAuthEnabled": true,
		"dianaAuthBaseUrl": dianaSrv.URL,
	}); err != nil {
		t.Fatal(err)
	}

	statusW := authRequest(a, http.MethodGet, "/api/auth/status", nil, nil, "", "198.51.100.40:1000")
	statusData := object(decodeEnvelope(t, statusW)["data"])
	if !flag(statusData["dianaAuthAvailable"]) || !flag(statusData["dianaOwnerPairAvailable"]) {
		t.Fatalf("expected dianaAuthAvailable & dianaOwnerPairAvailable, got %+v", statusData)
	}

	// 1. Diana 密码错误 -> 401
	badW := authRequest(a, http.MethodPost, "/api/auth/diana/login", Obj{"username": "diana_owner", "password": "wrong"}, nil, "", "198.51.100.40:1000")
	if badW.Code != http.StatusUnauthorized {
		t.Fatalf("bad diana password status=%d", badW.Code)
	}

	// 2. Diana 密码正确 -> 签发 bgmcat_session 且清理 Diana 临时会话
	goodW := authRequest(a, http.MethodPost, "/api/auth/diana/login", Obj{"username": "diana_owner", "password": "secret-pw"}, nil, "iPhone Safari", "198.51.100.40:1000")
	if goodW.Code != http.StatusOK || findCookie(goodW.Result(), authSessionCookieName) == nil {
		t.Fatalf("diana login failed: status=%d body=%s", goodW.Code, goodW.Body.String())
	}
	if logoutCalls.Load() != 1 {
		t.Fatalf("expected temporary diana_session logout=1, got %d", logoutCalls.Load())
	}

	// 3. Diana 6 位配对码轮询 -> 先 pending 后 approved
	pairW := authRequest(a, http.MethodPost, "/api/auth/diana/pair", Obj{}, nil, "iPhone Safari", "198.51.100.41:1000")
	pairData := object(decodeEnvelope(t, pairW)["data"])
	if str(pairData["code"]) != "654321" || str(pairData["pollToken"]) != "poll-xyz" {
		t.Fatalf("unexpected pair payload: %+v", pairData)
	}

	poll1W := authRequest(a, http.MethodPost, "/api/auth/diana/pair/status", Obj{"pollToken": "poll-xyz"}, nil, "iPhone Safari", "198.51.100.41:1000")
	poll1Data := object(decodeEnvelope(t, poll1W)["data"])
	if flag(poll1Data["approved"]) {
		t.Fatalf("expected 1st poll not approved: %+v", poll1Data)
	}

	poll2W := authRequest(a, http.MethodPost, "/api/auth/diana/pair/status", Obj{"pollToken": "poll-xyz"}, nil, "iPhone Safari", "198.51.100.41:1000")
	poll2Data := object(decodeEnvelope(t, poll2W)["data"])
	if !flag(poll2Data["approved"]) || !flag(poll2Data["authenticated"]) || findCookie(poll2W.Result(), authSessionCookieName) == nil {
		t.Fatalf("expected 2nd poll approved with cookie: %+v", poll2Data)
	}
	if logoutCalls.Load() != 2 {
		t.Fatalf("expected temporary diana_session logout=2, got %d", logoutCalls.Load())
	}

	// 4. Diana 6 位码认领登录
	claimW := authRequest(a, http.MethodPost, "/api/auth/diana/pair/claim", Obj{"code": "777888"}, nil, "iPhone Safari", "198.51.100.42:1000")
	claimData := object(decodeEnvelope(t, claimW)["data"])
	if claimW.Code != http.StatusOK || !flag(claimData["authenticated"]) || findCookie(claimW.Result(), authSessionCookieName) == nil {
		t.Fatalf("diana claim failed: status=%d body=%s", claimW.Code, claimW.Body.String())
	}
	if logoutCalls.Load() != 3 {
		t.Fatalf("expected temporary diana_session logout=3, got %d", logoutCalls.Load())
	}
}

func TestQRLoginTicketSingleUseAndAuthGuard(t *testing.T) {
	a := newAuthOAuthTestApp(t)
	boot := a.BootstrapResult()

	// 未登录不能签发扫码凭据
	unauthW := authRequest(a, http.MethodPost, "/api/auth/qr-ticket", Obj{}, nil, "", "127.0.0.1:4200")
	if unauthW.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauth qr-ticket, got %d", unauthW.Code)
	}

	// 管理员登录后签发免密扫码凭据
	loginW := authRequest(a, http.MethodPost, "/api/auth/login", Obj{
		"username": boot.Username,
		"password": boot.GeneratedPassword,
	}, nil, "macOS", "127.0.0.1:4200")
	if loginW.Code != http.StatusOK {
		t.Fatalf("admin login=%d body=%s", loginW.Code, loginW.Body.String())
	}
	adminCookie := findCookie(loginW.Result(), authSessionCookieName)

	ticketW := authRequest(a, http.MethodPost, "/api/auth/qr-ticket", Obj{}, adminCookie, "macOS", "127.0.0.1:4200")
	if ticketW.Code != http.StatusOK {
		t.Fatalf("qr-ticket=%d body=%s", ticketW.Code, ticketW.Body.String())
	}
	ticketData := object(decodeEnvelope(t, ticketW)["data"])
	code := str(ticketData["code"])
	if len(code) < 16 || str(ticketData["username"]) != boot.Username {
		t.Fatalf("unexpected qr-ticket payload: %+v", ticketData)
	}

	// 手机端兑换一次性扫码凭据并创建 qrcode 会话
	exW := authRequest(a, http.MethodPost, "/api/auth/oauth/exchange", Obj{"code": code}, nil, "Mozilla/5.0 (iPhone)", "198.51.100.77:5100")
	if exW.Code != http.StatusOK {
		t.Fatalf("qr exchange=%d body=%s", exW.Code, exW.Body.String())
	}
	exData := object(decodeEnvelope(t, exW)["data"])
	if str(exData["method"]) != "qrcode" || str(exData["login"]) != boot.Username {
		t.Fatalf("unexpected qr exchange data: %+v", exData)
	}
	phoneCookie := findCookie(exW.Result(), authSessionCookieName)
	if phoneCookie == nil || phoneCookie.Value == "" {
		t.Fatal("expected bgmcat_session cookie after qr exchange")
	}

	sessW := authRequest(a, http.MethodGet, "/api/auth/sessions", nil, phoneCookie, "Mozilla/5.0 (iPhone)", "198.51.100.77:5100")
	sessions := list(decodeEnvelope(t, sessW)["data"])
	foundQR := false
	for _, item := range sessions {
		s := object(item)
		if flag(s["current"]) && str(s["authMethod"]) == "qrcode" && str(s["githubLogin"]) == boot.Username {
			foundQR = true
		}
	}
	if !foundQR {
		t.Fatalf("expected current qrcode session in sessions list, got %+v", sessions)
	}

	// 同一扫码凭据二次兑换必须失效
	replayW := authRequest(a, http.MethodPost, "/api/auth/oauth/exchange", Obj{"code": code}, nil, "Mozilla/5.0 (iPhone)", "198.51.100.78:5100")
	if replayW.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on replayed qr-ticket, got %d body=%s", replayW.Code, replayW.Body.String())
	}
}
