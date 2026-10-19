// Package operator provides a deliberately narrow public account dashboard and
// a private, UI-configurable window-activation scheduler. OAuth stays in CPA.
package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Connection struct{ URL, Key string }
type Resolve func(context.Context) (Connection, error)
type Window struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     int64   `json:"reset_at"`
	Seconds     int64   `json:"limit_window_seconds"`
}
type Limits struct {
	Primary   *Window `json:"primary_window"`
	Secondary *Window `json:"secondary_window"`
}
type Quota struct {
	Plan   string  `json:"plan_type"`
	Limits *Limits `json:"rate_limit"`
}

// This DTO intentionally cannot decode or expose access/refresh/id tokens,
// configuration, filesystem paths, headers, proxies, or client API keys.
type Account struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Index      string `json:"auth_index"`
	Provider   string `json:"provider"`
	Email      string `json:"email"`
	Disabled   bool   `json:"disabled"`
	Status     string `json:"status"`
	Success    int64  `json:"success"`
	Failed     int64  `json:"failed"`
	Quota      *Quota `json:"quota,omitempty"`
	QuotaError bool   `json:"quota_error,omitempty"`
	Identity   struct {
		AccountID string `json:"chatgpt_account_id"`
	} `json:"id_token"`
}
type Schedule struct {
	NextRun   int64    `json:"next_run,omitempty"`
	AccountID string   `json:"account_id"`
	Enabled   bool     `json:"enabled"`
	Times     []string `json:"times"`
	Model     string   `json:"model"`
	Effort    string   `json:"effort"`
}
type Run struct {
	Slot   string `json:"slot"`
	At     int64  `json:"at"`
	Result string `json:"result"`
}
type Settings struct {
	Timezone  string         `json:"timezone"`
	Schedules []Schedule     `json:"schedules"`
	Runs      map[string]Run `json:"runs"`
}
type Service struct {
	resolve  Resolve
	path     string
	client   *http.Client
	mu       sync.Mutex
	settings Settings
	runMu    sync.Mutex
	cacheMu  sync.Mutex
	cached   []Account
	cacheAt  time.Time
}

