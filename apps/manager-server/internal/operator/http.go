package operator

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

//go:embed panel.html
var panel []byte

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
func publicAccounts(list []Account) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		out = append(out, map[string]any{"id": a.ID, "provider": a.Provider, "email": a.Email, "disabled": a.Disabled, "status": a.Status, "success": a.Success, "failed": a.Failed, "quota": a.Quota, "quota_error": a.QuotaError})
	}
	return out
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
		user, pass, ok := r.BasicAuth()
		got := sha256.Sum256([]byte(pass))
		if password == "" || !ok || user != "operator" || subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="CPA operator", charset="UTF-8"`)
			reply(w, 401, map[string]string{"error": "operator password required"})
			return
		}
		switch {
		case r.URL.Path == "/" && r.Method == "GET":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(panel)
		case r.URL.Path == "/api/accounts" && r.Method == "GET":
			list, e := s.Accounts(r.Context())
			if e != nil {
				reply(w, 502, map[string]string{"error": e.Error()})
				return
			}
			reply(w, 200, publicAccounts(list))
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
			if e := s.Toggle(r.Context(), v.ID, *v.Disabled); e != nil {
				reply(w, 502, map[string]string{"error": e.Error()})
				return
			}
			reply(w, 200, map[string]bool{"ok": true})
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
			_, _ = w.Write(panel)
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
			reply(w, 200, publicAccounts(a))
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
