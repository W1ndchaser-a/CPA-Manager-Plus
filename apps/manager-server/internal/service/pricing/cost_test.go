package pricing

import (
	"math"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

func TestCostForModelSeparatesCachedInputTokens(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-cached": {Prompt: 2, Completion: 4, Cache: 1},
	}

	cost := CostForModel("gpt-cached", ModelTokens{
		InputTokens:  1_000_000,
		OutputTokens: 500_000,
		CachedTokens: 250_000,
	}, prices)

	if math.Abs(cost-3.75) > 0.000001 {
		t.Fatalf("cost = %v, want 3.75", cost)
	}
}

func TestCostForModelDoesNotCreateNegativePromptCost(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-cached": {Prompt: 10, Cache: 1},
	}

	cost := CostForModel("gpt-cached", ModelTokens{
		InputTokens:  100_000,
		CachedTokens: 250_000,
	}, prices)

	if math.Abs(cost-0.25) > 0.000001 {
		t.Fatalf("cost = %v, want 0.25", cost)
	}
}

func TestCostForModelPricesFineGrainedCacheOutsideInput(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"claude-cached": {Prompt: 2, Completion: 4, Cache: 1, CacheRead: 1, CacheCreation: 3},
	}

	cost := CostForModel("claude-cached", ModelTokens{
		InputTokens:         2_600_000,
		OutputTokens:        250_000,
		CachedTokens:        0,
		CacheReadTokens:     2_000_000,
		CacheCreationTokens: 100_000,
	}, prices)

	if math.Abs(cost-4.3) > 0.000001 {
		t.Fatalf("cost = %v, want 4.3", cost)
	}
}

func TestCostForModelPricesResidualCompatCachedWithFineGrainedCache(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"mixed-cache": {Prompt: 2, Completion: 4, Cache: 1, CacheRead: 0.5, CacheCreation: 3},
	}

	cost := CostForModel("mixed-cache", ModelTokens{
		InputTokens:         1_300_000,
		CachedTokens:        100_000,
		CacheReadTokens:     200_000,
		CacheCreationTokens: 100_000,
	}, prices)

	if math.Abs(cost-2.3) > 0.000001 {
		t.Fatalf("cost = %v, want 2.3", cost)
	}
}

func TestCostForModelDoesNotDoubleBillOpenAICacheMirror(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.4-mini": {Prompt: 2, Cache: 1, CacheRead: 1},
	}
	cost := CostForModel("gpt-5.4-mini", ModelTokens{
		InputTokens:     1_000_000,
		CacheReadTokens: 400_000,
	}, prices)
	if math.Abs(cost-1.6) > 0.000001 {
		t.Fatalf("cost = %v, want 1.6", cost)
	}
}

func TestCostForModelAppliesContextTierToWholeAggregate(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"tiered-model": {
			Prompt: 1, Completion: 2,
			ContextTiers: []model.ModelPriceContextTier{
				{ThresholdTokens: 100, Prompt: 3, Completion: 4, PromptConfigured: true, CompletionConfigured: true},
			},
		},
	}
	cost := CostForModel("tiered-model", ModelTokens{
		ContextThresholdTokens: 100,
		InputTokens:            1_000_000,
		OutputTokens:           500_000,
	}, prices)
	if math.Abs(cost-5) > 0.000001 {
		t.Fatalf("tiered cost = %v, want 5", cost)
	}
	baseCost := CostForModel("tiered-model", ModelTokens{
		ContextThresholdTokens: model.ModelPriceBaseContextThreshold,
		InputTokens:            1_000_000,
		OutputTokens:           500_000,
	}, prices)
	if math.Abs(baseCost-2) > 0.000001 {
		t.Fatalf("base-band cost = %v, want 2", baseCost)
	}
}

func TestContextTierOverridesLegacyLongContextPricing(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.6-sol": {
			Prompt: 5, Completion: 30,
			ContextTiers: []model.ModelPriceContextTier{
				{ThresholdTokens: 272_000, Prompt: 10, Completion: 40, PromptConfigured: true, CompletionConfigured: true},
			},
		},
	}
	cost := CostForModel("gpt-5.6-sol", ModelTokens{
		ContextThresholdTokens: 272_000,
		InputTokens:            1_000_000,
		LongInputTokens:        1_000_000,
	}, prices)
	if math.Abs(cost-10) > 0.000001 {
		t.Fatalf("generic context-tier cost = %v, want 10", cost)
	}
}

