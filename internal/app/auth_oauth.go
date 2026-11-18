package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type cachedOIDCDiscovery struct {
	authEndpoint     string
	tokenEndpoint    string
	userinfoEndpoint string
	expiresAt        time.Time
}

var (
	oidcDiscoveryMu    sync.Mutex
	oidcDiscoveryCache = map[string]cachedOIDCDiscovery{}

	dianaPairProbeMu    sync.Mutex
	dianaPairProbeCache = struct {
		url       string
		available bool
		checkedAt time.Time
	}{}
)

func redirectAuthError(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/?authError="+url.QueryEscape(message), http.StatusFound)
}

func (a *App) effectiveGoogleAuthCredentials() (clientID, clientSecret string) {
	s := a.Settings()
	cid := strings.TrimSpace(str(s["googleAuthClientId"]))
	csec := strings.TrimSpace(str(s["googleAuthClientSecret"]))
	if cid != "" {
		return cid, csec
	}
	driveID, driveSec, _ := a.effectiveGoogleCredentials()
	return driveID, driveSec
}

func normalizeLockURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if !httpURL(raw) {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "0.0.0.0" || host == "::" {
		port := u.Port()
		if port != "" {
			u.Host = "127.0.0.1:" + port
		} else {
			u.Host = "127.0.0.1"
		}
	}
	return strings.TrimRight(u.String(), "/")
}

func (a *App) effectiveDianaAuthBaseURL() string {
	s := a.Settings()
	configured := strings.TrimRight(strings.TrimSpace(str(s["dianaAuthBaseUrl"])), "/")
	if configured != "" && configured != "http://127.0.0.1:18080" && httpURL(configured) {
		return configured
	}
	var lockCandidates []string
	if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
		lockCandidates = append(lockCandidates, filepath.Join(cacheDir, "diana", "diana.db.lock"))
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		lockCandidates = append(lockCandidates, filepath.Join(cwd, "..", "diana", "data", "diana.db.lock"))
	}
	for _, lockPath := range lockCandidates {
		data, err := os.ReadFile(lockPath)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		if len(lines) >= 2 {
			if resolved := normalizeLockURL(lines[1]); resolved != "" {
				return resolved
			}
		}
	}
	if configured != "" && httpURL(configured) {
		return configured
	}
	return "http://127.0.0.1:18080"
}

func (a *App) dianaAuthAvailability() (authAvail bool, pairAvail bool) {
	s := a.Settings()
	if !flag(s["dianaAuthEnabled"]) {
		return false, false
	}
	base := a.effectiveDianaAuthBaseURL()
	if !httpURL(base) {
		return false, false
	}

	dianaPairProbeMu.Lock()
	cached := dianaPairProbeCache
	dianaPairProbeMu.Unlock()
	if cached.url == base && time.Since(cached.checkedAt) < 15*time.Second {
		return true, cached.available
	}

	ctx, cancel := context.WithTimeout(a.ctx, 450*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/auth/owner/status", nil)
	available := false
	if err == nil {
		if res, rErr := a.http.Do(req); rErr == nil {
			var parsed Obj
			_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&parsed)
			res.Body.Close()
			if res.StatusCode == http.StatusOK && flag(parsed["available"]) {
				available = true
			}
		}
	}
	dianaPairProbeMu.Lock()
	dianaPairProbeCache = struct {
		url       string
		available bool
		checkedAt time.Time
	}{url: base, available: available, checkedAt: time.Now()}
	dianaPairProbeMu.Unlock()
	return true, available
}

func (a *App) publicAuthFlags() Obj {
	s := a.Settings()
	ghAvailable := flag(s["githubOAuthEnabled"]) && strings.TrimSpace(str(s["githubClientId"])) != ""
	googleID, _ := a.effectiveGoogleAuthCredentials()
	googleAvailable := flag(s["googleOAuthEnabled"]) && googleID != "" && len(list(s["googleAllowedEmails"])) > 0
	oidcIssuer := strings.TrimRight(strings.TrimSpace(str(s["oidcIssuerUrl"])), "/")
	oidcAvailable := flag(s["oidcEnabled"]) && httpURL(oidcIssuer) && strings.TrimSpace(str(s["oidcClientId"])) != "" && len(list(s["oidcAllowedUsers"])) > 0
	oidcDisplayName := firstNonEmpty(strings.TrimSpace(str(s["oidcDisplayName"])), "SSO")
	dianaAvail, dianaPairAvail := a.dianaAuthAvailability()
	return Obj{
		"githubOAuthAvailable":    ghAvailable,
		"googleOAuthAvailable":    googleAvailable,
		"dianaAuthAvailable":      dianaAvail,
		"dianaOwnerPairAvailable": dianaPairAvail,
		"oidcAvailable":           oidcAvailable,
		"oidcDisplayName":         oidcDisplayName,
	}
}

