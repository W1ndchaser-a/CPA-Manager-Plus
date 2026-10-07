// Package pricing converts token aggregates into monetary cost given a model price book.
package pricing

import (
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

// PerMillion divides by one million to convert token-priced units (per 1M tokens).
const PerMillion = 1_000_000.0

// ModelTokens represents the token totals consumed by a single model.
// CachedTokens is the remaining legacy/OpenAI-style cached input after any
// fine-grained cache_read/cache_creation values have already been removed.
type ModelTokens struct {
	PricingModel            string
	ContextThresholdTokens  int64
	InputTokens             int64
	OutputTokens            int64
	ReasoningTokens         int64
	TotalTokens             int64
	CachedTokens            int64
	CacheReadTokens         int64
	CacheCreationTokens     int64
	LongInputTokens         int64
	LongOutputTokens        int64
	LongCachedTokens        int64
	LongCacheReadTokens     int64
	LongCacheCreationTokens int64

	ImageInputTokens      int64
	ImageCacheReadTokens  int64
	ImageOutputTokens     int64
	CacheCreation1hTokens int64
	ImageTokensKnown      bool
	CacheCreationTTLKnown bool
}

// CostResult describes the monetary cost and whether the rate calculation
// was fully deterministic or had missing breakdowns / unquoted dimensions.
type CostResult struct {
	Cost     float64
	Complete bool
}

// CostForModel computes the dollar cost for a single (model, tokens) pair.
// InputTokens is the normalized total input, including cache buckets when the
// upstream protocol reports them separately. Fine-grained cache read/create
// dimensions are removed from prompt input and priced separately.
// Any residual CachedTokens are still charged at the legacy cache price; callers
// must pass the compatibility cached value, not CPA's Claude mirror copy.
// Older payloads keep the OpenAI-style cached-in-input behavior.
func CostForModel(modelName string, tokens ModelTokens, prices map[string]model.ModelPrice) float64 {
	return CalculateCostForModel(modelName, "default", tokens, prices).Cost
}

func costForPrice(modelName string, tokens ModelTokens, price model.ModelPrice) float64 {
	res := calculateCostForPrice(modelName, "default", tokens, price)
	return res.Cost
}

// ServiceTierMultiplier returns the OpenAI Priority processing multiplier for
// the actual usage service tier. This compatibility layer keeps today's tier
// multiplier rules centralized; a future price model should store explicit
// per-tier prices such as standard, priority, flex, and batch.
func ServiceTierMultiplier(modelName string, serviceTier string) float64 {
	tier := strings.ToLower(strings.TrimSpace(serviceTier))
	if tier == "flex" || tier == "batch" {
		return 0.5
	}
	if tier != "priority" && tier != "fast" {
		return 1
	}

	modelName = strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case isModelFamily(modelName, "gpt-5.6"):
		return 2
	case isModelFamily(modelName, "gpt-5.5"):
		return 2.5
	case isModelFamily(modelName, "gpt-5.4-mini"):
		return 2
	case isModelFamily(modelName, "gpt-5.4"):
		return 2
	case isModelFamily(modelName, "gpt-5.3-codex"):
		return 2
	default:
		return 1
	}
}

func CostForModelWithServiceTier(modelName string, serviceTier string, tokens ModelTokens, prices map[string]model.ModelPrice) float64 {
	return CalculateCostForModel(modelName, serviceTier, tokens, prices).Cost
}

// CalculateCostForModel computes cost and completeness for a single model and service tier.
func CalculateCostForModel(modelName string, serviceTier string, tokens ModelTokens, prices map[string]model.ModelPrice) CostResult {
	price, ok := resolveModelPrice(modelName, prices)
	if !ok {
		return CostResult{Cost: 0, Complete: false}
	}
	return calculateCostForPrice(modelName, serviceTier, tokens, price)
}

func CostForModelCandidatesWithServiceTier(modelNames []string, serviceTier string, tokens ModelTokens, prices map[string]model.ModelPrice) float64 {
	return CalculateCostForModelCandidates(modelNames, serviceTier, tokens, prices).Cost
}

