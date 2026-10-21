package operator

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed panel.html
var panel []byte

//go:embed activation.html
var activationPanel []byte

type VerifyAdmin func(context.Context, string) (bool, error)

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func headers(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, e := url.Parse(origin)
	return e == nil && u.Host == r.Host && (u.Scheme == "https" || u.Scheme == "http")
}
func jsonBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if !sameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		reply(w, 403, map[string]string{"error": "same-origin JSON required"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		reply(w, 400, map[string]string{"error": "invalid request"})
		return false
	}
	return true
}
func publicID(id string) string {
	h := sha256.Sum256([]byte("operator-account:" + id))
	return fmt.Sprintf("%x", h[:16])
}
func publicAccounts(list []Account) []map[string]any { return accountViews(list, false) }
func accountViews(list []Account, private bool) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		id, name := a.ID, a.Email
		if !private {
			id = publicID(a.ID)
			name = strings.SplitN(a.Email, "@", 2)[0]
			if name == "" {
				name = a.Provider
			}
		}
		out = append(out, map[string]any{"id": id, "provider": a.Provider, "email": name, "disabled": a.Disabled, "status": a.Status, "success": a.Success, "failed": a.Failed, "quota": a.Quota, "quota_error": a.QuotaError})
	}
	return out
}

// publicWindows exposes only opaque account IDs and window controls.
func (s *Service) publicWindows(ctx context.Context) (map[string]any, error) {
	list, err := s.accounts(ctx)
	if err != nil {
		return nil, err
	}
	settings := s.Settings()
	out := map[string]any{}
	for _, a := range list {
		if a.Provider != "codex" {
			continue
		}
		v := map[string]any{"enabled": false, "times": []string{}, "first_start": "", "timezone": settings.Timezone}
		for _, x := range settings.Schedules {
			if x.AccountID == a.ID {
				v["enabled"] = x.Enabled
				v["times"] = x.Times
				v["first_start"] = x.FirstStart
				v["next_run"] = x.NextRun
				break
			}
		}
		if run, ok := settings.Runs[a.ID]; ok {
			v["last_result"] = run.Result
			v["last_at"] = run.At
		}
		out[publicID(a.ID)] = v
	}
	return out, nil
}
func (s *Service) PublicHandler(password string) http.Handler {
	// Public listener has no reference to the full Manager router. Supplying an
	// admin key, forwarding headers, alternate verbs, paths or encodings cannot
	// turn it into a management proxy.
	want := sha256.Sum256([]byte(password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers(w)
		if strings.HasPrefix(r.URL.Path, "/v0/management/") {
			s.HubHandler().ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" && r.Method == "GET" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			page := strings.ReplaceAll(string(panel), "__AUTH_OWNER__", html.EscapeString(s.authOwner))
			_, _ = w.Write([]byte(page))
			return
		}
		if r.URL.Path == "/api/login" && r.Method == "POST" {
			var v struct {
				Password string `json:"password"`
			}
			if !jsonBody(w, r, &v) {
				return
			}
			got := sha256.Sum256([]byte(v.Password))
			if password == "" || subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
				time.Sleep(500 * time.Millisecond)
				reply(w, 401, map[string]string{"error": "Incorrect password"})
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "cpa_operator", Value: sessionToken(password, time.Now().Add(12*time.Hour).Unix()), Path: "/", MaxAge: 43200, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
		if r.URL.Path == "/api/logout" && r.Method == "POST" {
			var v struct{}
			if !jsonBody(w, r, &v) {
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "cpa_operator", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		got := sha256.Sum256([]byte(token))
		authorized := password != "" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && subtle.ConstantTimeCompare(want[:], got[:]) == 1
		if cookie, err := r.Cookie("cpa_operator"); err == nil {
			authorized = authorized || validSession(password, cookie.Value)
		}
		if !authorized {
			reply(w, 401, map[string]string{"error": "Password required"})
			return
		}
		switch {
		case r.URL.Path == "/api/codex/device/start" && r.Method == "POST":
			if s.authOwner != "" {
				reply(w, 409, map[string]string{"error": "Manage shared account logins at " + s.authOwner})
				return
			}
			var v struct {
				Mode      string `json:"mode"`
				AccountID string `json:"account_id"`
			}
			if !jsonBody(w, r, &v) {
				return
			}
			job, e := s.device.start(r.Context(), s, v.Mode, v.AccountID)
			if e != nil {
				reply(w, 400, map[string]string{"error": e.Error()})
				return
			}
			reply(w, 200, job)
		case r.URL.Path == "/api/codex/device/status" && r.Method == "GET":
			job, e := s.device.status(r.URL.Query().Get("id"))
			if e != nil {
				reply(w, 404, map[string]string{"error": e.Error()})
				return
			}
			reply(w, 200, job)
		case r.URL.Path == "/api/accounts" && r.Method == "GET":
			list, e := s.Accounts(r.Context())
			if e != nil {
				reply(w, 502, map[string]string{"error": e.Error()})
				return
			}
			reply(w, 200, publicAccounts(list))
		case r.URL.Path == "/api/windows" && r.Method == "GET":
			v, e := s.publicWindows(r.Context())
			if e != nil {
				reply(w, 502, map[string]string{"error": "CPA unavailable"})
				return
			}
			reply(w, 200, v)
		case r.URL.Path == "/api/account/window" && r.Method == "PUT":
			var v struct {
				ID         string `json:"id"`
				Enabled    *bool  `json:"enabled"`
				FirstStart string `json:"first_start"`
			}
			if !jsonBody(w, r, &v) {
				return
			}
			if v.Enabled == nil {
				reply(w, 400, map[string]string{"error": "enabled required"})
				return
			}
			id, e := s.resolvePublicID(r.Context(), v.ID)
			if e != nil {
				reply(w, 404, map[string]string{"error": "account not found"})
				return
			}
			if e = s.ConfigureAccountWindow(r.Context(), id, *v.Enabled, v.FirstStart); e != nil {
				reply(w, 400, map[string]string{"error": e.Error()})
				return
			}
			reply(w, 200, map[string]bool{"ok": true})
		case r.URL.Path == "/api/account/window/start" && r.Method == "POST":
			var v struct {
				ID string `json:"id"`
			}
			if !jsonBody(w, r, &v) {
				return
			}
			id, e := s.resolvePublicID(r.Context(), v.ID)
			if e != nil {
				reply(w, 404, map[string]string{"error": "account not found"})
				return
			}
			result, e := s.RunNow(r.Context(), id)
			s.cacheMu.Lock()
			s.cacheAt = time.Time{}
			s.cacheMu.Unlock()
			if e != nil {
				reply(w, 502, map[string]string{"error": result})
				return
			}
			reply(w, 200, map[string]string{"result": result})

		case r.URL.Path == "/api/account/status" && r.Method == "POST":
			var v struct {
				ID       string `json:"id"`
				Disabled *bool  `json:"disabled"`
			}
			if !jsonBody(w, r, &v) {
				return
			}
			if v.ID == "" || v.Disabled == nil {
				reply(w, 400, map[string]string{"error": "account and disabled required"})
				return
			}
			id, e := s.resolvePublicID(r.Context(), v.ID)
			if e != nil {
				reply(w, 404, map[string]string{"error": "account not found"})
				return
			}
			if e := s.Toggle(r.Context(), id, *v.Disabled); e != nil {
				reply(w, 502, map[string]string{"error": e.Error()})
				return
			}
			reply(w, 200, map[string]bool{"ok": true})
		case r.URL.Path == "/api/account/reset" && r.Method == "POST":
			var v struct {
				ID        string `json:"id"`
				RequestID string `json:"request_id"`
			}
			if !jsonBody(w, r, &v) {
				return
			}
			if !validRequestID(v.RequestID) {
				reply(w, 400, map[string]string{"error": "valid redemption UUID required"})
				return
			}
			id, e := s.resolvePublicID(r.Context(), v.ID)
			if e != nil {
				reply(w, 404, map[string]string{"error": "account not found"})
				return
			}
			result, e := s.ConsumeReset(r.Context(), id, v.RequestID)
			if e != nil {
				reply(w, 502, map[string]string{"error": e.Error()})
				return
			}
			reply(w, 200, map[string]string{"result": result})
		default:
			http.NotFound(w, r)
		}
	})
}
func (s *Service) AdminHandler(verify VerifyAdmin) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers(w)
		if r.URL.Path == "/activation/" && r.Method == "GET" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(activationPanel)
			return
		}
		ok, e := verify(r.Context(), r.Header.Get("Authorization"))
		if e != nil || !ok {
			reply(w, 401, map[string]string{"error": "admin key required"})
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/activation")
		switch {
		case path == "/api/accounts" && r.Method == "GET":
			a, e := s.Accounts(r.Context())
			if e != nil {
				reply(w, 502, map[string]string{"error": e.Error()})
				return
			}
			reply(w, 200, accountViews(a, true))
		case path == "/api/settings" && r.Method == "GET":
			reply(w, 200, s.Settings())
		case path == "/api/settings" && r.Method == "PUT":
			var v Settings
			if !jsonBody(w, r, &v) {
				return
			}
			if e := s.Configure(r.Context(), v); e != nil {
				reply(w, 400, map[string]string{"error": e.Error()})
				return
			}
			reply(w, 200, s.Settings())
		case path == "/api/run" && r.Method == "POST":
			var v struct {
				ID string `json:"id"`
			}
			if !jsonBody(w, r, &v) {
				return
			}
			result, e := s.RunNow(r.Context(), v.ID)
			if e != nil {
				reply(w, 502, map[string]string{"error": result})
				return
			}
			reply(w, 200, map[string]string{"result": result})
		default:
			http.NotFound(w, r)
		}
	})
}