func TestGPT61LongFastUsesExplicitServiceTierContextTier(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-6.1-sol": {
			Prompt: 2, Completion: 10, Cache: 0.1, CacheRead: 0.1, CacheCreation: 2.5,
			PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true, CacheCreationConfigured: true,
			ContextTiers: []model.ModelPriceContextTier{
				{ThresholdTokens: 272_000, Prompt: 4, Completion: 15, CacheRead: 0.2, CacheCreation: 5, PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true, CacheCreationConfigured: true},
			},
			ServiceTiers: []model.ModelPriceServiceTier{
				{
					Mode: "fast", ServiceTier: "priority",
					Prompt: 4, Completion: 20, CacheRead: 0.2, CacheCreation: 5,
					PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true, CacheCreationConfigured: true,
					ContextTiers: []model.ModelPriceContextTier{
						{ThresholdTokens: 272_000, Prompt: 8, Completion: 30, CacheRead: 0.4, CacheCreation: 10, PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true, CacheCreationConfigured: true},
					},
				},
			},
		},
	}
	tokens := ModelTokens{
		ContextThresholdTokens: 272_000,
		InputTokens:            1_000_000,
		OutputTokens:           1_000_000,
	}
	res := CalculateCostForModel("gpt-6.1-sol", "fast", tokens, prices)
	// 1M prompt @ $8/1M + 1M completion @ $30/1M = 38.0
	if math.Abs(res.Cost-38.0) > 0.000001 {
		t.Fatalf("gpt-6.1 long fast cost = %v, want 38.0", res.Cost)
	}
	if !res.Complete {
		t.Fatalf("expected complete pricing for gpt-6.1 long fast")
	}
}

func TestExplicitFastPriceAppliesToBaseContextBandForFastAndPriority(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.5": {
			Prompt: 5, Completion: 30,
			ContextTiers: []model.ModelPriceContextTier{
				{ThresholdTokens: 272_000, Prompt: 10, Completion: 45, PromptConfigured: true, CompletionConfigured: true},
			},
			ServiceTiers: []model.ModelPriceServiceTier{
				{Mode: "fast", ServiceTier: "priority", Prompt: 12.5, Completion: 75, PromptConfigured: true, CompletionConfigured: true},
			},
		},
	}
	tokens := ModelTokens{
		ContextThresholdTokens: model.ModelPriceBaseContextThreshold,
		InputTokens:            100_000,
		OutputTokens:           10_000,
	}
	for _, tier := range []string{"fast", "priority"} {
		if got := CostForModelWithServiceTier("gpt-5.5", tier, tokens, prices); math.Abs(got-2) > 0.000001 {
			t.Fatalf("%s base-band cost = %v, want 2", tier, got)
		}
	}
	if got := CostForModelWithServiceTier("gpt-5.5", "default", tokens, prices); math.Abs(got-0.8) > 0.000001 {
		t.Fatalf("default base-band cost = %v, want 0.8", got)
	}
}

func TestExplicitFastBaseBandDoesNotReenableLegacyLongContext(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.5": {
			Prompt: 5, Completion: 30,
			ContextTiers: []model.ModelPriceContextTier{
				{ThresholdTokens: 500_000, Prompt: 10, Completion: 45, PromptConfigured: true, CompletionConfigured: true},
			},
			ServiceTiers: []model.ModelPriceServiceTier{
				{Mode: "fast", ServiceTier: "priority", Prompt: 10, Completion: 20, PromptConfigured: true, CompletionConfigured: true},
			},
		},
	}
	tokens := ModelTokens{
		ContextThresholdTokens: model.ModelPriceBaseContextThreshold,
		InputTokens:            300_000,
		OutputTokens:           100_000,
		LongInputTokens:        300_000,
		LongOutputTokens:       100_000,
	}
	if got := CostForModelWithServiceTier("gpt-5.5", "priority", tokens, prices); math.Abs(got-5) > 0.000001 {
		t.Fatalf("priority base-band cost = %v, want 5", got)
	}
}

