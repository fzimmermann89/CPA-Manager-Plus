package operator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
