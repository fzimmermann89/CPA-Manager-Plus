package operator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSharedAuthOwnerRefusesIndependentDeviceLogin(t *testing.T) {
	s, _ := mockService(t)
	s.ConfigureAuthOwner("https://api.example.test/privat/\"&")
	r := httptest.NewRequest(http.MethodPost, "/api/codex/device/start", strings.NewReader(`{"mode":"add"}`))
	r.Header.Set("Authorization", "Bearer operator-password")
	w := httptest.NewRecorder()
	s.PublicHandler("operator-password").ServeHTTP(w, r)
	if w.Code != http.StatusConflict || s.device != nil {
		t.Fatalf("shared follower started independent device login: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.PublicHandler("operator-password").ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(w.Body.String(), "__AUTH_OWNER__") || !strings.Contains(w.Body.String(), "privat/&#34;&amp;") {
		t.Fatal("owner URL was not safely embedded")
	}
}

func TestDeviceLoginRetryResumesSameAccountCode(t *testing.T) {
	s, _ := mockService(t)
	bin := filepath.Join(t.TempDir(), "device-login")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'Codex device URL: https://auth.openai.com/codex/device\\nCodex device code: TEST-12345\\n'\nexec sleep 120\n"), 0700); err != nil {
		t.Fatal(err)
	}
	d := newDeviceLoginManager(bin, "unused-test-config")
	job, err := d.start(context.Background(), s, "reauthorize", publicID("a"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.mu.Lock()
		if d.active != nil && d.active.cmd != nil && d.active.cmd.Process != nil {
			_ = d.active.cmd.Process.Kill()
		}
		d.mu.Unlock()
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, err := d.status(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Code == "TEST-12345" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device code was not parsed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	resumed, err := d.start(context.Background(), s, "reauthorize", publicID("a"))
	if err != nil || resumed.ID != job.ID || resumed.Code != "TEST-12345" {
		t.Fatal("retry lost existing code", err, resumed.ID, resumed.Code)
	}
	if _, err := d.start(context.Background(), s, "add", ""); err == nil {
		t.Fatal("another account took over pending login")
	}
}
