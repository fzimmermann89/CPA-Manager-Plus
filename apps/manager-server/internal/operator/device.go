package operator

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DeviceLogin exposes only the one-time device code and coarse completion
// state. CLI output containing credential material is never sent to callers.
type DeviceLogin struct {
	ID        string    `json:"id"`
	Mode      string    `json:"mode"`
	URL       string    `json:"url,omitempty"`
	Code      string    `json:"code,omitempty"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	StartedAt time.Time `json:"started_at"`

	cmd         *exec.Cmd
	before      map[string]bool
	beforeCount int
	targetID    string
}

type deviceLoginManager struct {
	mu          sync.Mutex
	bin, config string
	active      *DeviceLogin
}

func newDeviceLoginManager(bin, config string) *deviceLoginManager {
	if bin == "" || config == "" {
		return nil
	}
	return &deviceLoginManager{bin: bin, config: config}
}

func randomDeviceID() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (d *deviceLoginManager) start(ctx context.Context, s *Service, mode, accountID string) (DeviceLogin, error) {
	if d == nil {
		return DeviceLogin{}, errors.New("device login is unavailable")
	}
	if mode != "add" && mode != "reauthorize" {
		return DeviceLogin{}, errors.New("invalid action")
	}
	var target Account
	if mode == "reauthorize" {
		id, err := s.resolvePublicID(ctx, accountID)
		if err != nil {
			return DeviceLogin{}, errors.New("account not found")
		}
		target, err = s.find(ctx, id)
		if err != nil || target.Provider != "codex" {
			return DeviceLogin{}, errors.New("Codex account not found")
		}
		if target.Email == "" {
			return DeviceLogin{}, errors.New("selected account has no verifiable email")
		}
	} else if accountID != "" {
		return DeviceLogin{}, errors.New("account is not accepted for add")
	}
	list, err := s.accounts(ctx)
	if err != nil {
		return DeviceLogin{}, errors.New("CPA unavailable")
	}
	before := map[string]bool{}
	matchingEmail := 0
	for _, a := range list {
		if a.Provider == "codex" {
			before[a.ID] = true
			if mode == "reauthorize" && strings.EqualFold(a.Email, target.Email) {
				matchingEmail++
			}
		}
	}
	if mode == "reauthorize" && matchingEmail != 1 {
		return DeviceLogin{}, errors.New("account email is ambiguous; use private management")
	}
	id, err := randomDeviceID()
	if err != nil {
		return DeviceLogin{}, errors.New("could not start device login")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active != nil && d.active.Status == "waiting" {
		if d.active.Mode == mode && d.active.targetID == target.ID {
			return *d.active, nil
		}
		return DeviceLogin{}, errors.New("another account login is already running")
	}
	jobCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	job := &DeviceLogin{ID: id, Mode: mode, Status: "starting", StartedAt: time.Now().UTC(), before: before, beforeCount: len(before), targetID: target.ID}
	job.cmd = exec.CommandContext(jobCtx, d.bin, "-config", d.config, "-codex-device-login", "-no-browser")
	stdout, err := job.cmd.StdoutPipe()
	if err != nil {
		return DeviceLogin{}, errors.New("could not start device login")
	}
	job.cmd.Stderr = io.Discard
	if err = job.cmd.Start(); err != nil {
		return DeviceLogin{}, errors.New("could not start device login")
	}
	job.Status = "waiting"
	d.active = job
	go func() {
		scan := bufio.NewScanner(stdout)
		scan.Buffer(make([]byte, 1024), 64*1024)
		for scan.Scan() {
			line := strings.TrimSpace(scan.Text())
			d.mu.Lock()
			if d.active == job {
				switch {
				case strings.HasPrefix(line, "Codex device URL:"):
					job.URL = "https://auth.openai.com/codex/device"
				case strings.HasPrefix(line, "Codex device code:"):
					job.Code = strings.TrimSpace(strings.TrimPrefix(line, "Codex device code:"))
				}
			}
			d.mu.Unlock()
		}
		waitErr := job.cmd.Wait()
		cancel()
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.active != job {
			return
		}
		if waitErr != nil {
			job.Status = "failed"
			job.Message = "Device authorization did not complete."
			return
		}
		updated, listErr := s.accounts(context.Background())
		if listErr != nil {
			job.Status = "failed"
			job.Message = "Login finished, but CPA could not confirm the account."
			return
		}
		if mode == "add" {
			for _, a := range updated {
				if a.Provider == "codex" && !before[a.ID] {
					job.Status = "complete"
					job.Message = "Codex account added."
					s.cacheMu.Lock()
					s.cacheAt = time.Time{}
					s.cacheMu.Unlock()
					return
				}
			}
			job.Status = "failed"
			job.Message = "No new Codex account appeared. If this was an existing identity, its credential may have been refreshed."
			return
		}
		codexCount := 0
		for _, a := range updated {
			if a.Provider == "codex" {
				codexCount++
			}
		}
		if codexCount > job.beforeCount {
			job.Status = "failed"
			job.Message = "Login created a separate Codex account; the selected account was not confirmed as updated."
			return
		}
		for _, a := range updated {
			if a.Provider == "codex" && (a.ID == target.ID || (target.Email != "" && strings.EqualFold(a.Email, target.Email))) {
				job.Status = "complete"
				job.Message = "Selected Codex account reauthorized."
				s.cacheMu.Lock()
				s.cacheAt = time.Time{}
				s.cacheMu.Unlock()
				return
			}
		}
		job.Status = "failed"
		job.Message = "The selected account could not be confirmed after login."
	}()
	return *job, nil
}

func (d *deviceLoginManager) status(id string) (DeviceLogin, error) {
	if d == nil {
		return DeviceLogin{}, errors.New("device login is unavailable")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active == nil || d.active.ID != id {
		return DeviceLogin{}, errors.New("device login not found")
	}
	return *d.active, nil
}