func TestLegacyLongContextAppliesServiceTierOnlyToShortSegment(t *testing.T) {
	tokens := ModelTokens{
		InputTokens:     400_000,
		LongInputTokens: 300_000,
	}
	tests := []struct {
		name   string
		prices map[string]model.ModelPrice
	}{
		{
			name: "explicit fast price",
			prices: map[string]model.ModelPrice{
				"gpt-5.5": {
					Prompt: 5,
					ServiceTiers: []model.ModelPriceServiceTier{
						{Mode: "fast", ServiceTier: "priority", Prompt: 12.5, PromptConfigured: true},
					},
				},
			},
		},
		{
			name: "compatibility multiplier",
			prices: map[string]model.ModelPrice{
				"gpt-5.5": {Prompt: 5},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CostForModelWithServiceTier("gpt-5.5", "priority", tokens, tt.prices); math.Abs(got-4.25) > 0.000001 {
				t.Fatalf("mixed priority cost = %v, want 4.25", got)
			}
		})
	}
}

func TestLegacyLongContextSplitsEveryTokenBucketForPriorityPricing(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.5": {
			Prompt: 5, Completion: 10, Cache: 1, CacheRead: 2, CacheCreation: 3,
			ServiceTiers: []model.ModelPriceServiceTier{
				{
					Mode: "fast", ServiceTier: "priority",
					Prompt: 10, Completion: 20, Cache: 2, CacheRead: 4, CacheCreation: 6,
					PromptConfigured: true, CompletionConfigured: true, CacheConfigured: true,
					CacheReadConfigured: true, CacheCreationConfigured: true,
				},
			},
		},
	}
	tokens := ModelTokens{
		InputTokens:             400_000,
		OutputTokens:            40_000,
		CachedTokens:            40_000,
		CacheReadTokens:         40_000,
		CacheCreationTokens:     40_000,
		LongInputTokens:         300_000,
		LongOutputTokens:        30_000,
		LongCachedTokens:        30_000,
		LongCacheReadTokens:     30_000,
		LongCacheCreationTokens: 30_000,
	}
	if got := CostForModelWithServiceTier("gpt-5.5", "priority", tokens, prices); math.Abs(got-3.93) > 0.000001 {
		t.Fatalf("mixed priority token-bucket cost = %v, want 3.93", got)
	}
}

func TestLegacyLongContextKeepsFlexAndBatchDiscounts(t *testing.T) {
	prices := map[string]model.ModelPrice{"gpt-5.5": {Prompt: 5}}
	tokens := ModelTokens{
		InputTokens:     400_000,
		LongInputTokens: 300_000,
	}
	standard := CostForModelWithServiceTier("gpt-5.5", "default", tokens, prices)
	for _, tier := range []string{"flex", "batch"} {
		if got := CostForModelWithServiceTier("gpt-5.5", tier, tokens, prices); math.Abs(got-standard*0.5) > 0.000001 {
			t.Fatalf("%s mixed long-context cost = %v, want %v", tier, got, standard*0.5)
		}
	}
}

func TestLegacyLongContextUsesExplicitNonPriorityServiceTierPrice(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.5": {
			Prompt: 5,
			ServiceTiers: []model.ModelPriceServiceTier{
				{Mode: "batch", ServiceTier: "batch", Prompt: 2, PromptConfigured: true},
			},
		},
	}
	tokens := ModelTokens{InputTokens: 300_000, LongInputTokens: 300_000}
	if got := CostForModelWithServiceTier("gpt-5.5", "batch", tokens, prices); math.Abs(got-1.2) > 0.000001 {
		t.Fatalf("explicit batch long-context cost = %v, want 1.2", got)
	}
}

func TestCostCandidatesUseClassifiedPricingModel(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"display-model": {Prompt: 1},
		"priced-alias": {
			Prompt: 2,
			ContextTiers: []model.ModelPriceContextTier{
				{ThresholdTokens: 100, Prompt: 7, PromptConfigured: true},
			},
		},
	}
	cost := CostForModelCandidatesWithServiceTier(
		[]string{"missing-resolved", "display-model"},
		"default",
		ModelTokens{
			PricingModel:           "priced-alias",
			ContextThresholdTokens: 100,
			InputTokens:            1_000_000,
		},
		prices,
	)
	if math.Abs(cost-7) > 0.000001 {
		t.Fatalf("classified pricing-model cost = %v, want 7", cost)
	}
}

