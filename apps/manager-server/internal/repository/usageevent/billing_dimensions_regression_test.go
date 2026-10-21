package usageevent_test

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usagearchive"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageevent"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageprojection"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/pricing"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "billing.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBillingDimensionsIngestPersistenceAndOfficialCost(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := usageevent.New(db)

	rawComplete := []byte(`{
		"request_id": "req-sonnet-1",
		"timestamp": "2026-10-08T12:00:00Z",
		"model": "claude-3-7-sonnet",
		"tokens": {
			"input_tokens": 500,
			"output_tokens": 500,
			"cached_tokens": 200,
			"cache_read_tokens": 200,
			"cache_creation_tokens": 300,
			"image_input_tokens": 150,
			"image_cache_read_tokens": 50,
			"image_output_tokens": 0,
			"cache_creation_1h_tokens": 100,
			"image_tokens_known": true,
			"cache_creation_ttl_known": true,
			"total_tokens": 1500
		}
	}`)

	evComplete, err := usage.NormalizeRaw(rawComplete)
	if err != nil {
		t.Fatalf("normalize complete raw event: %v", err)
	}

	if evComplete.ImageInputTokens != 150 || evComplete.ImageCacheReadTokens != 50 ||
		evComplete.CacheCreation1hTokens != 100 || !evComplete.ImageTokensKnown || !evComplete.CacheCreationTTLKnown {
		t.Fatalf("unexpected normalized billing details: %+v", evComplete.BillingTokenDetails)
	}

	rawLegacy := []byte(`{
		"request_id": "req-legacy-1",
		"timestamp": "2026-10-08T12:05:00Z",
		"model": "claude-3-7-sonnet",
		"tokens": {
			"input_tokens": 500,
			"output_tokens": 500,
			"cached_tokens": 200,
			"cache_read_tokens": 200,
			"cache_creation_tokens": 300,
			"total_tokens": 1500
		}
	}`)

	evLegacy, err := usage.NormalizeRaw(rawLegacy)
	if err != nil {
		t.Fatalf("normalize legacy raw event: %v", err)
	}
	if evLegacy.ImageTokensKnown || evLegacy.CacheCreationTTLKnown {
		t.Fatalf("legacy event must keep ImageTokensKnown=false and CacheCreationTTLKnown=false")
	}

	res, err := repo.InsertBatch(ctx, []model.UsageEvent{evComplete, evLegacy})
	if err != nil {
		t.Fatalf("insert batch: %v", err)
	}
	if res.Inserted != 2 {
		t.Fatalf("expected 2 inserted, got %d", res.Inserted)
	}

	t0 := evComplete.TimestampMS - 60000
	t1 := evLegacy.TimestampMS + 60000

	modelStats, err := repo.ModelStatsBetween(ctx, t0, t1)
	if err != nil {
		t.Fatalf("model stats between: %v", err)
	}
	if len(modelStats) != 1 {
		t.Fatalf("expected 1 model stat, got %d", len(modelStats))
	}
	stat := modelStats[0]
	if stat.ImageTokensKnown || stat.CacheCreationTTLKnown {
		t.Fatalf("aggregate with legacy event must be marked unknown for image/TTL, got %+v", stat.BillingTokenDetails)
	}

	// Official Claude Sonnet pricing:
	// Prompt: $3.0/M, Completion: $15.0/M, CacheRead: $0.3/M, CacheCreation (5m): $3.75/M, CacheCreation1h: $6.0/M.
	prices := map[string]model.ModelPrice{
		"claude-3-7-sonnet": {
			Prompt:                    3.0,
			Completion:                15.0,
			CacheRead:                 0.3,
			CacheCreation:             3.75,
			CacheCreation1h:           6.0,
			CacheCreation1hConfigured: true,
			PromptConfigured:          true,
			CompletionConfigured:      true,
			CacheReadConfigured:       true,
			CacheCreationConfigured:   true,
		},
	}

	pricingTokens := pricing.ModelTokens{
		PricingModel:           stat.PricingModel,
		ContextThresholdTokens: stat.ContextThresholdTokens,
		InputTokens:            stat.InputTokens,
		OutputTokens:           stat.OutputTokens,
		CachedTokens:           stat.CachedTokens,
		CacheReadTokens:        stat.CacheReadTokens,
		CacheCreationTokens:    stat.CacheCreationTokens,
		ImageInputTokens:       stat.ImageInputTokens,
		ImageCacheReadTokens:   stat.ImageCacheReadTokens,
		ImageOutputTokens:      stat.ImageOutputTokens,
		CacheCreation1hTokens:  stat.CacheCreation1hTokens,
		ImageTokensKnown:       stat.ImageTokensKnown,
		CacheCreationTTLKnown:  stat.CacheCreationTTLKnown,
	}

	costResult := pricing.CalculateCostForModelCandidates([]string{stat.Model}, stat.ServiceTier, pricingTokens, prices)
	if costResult.Complete {
		t.Fatalf("cost result must be incomplete because CacheCreationTTLKnown is false for model with 1h pricing")
	}

	// Now verify the single complete event produces exact official cost math:
	completeTokens := pricing.ModelTokens{
		InputTokens:           evComplete.NormalizedTotalInputTokens,
		OutputTokens:          evComplete.OutputTokens,
		CachedTokens:          usage.CompatibleCachedTokens(evComplete.CachedTokens, evComplete.CacheTokens, evComplete.CacheReadTokens, evComplete.CacheCreationTokens),
		CacheReadTokens:       evComplete.NormalizedCacheReadTokens,
		CacheCreationTokens:   evComplete.NormalizedCacheCreationTokens,
		ImageInputTokens:      evComplete.ImageInputTokens,
		ImageCacheReadTokens:  evComplete.ImageCacheReadTokens,
		ImageOutputTokens:     evComplete.ImageOutputTokens,
		CacheCreation1hTokens: evComplete.CacheCreation1hTokens,
		ImageTokensKnown:      evComplete.ImageTokensKnown,
		CacheCreationTTLKnown: evComplete.CacheCreationTTLKnown,
	}

	// Math breakdown:
	// Uncached input: 1000 - 200 (read) - 300 (creation) = 500 tokens @ $3.0/M = 0.0015
	// Output: 500 tokens @ $15.0/M = 0.0075
	// CacheRead: 200 tokens @ $0.3/M = 0.00006
	// 5m Creation: 300 - 100 (1h) = 200 tokens @ $3.75/M = 0.00075
	// 1h Creation: 100 tokens @ $6.0/M = 0.0006
	// Total: 0.0015 + 0.0075 + 0.00006 + 0.00075 + 0.0006 = 0.01041
	expectedCost := 0.01041
	completeCost := pricing.CalculateCostForModelCandidates([]string{evComplete.Model}, evComplete.ServiceTier, completeTokens, prices)
	if !completeCost.Complete {
		t.Fatalf("complete event cost must be complete, got incomplete")
	}
	if math.Abs(completeCost.Cost-expectedCost) > 1e-6 {
		t.Fatalf("complete event cost = %v, want %v", completeCost.Cost, expectedCost)
	}
}

