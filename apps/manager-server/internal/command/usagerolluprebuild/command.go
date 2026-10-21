// Package usagerolluprebuild implements the offline "rebuild-usage-rollups"
// maintenance command. It invalidates billing-derived rollup caches after an
// operator corrects the cache_creation_1h_tokens/cache_creation_ttl_known (or
// image billing) columns of authoritative raw events, then lets Manager Server
// rebuild them from usage_events. It never writes usage_events, archive
// segments, archive references, model prices, or manager data snapshots.
package usagerolluprebuild

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageaggregate"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usagemonitoring"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usagepricing"
	_ "modernc.org/sqlite"
)

// BillingRollupTables lists every derived table whose rows carry billing
// dimensions copied from usage_events. Rows are cleared and rebuilt from raw
// events; none of these tables is authoritative.
var BillingRollupTables = []string{
	"usage_hourly_aggregate_v1",
	"usage_pricing_hourly_rollups_v1",
	"usage_pricing_account_rollups_v1",
	"usage_monitoring_account_daily_rollups_v1",
	"usage_monitoring_api_key_daily_rollups_v1",
	"usage_account_model_rollups",
	"usage_dashboard_hourly_rollups",
}

type Options struct {
	// DryRun reports the plan and current derived row counts without writing.
	DryRun bool
	// AllowArchivedRaw permits a rebuild when archived raw rows were deleted.
	// Those events cannot be re-derived into the aggregate/stats rollups; the
	// pricing rollup can still recover them from the retained projection.
	AllowArchivedRaw bool
}

type Result struct {
	DryRun                     bool             `json:"dryRun"`
	LatestEventID              int64            `json:"latestEventId"`
	ArchivedRawEvents          int64            `json:"archivedRawEvents"`
	AllowArchivedRaw           bool             `json:"allowArchivedRaw"`
	ProjectionRowsSynced       int64            `json:"projectionRowsSynced"`
	DeletedRows                map[string]int64 `json:"deletedRows"`
	AggregateStructureRevision string           `json:"aggregateStructureRevision,omitempty"`
}

type options struct {
	DBPath           string
	DryRun           bool
	AllowArchivedRaw bool
}

// Run is the CLI entry point.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	dbPath, err := resolveDBPath(opts.DBPath)
	if err != nil {
		return err
	}
	if err := validateDatabaseFile(dbPath); err != nil {
		return err
	}
	databaseLock, err := processlock.Acquire(dbPath)
	if err != nil {
		return fmt.Errorf("acquire offline rebuild lock; stop Manager Server and retry: %w", err)
	}
	defer func() { _ = databaseLock.Close() }()
	dbPath = databaseLock.DatabasePath()
	if err := validateManagerDB(ctx, dbPath); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite %s: %w", dbPath, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `pragma busy_timeout = 5000`); err != nil {
		return fmt.Errorf("set busy timeout: %w", err)
	}
	result, err := Rebuild(ctx, db, Options{DryRun: opts.DryRun, AllowArchivedRaw: opts.AllowArchivedRaw})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