func TestServiceTierMultiplier(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		serviceTier string
		want        float64
	}{
		{name: "gpt-5.4 default", model: "gpt-5.4", serviceTier: "default", want: 1},
		{name: "gpt-5.6 priority", model: "gpt-5.6-sol", serviceTier: "priority", want: 2},
		{name: "namespaced gpt-5.6 fast", model: "openai/gpt-5.6-terra", serviceTier: "fast", want: 2},
		{name: "gpt-5.4 priority", model: "gpt-5.4", serviceTier: "priority", want: 2},
		{name: "gpt-5.4 fast", model: "gpt-5.4", serviceTier: "fast", want: 2},
		{name: "gpt-5.4 mini priority", model: "gpt-5.4-mini", serviceTier: "priority", want: 2},
		{name: "gpt-5.5 priority", model: "gpt-5.5", serviceTier: "priority", want: 2.5},
		{name: "gpt-5.3 codex priority", model: "gpt-5.3-codex", serviceTier: "priority", want: 2},
		{name: "unknown tier", model: "gpt-5.4", serviceTier: "accelerated", want: 1},
		{name: "unknown priority model", model: "gpt-unknown", serviceTier: "priority", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServiceTierMultiplier(tt.model, tt.serviceTier)
			if got != tt.want {
				t.Fatalf("ServiceTierMultiplier(%q, %q) = %v, want %v", tt.model, tt.serviceTier, got, tt.want)
			}
		})
	}
}

func TestCostForUnpricedModelReturnsZeroAndIncomplete(t *testing.T) {
	res := CalculateCostForModel("openai/gpt-5.6-sol", "default", ModelTokens{
		InputTokens:  1_000_000,
		OutputTokens: 100_000,
	}, nil)
	if res.Cost != 0 || res.Complete {
		t.Fatalf("unpriced model result = %+v, want cost=0 complete=false", res)
	}
}

func TestCostForGPT56PrefersConfiguredBasePrice(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.6-sol": {Prompt: 9, Completion: 18},
	}

	cost := CostForModel("gpt-5.6-sol", ModelTokens{InputTokens: 1_000_000}, prices)
	if math.Abs(cost-9) > 0.000001 {
		t.Fatalf("configured cost = %v, want 9", cost)
	}
}

func TestCostForGPT56AppliesLongContextMultipliers(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.6-sol": {
			Prompt: 2, Completion: 12, CacheRead: 0.2, CacheCreation: 2.5,
			PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true, CacheCreationConfigured: true,
		},
	}
	tokens := ModelTokens{
		InputTokens:             1_000_000,
		OutputTokens:            100_000,
		CacheReadTokens:         200_000,
		CacheCreationTokens:     100_000,
		LongInputTokens:         1_000_000,
		LongOutputTokens:        100_000,
		LongCacheReadTokens:     200_000,
		LongCacheCreationTokens: 100_000,
	}

	cost := CostForModel("gpt-5.6-sol", tokens, prices)
	if cost <= 0 {
		t.Fatalf("long-context cost = %v, want positive cost", cost)
	}
}

func TestCostForGPT56KeepsExactly272KAtStandardRates(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.6-luna": {
			Prompt: 0.2, Completion: 1.2,
			PromptConfigured: true, CompletionConfigured: true,
		},
	}
	cost := CostForModel("gpt-5.6-luna", ModelTokens{
		InputTokens:  272_000,
		OutputTokens: 100_000,
	}, prices)

	// 272K * 0.2 / 1M = 0.0544; 100K * 1.2 / 1M = 0.12 => 0.1744
	if math.Abs(cost-0.1744) > 0.000001 {
		t.Fatalf("272K cost = %v, want 0.1744", cost)
	}
}


func TestCostForGPT56RespectsExplicitZeroPrices(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.6-sol": {
			Prompt: 5, Completion: 30, Cache: 0.5,
			PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true, CacheCreationConfigured: true,
		},
	}
	cost := CostForModel("gpt-5.6-sol", ModelTokens{
		InputTokens:         250_000,
		CacheReadTokens:     150_000,
		CacheCreationTokens: 100_000,
	}, prices)

	if cost != 0 {
		t.Fatalf("explicit-zero cost = %v, want 0", cost)
	}
}


func TestCostForModelCandidatesKeepsResolvedNonGPTBehavior(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"resolved-other": {Prompt: 2, Completion: 4},
	}
	cost := CostForModelCandidatesWithServiceTier(
		[]string{"resolved-other", "gpt-5.6-sol"},
		"priority",
		ModelTokens{InputTokens: 1_000_000, LongInputTokens: 1_000_000},
		prices,
	)

	if math.Abs(cost-2) > 0.000001 {
		t.Fatalf("resolved non-GPT cost = %v, want 2", cost)
	}
}