func TestBillingDimensionsArchiveRecordsPreserveDetails(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := usageevent.New(db)
	archiveRepo := usagearchive.New(db)

	raw := []byte(`{
		"request_id": "arch-event-1",
		"timestamp": "2026-10-08T12:00:00Z",
		"model": "gpt-5.5",
		"tokens": {
			"input_tokens": 1500,
			"output_tokens": 600,
			"cached_tokens": 300,
			"cache_read_tokens": 300,
			"cache_creation_tokens": 200,
			"image_input_tokens": 250,
			"image_cache_read_tokens": 75,
			"image_output_tokens": 120,
			"cache_creation_1h_tokens": 80,
			"image_tokens_known": true,
			"cache_creation_ttl_known": true,
			"total_tokens": 2100
		}
	}`)

	ev, err := usage.NormalizeRaw(raw)
	if err != nil {
		t.Fatalf("normalize raw event: %v", err)
	}

	if _, err := repo.InsertBatch(ctx, []model.UsageEvent{ev}); err != nil {
		t.Fatalf("insert event: %v", err)
	}

	run, err := archiveRepo.CreateRun(ctx, "test-archive-run", ev.TimestampMS+1000, 10000)
	if err != nil {
		t.Fatalf("create archive run: %v", err)
	}

	_, err = archiveRepo.BeginArchive(ctx, run.ID, 10001)
	if err != nil {
		t.Fatalf("begin archive: %v", err)
	}

	records, err := archiveRepo.Records(ctx, run.ID, 0, 10, 1<<20)
	if err != nil {
		t.Fatalf("read archive records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 archive record, got %d", len(records))
	}

	restored, err := usage.NormalizeRaw(records[0].Payload)
	if err != nil {
		t.Fatalf("restore event from archive record JSON: %v", err)
	}

	if restored.ImageInputTokens != 250 || restored.ImageCacheReadTokens != 75 ||
		restored.ImageOutputTokens != 120 || restored.CacheCreation1hTokens != 80 ||
		!restored.ImageTokensKnown || !restored.CacheCreationTTLKnown {
		t.Fatalf("archived record lost billing details: %+v", restored.BillingTokenDetails)
	}
}

