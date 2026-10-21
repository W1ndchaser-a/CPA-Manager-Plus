package usagehourly

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/pricing"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

const billingHourMS = int64(time.Hour / time.Millisecond)

func billingPriceBook(models ...string) map[string]model.ModelPrice {
	prices := make(map[string]model.ModelPrice, len(models))
	for _, name := range models {
		prices[name] = model.ModelPrice{
			Prompt:                    3.0,
			Completion:                15.0,
			CacheRead:                 0.3,
			CacheCreation:             3.75,
			CacheCreation1h:           6.0,
			PromptConfigured:          true,
			CompletionConfigured:      true,
			CacheReadConfigured:       true,
			CacheCreationConfigured:   true,
			CacheCreation1hConfigured: true,
		}
	}
	return prices
}

// billingEvent is an Anthropic-style event: InputTokens is the uncached prompt and
// cache reads/writes are reported separately.
func billingEvent(hash string, timestampMS int64, modelName string, cacheCreation, cacheCreation1h int64, ttlKnown bool) usage.Event {
	event := readerEvent(hash, timestampMS, modelName, false, 5000, 500, nil)
	event.CacheReadTokens = 1000
	event.CacheCreationTokens = cacheCreation
	event.BillingTokenDetails = usage.BillingTokenDetails{
		ImageInputTokens:      300,
		ImageCacheReadTokens:  100,
		CacheCreation1hTokens: cacheCreation1h,
		ImageTokensKnown:      true,
		CacheCreationTTLKnown: ttlKnown,
	}
	event.CacheInputMode = usage.CacheInputModeSeparate
	event.NormalizedUncachedInputTokens = event.InputTokens
	event.NormalizedCacheReadTokens = event.CacheReadTokens
	event.NormalizedCacheCreationTokens = event.CacheCreationTokens
	event.NormalizedTotalInputTokens = event.InputTokens + event.CacheReadTokens + event.CacheCreationTokens
	event.TotalTokens = event.NormalizedTotalInputTokens + event.OutputTokens
	return event
}

func billingTokensForStat(stat store.ModelStat) pricing.ModelTokens {
	return pricing.ModelTokens{
		PricingModel:            stat.PricingModel,
		ContextThresholdTokens:  stat.ContextThresholdTokens,
		InputTokens:             stat.InputTokens,
		OutputTokens:            stat.OutputTokens,
		ReasoningTokens:         stat.ReasoningTokens,
		TotalTokens:             stat.TotalTokens,
		CachedTokens:            stat.CachedTokens,
		CacheReadTokens:         stat.CacheReadTokens,
		CacheCreationTokens:     stat.CacheCreationTokens,
		LongInputTokens:         stat.LongInputTokens,
		LongOutputTokens:        stat.LongOutputTokens,
		LongCachedTokens:        stat.LongCachedTokens,
		LongCacheReadTokens:     stat.LongCacheReadTokens,
		LongCacheCreationTokens: stat.LongCacheCreationTokens,
		ImageInputTokens:        stat.ImageInputTokens,
		ImageCacheReadTokens:    stat.ImageCacheReadTokens,
		ImageOutputTokens:       stat.ImageOutputTokens,
		CacheCreation1hTokens:   stat.CacheCreation1hTokens,
		ImageTokensKnown:        stat.ImageTokensKnown,
		CacheCreationTTLKnown:   stat.CacheCreationTTLKnown,
	}
}

func billingStatForModel(t *testing.T, stats []store.ModelStat, name string) store.ModelStat {
	t.Helper()
	for _, stat := range stats {
		if stat.Model == name {
			return stat
		}
	}
	t.Fatalf("model %q not found in stats %#v", name, stats)
	return store.ModelStat{}
}

