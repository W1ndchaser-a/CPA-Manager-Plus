package usage

import "testing"

func TestAssumeUnknownClaudeCacheWriteTTL(t *testing.T) {
	previous := unknownClaudeCacheWriteTTL
	t.Cleanup(func() { unknownClaudeCacheWriteTTL = previous })

	raw := func(model, tokens string) Event {
		event, err := NormalizeRaw([]byte(`{"timestamp":"2026-10-11T00:00:00Z","model":"` + model + `","tokens":` + tokens + `}`))
		if err != nil {
			t.Fatalf("normalize raw: %v", err)
		}
		return event
	}
	unknown := `{"input_tokens":2,"output_tokens":10,"cache_read_tokens":100,"cache_creation_tokens":600}`

	unknownClaudeCacheWriteTTL = ""
	if got := raw("claude-opus-5-5", unknown).CacheCreation1hTokens; got != 0 {
		t.Fatalf("disabled: 1h tokens = %d, want 0", got)
	}

	unknownClaudeCacheWriteTTL = "1h"
	event := raw("vm2api/claude-opus-5-5", unknown)
	if event.CacheCreation1hTokens != 600 || event.CacheCreationTTLKnown {
		t.Fatalf("claude unknown TTL: 1h=%d known=%v, want 600 false", event.CacheCreation1hTokens, event.CacheCreationTTLKnown)
	}
	if got := raw("gpt-6-sol", unknown).CacheCreation1hTokens; got != 0 {
		t.Fatalf("non-claude: 1h tokens = %d, want 0", got)
	}
	reported := raw("claude-opus-5-5", `{"input_tokens":2,"cache_creation_tokens":600,"cache_creation_1h_tokens":100,"cache_creation_ttl_known":true}`)
	if reported.CacheCreation1hTokens != 100 || !reported.CacheCreationTTLKnown {
		t.Fatalf("reported split overridden: 1h=%d known=%v", reported.CacheCreation1hTokens, reported.CacheCreationTTLKnown)
	}
	allFiveMinute := raw("claude-opus-5-5", `{"input_tokens":2,"cache_creation_tokens":600,"cache_creation_ttl_known":true}`)
	if allFiveMinute.CacheCreation1hTokens != 0 {
		t.Fatalf("known 5m writes reclassified: 1h=%d", allFiveMinute.CacheCreation1hTokens)
	}
}