func (a *App) handleOAuthExchange(w http.ResponseWriter, r *http.Request) error {
	o, e := body(w, r)
	if e != nil {
		return e
	}
	key := "exchange:" + str(o["code"])
	a.oauthMu.Lock()
	entry := a.oauth[key]
	delete(a.oauth, key)
	a.oauthMu.Unlock()
	if entry == nil || num(entry["expires"]) < time.Now().Unix() {
		return errors.New("登录兑换码无效")
	}
	method := firstNonEmpty(str(entry["method"]), "github")
	rawToken, session, err := a.createSession(r, method, str(entry["login"]), str(entry["id"]))
	if err != nil {
		return err
	}
	setSessionCookie(w, r, rawToken)
	ok(w, Obj{
		"token":     rawToken,
		"login":     entry["login"],
		"method":    method,
		"expiresAt": session["expiresAt"],
	})
	return nil
}

func (a *App) oauthExchangeForm(ctx context.Context, tokenEndpoint string, values url.Values) (Obj, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var parsed Obj
	_ = json.Unmarshal(raw, &parsed)
	if res.StatusCode >= 300 || str(parsed["error"]) != "" {
		desc := firstNonEmpty(str(parsed["error_description"]), str(parsed["error"]), fmt.Sprintf("HTTP %d", res.StatusCode))
		return nil, errors.New(desc)
	}
	return parsed, nil
}

func (a *App) discoverOIDCEndpoints(ctx context.Context, issuerURL string) (cachedOIDCDiscovery, error) {
	issuer := strings.TrimRight(strings.TrimSpace(issuerURL), "/")
	if !httpURL(issuer) {
		return cachedOIDCDiscovery{}, errors.New("OIDC Issuer 地址无效")
	}
	oidcDiscoveryMu.Lock()
	cached, ok := oidcDiscoveryCache[issuer]
	oidcDiscoveryMu.Unlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached, nil
	}
	doc, err := a.requestJSON(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return cachedOIDCDiscovery{}, fmt.Errorf("读取 OIDC 发现配置失败：%w", err)
	}
	authEP := strings.TrimSpace(str(doc["authorization_endpoint"]))
	tokenEP := strings.TrimSpace(str(doc["token_endpoint"]))
	userinfoEP := strings.TrimSpace(str(doc["userinfo_endpoint"]))
	if !httpURL(authEP) || !httpURL(tokenEP) {
		return cachedOIDCDiscovery{}, errors.New("OIDC 发现配置缺少 authorization_endpoint 或 token_endpoint")
	}
	entry := cachedOIDCDiscovery{
		authEndpoint:     authEP,
		tokenEndpoint:    tokenEP,
		userinfoEndpoint: userinfoEP,
		expiresAt:        time.Now().Add(10 * time.Minute),
	}
	oidcDiscoveryMu.Lock()
	oidcDiscoveryCache[issuer] = entry
	oidcDiscoveryMu.Unlock()
	return entry, nil
}

func decodeJWTClaims(idToken string) Obj {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) < 2 || parts[1] == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims Obj
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}

func (a *App) dianaCall(ctx context.Context, method, path string, payload any, cookieHeader string) (int, Obj, []*http.Cookie, error) {
	base := a.effectiveDianaAuthBaseURL()
	if !httpURL(base) {
		return 0, nil, nil, errors.New("Diana 服务地址无效")
	}
	var bodyReader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, nil, err
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bodyReader)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("User-Agent", "bgmcat/"+Version)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
	res, err := a.http.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("无法连接 Diana 服务（%s）：%w", base, err)
	}
	defer res.Body.Close()
	var parsed Obj
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&parsed)
	return res.StatusCode, parsed, res.Cookies(), nil
}

func (a *App) cleanupTemporaryDianaSession(cookies []*http.Cookie) {
	for _, c := range cookies {
		if c.Name == "diana_session" && strings.TrimSpace(c.Value) != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _, _, _ = a.dianaCall(ctx, http.MethodPost, "/api/auth/logout", Obj{}, "diana_session="+c.Value)
			cancel()
			return
		}
	}
}

