package cliproxy

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestEarliestResetRoutingConfig(t *testing.T) {
	for _, affinity := range []bool{false, true} {
		cfg := &internalconfig.Config{}
		errLoad := yaml.Unmarshal([]byte("routing:\n  strategy: earliest-reset\n"), cfg)
		if errLoad != nil {
			t.Fatal(errLoad)
		}
		cfg.Routing.SessionAffinity = affinity
		state := normalizedRoutingRuntimeState(cfg)
		if state.strategy != "earliest-reset" {
			t.Fatalf("strategy = %q", state.strategy)
		}
		selector := newRoutingSelector(state)
		if stoppable, ok := selector.(coreauth.StoppableSelector); ok {
			t.Cleanup(stoppable.Stop)
		}
		now := time.Now()
		auths := []*coreauth.Auth{
			{ID: "a", Provider: "claude", Quota: coreauth.QuotaState{Signals: map[string]string{"Anthropic-Ratelimit-Unified-7d-Reset": strconv.FormatInt(now.Add(5*24*time.Hour).Unix(), 10)}}},
			{ID: "b", Provider: "claude", Quota: coreauth.QuotaState{Signals: map[string]string{"Anthropic-Ratelimit-Unified-7d-Reset": strconv.FormatInt(now.Add(24*time.Hour).Unix(), 10)}}},
		}
		got, errPick := selector.Pick(context.Background(), "claude", "model", cliproxyexecutor.Options{Headers: http.Header{"X-Session-Id": []string{"test"}}}, auths)
		if errPick != nil || got.ID != "b" {
			t.Fatalf("affinity=%v: got %v, %v; want b", affinity, got, errPick)
		}
	}
}
