package operator

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// HubHandler preserves T3's root-relative usage API without exposing CPA auth
// configuration or a generic credential-backed HTTP proxy on the internet.
func (s *Service) HubHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers(w)
		c, e := s.resolve(r.Context())
		if e != nil {
			reply(w, 503, map[string]string{"error": "CPA unavailable"})
			return
		}
		token := ""
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		got := sha256.Sum256([]byte(token))
		want := sha256.Sum256([]byte(c.Key))
		if c.Key == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			reply(w, 401, map[string]string{"error": "hub key required"})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v0/management/auth-files" {
			list, e := s.accounts(r.Context())
			if e != nil {
				reply(w, 502, map[string]string{"error": e.Error()})
				return
			}
			out := make([]map[string]any, 0, len(list))
			for _, a := range list {
				out = append(out, map[string]any{"id": publicID(a.ID), "auth_index": a.Index, "provider": a.Provider, "email": strings.SplitN(a.Email, "@", 2)[0], "disabled": a.Disabled, "id_token": map[string]string{"chatgpt_account_id": a.Identity.AccountID}})
			}
			reply(w, 200, map[string]any{"files": out})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/v0/management/api-call" {
			var v struct {
				Index  string            `json:"auth_index"`
				Method string            `json:"method"`
				URL    string            `json:"url"`
				Header map[string]string `json:"header"`
				Data   string            `json:"data,omitempty"`
			}
			if !jsonBody(w, r, &v) {
				return
			}
			// Provider URL and auth headers come from our code, never the public caller.
			if v.Method != "GET" || v.Data != "" || (v.URL != "https://chatgpt.com/backend-api/wham/usage" && v.URL != "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits") {
				reply(w, 403, map[string]string{"error": "only quota reads are public; mutations require private administration"})
				return
			}
			list, e := s.accounts(r.Context())
			if e != nil {
				reply(w, 502, map[string]string{"error": "CPA unavailable"})
				return
			}
			for _, a := range list {
				if a.Index == v.Index && a.Provider == "codex" {
					body, e := s.provider(r.Context(), a, v.URL, "GET", nil)
					if e != nil {
						reply(w, 502, map[string]string{"error": e.Error()})
						return
					}
					reply(w, 200, map[string]any{"status_code": 200, "body": body})
					return
				}
			}
			reply(w, 404, map[string]string{"error": "account not found"})
			return
		}
		http.NotFound(w, r)
	})
}
