package usage

import "testing"

// Cache writes whose producer does not report the 5-minute/1-hour split stay
// unknown. They must never be reclassified as 1-hour writes at ingest: the
// environment switch that used to force a 1h assumption is retired, so even a
// stale deployment variable must not change accounting.
func TestUnknownClaudeCacheWriteTTLStaysUnknown(t *testing.T) {
	t.Setenv("USAGE_CLAUDE_UNKNOWN_CACHE_TTL", "1h")

	raw := func(model, tokens string) Event {
		event, err := NormalizeRaw([]byte(`{"timestamp":"2026-10-11T00:00:00Z","model":"` + model + `","tokens":` + tokens + `}`))
		if err != nil {
			t.Fatalf("normalize raw: %v", err)
		}
		return event
	}
	unknown := `{"input_tokens":2,"output_tokens":10,"cache_read_tokens":100,"cache_creation_tokens":600}`

	event := raw("vm2api/claude-opus-5-5", unknown)
	if event.CacheCreation1hTokens != 0 || event.CacheCreationTTLKnown {
		t.Fatalf("unknown TTL must stay unknown: 1h=%d known=%v, want 0 false",
			event.CacheCreation1hTokens, event.CacheCreationTTLKnown)
	}
	if got := raw("gpt-6-sol", unknown).CacheCreation1hTokens; got != 0 {
		t.Fatalf("non-claude: 1h tokens = %d, want 0", got)
	}
}

func TestExplicitCacheWriteTTLFlagsArePreserved(t *testing.T) {
	t.Setenv("USAGE_CLAUDE_UNKNOWN_CACHE_TTL", "1h")

	raw := func(tokens string) Event {
		event, err := NormalizeRaw([]byte(`{"timestamp":"2026-10-11T00:00:00Z","model":"claude-opus-5-5","tokens":` + tokens + `}`))
		if err != nil {
			t.Fatalf("normalize raw: %v", err)
		}
		return event
	}

	reported := raw(`{"input_tokens":2,"cache_creation_tokens":600,"cache_creation_1h_tokens":100,"cache_creation_ttl_known":true}`)
	if reported.CacheCreation1hTokens != 100 || !reported.CacheCreationTTLKnown {
		t.Fatalf("reported split overridden: 1h=%d known=%v", reported.CacheCreation1hTokens, reported.CacheCreationTTLKnown)
	}

	allFiveMinute := raw(`{"input_tokens":2,"cache_creation_tokens":600,"cache_creation_ttl_known":true}`)
	if allFiveMinute.CacheCreation1hTokens != 0 || !allFiveMinute.CacheCreationTTLKnown {
		t.Fatalf("known 5m writes reclassified: 1h=%d known=%v", allFiveMinute.CacheCreation1hTokens, allFiveMinute.CacheCreationTTLKnown)
	}

	explicitFalse := raw(`{"input_tokens":2,"cache_creation_tokens":600,"cache_creation_1h_tokens":100,"cache_creation_ttl_known":false}`)
	if explicitFalse.CacheCreation1hTokens != 100 || explicitFalse.CacheCreationTTLKnown {
		t.Fatalf("explicit unknown flag upgraded: 1h=%d known=%v", explicitFalse.CacheCreation1hTokens, explicitFalse.CacheCreationTTLKnown)
	}
}