func (a *App) authOAuthRoutes() {
	a.handle("POST /api/auth/oauth/exchange", a.handleOAuthExchange)
	a.handle("POST /api/auth/qr-ticket", func(w http.ResponseWriter, r *http.Request) error {
		webuiAuth, err := a.Store.Get("secrets", "webui_auth")
		if err != nil {
			return err
		}
		username := firstNonEmpty(strings.TrimSpace(str(webuiAuth["username"])), "admin")
		code := randomID(16)
		a.oauthMu.Lock()
		a.oauth["exchange:"+code] = Obj{
			"method":  "qrcode",
			"login":   username,
			"id":      "qr:" + username,
			"expires": time.Now().Add(10 * time.Minute).Unix(),
		}
		a.oauthMu.Unlock()
		ok(w, Obj{
			"code":             code,
			"username":         username,
			"expiresInSeconds": 600,
		})
		return nil
	})

	// Google OAuth 登录
	a.handle("GET /api/auth/google/start", func(w http.ResponseWriter, r *http.Request) error {
		s := a.Settings()
		clientID, _ := a.effectiveGoogleAuthCredentials()
		if !flag(s["googleOAuthEnabled"]) || clientID == "" || len(list(s["googleAllowedEmails"])) == 0 {
			return errors.New("Google 登录尚未启用或未配置允许邮箱")
		}
		redirectURI := a.requestPublicBaseURL(r) + "/api/auth/google/callback"
		state := randomID(32)
		verifier := randomID(32)
		sum := sha256.Sum256([]byte(verifier))
		a.oauthMu.Lock()
		a.oauth["gauth:"+state] = Obj{
			"verifier": verifier,
			"redirect": redirectURI,
			"expires":  time.Now().Add(10 * time.Minute).Unix(),
		}
		a.oauthMu.Unlock()
		http.SetCookie(w, &http.Cookie{
			Name:     "google_auth_state",
			Value:    state,
			Path:     "/api/auth/google",
			HttpOnly: true,
			Secure:   isHTTPSRequest(r),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   600,
		})
		q := url.Values{
			"client_id":             {clientID},
			"redirect_uri":          {redirectURI},
			"response_type":         {"code"},
			"scope":                 {"openid email profile"},
			"state":                 {state},
			"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
			"code_challenge_method": {"S256"},
			"prompt":                {"select_account"},
		}
		http.Redirect(w, r, "https://accounts.google.com/o/oauth2/v2/auth?"+q.Encode(), http.StatusFound)
		return nil
	})

	a.handle("GET /api/auth/google/callback", func(w http.ResponseWriter, r *http.Request) error {
		if errMsg := strings.TrimSpace(r.URL.Query().Get("error")); errMsg != "" {
			redirectAuthError(w, r, "Google 授权已取消或失败："+errMsg)
			return nil
		}
		state := r.URL.Query().Get("state")
		cookie, e := r.Cookie("google_auth_state")
		if e != nil || cookie.Value != state {
			redirectAuthError(w, r, "Google 登录状态无效，请重试")
			return nil
		}
		a.oauthMu.Lock()
		pending := a.oauth["gauth:"+state]
		delete(a.oauth, "gauth:"+state)
		a.oauthMu.Unlock()
		if pending == nil || num(pending["expires"]) < time.Now().Unix() {
			redirectAuthError(w, r, "Google 登录已过期，请重试")
			return nil
		}
		clientID, clientSecret := a.effectiveGoogleAuthCredentials()
		form := url.Values{
			"client_id":     {clientID},
			"code":          {r.URL.Query().Get("code")},
			"code_verifier": {str(pending["verifier"])},
			"redirect_uri":  {str(pending["redirect"])},
			"grant_type":    {"authorization_code"},
		}
		if clientSecret != "" {
			form.Set("client_secret", clientSecret)
		}
		token, err := a.oauthExchangeForm(r.Context(), "https://oauth2.googleapis.com/token", form)
		if err != nil {
			redirectAuthError(w, r, "Google 令牌兑换失败："+err.Error())
			return nil
		}
		user, err := a.requestJSON(r.Context(), http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil, map[string]string{
			"Authorization": "Bearer " + str(token["access_token"]),
			"Accept":        "application/json",
		})
		if err != nil {
			redirectAuthError(w, r, "获取 Google 用户信息失败："+err.Error())
			return nil
		}
		email := strings.TrimSpace(str(user["email"]))
		if email == "" || !(flag(user["verified_email"]) || flag(user["email_verified"])) {
			redirectAuthError(w, r, "Google 账号缺少已验证邮箱")
			return nil
		}
		allowed := false
		for _, item := range list(a.Settings()["googleAllowedEmails"]) {
			if strings.EqualFold(strings.TrimSpace(str(item)), email) {
				allowed = true
				break
			}
		}
		if !allowed {
			redirectAuthError(w, r, "此 Google 邮箱不在允许名单中")
			return nil
		}
		code := randomID(32)
		a.oauthMu.Lock()
		a.oauth["exchange:"+code] = Obj{
			"method":  "google",
			"login":   email,
			"id":      str(user["id"]),
			"expires": time.Now().Add(time.Minute).Unix(),
		}
		a.oauthMu.Unlock()
		http.Redirect(w, r, "/?login="+code, http.StatusFound)
		return nil
	})

	// 通用 OIDC / SSO 登录（Linux.do、Pocket ID、Authentik、Authelia、Keycloak 等）
	a.handle("GET /api/auth/oidc/start", func(w http.ResponseWriter, r *http.Request) error {
		s := a.Settings()
		issuer := strings.TrimSpace(str(s["oidcIssuerUrl"]))
		clientID := strings.TrimSpace(str(s["oidcClientId"]))
		if !flag(s["oidcEnabled"]) || !httpURL(issuer) || clientID == "" || len(list(s["oidcAllowedUsers"])) == 0 {
			return errors.New("OIDC / SSO 登录尚未配置")
		}
		ep, err := a.discoverOIDCEndpoints(r.Context(), issuer)
		if err != nil {
			return err
		}
		redirectURI := a.requestPublicBaseURL(r) + "/api/auth/oidc/callback"
		state := randomID(32)
		verifier := randomID(32)
		sum := sha256.Sum256([]byte(verifier))
		a.oauthMu.Lock()
		a.oauth["oidc:"+state] = Obj{
			"issuer":   issuer,
			"verifier": verifier,
			"redirect": redirectURI,
			"expires":  time.Now().Add(10 * time.Minute).Unix(),
		}
		a.oauthMu.Unlock()
		http.SetCookie(w, &http.Cookie{
			Name:     "oidc_auth_state",
			Value:    state,
			Path:     "/api/auth/oidc",
			HttpOnly: true,
			Secure:   isHTTPSRequest(r),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   600,
		})
		q := url.Values{
			"client_id":             {clientID},
			"redirect_uri":          {redirectURI},
			"response_type":         {"code"},
			"scope":                 {"openid profile email"},
			"state":                 {state},
			"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
			"code_challenge_method": {"S256"},
		}
		sep := "?"
		if strings.Contains(ep.authEndpoint, "?") {
			sep = "&"
		}
		http.Redirect(w, r, ep.authEndpoint+sep+q.Encode(), http.StatusFound)
		return nil
	})

	a.handle("GET /api/auth/oidc/callback", func(w http.ResponseWriter, r *http.Request) error {
		if errMsg := strings.TrimSpace(r.URL.Query().Get("error")); errMsg != "" {
			redirectAuthError(w, r, "SSO 授权失败："+errMsg)
			return nil
		}
		state := r.URL.Query().Get("state")
		cookie, e := r.Cookie("oidc_auth_state")
		if e != nil || cookie.Value != state {
			redirectAuthError(w, r, "SSO 登录状态无效，请重试")
			return nil
		}
		a.oauthMu.Lock()
		pending := a.oauth["oidc:"+state]
		delete(a.oauth, "oidc:"+state)
		a.oauthMu.Unlock()
		if pending == nil || num(pending["expires"]) < time.Now().Unix() {
			redirectAuthError(w, r, "SSO 登录已过期，请重试")
			return nil
		}
		s := a.Settings()
		ep, err := a.discoverOIDCEndpoints(r.Context(), str(pending["issuer"]))
		if err != nil {
			redirectAuthError(w, r, err.Error())
			return nil
		}
		form := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {r.URL.Query().Get("code")},
			"redirect_uri":  {str(pending["redirect"])},
			"client_id":     {strings.TrimSpace(str(s["oidcClientId"]))},
			"code_verifier": {str(pending["verifier"])},
		}
		if sec := strings.TrimSpace(str(s["oidcClientSecret"])); sec != "" {
			form.Set("client_secret", sec)
		}
		token, err := a.oauthExchangeForm(r.Context(), ep.tokenEndpoint, form)
		if err != nil {
			redirectAuthError(w, r, "SSO 令牌兑换失败："+err.Error())
			return nil
		}
		var claims Obj
		if ep.userinfoEndpoint != "" && str(token["access_token"]) != "" {
			claims, err = a.requestJSON(r.Context(), http.MethodGet, ep.userinfoEndpoint, nil, map[string]string{
				"Authorization": "Bearer " + str(token["access_token"]),
				"Accept":        "application/json",
			})
			if err != nil {
				redirectAuthError(w, r, "获取 SSO 用户信息失败："+err.Error())
				return nil
			}
		} else {
			claims = decodeJWTClaims(str(token["id_token"]))
		}
		if len(claims) == 0 {
			redirectAuthError(w, r, "未从 SSO 提供方获取到用户身份")
			return nil
		}
		candidates := []string{
			strings.TrimSpace(str(claims["email"])),
			strings.TrimSpace(str(claims["preferred_username"])),
			strings.TrimSpace(str(claims["username"])),
			strings.TrimSpace(str(claims["name"])),
			strings.TrimSpace(str(claims["sub"])),
		}
		matchedUser := ""
		allowAll := false
		for _, allowedRaw := range list(s["oidcAllowedUsers"]) {
			aVal := strings.TrimSpace(str(allowedRaw))
			if aVal == "" {
				continue
			}
			if aVal == "*" {
				allowAll = true
				continue
			}
			for _, cand := range candidates {
				if cand != "" && strings.EqualFold(aVal, cand) {
					matchedUser = cand
					break
				}
			}
			if matchedUser != "" {
				break
			}
		}
		if matchedUser == "" && allowAll {
			matchedUser = firstNonEmpty(candidates...)
		}
		if matchedUser == "" {
			redirectAuthError(w, r, "此 SSO 账号不在允许名单中")
			return nil
		}
		code := randomID(32)
		a.oauthMu.Lock()
		a.oauth["exchange:"+code] = Obj{
			"method":  "oidc",
			"login":   matchedUser,
			"id":      str(claims["sub"]),
			"expires": time.Now().Add(time.Minute).Unix(),
		}
		a.oauthMu.Unlock()
		http.Redirect(w, r, "/?login="+code, http.StatusFound)
		return nil
	})

	// Diana 联合登录（账号密码 / 机器人私聊 6 位验证码）
	a.handle("POST /api/auth/diana/login", func(w http.ResponseWriter, r *http.Request) error {
		if !flag(a.Settings()["dianaAuthEnabled"]) {
			return errors.New("Diana 快捷登录未开启")
		}
		ip := a.clientIP(r)
		if wait := a.throttle.check(ip); wait > 0 {
			writeRateLimited(w, wait)
			return nil
		}
		o, e := body(w, r)
		if e != nil {
			return e
		}
		username := strings.TrimSpace(str(o["username"]))
		password := str(o["password"])
		if username == "" || password == "" {
			return errors.New("请输入 Diana 账号与密码")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		status, parsed, cookies, err := a.dianaCall(ctx, http.MethodPost, "/api/auth/login", Obj{
			"username": username,
			"password": password,
		}, "")
		if err != nil {
			return err
		}
		if status >= 400 || !flag(parsed["ok"]) {
			a.cleanupTemporaryDianaSession(cookies)
			a.throttle.recordFailure(ip)
			a.sleepOnAuthFailure()
			msg := firstNonEmpty(str(parsed["error"]), str(parsed["message"]), "Diana 账号或密码错误")
			if status == http.StatusTooManyRequests {
				failWithCode(w, http.StatusTooManyRequests, "AUTH_RATE_LIMITED", msg)
			} else {
				fail(w, http.StatusUnauthorized, msg)
			}
			return nil
		}
		a.cleanupTemporaryDianaSession(cookies)
		a.throttle.recordSuccess(ip)
		rawToken, _, err := a.createSession(r, "diana", username, "diana:"+username)
		if err != nil {
			return err
		}
		setSessionCookie(w, r, rawToken)
		a.Log("info", fmt.Sprintf("通过 Diana 账号登录 WebUI（%s · %s）", username, ip))
		ok(w, Obj{
			"authenticated": true,
			"username":      username,
			"status":        a.status(),
		})
		return nil
	})

	a.handle("POST /api/auth/diana/pair", func(w http.ResponseWriter, r *http.Request) error {
		if !flag(a.Settings()["dianaAuthEnabled"]) {
			return errors.New("Diana 快捷登录未开启")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		status, parsed, _, err := a.dianaCall(ctx, http.MethodPost, "/api/auth/owner/pair", Obj{}, "")
		if err != nil {
			return err
		}
		if status >= 400 || str(parsed["code"]) == "" {
			return errors.New(firstNonEmpty(str(parsed["error"]), str(parsed["message"]), "获取 Diana 验证码失败"))
		}
		ok(w, Obj{
			"code":             str(parsed["code"]),
			"pollToken":        str(parsed["poll_token"]),
			"expiresInSeconds": num(parsed["expires_in_seconds"]),
		})
		return nil
	})

	a.handle("POST /api/auth/diana/pair/status", func(w http.ResponseWriter, r *http.Request) error {
		if !flag(a.Settings()["dianaAuthEnabled"]) {
			return errors.New("Diana 快捷登录未开启")
		}
		o, e := body(w, r)
		if e != nil {
			return e
		}
		pollToken := strings.TrimSpace(firstNonEmpty(str(o["pollToken"]), str(o["poll_token"])))
		if pollToken == "" {
			return errors.New("缺少 pollToken")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		status, parsed, cookies, err := a.dianaCall(ctx, http.MethodPost, "/api/auth/owner/pair/status", Obj{
			"poll_token": pollToken,
		}, "")
		if err != nil {
			return err
		}
		if status >= 400 {
			return errors.New(firstNonEmpty(str(parsed["error"]), str(parsed["message"]), "检查 Diana 配对状态失败"))
		}
		if !flag(parsed["approved"]) {
			ok(w, Obj{
				"approved":         false,
				"expired":          flag(parsed["expired"]),
				"expiresInSeconds": num(parsed["expires_in_seconds"]),
			})
			return nil
		}
		a.cleanupTemporaryDianaSession(cookies)
		rawToken, _, err := a.createSession(r, "diana", "Diana 主人", "diana:owner")
		if err != nil {
			return err
		}
		setSessionCookie(w, r, rawToken)
		a.Log("info", fmt.Sprintf("通过 Diana 私聊验证码登录 WebUI（%s）", a.clientIP(r)))
		ok(w, Obj{
			"approved":      true,
			"authenticated": true,
			"username":      "Diana 主人",
			"status":        a.status(),
		})
		return nil
	})

	a.handle("POST /api/auth/diana/pair/claim", func(w http.ResponseWriter, r *http.Request) error {
		if !flag(a.Settings()["dianaAuthEnabled"]) {
			return errors.New("Diana 快捷登录未开启")
		}
		ip := a.clientIP(r)
		if wait := a.throttle.check(ip); wait > 0 {
			writeRateLimited(w, wait)
			return nil
		}
		o, e := body(w, r)
		if e != nil {
			return e
		}
		code := strings.TrimSpace(str(o["code"]))
		if code == "" {
			return errors.New("请输入 6 位验证码")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		status, parsed, cookies, err := a.dianaCall(ctx, http.MethodPost, "/api/auth/owner/pair/claim", Obj{
			"code": code,
		}, "")
		if err != nil {
			return err
		}
		if status >= 400 || !flag(parsed["ok"]) {
			a.cleanupTemporaryDianaSession(cookies)
			a.throttle.recordFailure(ip)
			a.sleepOnAuthFailure()
			msg := firstNonEmpty(str(parsed["error"]), str(parsed["message"]), "验证码无效或已过期")
			fail(w, http.StatusUnauthorized, msg)
			return nil
		}
		a.cleanupTemporaryDianaSession(cookies)
		a.throttle.recordSuccess(ip)
		rawToken, _, err := a.createSession(r, "diana", "Diana 主人", "diana:owner")
		if err != nil {
			return err
		}
		setSessionCookie(w, r, rawToken)
		a.Log("info", fmt.Sprintf("通过 Diana 验证码认领登录 WebUI（%s）", ip))
		ok(w, Obj{
			"authenticated": true,
			"username":      "Diana 主人",
			"status":        a.status(),
		})
		return nil
	})
}
