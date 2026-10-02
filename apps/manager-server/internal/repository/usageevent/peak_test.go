package usageevent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func TestInsertBatchLabelsPeakEvents(t *testing.T) {
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for _, stmt := range []string{
		`insert into model_prices (model, prompt_per_1m, completion_per_1m, cache_per_1m, updated_at_ms) values ('deepseek-v4.1-flash', 0.15, 0.6, 0.003, 1)`,
		`insert into model_prices (model, prompt_per_1m, completion_per_1m, cache_per_1m, updated_at_ms) values ('flat-model', 1, 2, 0.1, 1)`,
		// Monday-Friday 01-04 UTC.
		`insert into model_price_peak_windows (model, days_mask, start_hour, end_hour) values ('deepseek-v4.1-flash', 62, 1, 4)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	monday := func(hour int) time.Time { return time.Date(2026, 10, 5, hour, 0, 0, 0, time.UTC) }
	sunday := time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		model string
		at    time.Time
		tier  string
		want  string
	}{
		{"peak hour default tier", "deepseek-v4.1-flash", monday(2), "auto", "peak"},
		{"peak hour empty tier", "deepseek-v4.1-flash", monday(3), "", "peak"},
		{"off-peak hour", "deepseek-v4.1-flash", monday(5), "auto", "auto"},
		{"weekend", "deepseek-v4.1-flash", sunday, "auto", "auto"},
		{"explicit tier is kept", "deepseek-v4.1-flash", monday(2), "priority", "priority"},
		{"model without windows", "flat-model", monday(2), "auto", "auto"},
		{"unpriced model", "unknown", monday(2), "auto", "auto"},
	}
	repo := New(db)
	events := make([]usage.Event, len(cases))
	for i, c := range cases {
		events[i] = usage.Event{
			EventHash:   canonicalTestHash("peak-" + c.name),
			TimestampMS: c.at.UnixMilli(),
			Timestamp:   c.at.Format(time.RFC3339Nano),
			Provider:    "openai-compatible-commandcode",
			Model:       c.model,
			ServiceTier: c.tier,
			CreatedAtMS: c.at.UnixMilli(),
		}
	}
	if _, err := repo.InsertBatch(ctx, events); err != nil {
		t.Fatalf("insert: %v", err)
	}
	for i, c := range cases {
		var got string
		if err := db.QueryRowContext(ctx, `select coalesce(service_tier, '') from usage_events where event_hash = ?`, events[i].EventHash).Scan(&got); err != nil {
			t.Fatalf("%s: read tier: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: service_tier = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPeakBookResolvedModelWithoutWindowsWins(t *testing.T) {
	book := peakBook{
		"resolved": nil,
		"display":  {{Days: []int{0, 1, 2, 3, 4, 5, 6}, StartHour: 0, EndHour: 24}},
	}
	event := usage.Event{ResolvedModel: "resolved", Model: "display", TimestampMS: time.Now().UnixMilli(), ServiceTier: "auto"}
	if got := book.peakServiceTier(event); got != "auto" {
		t.Fatalf("priced resolved model without windows must win pricing resolution, got %q", got)
	}
}