// CalculateCostForModelCandidates selects candidate model rates and computes cost and completeness.
func CalculateCostForModelCandidates(modelNames []string, serviceTier string, tokens ModelTokens, prices map[string]model.ModelPrice) CostResult {
	seen := map[string]bool{}
	candidates := make([]string, 0, len(modelNames))
	for _, modelName := range modelNames {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" || seen[modelName] {
			continue
		}
		seen[modelName] = true
		candidates = append(candidates, modelName)
	}
	behaviorModel := ""
	if len(candidates) > 0 {
		behaviorModel = candidates[0]
	}
	if pricingModel := strings.TrimSpace(tokens.PricingModel); pricingModel != "" {
		if price, ok := prices[pricingModel]; ok {
			return calculateCostForPrice(behaviorModel, serviceTier, tokens, price)
		}
	}
	for _, modelName := range candidates {
		price, ok := prices[modelName]
		if !ok {
			continue
		}
		return calculateCostForPrice(behaviorModel, serviceTier, tokens, price)
	}
	return CostResult{Cost: 0, Complete: false}
}

// SumCost folds CostForModel over a slice of (model, tokens) tuples.
type Item struct {
	Model  string
	Tokens ModelTokens
}

// SumCost adds up the cost across multiple items.
func SumCost(items []Item, prices map[string]model.ModelPrice) float64 {
	total := 0.0
	for _, item := range items {
		total += CostForModel(item.Model, item.Tokens, prices)
	}
	return total
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func fallbackPrice(value float64, fallback float64) float64 {
	if value > 0 {
		return value
	}
	return fallback
}

func isModelFamily(modelName string, family string) bool {
	modelName = normalizedModelSlug(modelName)
	return modelName == family || strings.HasPrefix(modelName, family+"-")
}

func isGPT56Model(modelName string) bool {
	return isModelFamily(modelName, "gpt-5.6")
}

func supportsLongContextPremium(modelName string) bool {
	slug := normalizedModelSlug(modelName)
	if isGPT56Model(slug) {
		return true
	}
	if slug == "gpt-5.5" || strings.HasPrefix(slug, "gpt-5.5-20") {
		return true
	}
	return slug == "gpt-5.4" || strings.HasPrefix(slug, "gpt-5.4-20") ||
		slug == "gpt-5.4-pro" || strings.HasPrefix(slug, "gpt-5.4-pro-20")
}

func costForPriceWithServiceTier(modelName, serviceTier string, tokens ModelTokens, price model.ModelPrice) float64 {
	return calculateCostForPrice(modelName, serviceTier, tokens, price).Cost
}

func calculateCostForPrice(modelName, serviceTier string, tokens ModelTokens, price model.ModelPrice) CostResult {
	// Gemini reports thoughts outside candidates; OpenAI includes them in output.
	// The aggregate total preserves that distinction even for mixed protocols.
	remaining := maxInt64(tokens.TotalTokens, 0) - maxInt64(tokens.InputTokens, 0)
	if remaining > tokens.OutputTokens && tokens.ReasoningTokens > 0 {
		extra := clampTokens(remaining-tokens.OutputTokens, tokens.ReasoningTokens)
		tokens.OutputTokens += extra
		if tokens.LongInputTokens == tokens.InputTokens {
			tokens.LongOutputTokens += extra
		}
	}
	normalizedTier := strings.ToLower(strings.TrimSpace(serviceTier))
	isStandard := normalizedTier == "" || normalizedTier == "default" || normalizedTier == "standard" || normalizedTier == "auto"

	// 1. Legacy long context (only for older books where ContextTiers is not configured)
	if len(price.ContextTiers) == 0 && supportsLongContextPremium(modelName) && tokens.LongInputTokens > 0 {
		return computeLegacyLongContext(modelName, serviceTier, tokens, price)
	}

	// 2. Modern pricing: resolve effective rate overrides for this serviceTier and context band
	effectivePrice, matchedTier, tierComplete := model.ModelPriceForServiceTierAndContext(price, serviceTier, tokens.ContextThresholdTokens)
	if matchedTier {
		if !tierComplete {
			// Long context requested with a service tier that has no quotation (e.g. GPT-5.5 long Fast).
			// Must not guess with multiplier, must not ignore service tier!
			return CostResult{Cost: 0, Complete: false}
		}
		return computeSegmentCost(modelName, tokens, effectivePrice, 1, 1, tierComplete)
	}

	// 3. Service tier not explicitly configured in price.ServiceTiers
	if isStandard {
		if tokens.ContextThresholdTokens > 0 {
			if baseContextPrice, ok := model.ModelPriceForContextThreshold(price, tokens.ContextThresholdTokens); ok {
				return computeSegmentCost(modelName, tokens, baseContextPrice, 1, 1, true)
			}
			return CostResult{Cost: 0, Complete: false}
		}
		return computeSegmentCost(modelName, tokens, price, 1, 1, true)
	}

	// Non-standard service tier not configured in price.ServiceTiers:
	isGemini := isModelFamily(modelName, "gemini")
	if isGemini {
		// Do not guess Gemini flex/batch discounts or cache pricing with generic multipliers
		return CostResult{Cost: 0, Complete: false}
	}

	if len(price.ContextTiers) > 0 {
		// Model has explicit context tiers, but requested non-standard tier is unquoted
		if tokens.ContextThresholdTokens > 0 {
			if normalizedTier == "flex" || normalizedTier == "batch" {
				if baseContextPrice, ok := model.ModelPriceForContextThreshold(price, tokens.ContextThresholdTokens); ok {
					return computeSegmentCost(modelName, tokens, baseContextPrice, 0.5, 0.5, false)
				}
			}
			// Fast / Priority long context without quote cannot be guessed!
			return CostResult{Cost: 0, Complete: false}
		}
		// Base context band fallback for legacy books:
		mult := ServiceTierMultiplier(modelName, serviceTier)
		return computeSegmentCost(modelName, tokens, price, mult, mult, false)
	}

	mult := ServiceTierMultiplier(modelName, serviceTier)
	return computeSegmentCost(modelName, tokens, price, mult, mult, false)
}

func computeLegacyLongContext(modelName, serviceTier string, tokens ModelTokens, price model.ModelPrice) CostResult {
	inputTokens := maxInt64(tokens.InputTokens, 0)
	outputTokens := maxInt64(tokens.OutputTokens, 0)
	cachedTokens := maxInt64(tokens.CachedTokens, 0)
	cacheReadTokens := maxInt64(tokens.CacheReadTokens, 0)
	cacheCreationTokens := maxInt64(tokens.CacheCreationTokens, 0)

	longInputTokens := clampTokens(tokens.LongInputTokens, inputTokens)
	longOutputTokens := clampTokens(tokens.LongOutputTokens, outputTokens)
	longCachedTokens := clampTokens(tokens.LongCachedTokens, cachedTokens)
	longCacheReadTokens := clampTokens(tokens.LongCacheReadTokens, cacheReadTokens)
	longCacheCreationTokens := clampTokens(tokens.LongCacheCreationTokens, cacheCreationTokens)

	shortTokens := ModelTokens{
		PricingModel:        tokens.PricingModel,
		InputTokens:         inputTokens - longInputTokens,
		OutputTokens:        outputTokens - longOutputTokens,
		CachedTokens:        cachedTokens - longCachedTokens,
		CacheReadTokens:     cacheReadTokens - longCacheReadTokens,
		CacheCreationTokens: cacheCreationTokens - longCacheCreationTokens,
	}
	longTokens := ModelTokens{
		PricingModel:        tokens.PricingModel,
		InputTokens:         longInputTokens,
		OutputTokens:        longOutputTokens,
		CachedTokens:        longCachedTokens,
		CacheReadTokens:     longCacheReadTokens,
		CacheCreationTokens: longCacheCreationTokens,
	}

	tier := strings.ToLower(strings.TrimSpace(serviceTier))
	isPriority := tier == "priority" || tier == "fast"
	isDiscount := tier == "flex" || tier == "batch"
	isStandard := tier == "" || tier == "default" || tier == "standard" || tier == "auto"

	var shortRes CostResult
	var longRes CostResult

	if isPriority {
		if stPrice, ok := model.ModelPriceForServiceTier(price, serviceTier); ok {
			shortRes = computeSegmentCost(modelName, shortTokens, stPrice, 1, 1, true)
		} else {
			mult := ServiceTierMultiplier(modelName, serviceTier)
			shortRes = computeSegmentCost(modelName, shortTokens, price, mult, mult, true)
		}
		longRes = computeSegmentCost(modelName, longTokens, price, 2, 1.5, true)
	} else if isDiscount {
		if stPrice, ok := model.ModelPriceForServiceTier(price, serviceTier); ok {
			shortRes = computeSegmentCost(modelName, shortTokens, stPrice, 1, 1, true)
			longRes = computeSegmentCost(modelName, longTokens, stPrice, 2, 1.5, true)
		} else {
			shortRes = computeSegmentCost(modelName, shortTokens, price, 0.5, 0.5, true)
			longRes = computeSegmentCost(modelName, longTokens, price, 2*0.5, 1.5*0.5, true)
		}
	} else if isStandard {
		shortRes = computeSegmentCost(modelName, shortTokens, price, 1, 1, true)
		longRes = computeSegmentCost(modelName, longTokens, price, 2, 1.5, true)
	} else {
		mult := ServiceTierMultiplier(modelName, serviceTier)
		shortRes = computeSegmentCost(modelName, shortTokens, price, mult, mult, true)
		longRes = computeSegmentCost(modelName, longTokens, price, 2*mult, 1.5*mult, true)
	}

	return CostResult{
		Cost:     shortRes.Cost + longRes.Cost,
		Complete: shortRes.Complete && longRes.Complete,
	}
}

func computeSegmentCost(
	modelName string,
	tokens ModelTokens,
	price model.ModelPrice,
	inputMultiplier float64,
	outputMultiplier float64,
	baseComplete bool,
) CostResult {
	inputTokens := maxInt64(tokens.InputTokens, 0)
	outputTokens := maxInt64(tokens.OutputTokens, 0)
	cachedTokens := maxInt64(tokens.CachedTokens, 0)
	cacheReadTokens := maxInt64(tokens.CacheReadTokens, 0)
	cacheCreationTokens := maxInt64(tokens.CacheCreationTokens, 0)

	// Deduct sub-buckets from totals
	creation1hTokens := clampTokens(tokens.CacheCreation1hTokens, cacheCreationTokens)
	creation5mTokens := cacheCreationTokens - creation1hTokens

	imageCacheReadTokens := clampTokens(tokens.ImageCacheReadTokens, cacheReadTokens)
	textCacheReadTokens := cacheReadTokens - imageCacheReadTokens

	imageOutputTokens := clampTokens(tokens.ImageOutputTokens, outputTokens)
	textOutputTokens := outputTokens - imageOutputTokens

	readTokens := cachedTokens + cacheReadTokens
	promptTokens := maxInt64(inputTokens-readTokens-cacheCreationTokens, 0)
	// ImageInputTokens is total image input (including cached image read).
	// Uncached image prompt tokens = max(ImageInputTokens - ImageCacheReadTokens, 0).
	rawImagePromptTokens := maxInt64(tokens.ImageInputTokens-imageCacheReadTokens, 0)
	imagePromptTokens := clampTokens(rawImagePromptTokens, promptTokens)
	textPromptTokens := promptTokens - imagePromptTokens

	// Resolve rates (respecting explicit zero prices)
	promptPrice := price.Prompt
	imagePromptPrice := promptPrice
	if configuredPriceValue(price.ImagePrompt, price.ImagePromptConfigured) {
		imagePromptPrice = price.ImagePrompt
	}

	completionPrice := price.Completion
	imageCompletionPrice := completionPrice
	if configuredPriceValue(price.ImageCompletion, price.ImageCompletionConfigured) {
		imageCompletionPrice = price.ImageCompletion
	}

	cacheReadPrice := price.CacheRead
	if !configuredPriceValue(cacheReadPrice, price.CacheReadConfigured) {
		cacheReadPrice = fallbackPrice(price.Cache, promptPrice*0.1)
	}
	imageCacheReadPrice := cacheReadPrice
	if configuredPriceValue(price.ImageCacheRead, price.ImageCacheReadConfigured) {
		imageCacheReadPrice = price.ImageCacheRead
	}

	cacheCreationPrice := price.CacheCreation
	if !configuredPriceValue(cacheCreationPrice, price.CacheCreationConfigured) {
		cacheCreationPrice = promptPrice
	}
	cacheCreation1hPrice := cacheCreationPrice
	if configuredPriceValue(price.CacheCreation1h, price.CacheCreation1hConfigured) {
		cacheCreation1hPrice = price.CacheCreation1h
	}

	cost := (float64(textPromptTokens)*promptPrice+
		float64(imagePromptTokens)*imagePromptPrice+
		float64(cachedTokens)*price.Cache+
		float64(textCacheReadTokens)*cacheReadPrice+
		float64(imageCacheReadTokens)*imageCacheReadPrice+
		float64(creation5mTokens)*cacheCreationPrice+
		float64(creation1hTokens)*cacheCreation1hPrice)*inputMultiplier/PerMillion +
		(float64(textOutputTokens)*completionPrice+
			float64(imageOutputTokens)*imageCompletionPrice)*outputMultiplier/PerMillion

	complete := baseComplete && (tokens.ReasoningTokens <= 0 || tokens.TotalTokens > 0)
	hasDistinctImage := (price.ImagePromptConfigured && price.ImagePrompt != promptPrice) ||
		(price.ImageCompletionConfigured && price.ImageCompletion != completionPrice) ||
		(price.ImageCacheReadConfigured && price.ImageCacheRead != cacheReadPrice)
	if hasDistinctImage && !tokens.ImageTokensKnown && (promptTokens > 0 || outputTokens > 0 || cacheReadTokens > 0) {
		complete = false
	}

	hasDistinct1h := price.CacheCreation1hConfigured && price.CacheCreation1h != cacheCreationPrice
	if hasDistinct1h && !tokens.CacheCreationTTLKnown && cacheCreationTokens > 0 {
		complete = false
	}

	return CostResult{
		Cost:     cost,
		Complete: complete,
	}
}

func resolveModelPrice(modelName string, prices map[string]model.ModelPrice) (model.ModelPrice, bool) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return model.ModelPrice{}, false
	}
	if price, ok := prices[modelName]; ok {
		return price, true
	}
	slug := normalizedModelSlug(modelName)
	if slug != "" && slug != modelName {
		if price, ok := prices[slug]; ok {
			return price, true
		}
	}
	for _, price := range prices {
		if price.SourceModelID != "" {
			src := strings.TrimSpace(price.SourceModelID)
			if src == modelName || src == slug {
				return price, true
			}
			srcSlug := normalizedModelSlug(src)
			if srcSlug == modelName || srcSlug == slug {
				return price, true
			}
		}
	}
	if modelName == "gpt-5.6" || slug == "gpt-5.6" {
		if solPrice, ok := prices["gpt-5.6-sol"]; ok {
			return solPrice, true
		}
	}
	return model.ModelPrice{}, false
}

func normalizedModelSlug(modelName string) string {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if index := strings.LastIndex(modelName, "/"); index >= 0 {
		modelName = modelName[index+1:]
	}
	return modelName
}

func clampTokens(value int64, total int64) int64 {
	if value <= 0 || total <= 0 {
		return 0
	}
	if value > total {
		return total
	}
	return value
}

func configuredPriceValue(value float64, configured bool) bool {
	return configured || value > 0
}
