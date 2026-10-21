package usagerolluprebuild

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

const testHourMS = int64(time.Hour / time.Millisecond)

func openRebuildTestStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	sqlDB, err := sqliterepo.Open(path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return store.New(sqlDB), sqlDB
}

// rebuildTestEvent is an Anthropic-style event: InputTokens is the uncached prompt
// and cache reads/writes are reported separately.
func rebuildTestEvent(hash string, timestampMS int64, cacheCreation, cacheCreation1h int64, ttlKnown bool) usage.Event {
	sum := sha256.Sum256([]byte(hash))
	return usage.Event{
		EventHash:           hex.EncodeToString(sum[:]),
		TimestampMS:         timestampMS,
		Timestamp:           time.UnixMilli(timestampMS).UTC().Format(time.RFC3339Nano),
		Model:               "claude-opus-5-5",
		Endpoint:            "POST /v1/messages",
		InputTokens:         5000,
		OutputTokens:        500,
		CacheReadTokens:     1000,
		CacheCreationTokens: cacheCreation,
		CacheInputMode:      usage.CacheInputModeSeparate,
		BillingTokenDetails: usage.BillingTokenDetails{
			ImageInputTokens:      300,
			ImageCacheReadTokens:  100,
			CacheCreation1hTokens: cacheCreation1h,
			ImageTokensKnown:      true,
			CacheCreationTTLKnown: ttlKnown,
		},
		NormalizedTotalInputTokens:    5000 + 1000 + cacheCreation,
		NormalizedCacheReadTokens:     1000,
		NormalizedCacheCreationTokens: cacheCreation,
		NormalizedUncachedInputTokens: 5000,
		TotalTokens:                   5000 + 1000 + cacheCreation + 500,
		CreatedAtMS:                   timestampMS,
	}
}

func catchUpAllDerived(t *testing.T, ctx context.Context, db *store.Store) {
	t.Helper()
	nowMS := time.Now().UnixMilli()
	for {
		result, err := db.CatchUpUsageHourlyAggregate(ctx, 100, nowMS)
		if err != nil {
			t.Fatalf("catch up hourly aggregate: %v", err)
		}
		if !result.Pending {
			break
		}
	}
	for {
		result, err := db.CatchUpUsageMonitoringProjection(ctx, 100, nowMS)
		if err != nil {
			t.Fatalf("catch up projection: %v", err)
		}
		if !result.Pending {
			break
		}
	}
	for {
		result, err := db.CatchUpUsagePricing(ctx, 100, nowMS)
		if err != nil {
			t.Fatalf("catch up pricing: %v", err)
		}
		if !result.Pending {
			break
		}
	}
	for {
		result, err := db.CatchUpUsageMonitoringStats(ctx, 100, nowMS)
		if err != nil {
			t.Fatalf("catch up stats: %v", err)
		}
		if !result.Pending {
			break
		}
	}
}