func TestCostForModelWithServiceTier(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.4": {Prompt: 2.5, Completion: 5, Cache: 1},
	}

	tokens := ModelTokens{InputTokens: 1_000_000}
	if cost := CostForModelWithServiceTier("gpt-5.4", "default", tokens, prices); math.Abs(cost-2.5) > 0.000001 {
		t.Fatalf("default cost = %v, want 2.5", cost)
	}
	if cost := CostForModelWithServiceTier("gpt-5.4", "priority", tokens, prices); math.Abs(cost-5) > 0.000001 {
		t.Fatalf("priority cost = %v, want 5", cost)
	}
	if cost := CostForModelWithServiceTier("missing-model", "priority", tokens, prices); cost != 0 {
		t.Fatalf("missing model cost = %v, want 0", cost)
	}
}

func TestCostForModelCandidatesWithServiceTierFallsBackToRequestedModel(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.4": {Prompt: 2.5, Completion: 5, Cache: 1},
	}

	cost := CostForModelCandidatesWithServiceTier(
		[]string{"missing-upstream", "gpt-5.4"},
		"priority",
		ModelTokens{InputTokens: 1_000_000},
		prices,
	)

	if math.Abs(cost-2.5) > 0.000001 {
		t.Fatalf("fallback cost = %v, want 2.5", cost)
	}
}

func TestCostForModelCandidatesWithServiceTierPrefersResolvedModel(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-resolved": {Prompt: 1, Completion: 2, Cache: 0.5},
		"gpt-5.4":      {Prompt: 2.5, Completion: 5, Cache: 1},
	}

	cost := CostForModelCandidatesWithServiceTier(
		[]string{"gpt-resolved", "gpt-5.4"},
		"priority",
		ModelTokens{InputTokens: 1_000_000},
		prices,
	)

	if math.Abs(cost-1) > 0.000001 {
		t.Fatalf("resolved cost = %v, want 1", cost)
	}
}

func TestCostForModelWithServiceTierPreservesCacheBuckets(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.4": {Prompt: 2, Completion: 4, Cache: 1, CacheRead: 0.5, CacheCreation: 3},
	}

	cost := CostForModelWithServiceTier("gpt-5.4", "priority", ModelTokens{
		InputTokens:         1_300_000,
		CachedTokens:        100_000,
		CacheReadTokens:     200_000,
		CacheCreationTokens: 100_000,
	}, prices)

	if math.Abs(cost-4.6) > 0.000001 {
		t.Fatalf("priority cache cost = %v, want 4.6", cost)
	}
}

func TestLongContextPremiumCoversGPT54AndGPT55(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.4": {Prompt: 2.5, Completion: 15, Cache: 0.25},
		"gpt-5.5": {Prompt: 5, Completion: 30, Cache: 0.5},
	}
	tokens := ModelTokens{
		InputTokens:      300_000,
		OutputTokens:     100_000,
		LongInputTokens:  300_000,
		LongOutputTokens: 100_000,
	}
	if got := CostForModel("gpt-5.4", tokens, prices); math.Abs(got-3.75) > 0.000001 {
		t.Fatalf("gpt-5.4 long cost = %v, want 3.75", got)
	}
	if got := CostForModel("gpt-5.5", tokens, prices); math.Abs(got-7.5) > 0.000001 {
		t.Fatalf("gpt-5.5 long cost = %v, want 7.5", got)
	}
}

func TestFlexUsesHalfPrice(t *testing.T) {
	prices := map[string]model.ModelPrice{"gpt-5.5": {Prompt: 5, Completion: 30, Cache: 0.5}}
	got := CostForModelWithServiceTier("gpt-5.5", "flex", ModelTokens{InputTokens: 1_000_000}, prices)
	if math.Abs(got-2.5) > 0.000001 {
		t.Fatalf("flex cost = %v, want 2.5", got)
	}
}

