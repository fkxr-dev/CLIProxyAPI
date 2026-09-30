package registry

import (
	"testing"
	"time"
)

func TestTimedSuspensionExpiresWithoutAnotherRequest(t *testing.T) {
	r := newTestModelRegistry()
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	r.nowFunc = func() time.Time { return now }
	r.RegisterClient("client", "codex", []*ModelInfo{{ID: "astra"}})
	epoch := r.ClientRegistrationEpoch("client")
	apply := func(generation uint64, until time.Time) {
		t.Helper()
		if !r.ApplyClientModelProjections("client", epoch, generation, []ClientModelProjection{{ModelID: "astra", Suspended: true, SuspendReason: "server_error", SuspendUntil: until}}) {
			t.Fatal("projection rejected")
		}
	}
	check := func(want int) {
		t.Helper()
		for _, handler := range []string{"openai", "claude", "gemini"} {
			if got := len(r.GetAvailableModels(handler)); got != want {
				t.Fatalf("%s models = %d, want %d", handler, got, want)
			}
		}
		if got := len(r.GetAvailableModelInfos()); got != want {
			t.Fatalf("model infos = %d, want %d", got, want)
		}
		if got := len(r.GetAvailableModelsByProvider("codex")); got != want {
			t.Fatalf("provider models = %d, want %d", got, want)
		}
		if got := r.GetModelCount("astra"); got != want {
			t.Fatalf("model count = %d, want %d", got, want)
		}
		if got := r.IsModelSuspendedForClient("client", "astra"); got != (want == 0) {
			t.Fatalf("suspended = %v", got)
		}
	}
	apply(1, now.Add(time.Minute))
	check(0)
	now = now.Add(30 * time.Second)
	apply(2, now.Add(time.Minute))
	// An older result must not shorten the renewed cooldown.
	r.ApplyClientModelProjections("client", epoch, 1, []ClientModelProjection{{ModelID: "astra"}})
	now = now.Add(30 * time.Second)
	check(0)
	now = now.Add(30 * time.Second)
	check(1)
	apply(3, time.Time{})
	now = now.Add(24 * time.Hour)
	check(0)
	r.ResumeClientModel("client", "astra")
	check(1)
}