func TestRebuildClearsAndRedrivesBillingDimensionsFromRawEvents(t *testing.T) {
	db, sqlDB := openRebuildTestStore(t)
	ctx := context.Background()
	hourStart := int64(1_800_000_000_000)
	hourStart -= hourStart % testHourMS

	if _, err := db.InsertEvents(ctx, []usage.Event{
		rebuildTestEvent("rebuild-1", hourStart+1000, 1000, 400, true),
		rebuildTestEvent("rebuild-2", hourStart+2000, 600, 0, false),
	}); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	catchUpAllDerived(t, ctx, db)

	// The original rows were produced with a guessed 1h split (unknown TTL).
	var before1h int64
	if err := sqlDB.QueryRowContext(ctx, `select coalesce(sum(cache_creation_1h_tokens), 0)
		from usage_pricing_hourly_rollups_v1`).Scan(&before1h); err != nil {
		t.Fatalf("read pre-rebuild 1h tokens: %v", err)
	}
	if before1h != 400 {
		t.Fatalf("pre-rebuild 1h tokens = %d, want 400", before1h)
	}

	// Evidence correction, matching the operator script: the second event has no
	// reliable TTL, so its guessed 1h write is removed and the TTL stays unknown.
	corrected := rebuildTestEvent("rebuild-2", 0, 0, 0, false).EventHash
	if _, err := sqlDB.ExecContext(ctx, `update usage_events set
		cache_creation_1h_tokens = 100,
		cache_creation_ttl_known = 1
		where event_hash = ?`, corrected); err != nil {
		t.Fatalf("correct raw event: %v", err)
	}

	dryRun, err := Rebuild(ctx, sqlDB, Options{DryRun: true})
	if err != nil {
		t.Fatalf("dry run rebuild: %v", err)
	}
	if dryRun.ProjectionRowsSynced != 1 {
		t.Fatalf("dry run projection drift = %d, want 1", dryRun.ProjectionRowsSynced)
	}
	if dryRun.DeletedRows["usage_pricing_hourly_rollups_v1"] == 0 {
		t.Fatal("dry run must report the derived rows it would clear")
	}
	var stillPopulated int
	if err := sqlDB.QueryRowContext(ctx, `select count(*) from usage_pricing_hourly_rollups_v1`).Scan(&stillPopulated); err != nil {
		t.Fatalf("count after dry run: %v", err)
	}
	if stillPopulated == 0 {
		t.Fatal("dry run modified derived rows")
	}

	result, err := Rebuild(ctx, sqlDB, Options{})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if result.LatestEventID == 0 || !strings.Contains(result.AggregateStructureRevision, ":rebuild-") {
		t.Fatalf("unexpected rebuild result: %#v", result)
	}
	for _, table := range BillingRollupTables {
		var remaining int
		if err := sqlDB.QueryRowContext(ctx, `select count(*) from `+table).Scan(&remaining); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if remaining != 0 {
			t.Fatalf("%s was not cleared: %d rows", table, remaining)
		}
	}
	var status, revision string
	var coverage int64
	if err := sqlDB.QueryRowContext(ctx, `select status, structure_revision, coverage_event_id
		from usage_hourly_aggregate_state where aggregate_name = ?`,
		"hourly_core").Scan(&status, &revision, &coverage); err != nil {
		t.Fatalf("read aggregate state: %v", err)
	}
	if status != "pending" || coverage != 0 || revision != result.AggregateStructureRevision {
		t.Fatalf("aggregate state = %s/%s/%d", status, revision, coverage)
	}
	var checkpoint int64
	if err := sqlDB.QueryRowContext(ctx, `select last_event_id from usage_rollup_checkpoints
		where name = 'account_history'`).Scan(&checkpoint); err != nil {
		t.Fatalf("read account history checkpoint: %v", err)
	}
	if checkpoint != 0 {
		t.Fatalf("account history checkpoint = %d, want 0", checkpoint)
	}

	// Authoritative raw rows and their projection mirror survive untouched apart
	// from the derived projection dimension sync.
	var raw1h, rawKnown int64
	if err := sqlDB.QueryRowContext(ctx, `select cache_creation_1h_tokens, cache_creation_ttl_known
		from usage_events where event_hash = ?`, corrected).Scan(&raw1h, &rawKnown); err != nil {
		t.Fatalf("read corrected raw event: %v", err)
	}
	if raw1h != 100 || rawKnown != 1 {
		t.Fatalf("rebuild rewrote raw event: 1h=%d known=%d", raw1h, rawKnown)
	}
	var projection1h, projectionKnown int64
	if err := sqlDB.QueryRowContext(ctx, `select p.cache_creation_1h_tokens, p.cache_creation_ttl_known
		from usage_monitoring_event_projection_v1 p
		join usage_events e on e.id = p.event_id
		where e.event_hash = ?`, corrected).Scan(&projection1h, &projectionKnown); err != nil {
		t.Fatalf("read projection row: %v", err)
	}
	if projection1h != 100 || projectionKnown != 1 {
		t.Fatalf("projection not synced: 1h=%d known=%d", projection1h, projectionKnown)
	}

	catchUpAllDerived(t, ctx, db)
	var aggregate1h, aggregateKnown int64
	if err := sqlDB.QueryRowContext(ctx, `select coalesce(sum(cache_creation_1h_tokens), 0),
		coalesce(min(cache_creation_ttl_known), 0) from usage_hourly_aggregate_v1`).
		Scan(&aggregate1h, &aggregateKnown); err != nil {
		t.Fatalf("read aggregate rollup: %v", err)
	}
	if aggregate1h != 500 || aggregateKnown != 1 {
		t.Fatalf("rebuilt aggregate dims = 1h %d known %d, want 500/1", aggregate1h, aggregateKnown)
	}
	var pricing1h, pricingKnown int64
	if err := sqlDB.QueryRowContext(ctx, `select coalesce(sum(cache_creation_1h_tokens), 0),
		coalesce(min(cache_creation_ttl_known), 0) from usage_pricing_hourly_rollups_v1`).
		Scan(&pricing1h, &pricingKnown); err != nil {
		t.Fatalf("read pricing rollup: %v", err)
	}
	if pricing1h != 500 || pricingKnown != 1 {
		t.Fatalf("rebuilt pricing dims = 1h %d known %d, want 500/1", pricing1h, pricingKnown)
	}
}

func TestRebuildRefusesArchivedRawUnlessAllowed(t *testing.T) {
	_, sqlDB := openRebuildTestStore(t)
	ctx := context.Background()
	for _, statement := range []string{
		`insert into usage_archive_runs (
			id, schema_version, format, status, cutoff_timestamp_ms,
			target_event_id, event_count, created_at_ms, updated_at_ms
		) values ('run-1', 1, 'gzip-jsonl-v1', 'archived', 1, 1, 1, 1, 1)`,
		`insert into usage_archive_segments (
			run_id, sequence, status, file_name, first_event_id, last_event_id,
			min_timestamp_ms, max_timestamp_ms, event_count,
			uncompressed_bytes, compressed_bytes, content_sha256,
			event_hash_digest, created_at_ms
		) values ('run-1', 1, 'verified', 'segment-1.jsonl.gz', 1, 1, 1, 1, 1, 1, 1, 'sha', 'digest', 1)`,
		`insert into usage_event_identity_ledger (
			event_hash, raw_event_id, timestamp_ms, bucket_ms, first_seen_at_ms, updated_at_ms
		) values ('archived-hash', 1, 1, 0, 1, 1)`,
		`insert into usage_archive_event_refs (
			event_hash, run_id, segment_sequence, raw_event_id, timestamp_ms,
			archived_at_ms, raw_deleted_at_ms
		) values ('archived-hash', 'run-1', 1, 1, 1, 1, 1)`,
	} {
		if _, err := sqlDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare archive reference: %v", err)
		}
	}
	if _, err := Rebuild(ctx, sqlDB, Options{}); err == nil {
		t.Fatal("rebuild must refuse archived raw rows by default")
	}
	if _, err := Rebuild(ctx, sqlDB, Options{AllowArchivedRaw: true}); err != nil {
		t.Fatalf("explicit archived raw override: %v", err)
	}
}
