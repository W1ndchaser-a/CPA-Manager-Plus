package usageevent

import (
	"context"
	"database/sql"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

// peakBook maps each priced model to its configured peak windows. A model
// with an empty slice is priced but has no windows; it still wins pricing
// resolution so a lower-priority candidate's windows are not applied to it.
type peakBook map[string][]model.ModelPricePeakWindow

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func loadPeakBook(ctx context.Context, db queryer) (peakBook, error) {
	rows, err := db.QueryContext(ctx, `select p.model, w.days_mask, w.start_hour, w.end_hour
		from model_prices p
		left join model_price_peak_windows w on w.model = p.model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	book := peakBook{}
	for rows.Next() {
		var modelID string
		var daysMask, startHour, endHour sql.NullInt64
		if err := rows.Scan(&modelID, &daysMask, &startHour, &endHour); err != nil {
			return nil, err
		}
		windows := book[modelID]
		if daysMask.Valid {
			windows = append(windows, model.ModelPricePeakWindow{
				Days:      model.PeakWindowDaysFromMask(int(daysMask.Int64)),
				StartHour: int(startHour.Int64),
				EndHour:   int(endHour.Int64),
			})
		}
		book[modelID] = windows
	}
	return book, rows.Err()
}

// peakServiceTier returns the service tier for an event: "peak" when the
// event's priced model has a window covering its timestamp and the provider
// reported no explicit tier, otherwise the tier unchanged. The priced model
// follows the same precedence as the pricing rollups: resolved model, then
// analytics (display) model, then requested model.
func (book peakBook) peakServiceTier(event model.UsageEvent) string {
	if !defaultTier(event.ServiceTier) {
		return event.ServiceTier
	}
	for _, candidate := range []string{event.ResolvedModel, event.Model, event.RequestedModel} {
		windows, priced := book[strings.TrimSpace(candidate)]
		if !priced {
			continue
		}
		if model.InPeakWindow(windows, event.TimestampMS) {
			return model.ModelPriceServiceTierPeak
		}
		return event.ServiceTier
	}
	return event.ServiceTier
}

// defaultTier reports tiers that mean "standard processing" and may therefore
// be re-labelled as peak. Explicit tiers such as priority or flex are kept.
func defaultTier(tier string) bool {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "", "auto", "default":
		return true
	default:
		return false
	}
}
