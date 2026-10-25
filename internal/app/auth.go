package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (a *App) validSession(token string) bool {
	s, e := a.Store.Get("sessions", hash(token))
	return e == nil && s != nil && num(s["expires"]) > time.Now().Unix()
}
func (a *App) authRoutes() {
	a.handle("GET /api/auth/github/start", func(w http.ResponseWriter, r *http.Request) error {
		s := a.Settings()
		if !flag(s["githubOAuthEnabled"]) || str(s["githubClientId"]) == "" {
			return errors.New("GitHub 登录尚未配置")
		}
		state := randomID(32)
		a.oauthMu.Lock()
		a.oauth["gh:"+state] = Obj{"expires": time.Now().Add(10 * time.Minute).Unix()}
		a.oauthMu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "github_state", Value: state, Path: "/api/auth/github", HttpOnly: true, Secure: strings.HasPrefix(a.cfg.PublicURL, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: 600})
		http.Redirect(w, r, "https://github.com/login/oauth/authorize?"+url.Values{"client_id": {str(s["githubClientId"])}, "redirect_uri": {a.cfg.PublicURL + "/api/auth/github/callback"}, "state": {state}}.Encode(), 302)
		return nil
	})
	a.handle("GET /api/auth/github/callback", func(w http.ResponseWriter, r *http.Request) error {
		state := r.URL.Query().Get("state")
		cookie, e := r.Cookie("github_state")
		if e != nil || cookie.Value != state {
			return errors.New("GitHub 登录状态无效")
		}
		a.oauthMu.Lock()
		pending := a.oauth["gh:"+state]
		delete(a.oauth, "gh:"+state)
		a.oauthMu.Unlock()
		if pending == nil || num(pending["expires"]) < time.Now().Unix() {
			return errors.New("登录已过期")
		}
		s := a.Settings()
		token, e := a.requestJSON(r.Context(), "POST", "https://github.com/login/oauth/access_token", Obj{"client_id": s["githubClientId"], "client_secret": s["githubClientSecret"], "code": r.URL.Query().Get("code"), "redirect_uri": a.cfg.PublicURL + "/api/auth/github/callback"}, map[string]string{"Accept": "application/json"})
		if e != nil {
			return e
		}
		user, e := a.requestJSON(r.Context(), "GET", "https://api.github.com/user", nil, map[string]string{"Authorization": "Bearer " + str(token["access_token"]), "Accept": "application/vnd.github+json"})
		if e != nil {
			return e
		}
		allowed := false
		for _, login := range list(s["githubAllowedLogins"]) {
			if strings.EqualFold(str(login), str(user["login"])) {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("此 GitHub 账号不在允许名单中")
		}
		code := randomID(32)
		a.oauthMu.Lock()
		a.oauth["exchange:"+code] = Obj{"login": user["login"], "id": user["id"], "expires": time.Now().Add(time.Minute).Unix()}
		a.oauthMu.Unlock()
		http.Redirect(w, r, "/?login="+code, 302)
		return nil
	})
	a.handle("POST /api/auth/github/exchange", func(w http.ResponseWriter, r *http.Request) error {
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
		token := randomID(32)
		id, e := a.Store.NextID()
		if e != nil {
			return e
		}
		expires := time.Now().Add(30 * 24 * time.Hour)
		session := Obj{"id": id, "githubLogin": entry["login"], "githubId": entry["id"], "createdAt": now(), "lastSeenAt": now(), "expiresAt": expires.Format(time.RFC3339), "expires": expires.Unix()}
		if e = a.Store.Put("sessions", hash(token), session); e != nil {
			return e
		}
		ok(w, Obj{"token": token, "login": entry["login"], "expiresAt": session["expiresAt"]})
		return nil
	})
	a.handle("GET /api/auth/sessions", func(w http.ResponseWriter, r *http.Request) error {
		sessions, e := a.Store.List("sessions")
		if e == nil {
			ok(w, sessions)
		}
		return e
	})
	a.handle("DELETE /api/auth/sessions/{id}", func(w http.ResponseWriter, r *http.Request) error {
		rows, e := a.Store.db.Query("SELECT key,value FROM documents WHERE kind='sessions'")
		if e != nil {
			return e
		}
		keys := []string{}
		for rows.Next() {
			var key, raw string
			_ = rows.Scan(&key, &raw)
			var s Obj
			_ = json.Unmarshal([]byte(raw), &s)
			if str(s["id"]) == r.PathValue("id") {
				keys = append(keys, key)
			}
		}
		rows.Close()
		for _, key := range keys {
			if e = a.Store.Delete("sessions", key); e != nil {
				return e
			}
		}
		ok(w, Obj{"revoked": true})
		return nil
	})
	a.handle("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) error {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		e := a.Store.Delete("sessions", hash(token))
		if e == nil {
			ok(w, Obj{"revoked": true})
		}
		return e
	})
}