// A full UTC hour served by the permanent hourly aggregate must price exactly
// like the same events read from raw rows, including a window that differs only
// by one millisecond on each side.
func TestReaderBillingDimensionsMatchRawAcrossFullAndEdgeWindows(t *testing.T) {
	db := newReaderTestStore(t)
	ctx := context.Background()
	hourStart := int64(1_800_000_000_000)
	hourStart -= hourStart % billingHourMS
	hourEnd := hourStart + billingHourMS

	if _, err := db.InsertEvents(ctx, []usage.Event{
		billingEvent("billing-full-1", hourStart+10*60*1000, "claude-opus-5-5", 1000, 400, true),
		billingEvent("billing-full-2", hourStart+55*60*1000, "claude-opus-5-5", 600, 0, true),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	catchUpReaderRollup(t, ctx, db)

	rawAggregate, err := db.AggregateBetween(ctx, hourStart, hourEnd)
	if err != nil {
		t.Fatalf("raw aggregate: %v", err)
	}
	rawStats, err := db.ModelStatsBetween(ctx, hourStart, hourEnd)
	if err != nil {
		t.Fatalf("raw model stats: %v", err)
	}
	raw := billingStatForModel(t, rawStats, "claude-opus-5-5")
	if raw.CacheCreation1hTokens != 400 || !raw.CacheCreationTTLKnown {
		t.Fatalf("raw dims lost: %#v", raw.BillingTokenDetails)
	}

	reader := New(db, true)
	fullSnapshot, ok := reader.Load(ctx, hourStart, hourEnd)
	if !ok {
		t.Fatal("reader did not use the permanent hourly aggregate")
	}
	if fullSnapshot.Aggregate.BillingTokenDetails != rawAggregate.BillingTokenDetails {
		t.Fatalf("full-hour aggregate dims = %#v, want %#v",
			fullSnapshot.Aggregate.BillingTokenDetails, rawAggregate.BillingTokenDetails)
	}
	full := billingStatForModel(t, fullSnapshot.ModelStats, "claude-opus-5-5")
	if full.BillingTokenDetails != raw.BillingTokenDetails {
		t.Fatalf("full-hour dims = %#v, want %#v", full.BillingTokenDetails, raw.BillingTokenDetails)
	}

	edgeSnapshot, ok := reader.Load(ctx, hourStart-1, hourEnd+1)
	if !ok {
		t.Fatal("edge reader did not use the permanent hourly aggregate")
	}
	if edgeSnapshot.Aggregate.BillingTokenDetails != rawAggregate.BillingTokenDetails {
		t.Fatalf("edge aggregate dims = %#v, want %#v",
			edgeSnapshot.Aggregate.BillingTokenDetails, rawAggregate.BillingTokenDetails)
	}
	edge := billingStatForModel(t, edgeSnapshot.ModelStats, "claude-opus-5-5")
	if edge.BillingTokenDetails != raw.BillingTokenDetails {
		t.Fatalf("edge dims = %#v, want %#v", edge.BillingTokenDetails, raw.BillingTokenDetails)
	}

	prices := billingPriceBook("claude-opus-5-5")
	fullResult := pricing.CalculateCostForModelCandidates([]string{full.Model}, full.ServiceTier, billingTokensForStat(full), prices)
	edgeResult := pricing.CalculateCostForModelCandidates([]string{edge.Model}, edge.ServiceTier, billingTokensForStat(edge), prices)
	if !fullResult.Complete || !edgeResult.Complete {
		t.Fatalf("cost completeness lost: full=%v edge=%v", fullResult.Complete, edgeResult.Complete)
	}
	if math.Abs(fullResult.Cost-edgeResult.Cost) > 1e-12 {
		t.Fatalf("full-hour cost %v != edge cost %v", fullResult.Cost, edgeResult.Cost)
	}
	// 10000 uncached prompt @ 3 + 1000 output @ 15 + 2000 read @ 0.3 + 1200 5m @ 3.75 + 400 1h @ 6 (per million).
	const wantCost = 0.03 + 0.015 + 0.0006 + 0.0045 + 0.0024
	if math.Abs(fullResult.Cost-wantCost) > 1e-9 {
		t.Fatalf("cost = %v, want %v", fullResult.Cost, wantCost)
	}
}

// Mixed 5-minute and 1-hour cache writes must keep the explicit split instead of
// folding the whole cache-write bucket into either TTL.
func TestReaderMixedFiveMinuteAndOneHourDimensions(t *testing.T) {
	db := newReaderTestStore(t)
	ctx := context.Background()
	hourStart := int64(1_800_000_000_000)
	hourStart -= hourStart % billingHourMS
	hourEnd := hourStart + billingHourMS

	if _, err := db.InsertEvents(ctx, []usage.Event{
		billingEvent("billing-mixed-1", hourStart+1000, "claude-mixed", 1000, 400, true),
		billingEvent("billing-mixed-2", hourStart+2000, "claude-mixed", 600, 0, true),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	catchUpReaderRollup(t, ctx, db)

	rawStats, err := db.ModelStatsBetween(ctx, hourStart, hourEnd)
	if err != nil {
		t.Fatalf("raw model stats: %v", err)
	}
	raw := billingStatForModel(t, rawStats, "claude-mixed")
	reader := New(db, true)
	snapshot, ok := reader.Load(ctx, hourStart, hourEnd)
	if !ok {
		t.Fatal("reader did not use the permanent hourly aggregate")
	}
	rollup := billingStatForModel(t, snapshot.ModelStats, "claude-mixed")
	for name, stat := range map[string]store.ModelStat{"raw": raw, "rollup": rollup} {
		if stat.CacheCreationTokens != 1600 || stat.CacheCreation1hTokens != 400 || !stat.CacheCreationTTLKnown {
			t.Fatalf("%s split lost: %#v", name, stat.BillingTokenDetails)
		}
	}
	result := pricing.CalculateCostForModelCandidates(
		[]string{rollup.Model}, rollup.ServiceTier, billingTokensForStat(rollup), billingPriceBook("claude-mixed"),
	)
	if !result.Complete {
		t.Fatal("mixed known split must price completely")
	}
}

// Identical usage with a known 5m-only TTL versus an unknown TTL keeps the same
// token accounting, but only the known event is fully priced and neither invents
// 1-hour cache writes.
func TestReaderKnownZeroVersusUnknownTTLCost(t *testing.T) {
	db := newReaderTestStore(t)
	ctx := context.Background()
	hourStart := int64(1_800_000_000_000)
	hourStart -= hourStart % billingHourMS
	hourEnd := hourStart + billingHourMS

	if _, err := db.InsertEvents(ctx, []usage.Event{
		billingEvent("billing-known", hourStart+1000, "claude-known-5m", 600, 0, true),
		billingEvent("billing-unknown", hourStart+2000, "claude-unknown", 600, 0, false),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	catchUpReaderRollup(t, ctx, db)

	rawStats, err := db.ModelStatsBetween(ctx, hourStart, hourEnd)
	if err != nil {
		t.Fatalf("raw model stats: %v", err)
	}
	reader := New(db, true)
	snapshot, ok := reader.Load(ctx, hourStart, hourEnd)
	if !ok {
		t.Fatal("reader did not use the permanent hourly aggregate")
	}
	rawKnown := billingStatForModel(t, rawStats, "claude-known-5m")
	rawUnknown := billingStatForModel(t, rawStats, "claude-unknown")
	rollupKnown := billingStatForModel(t, snapshot.ModelStats, "claude-known-5m")
	rollupUnknown := billingStatForModel(t, snapshot.ModelStats, "claude-unknown")

	if rawKnown.BillingTokenDetails != rollupKnown.BillingTokenDetails {
		t.Fatalf("known dims differ raw=%#v rollup=%#v", rawKnown.BillingTokenDetails, rollupKnown.BillingTokenDetails)
	}
	if rawUnknown.BillingTokenDetails != rollupUnknown.BillingTokenDetails {
		t.Fatalf("unknown dims differ raw=%#v rollup=%#v", rawUnknown.BillingTokenDetails, rollupUnknown.BillingTokenDetails)
	}
	for name, stat := range map[string]store.ModelStat{"known": rollupKnown, "unknown": rollupUnknown} {
		if stat.CacheCreation1hTokens != 0 {
			t.Fatalf("%s TTL was guessed: 1h=%d", name, stat.CacheCreation1hTokens)
		}
		if stat.CacheCreationTokens != 600 {
			t.Fatalf("%s cache writes changed: %d", name, stat.CacheCreationTokens)
		}
	}
	if !rollupKnown.CacheCreationTTLKnown || rollupUnknown.CacheCreationTTLKnown {
		t.Fatalf("TTL known flags collapsed: known=%v unknown=%v",
			rollupKnown.CacheCreationTTLKnown, rollupUnknown.CacheCreationTTLKnown)
	}

	prices := billingPriceBook("claude-known-5m", "claude-unknown")
	knownResult := pricing.CalculateCostForModelCandidates(
		[]string{rollupKnown.Model}, rollupKnown.ServiceTier, billingTokensForStat(rollupKnown), prices,
	)
	unknownResult := pricing.CalculateCostForModelCandidates(
		[]string{rollupUnknown.Model}, rollupUnknown.ServiceTier, billingTokensForStat(rollupUnknown), prices,
	)
	if !knownResult.Complete {
		t.Fatal("known 5m-only TTL must price completely")
	}
	if unknownResult.Complete {
		t.Fatal("unknown TTL must stay incomplete when a distinct 1h rate exists")
	}
	if math.Abs(knownResult.Cost-unknownResult.Cost) > 1e-12 {
		t.Fatalf("identical usage priced differently: known=%v unknown=%v", knownResult.Cost, unknownResult.Cost)
	}
}
