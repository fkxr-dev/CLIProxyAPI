package auth

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func resetTestAuth(id string, reset time.Time) *Auth {
	return &Auth{ID: id, Provider: "claude", Status: StatusActive, Quota: QuotaState{
		Signals: map[string]string{"Anthropic-Ratelimit-Unified-7d-Reset": strconv.FormatInt(reset.Unix(), 10)},
	}}
}

func TestEarliestResetSelection(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		modify func([]*Auth)
		want   []string
	}{
		{name: "nearest weekly reset", want: []string{"b", "b", "b"}},
		{name: "ties rotate", modify: func(a []*Auth) { a[2].Quota = a[1].Quota.Clone() }, want: []string{"b", "c", "b"}},
		{name: "priority wins", modify: func(a []*Auth) { a[0].Attributes = map[string]string{"priority": "10"} }, want: []string{"a", "a"}},
		{name: "disabled excluded", modify: func(a []*Auth) { a[1].Disabled = true }, want: []string{"c", "c"}},
		{name: "cooldown excluded", modify: func(a []*Auth) {
			a[1].Quota.Exceeded = true
			a[1].Quota.Reason = "credential_quota"
			a[1].Quota.NextRecoverAt = now.Add(time.Hour)
		}, want: []string{"c", "c"}},
		{name: "unknown gets discovery traffic", modify: func(a []*Auth) { a[0].Quota.Signals = nil }, want: []string{"a", "b", "a"}},
		{name: "expired gets discovery traffic", modify: func(a []*Auth) { a[0].Quota = resetTestAuth("", now).Quota }, want: []string{"a", "b", "a"}},
		{name: "other providers rotate", modify: func(a []*Auth) {
			for _, v := range a {
				v.Provider = "codex"
			}
		}, want: []string{"a", "b", "c"}},
		{name: "mixed providers rotate", modify: func(a []*Auth) { a[0].Provider = "codex" }, want: []string{"a", "b", "c"}},
		{name: "all unknown rotate", modify: func(a []*Auth) {
			for _, v := range a {
				v.Quota.Signals = nil
			}
		}, want: []string{"a", "b", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auths := []*Auth{resetTestAuth("a", now.Add(5*24*time.Hour)), resetTestAuth("b", now.Add(24*time.Hour)), resetTestAuth("c", now.Add(2*24*time.Hour))}
			if tc.modify != nil {
				tc.modify(auths)
			}
			s := &EarliestResetSelector{nowFunc: func() time.Time { return now }}
			for _, want := range tc.want {
				got, errPick := s.Pick(context.Background(), "mixed", "model", cliproxyexecutor.Options{}, auths)
				if errPick != nil || got.ID != want {
					t.Fatalf("got %v, %v; want %s", got, errPick, want)
				}
			}
		})
	}
}

func TestClaudeWeeklyResetValidation(t *testing.T) {
	now := time.Unix(1789171200, 0)
	for _, value := range []string{"", "garbage", "-1", strconv.FormatInt(now.Unix(), 10), strconv.FormatInt(now.Add(8*24*time.Hour).Unix(), 10), strconv.FormatInt(now.UnixMilli(), 10)} {
		if got := claudeWeeklyReset(map[string]string{"anthropic-ratelimit-unified-7d-reset": value}, now); !got.IsZero() {
			t.Fatalf("accepted invalid reset %q: %v", value, got)
		}
	}
	want := now.Add(time.Hour)
	if got := claudeWeeklyReset(map[string]string{"anthropic-ratelimit-unified-7d-reset": strconv.FormatInt(want.Unix(), 10), "Anthropic-Ratelimit-Unified-5h-Reset": strconv.FormatInt(now.Add(time.Minute).Unix(), 10)}, now); !got.Equal(want) {
		t.Fatalf("reset = %v, want weekly %v", got, want)
	}
}

func TestManagerEarliestResetAffinity(t *testing.T) {
	for _, mode := range []string{"none", "explicit", "lcp"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var selector Selector = &EarliestResetSelector{}
			if mode != "none" {
				affinity := NewSessionAffinitySelector(selector)
				t.Cleanup(affinity.Stop)
				selector = affinity
			}
			m := NewManager(nil, selector, nil)
			m.RegisterExecutor(schedulerProviderTestExecutor{provider: "claude"})
			const model = "reset-test-model"
			now := time.Now()
			a, b := resetTestAuth("a-"+t.Name(), now.Add(5*24*time.Hour)), resetTestAuth("b-"+t.Name(), now.Add(24*time.Hour))
			for _, candidate := range []*Auth{a, b} {
				registry.GetGlobalRegistry().RegisterClient(candidate.ID, "claude", []*registry.ModelInfo{{ID: model}})
				t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(candidate.ID) })
				if _, errRegister := m.Register(ctx, candidate); errRegister != nil {
					t.Fatal(errRegister)
				}
			}
			pick := func(session, want string) {
				t.Helper()
				opts := cliproxyexecutor.Options{Metadata: map[string]any{}}
				if mode == "explicit" {
					opts.Headers = http.Header{"X-Session-Id": []string{session}}
				}
				if mode == "lcp" {
					opts.SourceFormat = sdktranslator.FormatOpenAI
					opts.OriginalRequest = []byte(fmt.Sprintf(`{"messages":[{"role":"system","content":%q},{"role":"user","content":"hello"}]}`, session))
					opts.Metadata[cliproxyexecutor.CallerScopeMetadataKey] = "reset-test"
				}
				got, errPick := m.SelectAuth(ctx, "claude", model, opts)
				if errPick != nil || got.ID != want {
					t.Fatalf("session %s: got %v, %v; want %s", session, got, errPick, want)
				}
			}
			pick("stable", b.ID)
			// A fresh upstream observation changes the preferred reset immediately.
			observationCtx := logging.WithResponseHeadersHolder(ctx)
			logging.SetResponseHeaders(observationCtx, http.Header{"Anthropic-Ratelimit-Unified-7d-Reset": []string{strconv.FormatInt(now.Add(time.Hour).Unix(), 10)}})
			m.MarkResult(observationCtx, Result{AuthID: a.ID, Provider: "claude", Model: model, Success: true})
			wantStable := b.ID
			if mode == "none" {
				wantStable = a.ID
			}
			pick("stable", wantStable)
			pick("fresh", a.ID)
			// A disabled binding fails over to the remaining available account.
			updated, _ := m.GetByID(a.ID)
			updated.Disabled = true
			if _, errUpdate := m.Update(ctx, updated); errUpdate != nil {
				t.Fatal(errUpdate)
			}
			pick("fresh", b.ID)
		})
	}
}