func BenchmarkCostForModelWithExplicitServiceTier(b *testing.B) {
	prices := map[string]model.ModelPrice{
		"gpt-5.5": {
			Prompt: 5, Completion: 30, Cache: 0.5,
			ContextTiers: []model.ModelPriceContextTier{
				{ThresholdTokens: 272_000, Prompt: 10, Completion: 45, PromptConfigured: true, CompletionConfigured: true},
			},
			ServiceTiers: []model.ModelPriceServiceTier{
				{Mode: "fast", ServiceTier: "priority", Prompt: 12.5, Completion: 75, PromptConfigured: true, CompletionConfigured: true},
			},
		},
	}
	tokens := ModelTokens{
		ContextThresholdTokens: model.ModelPriceBaseContextThreshold,
		InputTokens:            100_000,
		OutputTokens:           10_000,
	}
	b.ReportAllocs()
	var cost float64
	for b.Loop() {
		cost = CostForModelWithServiceTier("gpt-5.5", "priority", tokens, prices)
	}
	if cost == 0 {
		b.Fatal("cost = 0")
	}
}

func TestPeakServiceTierUsesPeakRates(t *testing.T) {
	prices := map[string]model.ModelPrice{"deepseek-v4.1-flash": {
		Prompt: 0.15, Completion: 0.6, Cache: 0.003, CacheRead: 0.003,
		PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true,
		ServiceTiers: []model.ModelPriceServiceTier{{
			Mode: "peak", ServiceTier: "peak", Prompt: 0.3, Completion: 1.2, CacheRead: 0.006,
			PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true,
		}},
	}}
	tokens := ModelTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	base := CostForModelWithServiceTier("deepseek-v4.1-flash", "auto", tokens, prices)
	peak := CostForModelWithServiceTier("deepseek-v4.1-flash", model.ModelPriceServiceTierPeak, tokens, prices)
	if base < 0.7499 || base > 0.7501 || peak < 1.4999 || peak > 1.5001 {
		t.Fatalf("base = %v, peak = %v; want 0.75 and 1.5", base, peak)
	}
}

func TestGemini31LongPriorityAndFlexSelectExactRates(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gemini-3.1-pro-low": {
			Prompt: 2, Completion: 12, Cache: 0.2, CacheRead: 0.2,
			PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true,
			ContextTiers: []model.ModelPriceContextTier{
				{ThresholdTokens: 200_000, Prompt: 4, Completion: 18, CacheRead: 0.4, PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true},
			},
			ServiceTiers: []model.ModelPriceServiceTier{
				{
					Mode: "priority", ServiceTier: "priority",
					Prompt: 3.6, Completion: 21.6, CacheRead: 0.36,
					PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true,
					ContextTiers: []model.ModelPriceContextTier{
						{ThresholdTokens: 200_000, Prompt: 7.2, Completion: 32.4, CacheRead: 0.72, PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true},
					},
				},
				{
					Mode: "flex", ServiceTier: "flex",
					Prompt: 1, Completion: 6, CacheRead: 0.2,
					PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true,
					ContextTiers: []model.ModelPriceContextTier{
						{ThresholdTokens: 200_000, Prompt: 2, Completion: 9, CacheRead: 0.4, PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true},
					},
				},
			},
		},
	}

	tokens := ModelTokens{
		ContextThresholdTokens: 200_000,
		InputTokens:            1_000_000,
		OutputTokens:           1_000_000,
	}

	prioRes := CalculateCostForModel("gemini-3.1-pro-low", "priority", tokens, prices)
	// 1M @ 7.2 + 1M @ 32.4 = 39.6
	if math.Abs(prioRes.Cost-39.6) > 0.000001 {
		t.Fatalf("gemini 3.1 long priority cost = %v, want 39.6", prioRes.Cost)
	}
	if !prioRes.Complete {
		t.Fatalf("expected complete pricing for gemini 3.1 long priority")
	}

	flexRes := CalculateCostForModel("gemini-3.1-pro-low", "flex", tokens, prices)
	// 1M @ 2 + 1M @ 9 = 11.0
	if math.Abs(flexRes.Cost-11.0) > 0.000001 {
		t.Fatalf("gemini 3.1 long flex cost = %v, want 11.0", flexRes.Cost)
	}
	if !flexRes.Complete {
		t.Fatalf("expected complete pricing for gemini 3.1 long flex")
	}
}

