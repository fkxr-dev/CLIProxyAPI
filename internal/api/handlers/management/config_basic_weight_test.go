package management

import "testing"

func TestNormalizeRoutingStrategyWeightedRoundRobin(t *testing.T) {
	for _, input := range []string{"weighted-round-robin", "weightedroundrobin", "wrr"} {
		got, ok := normalizeRoutingStrategy(input)
		if !ok || got != "weighted-round-robin" {
			t.Fatalf("normalizeRoutingStrategy(%q) = %q, %v; want weighted-round-robin, true", input, got, ok)
		}
	}
}

func TestNormalizeRoutingStrategyEarliestReset(t *testing.T) {
	for _, input := range []string{"earliest-reset", " EARLIEST-RESET "} {
		got, ok := normalizeRoutingStrategy(input)
		if !ok || got != "earliest-reset" {
			t.Fatalf("normalizeRoutingStrategy(%q) = %q, %v", input, got, ok)
		}
	}
}
