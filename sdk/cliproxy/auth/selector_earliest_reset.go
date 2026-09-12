package auth

import (
	"context"
	"strconv"
	"strings"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

// EarliestResetSelector prefers the soonest upcoming Claude weekly quota reset
// within the highest available priority tier. Unknown resets remain eligible
// alongside the earliest reset so traffic can refresh their passive observations.
// Other providers and mixed-provider pools retain round-robin selection.
// Wrap this selector in SessionAffinitySelector to preserve established bindings.
type EarliestResetSelector struct {
	roundRobin RoundRobinSelector
	nowFunc    func() time.Time
}

func (s *EarliestResetSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	now := time.Now()
	if s.nowFunc != nil {
		now = s.nowFunc()
	}
	available, errAvailable := getSelectorAvailableAuths(ctx, auths, provider, model, now)
	if errAvailable != nil {
		return nil, errAvailable
	}
	available = earliestClaudeResetCandidates(available, now)
	if ctx == nil {
		ctx = context.Background()
	}
	// Availability was already checked with the resolved route model (or our
	// clock above). The delegate must not reinterpret it using a different clock.
	ctx = context.WithValue(ctx, prevalidatedAuthCandidatesKey{}, true)
	return s.roundRobin.Pick(ctx, provider, model, opts, available)
}

func earliestClaudeResetCandidates(auths []*Auth, now time.Time) []*Auth {
	var earliest time.Time
	resets := make([]time.Time, len(auths))
	for i, candidate := range auths {
		if candidate == nil || !strings.EqualFold(strings.TrimSpace(candidate.Provider), "claude") {
			return auths
		}
		reset := claudeWeeklyReset(candidate.Quota.Signals, now)
		resets[i] = reset
		if !reset.IsZero() && (earliest.IsZero() || reset.Before(earliest)) {
			earliest = reset
		}
	}
	if earliest.IsZero() {
		return auths
	}
	preferred := make([]*Auth, 0, len(auths))
	for i, candidate := range auths {
		if resets[i].IsZero() || resets[i].Equal(earliest) {
			preferred = append(preferred, candidate)
		}
	}
	return preferred
}

func claudeWeeklyReset(signals map[string]string, now time.Time) time.Time {
	for key, value := range signals {
		if !strings.EqualFold(key, "Anthropic-Ratelimit-Unified-7d-Reset") {
			continue
		}
		seconds, errParse := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if errParse != nil {
			return time.Time{}
		}
		reset := time.Unix(seconds, 0)
		// Expired observations cannot predict the next rolling window. Implausible
		// timestamps (including milliseconds) must not demote an account forever.
		if !reset.After(now) || reset.After(now.Add(7*24*time.Hour)) {
			return time.Time{}
		}
		return reset
	}
	return time.Time{}
}