func TestGPT55LongFastIsUnsupportedAndUnquoted(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-5.5": {
			Prompt: 5, Completion: 30, CacheRead: 0.5,
			PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true,
			ContextTiers: []model.ModelPriceContextTier{
				{ThresholdTokens: 272_000, Prompt: 10, Completion: 45, CacheRead: 1, PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true},
			},
			ServiceTiers: []model.ModelPriceServiceTier{
				{Mode: "fast", ServiceTier: "priority", Prompt: 12.5, Completion: 75, CacheRead: 1.25, PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true},
			},
		},
	}

	tokens := ModelTokens{
		ContextThresholdTokens: 272_000,
		InputTokens:            1_000_000,
		OutputTokens:           100_000,
	}
	res := CalculateCostForModel("gpt-5.5", "fast", tokens, prices)
	if res.Complete {
		t.Fatalf("gpt-5.5 long fast must be unquoted and marked incomplete, got complete=true")
	}
	if res.Cost != 0 {
		t.Fatalf("gpt-5.5 long fast must not guess price or disguise rate, got cost=%v", res.Cost)
	}
}

func TestMultimodalAndTTLSubBucketsExactCalculation(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-image-2": {
			Prompt: 5, Completion: 30, CacheRead: 1.25, CacheCreation: 5,
			PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true, CacheCreationConfigured: true,
			ImagePrompt: 8, ImagePromptConfigured: true,
			ImageCompletion: 30, ImageCompletionConfigured: true,
			ImageCacheRead: 2, ImageCacheReadConfigured: true,
			CacheCreation1h: 8, CacheCreation1hConfigured: true,
		},
	}

	tokens := ModelTokens{
		InputTokens:           2_000_000,
		OutputTokens:          500_000,
		CacheReadTokens:       400_000,
		CacheCreationTokens:   600_000,
		ImageInputTokens:      400_000, // Total image input including 100K image cache read (300K prompt)
		ImageOutputTokens:     200_000,
		ImageCacheReadTokens:  100_000,
		CacheCreation1hTokens: 200_000,
		ImageTokensKnown:      true,
		CacheCreationTTLKnown: true,
	}

	res := CalculateCostForModel("gpt-image-2", "default", tokens, prices)
	// Prompt: 2M - 400K (read) - 600K (create) = 1M prompt.
	// image prompt: 300K @ $8 = 2.4
	// text prompt: 700K @ $5 = 3.5
	// image cache read: 100K @ $2 = 0.2
	// text cache read: 300K @ $1.25 = 0.375
	// 1h cache create: 200K @ $8 = 1.6
	// 5m cache create: 400K @ $5 = 2.0
	// image output: 200K @ $30 = 6.0
	// text output: 300K @ $30 = 9.0
	// Total: 2.4 + 3.5 + 0.2 + 0.375 + 1.6 + 2.0 + 6.0 + 9.0 = 25.075
	if math.Abs(res.Cost-25.075) > 0.000001 {
		t.Fatalf("multimodal cost = %v, want 25.075", res.Cost)
	}
	if !res.Complete {
		t.Fatalf("expected complete result when all breakdowns known")
	}
}

func TestUnknownBreakdownsMarkIncompleteEstimate(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-image-2": {
			Prompt: 5, Completion: 30, CacheRead: 1.25, CacheCreation: 5,
			PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true, CacheCreationConfigured: true,
			ImagePrompt: 8, ImagePromptConfigured: true,
			ImageCompletion: 30, ImageCompletionConfigured: true,
			ImageCacheRead: 2, ImageCacheReadConfigured: true,
			CacheCreation1h: 8, CacheCreation1hConfigured: true,
		},
	}

	// Missing ImageTokensKnown
	tokensImageUnknown := ModelTokens{
		InputTokens:           1_000_000,
		OutputTokens:          100_000,
		ImageTokensKnown:      false,
		CacheCreationTTLKnown: true,
	}
	res1 := CalculateCostForModel("gpt-image-2", "default", tokensImageUnknown, prices)
	if res1.Complete {
		t.Fatalf("expected complete=false when ImageTokensKnown=false on distinct image rates")
	}

	// Missing CacheCreationTTLKnown
	tokensTTLUnknown := ModelTokens{
		InputTokens:           1_000_000,
		CacheCreationTokens:   500_000,
		ImageTokensKnown:      true,
		CacheCreationTTLKnown: false,
	}
	res2 := CalculateCostForModel("gpt-image-2", "default", tokensTTLUnknown, prices)
	if res2.Complete {
		t.Fatalf("expected complete=false when CacheCreationTTLKnown=false on distinct 1h rates")
	}
}

