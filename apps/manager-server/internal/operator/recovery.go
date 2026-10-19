package operator

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

const recoveryInterval = time.Minute

type recoveryState struct {
	Attempts  int
	NextClear time.Time
	LastClear time.Time
}

// Missing quota fields are unknown, never permission to clear a cooldown.
// This view is deliberately separate from the display DTO's zero defaults.
type recoveryWindow struct {
	Used    *float64 `json:"used_percent"`
	Seconds *int64   `json:"limit_window_seconds"`
}
type recoveryLimit struct {
	Allowed   *bool           `json:"allowed"`
	Reached   *bool           `json:"limit_reached"`
	Primary   *recoveryWindow `json:"primary_window"`
	Secondary *recoveryWindow `json:"secondary_window"`
}

func (l *recoveryLimit) usable() (available, known bool) {
	if l == nil || l.Allowed == nil || l.Reached == nil {
		return false, false
	}
	if !*l.Allowed || *l.Reached {
		return false, true
	}
	if l.Primary == nil {
		return false, false
	}
	for _, w := range []*recoveryWindow{l.Primary, l.Secondary} {
		if w == nil {
			continue
		}
		if w.Used == nil || w.Seconds == nil || *w.Seconds <= 0 || *w.Used < 0 || *w.Used > 100 {
			return false, false
		}
		if *w.Used >= 100 {
			return false, true
		}
	}
	return true, true
}
func recoveryQuota(raw string) (available, known bool) {
	var q struct {
		Limit      *recoveryLimit `json:"rate_limit"`
		Additional []struct {
			Limit *recoveryLimit `json:"rate_limit"`
		} `json:"additional_rate_limits"`
	}
	if json.Unmarshal([]byte(raw), &q) != nil {
		return false, false
	}
	if available, known = q.Limit.usable(); !available {
		return available, known
	}
	// Clearing a credential clears every local model ban. Do not mistake a
	// healthy general bucket for recovery of an exhausted additional bucket.
	for _, extra := range q.Additional {
		if available, known = extra.Limit.usable(); !available {
			return available, known
		}
	}
	return true, true
}
func quotaBlocked(a Account) bool {
	if a.Disabled || a.Provider != "codex" || !a.Unavailable {
		return false
	}
	var message struct {
		Type  string `json:"type"`
		Code  string `json:"code"`
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(a.StatusMessage), &message) != nil {
		return false
	}
	return message.Type == "usage_limit_reached" || message.Code == "usage_limit_reached" || message.Error.Type == "usage_limit_reached" || message.Error.Code == "usage_limit_reached"
}
func recoveryDelay(attempt int) time.Duration {
	delay := time.Minute
	for i := 1; i < attempt && delay < 15*time.Minute; i++ {
		delay *= 2
	}
	if delay > 15*time.Minute {
		return 15 * time.Minute
	}
	return delay
}

// RecoverCooldowns polls quota metadata only: it sends no inference prompts,
// consumes no banked resets, and never changes account enable/disable state.
func (s *Service) RecoverCooldowns(ctx context.Context, now time.Time) error {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	list, err := s.accounts(ctx)
	if err != nil {
		return err
	}
	if s.recovery == nil {
		s.recovery = map[string]recoveryState{}
	}
	present := map[string]bool{}
	for _, a := range list {
		present[a.ID] = true
		state := s.recovery[a.ID]
		if !quotaBlocked(a) {
			if a.Disabled || (!a.Unavailable && now.Sub(state.LastClear) >= 30*time.Minute) {
				delete(s.recovery, a.ID)
			}
			continue
		}
		// Always recheck fresh upstream quota, even while a clear retry backs off.
		// The existing dashboard's cached readings cannot authorize recovery.
		raw, e := s.provider(ctx, a, "https://chatgpt.com/backend-api/wham/usage", "GET", nil)
		if e != nil {
			continue
		}
		usable, known := recoveryQuota(raw)
		if !known {
			continue
		}
		if !usable {
			delete(s.recovery, a.ID)
			continue
		}
		if now.Before(state.NextClear) {
			continue
		}

		// Serialize the final check with toggles, redemptions, and scheduled starts.
		// A new 429 or user disabling an account during the read invalidates it.
		s.runMu.Lock()
		current, e := s.find(ctx, a.ID)
		if e != nil || !quotaBlocked(current) || current.NextRetryAfter != a.NextRetryAfter || current.StatusMessage != a.StatusMessage || current.Index != a.Index {
			s.runMu.Unlock()
			continue
		}
		state.Attempts++
		state.LastClear = now
		state.NextClear = now.Add(recoveryDelay(state.Attempts))
		s.recovery[a.ID] = state
		e = s.call(ctx, "POST", "reset-quota", map[string]string{"auth_index": current.Index}, nil)
		s.runMu.Unlock()
		if e != nil {
			log.Printf("quota recovery: local cooldown clear failed account=%s", publicID(a.ID))
			continue
		}
		s.cacheMu.Lock()
		s.cacheAt = time.Time{}
		s.cacheMu.Unlock()
		log.Printf("quota recovery: cleared stale cooldown account=%s", publicID(a.ID))
	}
	for id := range s.recovery {
		if !present[id] {
			delete(s.recovery, id)
		}
	}
	return nil
}
func (s *Service) recoveryLoop(ctx context.Context) {
	log.Printf("quota recovery: enabled; interval=%s", recoveryInterval)
	// Reconcile immediately at startup, then once a minute. Each pass is bounded
	// and separate from the window scheduler, so neither timer blocks the other.
	ticker := time.NewTicker(recoveryInterval)
	defer ticker.Stop()
	for {
		c, cancel := context.WithTimeout(ctx, 90*time.Second)
		if err := s.RecoverCooldowns(c, time.Now()); err != nil && ctx.Err() == nil {
			log.Print("quota recovery: CPA account list unavailable")
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