// Rebuild clears the billing-derived rollup caches and resets their
// checkpoints so the running Manager Server rebuilds them in bounded batches.
// usage_events is read only.
func Rebuild(ctx context.Context, db *sql.DB, opts Options) (Result, error) {
	result := Result{
		DryRun:           opts.DryRun,
		AllowArchivedRaw: opts.AllowArchivedRaw,
		DeletedRows:      make(map[string]int64, len(BillingRollupTables)),
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := tx.QueryRowContext(ctx, `select coalesce(max(id), 0) from usage_events`).Scan(&result.LatestEventID); err != nil {
		return Result{}, fmt.Errorf("read latest usage event: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `select count(*) from usage_archive_event_refs
		where raw_deleted_at_ms is not null`).Scan(&result.ArchivedRawEvents); err != nil {
		return Result{}, fmt.Errorf("count archived raw events: %w", err)
	}
	nowMS := time.Now().UnixMilli()
	projectionQuery := `select count(*) from usage_monitoring_event_projection_v1 p
		join usage_events e on e.id = p.event_id
		where p.image_input_tokens <> coalesce(e.image_input_tokens, 0)
			or p.image_cache_read_tokens <> coalesce(e.image_cache_read_tokens, 0)
			or p.image_output_tokens <> coalesce(e.image_output_tokens, 0)
			or p.cache_creation_1h_tokens <> coalesce(e.cache_creation_1h_tokens, 0)
			or p.image_tokens_known <> coalesce(e.image_tokens_known, 0)
			or p.cache_creation_ttl_known <> coalesce(e.cache_creation_ttl_known, 0)`
	var projectionDrift int64
	if err := tx.QueryRowContext(ctx, projectionQuery).Scan(&projectionDrift); err != nil {
		return Result{}, fmt.Errorf("count projection billing drift: %w", err)
	}

	if opts.DryRun {
		result.ProjectionRowsSynced = projectionDrift
		for _, table := range BillingRollupTables {
			var count int64
			if err := tx.QueryRowContext(ctx, `select count(*) from `+table).Scan(&count); err != nil {
				return Result{}, fmt.Errorf("count %s: %w", table, err)
			}
			result.DeletedRows[table] = count
		}
		return result, nil
	}

	if result.ArchivedRawEvents > 0 && !opts.AllowArchivedRaw {
		return Result{}, fmt.Errorf(
			"refusing to rebuild: %d raw events were archived and deleted; the hourly aggregate and stats rollups cannot re-derive them (pricing can use the retained projection). Re-run with --allow-archived-raw only after accepting that archived periods leave those two caches",
			result.ArchivedRawEvents,
		)
	}

	syncStatement := `update usage_monitoring_event_projection_v1 as p set
		image_input_tokens = coalesce(e.image_input_tokens, 0),
		image_cache_read_tokens = coalesce(e.image_cache_read_tokens, 0),
		image_output_tokens = coalesce(e.image_output_tokens, 0),
		cache_creation_1h_tokens = coalesce(e.cache_creation_1h_tokens, 0),
		image_tokens_known = coalesce(e.image_tokens_known, 0),
		cache_creation_ttl_known = coalesce(e.cache_creation_ttl_known, 0),
		updated_at_ms = ?
	from usage_events as e
	where e.id = p.event_id
		and (p.image_input_tokens <> coalesce(e.image_input_tokens, 0)
			or p.image_cache_read_tokens <> coalesce(e.image_cache_read_tokens, 0)
			or p.image_output_tokens <> coalesce(e.image_output_tokens, 0)
			or p.cache_creation_1h_tokens <> coalesce(e.cache_creation_1h_tokens, 0)
			or p.image_tokens_known <> coalesce(e.image_tokens_known, 0)
			or p.cache_creation_ttl_known <> coalesce(e.cache_creation_ttl_known, 0))`
	synced, err := tx.ExecContext(ctx, syncStatement, nowMS)
	if err != nil {
		return Result{}, fmt.Errorf("sync projection billing dimensions: %w", err)
	}
	result.ProjectionRowsSynced, err = synced.RowsAffected()
	if err != nil {
		return Result{}, fmt.Errorf("count synced projection rows: %w", err)
	}

	for _, table := range BillingRollupTables {
		deleted, err := tx.ExecContext(ctx, `delete from `+table)
		if err != nil {
			return Result{}, fmt.Errorf("clear %s: %w", table, err)
		}
		count, err := deleted.RowsAffected()
		if err != nil {
			return Result{}, fmt.Errorf("count cleared rows in %s: %w", table, err)
		}
		result.DeletedRows[table] = count
	}

	if err := resetAggregateState(ctx, tx, result.LatestEventID, nowMS, &result); err != nil {
		return Result{}, err
	}
	if err := resetPricingState(ctx, tx, result.LatestEventID, nowMS); err != nil {
		return Result{}, err
	}
	if err := resetStatsState(ctx, tx, result.LatestEventID, nowMS); err != nil {
		return Result{}, err
	}
	if err := resetRollupCheckpoints(ctx, tx, result.LatestEventID, nowMS); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, err
	}
	return result, nil
}

func resetAggregateState(ctx context.Context, tx *sql.Tx, latestEventID, nowMS int64, result *Result) error {
	revision := fmt.Sprintf("%s:rebuild-%s", usageaggregate.StructureRevision, rebuildMarker(latestEventID, nowMS))
	if _, err := tx.ExecContext(ctx, `insert into usage_hourly_aggregate_state (
		aggregate_name, schema_version, status, backfill_last_event_id, coverage_event_id,
		target_event_id, processed_events, min_bucket_ms, max_bucket_ms,
		last_run_started_at_ms, updated_at_ms, finished_at_ms, last_error, structure_revision
	) values (?, ?, 'pending', 0, 0, ?, 0, null, null, null, ?, null, null, ?)
	on conflict(aggregate_name) do update set
		schema_version = excluded.schema_version,
		structure_revision = excluded.structure_revision,
		status = excluded.status,
		backfill_last_event_id = 0,
		coverage_event_id = 0,
		target_event_id = excluded.target_event_id,
		processed_events = 0,
		min_bucket_ms = null,
		max_bucket_ms = null,
		last_run_started_at_ms = null,
		updated_at_ms = excluded.updated_at_ms,
		finished_at_ms = null,
		last_error = null`,
		usageaggregate.AggregateName, usageaggregate.SchemaVersion, latestEventID, nowMS, revision,
	); err != nil {
		return fmt.Errorf("reset hourly aggregate state: %w", err)
	}
	result.AggregateStructureRevision = revision
	return nil
}

func resetPricingState(ctx context.Context, tx *sql.Tx, latestEventID, nowMS int64) error {
	if _, err := tx.ExecContext(ctx, `insert into usage_pricing_rollup_state (
		rollup_name, schema_version, structure_revision, status,
		backfill_last_event_id, coverage_event_id, target_event_id, processed_events,
		min_bucket_ms, max_bucket_ms, last_run_started_at_ms, updated_at_ms,
		finished_at_ms, last_error
	) values (?, ?, '', 'pending', 0, 0, ?, 0, null, null, null, ?, null, null)
	on conflict(rollup_name) do update set
		schema_version = excluded.schema_version,
		structure_revision = '',
		status = excluded.status,
		backfill_last_event_id = 0,
		coverage_event_id = 0,
		target_event_id = excluded.target_event_id,
		processed_events = 0,
		min_bucket_ms = null,
		max_bucket_ms = null,
		last_run_started_at_ms = null,
		updated_at_ms = excluded.updated_at_ms,
		finished_at_ms = null,
		last_error = null`,
		usagepricing.RollupName, usagepricing.SchemaVersion, latestEventID, nowMS,
	); err != nil {
		return fmt.Errorf("reset pricing rollup state: %w", err)
	}
	return nil
}

func resetStatsState(ctx context.Context, tx *sql.Tx, latestEventID, nowMS int64) error {
	if _, err := tx.ExecContext(ctx, `insert into usage_monitoring_rollup_state (
		rollup_name, schema_version, structure_revision, status,
		backfill_last_event_id, coverage_event_id, target_event_id, processed_events,
		last_run_started_at_ms, updated_at_ms, finished_at_ms, last_error
	) values (?, ?, '', 'pending', 0, 0, ?, 0, null, ?, null, null)
	on conflict(rollup_name) do update set
		schema_version = excluded.schema_version,
		structure_revision = '',
		status = excluded.status,
		backfill_last_event_id = 0,
		coverage_event_id = 0,
		target_event_id = excluded.target_event_id,
		processed_events = 0,
		last_run_started_at_ms = null,
		updated_at_ms = excluded.updated_at_ms,
		finished_at_ms = null,
		last_error = null`,
		usagemonitoring.StatsRollupName, usagemonitoring.SchemaVersion, latestEventID, nowMS,
	); err != nil {
		return fmt.Errorf("reset monitoring stats rollup state: %w", err)
	}
	return nil
}

func resetRollupCheckpoints(ctx context.Context, tx *sql.Tx, latestEventID, nowMS int64) error {
	for _, name := range []string{"account_history", "dashboard_hourly"} {
		if _, err := tx.ExecContext(ctx, `insert into usage_rollup_checkpoints (
			name, last_event_id, updated_at_ms, last_error,
			last_run_started_at_ms, last_run_finished_at_ms
		) values (?, 0, ?, null, null, null)
		on conflict(name) do update set
			last_event_id = 0,
			updated_at_ms = excluded.updated_at_ms,
			last_error = null,
			last_run_started_at_ms = null,
			last_run_finished_at_ms = null`, name, nowMS); err != nil {
			return fmt.Errorf("reset %s rollup checkpoint: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `delete from usage_rollup_rebuild_state
		where name in ('account_history', 'dashboard_hourly')`); err != nil {
		return fmt.Errorf("clear rollup rebuild targets: %w", err)
	}
	if latestEventID > 0 {
		if _, err := tx.ExecContext(ctx, `insert into usage_rollup_rebuild_state (
			name, target_event_id, updated_at_ms
		) values ('account_history', ?, 0), ('dashboard_hourly', ?, 0)`,
			latestEventID, latestEventID); err != nil {
			return fmt.Errorf("request rollup rebuilds: %w", err)
		}
	}
	return nil
}

func rebuildMarker(latestEventID, nowMS int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", latestEventID, nowMS)))
	return hex.EncodeToString(sum[:])[:32]
}

func parseArgs(args []string, stderr io.Writer) (options, error) {
	var opts options
	fs := flag.NewFlagSet("rebuild-usage-rollups", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.DBPath, "db-path", "", "SQLite database path; defaults to Manager Server config")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "report the plan and derived row counts without writing")
	fs.BoolVar(&opts.AllowArchivedRaw, "allow-archived-raw", false, "allow the rebuild when archived raw events were deleted")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: cpa-manager-plus rebuild-usage-rollups [--db-path PATH] [--dry-run] [--allow-archived-raw]")
		_, _ = fmt.Fprintln(stderr, "Clears billing-derived rollup caches so Manager Server rebuilds them from usage_events.")
		_, _ = fmt.Fprintln(stderr, "Stop Manager Server before running this offline command. usage_events is never modified.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return opts, nil
}

func resolveDBPath(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override), nil
	}
	cfg, err := config.LoadWithoutCreatingDefault()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	if strings.TrimSpace(cfg.DBPath) == "" {
		return "", errors.New("SQLite database path is empty; pass --db-path")
	}
	return cfg.DBPath, nil
}

func validateDatabaseFile(dbPath string) error {
	info, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("SQLite database not found at %s; pass --db-path", dbPath)
		}
		return fmt.Errorf("stat sqlite %s: %w", dbPath, err)
	}
	if info.IsDir() || info.Size() == 0 {
		return fmt.Errorf("SQLite database at %s is not a non-empty file", dbPath)
	}
	return nil
}

func validateManagerDB(ctx context.Context, dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(ctx, `select count(*) from sqlite_schema
		where type = 'table' and name in ('settings', 'usage_events')`).Scan(&count); err != nil {
		return fmt.Errorf("validate sqlite %s: %w", dbPath, err)
	}
	if count != 2 {
		return fmt.Errorf("SQLite database at %s does not look like a CPA Manager Plus Manager Server database", dbPath)
	}
	return nil
}