func TestBillingDimensionsRetainedProjectionRebuild(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := usageevent.New(db)

	raw := []byte(`{
		"request_id": "proj-event-1",
		"timestamp": "2026-10-08T12:00:00Z",
		"model": "gpt-5.5",
		"tokens": {
			"input_tokens": 1200,
			"output_tokens": 400,
			"image_input_tokens": 180,
			"image_cache_read_tokens": 60,
			"image_output_tokens": 90,
			"cache_creation_1h_tokens": 50,
			"image_tokens_known": true,
			"cache_creation_ttl_known": true,
			"total_tokens": 1600
		}
	}`)

	ev, err := usage.NormalizeRaw(raw)
	if err != nil {
		t.Fatalf("normalize raw event: %v", err)
	}

	inserted, err := repo.InsertBatch(ctx, []model.UsageEvent{ev})
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := usageprojection.UpsertEventRange(ctx, tx, 0, int64(inserted.Inserted), time.Now().UnixMilli()); err != nil {
		t.Fatalf("upsert event range to projection: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	var imgIn, imgRead, imgOut, ttl1h int64
	var imgKnown, ttlKnown int
	err = db.QueryRowContext(ctx, `select image_input_tokens, image_cache_read_tokens, image_output_tokens,
		cache_creation_1h_tokens, image_tokens_known, cache_creation_ttl_known
		from usage_monitoring_event_projection_v1 where event_id = 1`).Scan(
		&imgIn, &imgRead, &imgOut, &ttl1h, &imgKnown, &ttlKnown,
	)
	if err != nil {
		t.Fatalf("query projection columns: %v", err)
	}

	if imgIn != 180 || imgRead != 60 || imgOut != 90 || ttl1h != 50 || imgKnown != 1 || ttlKnown != 1 {
		t.Fatalf("projection row lost billing dimensions: got imgIn=%d imgRead=%d imgOut=%d ttl1h=%d imgKnown=%d ttlKnown=%d",
			imgIn, imgRead, imgOut, ttl1h, imgKnown, ttlKnown)
	}
}

// The filtered analytics reads used by summary/model/timeline panels must carry
// the same billing dimensions as the unfiltered core reads. Otherwise the same
// window prices differently depending on which query path served it.
func TestBillingDimensionsRawFilteredAnalyticsMatchCoreReads(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := usageevent.New(db)

	rawKnown := []byte(`{
		"request_id": "dims-known",
		"timestamp": "2026-10-08T12:00:00Z",
		"model": "claude-known",
		"tokens": {
			"input_tokens": 1000,
			"output_tokens": 100,
			"cache_read_tokens": 200,
			"cache_creation_tokens": 300,
			"cache_creation_1h_tokens": 120,
			"image_input_tokens": 40,
			"image_cache_read_tokens": 10,
			"image_output_tokens": 5,
			"image_tokens_known": true,
			"cache_creation_ttl_known": true,
			"total_tokens": 1100
		}
	}`)
	rawUnknown := []byte(`{
		"request_id": "dims-unknown",
		"timestamp": "2026-10-08T12:30:00Z",
		"model": "claude-unknown",
		"tokens": {
			"input_tokens": 1000,
			"output_tokens": 100,
			"cache_read_tokens": 200,
			"cache_creation_tokens": 300,
			"image_input_tokens": 40,
			"image_cache_read_tokens": 10,
			"image_output_tokens": 5,
			"image_tokens_known": false,
			"cache_creation_ttl_known": false,
			"total_tokens": 1100
		}
	}`)

	events := make([]model.UsageEvent, 0, 2)
	for _, raw := range [][]byte{rawKnown, rawUnknown} {
		event, err := usage.NormalizeRaw(raw)
		if err != nil {
			t.Fatalf("normalize raw event: %v", err)
		}
		events = append(events, event)
	}
	if _, err := repo.InsertBatch(ctx, events); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	fromMS := events[0].TimestampMS - 1
	toMS := events[1].TimestampMS + 1

	coreStats, err := repo.ModelStatsBetween(ctx, fromMS, toMS)
	if err != nil {
		t.Fatalf("core model stats: %v", err)
	}
	filteredStats, err := repo.ModelStatsWithFilter(ctx, usageevent.AnalyticsFilter{
		FromMS:        fromMS,
		ToMS:          toMS,
		IncludeFailed: true,
	}, 0)
	if err != nil {
		t.Fatalf("filtered model stats: %v", err)
	}
	coreDims := map[string]usage.BillingTokenDetails{}
	for _, stat := range coreStats {
		coreDims[stat.Model] = stat.BillingTokenDetails
	}
	if len(coreDims) != 2 {
		t.Fatalf("expected two core models, got %#v", coreDims)
	}
	for _, stat := range filteredStats {
		if got, ok := coreDims[stat.Model]; !ok || got != stat.BillingTokenDetails {
			t.Fatalf("filtered dims for %s = %#v, want %#v", stat.Model, stat.BillingTokenDetails, got)
		}
	}
	known := coreDims["claude-known"]
	unknown := coreDims["claude-unknown"]
	if known.CacheCreation1hTokens != 120 || !known.CacheCreationTTLKnown || !known.ImageTokensKnown {
		t.Fatalf("known dims lost: %#v", known)
	}
	if unknown.CacheCreation1hTokens != 0 || unknown.CacheCreationTTLKnown || unknown.ImageTokensKnown {
		t.Fatalf("unknown dims upgraded: %#v", unknown)
	}

	timeline, err := repo.TimelineWithFilter(ctx, usageevent.AnalyticsFilter{
		FromMS:        fromMS,
		ToMS:          toMS,
		IncludeFailed: true,
	}, "hour", time.UTC)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	var timeline1h, timelineCacheCreation int64
	var timelineKnown bool
	for index, point := range timeline {
		timeline1h += point.CacheCreation1hTokens
		timelineCacheCreation += point.CacheCreationTokens
		if index == 0 {
			timelineKnown = point.CacheCreationTTLKnown
		} else {
			timelineKnown = timelineKnown && point.CacheCreationTTLKnown
		}
	}
	if timeline1h != 120 || timelineCacheCreation != 600 {
		t.Fatalf("timeline dims = 1h %d creation %d, want 120/600", timeline1h, timelineCacheCreation)
	}
	if timelineKnown {
		t.Fatal("timeline merged an unknown TTL into a known one")
	}
}