func New(resolve Resolve, path string) (*Service, error) {
	s := &Service{resolve: resolve, path: path, client: &http.Client{Timeout: 65 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}, settings: Settings{Timezone: "Europe/Berlin", Schedules: []Schedule{}, Runs: map[string]Run{}}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(b, &s.settings); err != nil {
			return nil, fmt.Errorf("invalid activation settings")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if s.settings.Runs == nil {
		s.settings.Runs = map[string]Run{}
	}
	if err = validate(s.settings); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Service) call(ctx context.Context, method, path string, body any, out any) error {
	c, err := s.resolve(ctx)
	if err != nil {
		return err
	}
	var data io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		data = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+"/v0/management/"+path, data)
	if err != nil {
		return errors.New("invalid CPA connection")
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	r, err := s.client.Do(req)
	if err != nil {
		return errors.New("CPA connection failed")
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return fmt.Errorf("CPA returned HTTP %d", r.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(out)
}
func (s *Service) accounts(ctx context.Context) ([]Account, error) {
	var d struct {
		Files []Account `json:"files"`
	}
	if err := s.call(ctx, "GET", "auth-files", nil, &d); err != nil {
		return nil, err
	}
	if d.Files == nil {
		d.Files = []Account{}
	}
	return d.Files, nil
}
func (s *Service) provider(ctx context.Context, a Account, url, method string, data any) (string, error) {
	h := map[string]string{"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json", "OpenAI-Beta": "codex-1", "Originator": "Codex Desktop"}
	if a.Identity.AccountID != "" {
		h["Chatgpt-Account-Id"] = a.Identity.AccountID
	}
	b := map[string]any{"auth_index": a.Index, "method": method, "url": url, "header": h}
	if data != nil {
		raw, _ := json.Marshal(data)
		b["data"] = string(raw)
	}
	var out struct {
		Status int    `json:"status_code"`
		Body   string `json:"body"`
	}
	if err := s.call(ctx, "POST", "api-call", b, &out); err != nil {
		return "", err
	}
	if out.Status < 200 || out.Status >= 300 {
		return "", fmt.Errorf("provider returned HTTP %d", out.Status)
	}
	return out.Body, nil
}
func (s *Service) quota(ctx context.Context, a Account) (*Quota, error) {
	raw, err := s.provider(ctx, a, "https://chatgpt.com/backend-api/wham/usage", "GET", nil)
	if err != nil {
		return nil, err
	}
	var q Quota
	if err = json.Unmarshal([]byte(raw), &q); err != nil {
		return nil, errors.New("invalid provider quota")
	}
	return &q, nil
}
func (s *Service) Accounts(ctx context.Context) ([]Account, error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if time.Since(s.cacheAt) < 30*time.Second && s.cached != nil {
		return s.cached, nil
	}
	list, err := s.accounts(ctx)
	if err != nil {
		return nil, err
	}
	for i, a := range list {
		if a.Provider == "codex" && !a.Disabled {
			q, e := s.quota(ctx, a)
			list[i].Quota = q
			list[i].QuotaError = e != nil
		}
	}
	s.cached = list
	s.cacheAt = time.Now()
	return list, nil
}
func (s *Service) find(ctx context.Context, id string) (Account, error) {
	list, e := s.accounts(ctx)
	if e != nil {
		return Account{}, e
	}
	for _, a := range list {
		if a.ID == id {
			return a, nil
		}
	}
	return Account{}, errors.New("account not found")
}
func (s *Service) Toggle(ctx context.Context, id string, disabled bool) error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	a, e := s.find(ctx, id)
	if e != nil {
		return e
	}
	// Only a status mutation. No arbitrary auth-file fields or paths accepted.
	e = s.call(ctx, "PATCH", "auth-files/status", map[string]any{"name": a.Name, "disabled": disabled}, nil)
	s.cacheMu.Lock()
	s.cacheAt = time.Time{}
	s.cacheMu.Unlock()
	return e
}
func validate(v Settings) error {
	if _, e := time.LoadLocation(v.Timezone); e != nil {
		return errors.New("invalid timezone")
	}
	if len(v.Schedules) > 100 {
		return errors.New("too many schedules")
	}
	ids := map[string]bool{}
	for _, x := range v.Schedules {
		if x.AccountID == "" || ids[x.AccountID] {
			return errors.New("missing or duplicate account")
		}
		ids[x.AccountID] = true
		if x.Model != "gpt-6-luna" || x.Effort != "low" {
			return errors.New("activation uses gpt-6-luna with low reasoning only")
		}
		if len(x.Times) > 24 {
			return errors.New("too many daily times")
		}
		times := map[string]bool{}
		for _, t := range x.Times {
			p, e := time.Parse("15:04", t)
			if e != nil || p.Format("15:04") != t || times[t] {
				return errors.New("times must be unique HH:MM values")
			}
			times[t] = true
		}
	}
	return nil
}
func (s *Service) saveLocked() error {
	b, e := json.MarshalIndent(s.settings, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(s.path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".activation-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), s.path)
}
func (s *Service) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.settings)
	var v Settings
	_ = json.Unmarshal(b, &v)
	loc, _ := time.LoadLocation(v.Timezone)
	if loc != nil {
		now := time.Now().In(loc)
		for i, x := range v.Schedules {
			if !x.Enabled {
				continue
			}
			for day := 0; day < 2; day++ {
				date := now.AddDate(0, 0, day)
				for _, clock := range x.Times {
					t, _ := time.Parse("15:04", clock)
					next := time.Date(date.Year(), date.Month(), date.Day(), t.Hour(), t.Minute(), 0, 0, loc)
					if next.Format("15:04") != clock {
						continue
					}
					if next.After(now) && (v.Schedules[i].NextRun == 0 || next.Unix() < v.Schedules[i].NextRun) {
						v.Schedules[i].NextRun = next.Unix()
					}
				}
			}
		}
	}
	return v
}
func (s *Service) Configure(ctx context.Context, v Settings) error {
	if e := validate(v); e != nil {
		return e
	}
	list, e := s.accounts(ctx)
	if e != nil {
		return e
	}
	ids := map[string]bool{}
	for _, a := range list {
		if a.Provider == "codex" {
			ids[a.ID] = true
		}
	}
	for _, x := range v.Schedules {
		if !ids[x.AccountID] {
			return errors.New("schedule must select an existing Codex account")
		}
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.settings
	for i := range v.Schedules {
		v.Schedules[i].NextRun = 0
	}
	v.Runs = s.settings.Runs
	s.settings = v
	if e = s.saveLocked(); e != nil {
		s.settings = old
	}
	return e
}

// latestSlot ignores missed schedules older than ten minutes. No catch-up burst
// after downtime or clock jumps. Local date/time makes fall-back DST run once.
func latestSlot(now time.Time, times []string) (string, time.Time, bool) {
	sorted := append([]string(nil), times...)
	sort.Strings(sorted)
	for i := len(sorted) - 1; i >= 0; i-- {
		t, _ := time.Parse("15:04", sorted[i])
		slot := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
		if slot.Format("15:04") != sorted[i] {
			continue
		}
		if !now.Before(slot) && now.Sub(slot) < 10*time.Minute {
			return now.Format("2006-01-02") + "T" + sorted[i], slot, true
		}
	}
	return "", time.Time{}, false
}
func (s *Service) record(id, slot, result string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, had := s.settings.Runs[id]
	s.settings.Runs[id] = Run{Slot: slot, At: now.Unix(), Result: result}
	if e := s.saveLocked(); e != nil {
		if had {
			s.settings.Runs[id] = old
		} else {
			delete(s.settings.Runs, id)
		}
		return e
	}
	return nil
}
func (s *Service) activate(ctx context.Context, x Schedule, slot string, scheduledAt, now time.Time) (string, error) {
	a, e := s.find(ctx, x.AccountID)
	if e != nil {
		return "", e
	}
	if a.Provider != "codex" {
		return "", errors.New("only Codex accounts can be activated")
	}
	if a.Disabled {
		return "account disabled", nil
	}
	q, e := s.quota(ctx, a)
	if e != nil {
		return "", e
	}
	if q.Limits == nil || q.Limits.Primary == nil || q.Limits.Primary.Seconds != 18000 {
		return "no five-hour window reported", nil
	}
	w := q.Limits.Primary
	if q.Limits.Secondary != nil && q.Limits.Secondary.UsedPercent >= 100 {
		return "weekly allowance exhausted", nil
	}
	if w.ResetAt > now.Unix() {
		if slot != "manual" && w.ResetAt < scheduledAt.Add(10*time.Minute).Unix() {
			return "waiting for reset", nil
		}
		return "window already active", nil
	}
	// Persist the attempt BEFORE sending. On errors/crashes do not duplicate an
	// uncertain provider request. Never retry/fail over to another credential.
	if e = s.record(a.ID, slot, "request attempted", now); e != nil {
		return "", e
	}
	input := map[string]any{"model": x.Model, "instructions": "Reply OK only.", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "OK?"}}}}, "reasoning": map[string]string{"effort": x.Effort}, "stream": true, "store": false}
	raw, e := s.provider(ctx, a, "https://chatgpt.com/backend-api/codex/responses", "POST", input)
	if e != nil {
		return "", e
	}
	if !strings.Contains(raw, "response.completed") {
		return "", errors.New("activation response did not complete")
	}
	after, e := s.quota(ctx, a)
	if e != nil {
		return "request completed; verification unavailable", nil
	}
	if after.Limits != nil && after.Limits.Primary != nil && after.Limits.Primary.ResetAt > now.Unix() {
		return "five-hour window started", nil
	}
	return "request completed; window start unconfirmed", nil
}
func (s *Service) RunNow(ctx context.Context, id string) (string, error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	x := Schedule{AccountID: id, Model: "gpt-6-luna", Effort: "low"}
	now := time.Now()
	result, e := s.activate(ctx, x, "manual", now, now)
	if e != nil {
		result = e.Error()
	}
	if re := s.record(id, "manual", result, now); re != nil {
		return "", re
	}
	return result, e
}
func (s *Service) Tick(ctx context.Context, now time.Time) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	v := s.Settings()
	loc, e := time.LoadLocation(v.Timezone)
	if e != nil {
		return
	}
	now = now.In(loc)
	for _, x := range v.Schedules {
		if !x.Enabled {
			continue
		}
		slot, at, ok := latestSlot(now, x.Times)
		if !ok || v.Runs[x.AccountID].Slot == slot {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 90*time.Second)
		result, err := s.activate(c, x, slot, at, now)
		cancel()
		if result == "waiting for reset" {
			continue
		}
		if err != nil {
			result = err.Error()
		}
		_ = s.record(x.AccountID, slot, result, now)
	}
}
func (s *Service) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.Tick(ctx, now)
			}
		}
	}()
}