func TestExplicitZeroRatesArePreserved(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"free-model": {
			Prompt: 0, Completion: 0, CacheRead: 0, ImagePrompt: 0,
			PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true, ImagePromptConfigured: true,
		},
	}
	tokens := ModelTokens{
		InputTokens:      1_000_000,
		OutputTokens:     1_000_000,
		CacheReadTokens:  500_000,
		ImageInputTokens: 200_000,
		ImageTokensKnown: true,
	}
	res := CalculateCostForModel("free-model", "default", tokens, prices)
	if res.Cost != 0 {
		t.Fatalf("explicit zero cost = %v, want 0", res.Cost)
	}
	if !res.Complete {
		t.Fatalf("expected complete pricing for configured zero rates")
	}
}

func TestImageTotalInputIncludesCachedImageRead(t *testing.T) {
	// Exact sample from Main:
	// input=100, cacheRead=20, imageInput=80, imageRead=15, output=50, imageOutput=50
	// Rates: prompt: 5, completion: 30, cacheRead: 1.25, imagePrompt: 8, imageCompletion: 30, imageCacheRead: 2
	// Expected official cost: 0.00213125
	prices := map[string]model.ModelPrice{
		"gpt-image-2": {
			Prompt: 5, Completion: 30, CacheRead: 1.25, CacheCreation: 5,
			PromptConfigured: true, CompletionConfigured: true, CacheReadConfigured: true, CacheCreationConfigured: true,
			ImagePrompt: 8, ImagePromptConfigured: true,
			ImageCompletion: 30, ImageCompletionConfigured: true,
			ImageCacheRead: 2, ImageCacheReadConfigured: true,
		},
	}

	tokens := ModelTokens{
		InputTokens:          100,
		CacheReadTokens:      20,
		ImageInputTokens:     80,
		ImageCacheReadTokens: 15,
		OutputTokens:         50,
		ImageOutputTokens:    50,
		ImageTokensKnown:     true,
	}

	res := CalculateCostForModel("gpt-image-2", "default", tokens, prices)
	const want = 0.00213125
	if math.Abs(res.Cost-want) > 0.000000001 {
		t.Fatalf("cost = %.8f, want %.8f (diff = %e)", res.Cost, want, res.Cost-want)
	}
	if !res.Complete {
		t.Fatalf("expected complete pricing when image breakdowns known")
	}
}

func TestCanonicalAndPrefixedModelResolution(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"deepseek-v4.1-flash": {
			Prompt: 0.15, Completion: 0.6,
			PromptConfigured: true, CompletionConfigured: true,
			SourceModelID: "deepseek/deepseek-v4.1-flash",
		},
		"gpt-5.6-sol": {
			Prompt: 2, Completion: 12,
			PromptConfigured: true, CompletionConfigured: true,
		},
		"claude-sonnet-5-5": {
			Prompt: 3, Completion: 15,
			PromptConfigured: true, CompletionConfigured: true,
		},
		"gpt-6-astra": {
			Prompt: 10, Completion: 50,
			PromptConfigured: true, CompletionConfigured: true,
		},
		"claude-opus-4-6": {
			Prompt: 5, Completion: 25,
			PromptConfigured: true, CompletionConfigured: true,
		},
	}

	testCases := []struct {
		modelName  string
		wantPrompt float64
	}{
		{"deepseek/deepseek-v4.1-flash", 0.15},
		{"cmd/deepseek-v4.1-flash", 0.15},
		{"0029/gpt-5.6-sol", 2.0},
		{"vm2api/claude-sonnet-5-5", 3.0},
		{"cmd/claude-sonnet-5-5", 3.0},
		{"cmd/gpt-6-astra", 10.0},
		{"vm2api/claude-opus-4-6", 5.0},
		{"gpt-5.6", 2.0},
	}

	for _, tc := range testCases {
		res := CalculateCostForModel(tc.modelName, "default", ModelTokens{InputTokens: 1_000_000}, prices)
		if math.Abs(res.Cost-tc.wantPrompt) > 0.000001 {
			t.Fatalf("model %s cost = %v, want %v", tc.modelName, res.Cost, tc.wantPrompt)
		}
		if !res.Complete {
			t.Fatalf("model %s should resolve to complete price", tc.modelName)
		}
	}
}
