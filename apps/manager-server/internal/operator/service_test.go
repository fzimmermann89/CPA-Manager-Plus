package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mockService(t *testing.T) (*Service, *int) {
	t.Helper()
	calls := 0
	reset := int64(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer manager-secret" {
			t.Error("missing CPA auth")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_, _ = w.Write([]byte(`{"files":[{"id":"a","name":"a.json","auth_index":"index-a","provider":"codex","email":"a@example.test","status":"active","success":12,"access_token":"LEAK-ME","refresh_token":"LEAK-ME","path":"/private/auth","id_token":{"chatgpt_account_id":"workspace"}}]}`))
		case "/v0/management/auth-files/status":
			var v map[string]any
			_ = json.NewDecoder(r.Body).Decode(&v)
			if len(v) != 2 || v["name"] != "a.json" {
				t.Error("unexpected mutation", v)
			}
			_, _ = w.Write([]byte(`{}`))
		case "/v0/management/api-call":
			var v struct {
				Index  string            `json:"auth_index"`
				URL    string            `json:"url"`
				Data   string            `json:"data"`
				Header map[string]string `json:"header"`
			}
			_ = json.NewDecoder(r.Body).Decode(&v)
			if v.Index != "index-a" || v.Header["Authorization"] != "Bearer $TOKEN$" {
				t.Error("wrong account or credential handling")
			}
			if strings.HasSuffix(v.URL, "/responses") {
				calls++
				reset = time.Now().Add(5 * time.Hour).Unix()
				var d map[string]any
				_ = json.Unmarshal([]byte(v.Data), &d)
				if d["model"] != "gpt-6-luna" || d["reasoning"].(map[string]any)["effort"] != "low" {
					t.Error("wrong activation model")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": "event: response.completed\n"})
			} else {
				q := Quota{Limits: &Limits{Primary: &Window{ResetAt: reset, Seconds: 18000}}}
				b, _ := json.Marshal(q)
				_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": string(b)})
			}
		default:
			t.Error("unexpected upstream path", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	s, e := New(func(context.Context) (Connection, error) { return Connection{server.URL, "manager-secret"}, nil }, filepath.Join(t.TempDir(), "activation.json"))
	if e != nil {
		t.Fatal(e)
	}
	return s, &calls
}
func TestActivationTargetsOneAccountAndPersistsSlot(t *testing.T) {
	s, calls := mockService(t)
	now := time.Now()
	slot := now.Format("15:04")
	if e := s.Configure(context.Background(), Settings{Timezone: "UTC", Schedules: []Schedule{{AccountID: "a", Enabled: true, Times: []string{slot}, Model: "gpt-6-luna", Effort: "low"}}}); e != nil {
		t.Fatal(e)
	}
	// Use the current UTC slot so this test is independent of the host timezone.
	v := s.Settings()
	v.Schedules[0].Times = []string{now.UTC().Format("15:04")}
	if e := s.Configure(context.Background(), v); e != nil {
		t.Fatal(e)
	}
	s.Tick(context.Background(), now)
	s.Tick(context.Background(), now.Add(time.Second))
	if *calls != 1 {
		t.Fatalf("got %d provider requests", *calls)
	}
	restarted, e := New(s.resolve, s.path)
	if e != nil {
		t.Fatal(e)
	}
	restarted.client = s.client
	restarted.Tick(context.Background(), now.Add(2*time.Second))
	if *calls != 1 {
		t.Fatal("restart duplicated activation")
	}
	if s.Settings().Runs["a"].Result != "five-hour window started" {
		t.Fatal(s.Settings().Runs)
	}
}
func TestManualRunSkipsActiveWindow(t *testing.T) {
	s, n := mockService(t)
	for i := 0; i < 2; i++ {
		if _, e := s.RunNow(context.Background(), "a"); e != nil {
			t.Fatal(e)
		}
	}
	if *n != 1 {
		t.Fatal("active window restarted")
	}
}
func TestValidationAndMissedSlots(t *testing.T) {
	for _, v := range []Settings{{Timezone: "bad"}, {Timezone: "UTC", Schedules: []Schedule{{AccountID: "a", Model: "expensive", Effort: "high"}}}, {Timezone: "UTC", Schedules: []Schedule{{AccountID: "a", Model: "gpt-6-luna", Effort: "low", Times: []string{"25:00"}}}}} {
		if validate(v) == nil {
			t.Fatal("invalid schedule accepted")
		}
	}
	now := time.Date(2026, 10, 19, 5, 15, 0, 0, time.UTC)
	if _, _, ok := latestSlot(now, []string{"05:00"}); ok {
		t.Fatal("missed schedule caught up")
	}
	loc, _ := time.LoadLocation("Europe/Berlin")
	if _, _, ok := latestSlot(time.Date(2026, 10, 19, 5, 1, 0, 0, loc), []string{"05:00"}); !ok {
		t.Fatal("timezone slot missing")
	}
}
func TestPublicBoundary(t *testing.T) {
	s, _ := mockService(t)
	h := s.PublicHandler("public-password")
	for _, path := range []string{"/management.html", "/setup", "/activation/api/settings", "/v0/management/config", "/v0/management/auth-files/download", "/v0/management/api-keys", "/%2e%2e/activation/api/settings", "/api/account/status/../config"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer public-password")
		req.Header.Set("X-Forwarded-For", "100.1.2.3")
		req.Header.Set("Tailscale-User-Login", "admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code == 200 {
			t.Fatal("public privilege escalation", path)
		}
	}
	req := httptest.NewRequest("GET", "/api/accounts", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	req.Header.Set("Authorization", "Bearer public-password")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), "LEAK-ME") || strings.Contains(w.Body.String(), "id_token") || strings.Contains(w.Body.String(), "auth_index") || strings.Contains(w.Body.String(), "/private/auth") {
		t.Fatal("sensitive DTO", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"id":"a","disabled":true,"access_token":"x"}`, `{"id":"a"}`} {
		req = httptest.NewRequest("POST", "/api/account/status", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer public-password")
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	req = httptest.NewRequest("POST", "/api/account/status", strings.NewReader(`{"id":"`+publicID("a")+`","disabled":true}`))
	req.Header.Set("Authorization", "Bearer public-password")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://attacker.test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("CSRF allowed")
	}
	req.Header.Del("Origin")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal("toggle failed", w.Code)
	}
}
func TestAdminAndHubRestrictions(t *testing.T) {
	s, _ := mockService(t)
	h := s.AdminHandler(func(_ context.Context, h string) (bool, error) { return h == "Bearer admin-secret", nil })
	req := httptest.NewRequest("PUT", "/activation/api/settings", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer public-password")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("operator changed schedules")
	}
	h = s.HubHandler()
	req = httptest.NewRequest("POST", "/v0/management/api-call", strings.NewReader(`{"auth_index":"index-a","method":"GET","url":"http://127.0.0.1/config","header":{"Authorization":"Bearer $TOKEN$"}}`))
	req.Header.Set("Authorization", "Bearer manager-secret")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("public hub SSRF accepted")
	}
	req = httptest.NewRequest("GET", "/v0/management/auth-files", nil)
	req.Header.Set("Authorization", "Bearer manager-secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), "LEAK-ME") || strings.Contains(w.Body.String(), "/private/auth") {
		t.Fatal("hub credential leak")
	}
}

func TestSpringForwardSlotIsSkipped(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 3, 29, 3, 1, 0, 0, loc)
	if _, _, ok := latestSlot(now, []string{"02:00"}); ok {
		t.Fatal("nonexistent spring-forward time was executed")
	}
}

func TestPasswordOnlySessionAndIdentity(t *testing.T) {
	s, _ := mockService(t)
	h := s.PublicHandler("public-password")
	// The login shell contains no accounts and needs no Basic-auth browser prompt.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("login shell", w.Code)
	}
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"public-password"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatal("login failed", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	req = httptest.NewRequest("GET", "/api/accounts", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), "@") || strings.Contains(w.Body.String(), "a.json") || !strings.Contains(w.Body.String(), `"email":"a"`) {
		t.Fatal("identity not redacted", w.Code, w.Body.String())
	}
	var list []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if list[0]["id"] != publicID("a") {
		t.Fatal("unredacted account id")
	}
	if validSession("public-password", sessionToken("public-password", time.Now().Add(-time.Minute).Unix())) || validSession("different-password", cookie.Value) || validSession("public-password", cookie.Value+"x") {
		t.Fatal("invalid session accepted")
	}
	req = httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"public-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("login CSRF allowed")
	}
}
func TestBankedResetUsesOnlySelectedAccountAndUUID(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/management/auth-files" {
			_, _ = w.Write([]byte(`{"files":[{"id":"a","name":"a.json","auth_index":"index-a","provider":"codex"}]}`))
			return
		}
		if r.URL.Path != "/v0/management/api-call" {
			t.Error("unexpected path", r.URL.Path)
			return
		}
		var v struct {
			Index  string            `json:"auth_index"`
			URL    string            `json:"url"`
			Method string            `json:"method"`
			Data   string            `json:"data"`
			Header map[string]string `json:"header"`
		}
		_ = json.NewDecoder(r.Body).Decode(&v)
		if v.Index != "index-a" || v.Method != "POST" || v.URL != "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume" || v.Header["Authorization"] != "Bearer $TOKEN$" {
			t.Fatal("wrong redemption target")
		}
		if v.Data != `{"redeem_request_id":"12345678-1234-1234-1234-123456789abc"}` {
			t.Error("wrong redemption payload", v.Data)
		}
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "body": `{"code":"reset"}`})
	}))
	defer upstream.Close()
	s, e := New(func(context.Context) (Connection, error) { return Connection{URL: upstream.URL, Key: "secret"}, nil }, filepath.Join(t.TempDir(), "settings.json"))
	if e != nil {
		t.Fatal(e)
	}
	h := s.PublicHandler("public-password")
	for _, body := range []string{`{"id":"` + publicID("a") + `","request_id":"invalid"}`, `{"id":"` + publicID("a") + `","request_id":"12345678-1234-1234-1234-123456789abc","url":"https://evil.test"}`} {
		req := httptest.NewRequest("POST", "/api/account/reset", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer public-password")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatal("unsafe reset allowed", w.Code)
		}
	}
	req := httptest.NewRequest("POST", "/api/account/reset", strings.NewReader(`{"id":"`+publicID("a")+`","request_id":"12345678-1234-1234-1234-123456789abc"}`))
	req.Header.Set("Authorization", "Bearer public-password")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || calls != 1 {
		t.Fatal("reset failed", w.Code, calls)
	}
}

func TestPublicPerAccountWindows(t *testing.T) {
	s, calls := mockService(t)
	// An edit must preserve other accounts and scheduler deduplication records.
	s.settings.Schedules = []Schedule{{AccountID: "other", Enabled: true, Times: []string{"08:00"}, Model: "gpt-6-luna", Effort: "low"}}
	s.settings.Runs["other"] = Run{Slot: "saved", Result: "five-hour window started"}
	h := s.PublicHandler("public-password")
	request := func(method, path, body, origin string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.Header.Set("Authorization", "Bearer public-password")
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := request("GET", "/api/windows", "", "", false); w.Code != 401 {
		t.Fatal("anonymous windows", w.Code)
	}
	body := `{"id":"` + publicID("a") + `","enabled":true,"times":["05:00","10:00"]}`
	if w := request("PUT", "/api/account/window", body, "https://evil.test", true); w.Code != 403 {
		t.Fatal("window CSRF allowed", w.Code)
	}
	if w := request("PUT", "/api/account/window", strings.TrimSuffix(body, "}")+`,"model":"other"}`, "", true); w.Code != 400 {
		t.Fatal("arbitrary config allowed", w.Code)
	}
	if w := request("PUT", "/api/account/window", body, "", true); w.Code != 200 {
		t.Fatal("save window", w.Code, w.Body.String())
	}
	settings := s.Settings()
	if len(settings.Schedules) != 2 || settings.Schedules[0].AccountID != "other" || settings.Runs["other"].Slot != "saved" {
		t.Fatal("changed another account or runs")
	}
	if w := request("GET", "/api/windows", "", "", true); w.Code != 200 || strings.Contains(w.Body.String(), `"account_id"`) || strings.Contains(w.Body.String(), "@") || !strings.Contains(w.Body.String(), publicID("a")) {
		t.Fatal("public window DTO", w.Code, w.Body.String())
	}
	for _, bad := range []string{`{"id":"` + publicID("a") + `","enabled":true,"times":[]}`, `{"id":"` + publicID("a") + `","enabled":true,"times":["25:00"]}`} {
		if w := request("PUT", "/api/account/window", bad, "", true); w.Code != 400 {
			t.Fatal("invalid times allowed", w.Code)
		}
	}
	for i := 0; i < 2; i++ {
		if w := request("POST", "/api/account/window/start", `{"id":"`+publicID("a")+`"}`, "", true); w.Code != 200 {
			t.Fatal("public start", w.Code, w.Body.String())
		}
	}
	if *calls != 1 {
		t.Fatal("active window restarted", *calls)
	}
	if w := request("GET", "/api/settings", "", "", true); w.Code != 404 {
		t.Fatal("general config became public")
	}
}
