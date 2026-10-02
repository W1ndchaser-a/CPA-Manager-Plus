package model

import (
	"testing"
	"time"
)

func peakTestPrice(windows ...ModelPricePeakWindow) ModelPrice {
	return ModelPrice{
		Prompt: 0.15, Completion: 0.6, Cache: 0.003,
		ServiceTiers: []ModelPriceServiceTier{{
			Mode: "peak", ServiceTier: "peak",
			Prompt: 0.3, Completion: 1.2, Cache: 0.006,
			PromptConfigured: true, CompletionConfigured: true, CacheConfigured: true,
		}},
		PeakWindows: windows,
	}
}

func TestInPeakWindow(t *testing.T) {
	windows := []ModelPricePeakWindow{
		{Days: []int{1, 2, 3, 4, 5}, StartHour: 1, EndHour: 4},
		{Days: []int{1, 2, 3, 4, 5}, StartHour: 6, EndHour: 10},
	}
	// 2026-10-05 is a Monday, 2026-10-04 a Sunday.
	cases := []struct {
		at   time.Time
		want bool
	}{
		{time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC), true},
		{time.Date(2026, 10, 5, 3, 59, 59, 0, time.UTC), true},
		{time.Date(2026, 10, 5, 4, 0, 0, 0, time.UTC), false},
		{time.Date(2026, 10, 5, 5, 30, 0, 0, time.UTC), false},
		{time.Date(2026, 10, 5, 9, 59, 0, 0, time.UTC), true},
		{time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC), false},
		{time.Date(2026, 10, 5, 0, 59, 0, 0, time.UTC), false},
		{time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		if got := InPeakWindow(windows, c.at.UnixMilli()); got != c.want {
			t.Errorf("InPeakWindow(%s) = %v, want %v", c.at, got, c.want)
		}
	}
	if InPeakWindow(nil, time.Now().UnixMilli()) {
		t.Error("no windows must never be peak")
	}
}

func TestInPeakWindowUsesUTCWeekday(t *testing.T) {
	// Sunday 23:30 in UTC-5 is Monday 04:30 UTC; only the UTC reading counts.
	zone := time.FixedZone("UTC-5", -5*3600)
	at := time.Date(2026, 10, 4, 23, 30, 0, 0, zone)
	if !InPeakWindow([]ModelPricePeakWindow{{Days: []int{1}, StartHour: 4, EndHour: 5}}, at.UnixMilli()) {
		t.Error("expected Monday 04:30 UTC to match")
	}
}

func TestNormalizeModelPricePeakWindows(t *testing.T) {
	got, err := NormalizeModelPricePeakWindows([]ModelPricePeakWindow{
		{Days: []int{5, 1, 1}, StartHour: 6, EndHour: 10},
		{Days: []int{1}, StartHour: 1, EndHour: 4},
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(got) != 2 || got[0].StartHour != 1 || len(got[1].Days) != 2 || got[1].Days[0] != 1 || got[1].Days[1] != 5 {
		t.Fatalf("unexpected normalization: %#v", got)
	}
	for _, bad := range []ModelPricePeakWindow{
		{Days: []int{1}, StartHour: 4, EndHour: 4},
		{Days: []int{1}, StartHour: -1, EndHour: 4},
		{Days: []int{1}, StartHour: 20, EndHour: 25},
		{Days: nil, StartHour: 1, EndHour: 4},
		{Days: []int{7}, StartHour: 1, EndHour: 4},
	} {
		if _, err := NormalizeModelPricePeakWindows([]ModelPricePeakWindow{bad}); err == nil {
			t.Errorf("expected error for %#v", bad)
		}
	}
}

func TestPeakWindowDaysMaskRoundTrip(t *testing.T) {
	days := []int{0, 3, 6}
	got := PeakWindowDaysFromMask(PeakWindowDaysMask(days))
	if len(got) != 3 || got[0] != 0 || got[1] != 3 || got[2] != 6 {
		t.Fatalf("round trip = %v", got)
	}
}

func TestValidateModelPriceRequiresPeakTier(t *testing.T) {
	window := ModelPricePeakWindow{Days: []int{1}, StartHour: 1, EndHour: 4}
	if err := ValidateModelPrice("m", peakTestPrice(window)); err != nil {
		t.Fatalf("valid price rejected: %v", err)
	}
	noTier := peakTestPrice(window)
	noTier.ServiceTiers = nil
	if err := ValidateModelPrice("m", noTier); err == nil {
		t.Fatal("peak windows without a peak service tier rule must be rejected")
	}
}

func TestStructureRevisionTracksPeakWindowsOnlyWhenPresent(t *testing.T) {
	plain := map[string]ModelPrice{"m": {Prompt: 1, Completion: 2}}
	withEmpty := map[string]ModelPrice{"m": {Prompt: 1, Completion: 2, PeakWindows: []ModelPricePeakWindow{}}}
	if ModelPriceStructureRevision(plain) != ModelPriceStructureRevision(withEmpty) {
		t.Fatal("books without windows must keep their revision")
	}
	a := map[string]ModelPrice{"m": peakTestPrice(ModelPricePeakWindow{Days: []int{1}, StartHour: 1, EndHour: 4})}
	b := map[string]ModelPrice{"m": peakTestPrice(ModelPricePeakWindow{Days: []int{1}, StartHour: 1, EndHour: 5})}
	if ModelPriceStructureRevision(a) == ModelPriceStructureRevision(b) {
		t.Fatal("changing a window must change the revision")
	}
	if ModelPriceStructureRevision(a) == ModelPriceStructureRevision(plain) {
		t.Fatal("adding a window must change the revision")
	}
}
