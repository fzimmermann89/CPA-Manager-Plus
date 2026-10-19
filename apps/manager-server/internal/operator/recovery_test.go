package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const recoveredQuota = `{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":24,"limit_window_seconds":18000},"secondary_window":{"used_percent":4,"limit_window_seconds":604800}}}`
const exhaustedQuota = `{"rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"used_percent":100,"limit_window_seconds":18000}}}`

func TestRecoveryQuotaRequiresExplicitHealthyLimits(t *testing.T) {
	for _, tc := range []struct {
		name, raw     string
		usable, known bool
	}{
		{"restored", recoveredQuota, true, true},
		{"exhausted", exhaustedQuota, false, true},
		{"empty", "{}", false, false},
		{"malformed", "not json", false, false},
		{"missing flags", `{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000}}}`, false, false},
		{"missing percent", `{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"limit_window_seconds":18000}}}`, false, false},
		{"missing window", `{"rate_limit":{"allowed":true,"limit_reached":false}}`, false, false},
		{"invalid percent", strings.Replace(recoveredQuota, `"used_percent":24`, `"used_percent":-1`, 1), false, false},
		{"weekly exhausted", strings.Replace(recoveredQuota, `"used_percent":4`, `"used_percent":100`, 1), false, true},
		{"additional bucket exhausted", strings.TrimSuffix(recoveredQuota, "}") + `,"additional_rate_limits":[{"rate_limit":{"allowed":false,"limit_reached":true}}]}`, false, true},
		{"additional bucket unknown", strings.TrimSuffix(recoveredQuota, "}") + `,"additional_rate_limits":[{}]}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usable, known := recoveryQuota(tc.raw)
			if usable != tc.usable || known != tc.known {
				t.Fatalf("got %v,%v", usable, known)
			}
		})
	}
}

type recoveryFixture struct {
	mu                                                                          sync.Mutex
	blocked, disabled, disableDuringRead, changeDuringRead, failRead, failClear bool
	message, deadline, quota                                                    string
	reads, clears                                                               int
}

func recoveryService(t *testing.T) (*Service, *recoveryFixture) {
	t.Helper()
	f := &recoveryFixture{blocked: true, message: `{"error":{"type":"usage_limit_reached"}}`, deadline: "2099-01-01T00:00:00Z", quota: recoveredQuota}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer test-management" {
			t.Error("missing management auth")
		}
		switch r.URL.Path {
		case "/v0/management/auth-files":
			status, message := "active", ""
			if f.blocked {
				status, message = "error", f.message
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{"id": "recover", "name": "private.json", "auth_index": "selected-index", "provider": "codex", "email": "private@example.test", "disabled": f.disabled, "unavailable": f.blocked, "status": status, "status_message": message, "next_retry_after": f.deadline}}})
		case "/v0/management/api-call":
			var v struct {
				Index  string            `json:"auth_index"`
				Method string            `json:"method"`
				URL    string            `json:"url"`
				Header map[string]string `json:"header"`
				Data   string            `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&v)
			if v.Index != "selected-index" || v.Method != "GET" || v.URL != "https://chatgpt.com/backend-api/wham/usage" || v.Data != "" || v.Header["Authorization"] != "Bearer $TOKEN$" {
				t.Error("recovery must only read selected-account quota", v.Method, v.URL)
			}
			f.reads++
			if f.disableDuringRead {
				f.disabled = true
			}
			if f.changeDuringRead {
				f.deadline = "2099-01-02T00:00:00Z"
			}
			status := 200
			if f.failRead {
				status = 429
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": status, "body": f.quota})
		case "/v0/management/reset-quota":
			var v map[string]string
			_ = json.NewDecoder(r.Body).Decode(&v)
			if r.Method != "POST" || len(v) != 1 || v["auth_index"] != "selected-index" {
				t.Error("wrong cooldown clear target")
			}
			f.clears++
			if f.failClear {
				w.WriteHeader(503)
				return
			}
			f.blocked = false
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	s, err := New(func(context.Context) (Connection, error) {
		return Connection{URL: server.URL, Key: "test-management"}, nil
	}, filepath.Join(t.TempDir(), "activation.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}
func (f *recoveryFixture) change(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }
func (f *recoveryFixture) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads, f.clears
}
func TestRecoveryDetectsExternalResetBeforeOldDeadline(t *testing.T) {
	s, f := recoveryService(t)
	now := time.Now()
	f.change(func() { f.quota = exhaustedQuota })
	if err := s.RecoverCooldowns(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if _, clears := f.counts(); clears != 0 {
		t.Fatal("cleared genuine exhaustion")
	}
	// External reset changes only the provider quota; CPA still reports 2099.
	f.change(func() { f.quota = recoveredQuota })
	if err := s.RecoverCooldowns(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if reads, clears := f.counts(); reads != 2 || clears != 1 {
		t.Fatal("external reset not recovered", reads, clears)
	}
	if err := s.RecoverCooldowns(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if reads, clears := f.counts(); reads != 2 || clears != 1 {
		t.Fatal("active account was polled/reset again", reads, clears)
	}
}
func TestRecoverySkipsUnsafeCandidates(t *testing.T) {
	for _, kind := range []string{"disabled", "active", "nonquota", "quota read failure", "missing quota", "disabled during read", "new cooldown during read"} {
		t.Run(kind, func(t *testing.T) {
			s, f := recoveryService(t)
			f.change(func() {
				switch kind {
				case "disabled":
					f.disabled = true
				case "active":
					f.blocked = false
				case "nonquota":
					f.message = `{"error":{"type":"authentication_error"}}`
				case "quota read failure":
					f.failRead = true
				case "missing quota":
					f.quota = "{}"
				case "disabled during read":
					f.disableDuringRead = true
				case "new cooldown during read":
					f.changeDuringRead = true
				}
			})
			if err := s.RecoverCooldowns(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			if reads, clears := f.counts(); clears != 0 || ((kind == "disabled" || kind == "active" || kind == "nonquota") && reads != 0) {
				t.Fatal("unsafe recovery", reads, clears)
			}
		})
	}
}
func TestRecoveryBacksOffRepeated429ButKeepsRechecking(t *testing.T) {
	s, f := recoveryService(t)
	now := time.Now()
	for _, step := range []struct{ minutes, clears int }{{0, 1}, {1, 2}, {2, 2}, {3, 3}, {4, 3}, {7, 4}} {
		// Mimic a fresh real 429 after each local clear despite optimistic metadata.
		f.change(func() { f.blocked = true })
		if err := s.RecoverCooldowns(context.Background(), now.Add(time.Duration(step.minutes)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if _, clears := f.counts(); clears != step.clears {
			t.Fatal("backoff did not hold", step.minutes, clears)
		}
	}
	if reads, _ := f.counts(); reads != 6 {
		t.Fatal("stopped checking quota during backoff", reads)
	}
	if recoveryDelay(100) != 15*time.Minute {
		t.Fatal("backoff cap")
	}
}
func TestRecoveryBacksOffFailedClearAndIgnoresDashboardCache(t *testing.T) {
	s, f := recoveryService(t)
	now := time.Now()
	f.change(func() { f.failClear = true })
	_ = s.RecoverCooldowns(context.Background(), now)
	_ = s.RecoverCooldowns(context.Background(), now.Add(30*time.Second))
	if reads, clears := f.counts(); reads != 2 || clears != 1 {
		t.Fatal("clear error retried too soon", reads, clears)
	}
	f.change(func() { f.failClear = false; f.quota = exhaustedQuota })
	s.cached = []Account{{ID: "recover", Quota: &Quota{Limits: &Limits{Primary: &Window{UsedPercent: 0, Seconds: 18000}}}}}
	s.cacheAt = now
	_ = s.RecoverCooldowns(context.Background(), now.Add(time.Minute))
	if _, clears := f.counts(); clears != 1 {
		t.Fatal("cached dashboard authorized recovery")
	}
}
