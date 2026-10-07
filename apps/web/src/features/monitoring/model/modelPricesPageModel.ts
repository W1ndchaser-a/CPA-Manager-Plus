import {
  resolveConfiguredModelPrice,
  type ModelPrice,
  type ModelPriceContextTier,
  type ModelPricePeakWindow,
  type ModelPriceServiceTier,
} from '@/utils/usage';
import type {
  ModelPriceUsageSummaryResponse,
  ModelPriceSyncCandidate,
  ModelPriceSyncCandidateSet,
} from '@/services/api/usageService';

export type ModelPriceFilter = 'all' | 'missing' | 'saved' | 'candidates';

export type PriceRuleDraft = {
  prompt: string;
  completion: string;
  cache: string;
  cacheRead: string;
  cacheCreation: string;
  imagePrompt: string;
  imageCompletion: string;
  imageCacheRead: string;
  cacheCreation1h: string;
};

export type ContextTierDraft = PriceRuleDraft & {
  thresholdTokens: string;
};

export type ServiceTierDraft = PriceRuleDraft & {
  mode: string;
  serviceTier: string;
  contextTiers: ContextTierDraft[];
};
export type PriceDraft = {
  model: string;
  prompt: string;
  completion: string;
  cache: string;
  cacheRead: string;
  cacheCreation: string;
  imagePrompt: string;
  imageCompletion: string;
  imageCacheRead: string;
  cacheCreation1h: string;
  contextTiers: ContextTierDraft[];
  serviceTiers: ServiceTierDraft[];
  /** Not editable in the UI yet; carried through so saving a row keeps its windows. */
  peakWindows?: ModelPricePeakWindow[];
};

export type PriceDraftValidationError =
  | 'context_threshold'
  | 'context_duplicate_threshold'
  | 'context_price'
  | 'service_identity'
  | 'service_duplicate_identity'
  | 'service_price'
  | 'service_context_threshold';

export type ModelPriceRow = {
  model: string;
  calls: number;
  requestedCalls: number;
  resolvedCalls: number;
  hasPrice: boolean;
  price?: ModelPrice;
  candidateCount: number;
};

export type ModelPriceSummary = {
  total: number;
  saved: number;
  missing: number;
  candidates: number;
};

export type ModelPriceCandidateGroup = {
  source: string;
  candidates: ModelPriceSyncCandidate[];
};

export const createEmptyPriceDraft = (): PriceDraft => ({
  model: '',
  prompt: '',
  completion: '',
  cache: '',
  cacheRead: '',
  cacheCreation: '',
  imagePrompt: '',
  imageCompletion: '',
  imageCacheRead: '',
  cacheCreation1h: '',
  contextTiers: [],
  serviceTiers: [],
});

const createConfiguredDraftValue = (value: number | undefined, configured?: boolean): string =>
  configured || Number(value) > 0 ? String(Number(value) || 0) : '';

const createRulePriceDraft = (
  rule: ModelPriceContextTier | ModelPriceServiceTier
): PriceRuleDraft => ({
  prompt: createConfiguredDraftValue(rule.prompt, rule.promptConfigured),
  completion: createConfiguredDraftValue(rule.completion, rule.completionConfigured),
  cache: createConfiguredDraftValue(rule.cache, rule.cacheConfigured),
  cacheRead: createConfiguredDraftValue(rule.cacheRead, rule.cacheReadConfigured),
  cacheCreation: createConfiguredDraftValue(rule.cacheCreation, rule.cacheCreationConfigured),
  imagePrompt: createConfiguredDraftValue(rule.imagePrompt, rule.imagePromptConfigured),
  imageCompletion: createConfiguredDraftValue(rule.imageCompletion, rule.imageCompletionConfigured),
  imageCacheRead: createConfiguredDraftValue(rule.imageCacheRead, rule.imageCacheReadConfigured),
  cacheCreation1h: createConfiguredDraftValue(rule.cacheCreation1h, rule.cacheCreation1hConfigured),
});

export const createEmptyContextTierDraft = (): ContextTierDraft => ({
  thresholdTokens: '',
  prompt: '',
  completion: '',
  cache: '',
  cacheRead: '',
  cacheCreation: '',
  imagePrompt: '',
  imageCompletion: '',
  imageCacheRead: '',
  cacheCreation1h: '',
});

export const createEmptyServiceTierDraft = (): ServiceTierDraft => ({
  mode: '',
  serviceTier: '',
  prompt: '',
  completion: '',
  cache: '',
  cacheRead: '',
  cacheCreation: '',
  imagePrompt: '',
  imageCompletion: '',
  imageCacheRead: '',
  cacheCreation1h: '',
  contextTiers: [],
});

export const createPriceDraft = (model: string, price?: ModelPrice): PriceDraft => ({
  model,
  prompt: price ? createConfiguredDraftValue(price.prompt, price.promptConfigured) : '',
  completion: price ? createConfiguredDraftValue(price.completion, price.completionConfigured) : '',
  cache: price ? String(price.cache) : '',
  cacheRead: price ? createConfiguredDraftValue(price.cacheRead, price.cacheReadConfigured) : '',
  cacheCreation: price
    ? createConfiguredDraftValue(price.cacheCreation, price.cacheCreationConfigured)
    : '',
  imagePrompt: price
    ? createConfiguredDraftValue(price.imagePrompt, price.imagePromptConfigured)
    : '',
  imageCompletion: price
    ? createConfiguredDraftValue(price.imageCompletion, price.imageCompletionConfigured)
    : '',
  imageCacheRead: price
    ? createConfiguredDraftValue(price.imageCacheRead, price.imageCacheReadConfigured)
    : '',
  cacheCreation1h: price
    ? createConfiguredDraftValue(price.cacheCreation1h, price.cacheCreation1hConfigured)
    : '',
  contextTiers: (price?.contextTiers ?? []).map((tier) => ({
    thresholdTokens: String(tier.thresholdTokens),
    ...createRulePriceDraft(tier),
  })),
  serviceTiers: (price?.serviceTiers ?? []).map((tier) => ({
    mode: tier.mode,
    serviceTier: tier.serviceTier,
    ...createRulePriceDraft(tier),
    contextTiers: (tier.contextTiers ?? []).map((ct) => ({
      thresholdTokens: String(ct.thresholdTokens),
      ...createRulePriceDraft(ct),
    })),
  })),
  peakWindows: price?.peakWindows ?? [],
});

export const parsePriceValue = (value?: string) => {
  if (value === undefined || value === null) return 0;
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
};

const PRICE_RULE_FIELDS = [
  'prompt',
  'completion',
  'cache',
  'cacheRead',
  'cacheCreation',
  'imagePrompt',
  'imageCompletion',
  'imageCacheRead',
  'cacheCreation1h',
] as const satisfies readonly (keyof PriceRuleDraft)[];

const hasConfiguredRulePrice = (draft: PriceRuleDraft) =>
  PRICE_RULE_FIELDS.some((field) => String(draft[field] ?? '').trim() !== '');

const hasInvalidRulePrice = (draft: PriceRuleDraft) =>
  PRICE_RULE_FIELDS.some((field) => {
    const value = String(draft[field] ?? '').trim();
    if (!value) return false;
    const parsed = Number(value);
    return !Number.isFinite(parsed) || parsed < 0;
  });

export const validatePriceDraft = (draft: PriceDraft): PriceDraftValidationError | null => {
  const contextThresholds = new Set<number>();
  for (const tier of draft.contextTiers) {
    const threshold = Number(tier.thresholdTokens);
    if (!Number.isSafeInteger(threshold) || threshold <= 0) return 'context_threshold';
    if (contextThresholds.has(threshold)) return 'context_duplicate_threshold';
    contextThresholds.add(threshold);
    if (!hasConfiguredRulePrice(tier) || hasInvalidRulePrice(tier)) return 'context_price';
  }

  const serviceIdentifiers = new Set<string>();
  for (const tier of draft.serviceTiers) {
    const mode = tier.mode.trim().toLowerCase();
    const serviceTier = tier.serviceTier.trim().toLowerCase();
    if (!mode || !serviceTier) return 'service_identity';
    if (serviceIdentifiers.has(mode) || serviceIdentifiers.has(serviceTier)) {
      return 'service_duplicate_identity';
    }
    serviceIdentifiers.add(mode);
    serviceIdentifiers.add(serviceTier);
    if (!hasConfiguredRulePrice(tier) && (!tier.contextTiers || tier.contextTiers.length === 0)) {
      return 'service_price';
    }
    if (hasInvalidRulePrice(tier)) return 'service_price';
    for (const sct of tier.contextTiers ?? []) {
      const threshold = Number(sct.thresholdTokens);
      if (!Number.isSafeInteger(threshold) || threshold <= 0 || !contextThresholds.has(threshold)) {
        return 'service_context_threshold';
      }
      if (!hasConfiguredRulePrice(sct) || hasInvalidRulePrice(sct)) return 'service_price';
    }
  }
  return null;
};

const buildRulePrice = (draft: PriceRuleDraft) => ({
  prompt: parsePriceValue(draft.prompt),
  completion: parsePriceValue(draft.completion),
  cache: parsePriceValue(draft.cache),
  cacheRead: parsePriceValue(draft.cacheRead),
  cacheCreation: parsePriceValue(draft.cacheCreation),
  imagePrompt: parsePriceValue(draft.imagePrompt),
  imageCompletion: parsePriceValue(draft.imageCompletion),
  imageCacheRead: parsePriceValue(draft.imageCacheRead),
  cacheCreation1h: parsePriceValue(draft.cacheCreation1h),
  promptConfigured: String(draft.prompt ?? '').trim() !== '',
  completionConfigured: String(draft.completion ?? '').trim() !== '',
  cacheConfigured: String(draft.cache ?? '').trim() !== '',
  cacheReadConfigured: String(draft.cacheRead ?? '').trim() !== '',
  cacheCreationConfigured: String(draft.cacheCreation ?? '').trim() !== '',
  imagePromptConfigured: String(draft.imagePrompt ?? '').trim() !== '',
  imageCompletionConfigured: String(draft.imageCompletion ?? '').trim() !== '',
  imageCacheReadConfigured: String(draft.imageCacheRead ?? '').trim() !== '',
  cacheCreation1hConfigured: String(draft.cacheCreation1h ?? '').trim() !== '',
});

export const buildPriceFromDraft = (draft: PriceDraft): ModelPrice | null => {
  const model = draft.model.trim();
  if (!model || validatePriceDraft(draft)) return null;
  const prompt = parsePriceValue(draft.prompt);
  const completion = parsePriceValue(draft.completion);
  const cache = String(draft.cache ?? '').trim() === '' ? prompt : parsePriceValue(draft.cache);
  return {
    prompt,
    completion,
    cache,
    cacheRead: parsePriceValue(draft.cacheRead),
    cacheCreation: parsePriceValue(draft.cacheCreation),
    imagePrompt: parsePriceValue(draft.imagePrompt),
    imageCompletion: parsePriceValue(draft.imageCompletion),
    imageCacheRead: parsePriceValue(draft.imageCacheRead),
    cacheCreation1h: parsePriceValue(draft.cacheCreation1h),
    promptConfigured: String(draft.prompt ?? '').trim() !== '',
    completionConfigured: String(draft.completion ?? '').trim() !== '',
    cacheReadConfigured: String(draft.cacheRead ?? '').trim() !== '',
    cacheCreationConfigured: String(draft.cacheCreation ?? '').trim() !== '',
    imagePromptConfigured: String(draft.imagePrompt ?? '').trim() !== '',
    imageCompletionConfigured: String(draft.imageCompletion ?? '').trim() !== '',
    imageCacheReadConfigured: String(draft.imageCacheRead ?? '').trim() !== '',
    cacheCreation1hConfigured: String(draft.cacheCreation1h ?? '').trim() !== '',
    source: 'manual',
    contextTiers: draft.contextTiers
      .map((tier) => ({
        thresholdTokens: Number(tier.thresholdTokens),
        ...buildRulePrice(tier),
      }))
      .sort((left, right) => left.thresholdTokens - right.thresholdTokens),
    serviceTiers: draft.serviceTiers
      .map((tier) => ({
        mode: tier.mode.trim().toLowerCase(),
        serviceTier: tier.serviceTier.trim().toLowerCase(),
        ...buildRulePrice(tier),
        contextTiers: tier.contextTiers?.length
          ? tier.contextTiers
              .map((ct) => ({
                thresholdTokens: Number(ct.thresholdTokens),
                ...buildRulePrice(ct),
              }))
              .sort((a, b) => a.thresholdTokens - b.thresholdTokens)
          : undefined,
      }))
      .sort(
        (left, right) =>
          left.mode.localeCompare(right.mode) || left.serviceTier.localeCompare(right.serviceTier)
      ),
    peakWindows: draft.peakWindows?.length ? draft.peakWindows : undefined,
  };
};

export const applyCandidatePrice = (
  prices: Record<string, ModelPrice>,
  model: string,
  candidate: ModelPriceSyncCandidate
): Record<string, ModelPrice> => ({
  ...prices,
  [model]: {
    ...candidate.price,
    source: candidate.price.source || 'sync',
    sourceModelId: candidate.sourceModelId,
  },
});

export const getModelPriceCandidateSource = (candidate: ModelPriceSyncCandidate) =>
  candidate.price.source?.trim() || 'sync';

export const getModelPriceCandidateIdentity = (candidate: ModelPriceSyncCandidate) =>
  JSON.stringify([getModelPriceCandidateSource(candidate), candidate.sourceModelId]);

export const groupModelPriceCandidatesBySource = (
  candidates: ModelPriceSyncCandidate[]
): ModelPriceCandidateGroup[] => {
  const groups = new Map<string, ModelPriceSyncCandidate[]>();
  candidates.forEach((candidate) => {
    const source = getModelPriceCandidateSource(candidate);
    const group = groups.get(source);
    if (group) {
      group.push(candidate);
      return;
    }
    groups.set(source, [candidate]);
  });
  return Array.from(groups, ([source, sourceCandidates]) => ({
    source,
    candidates: sourceCandidates,
  }));
};

export const buildSyncPriceModelsFromSummary = (
  summary: ModelPriceUsageSummaryResponse | null,
  prices: Record<string, ModelPrice>
) => {
  const models = new Set<string>(Object.keys(prices));
  summary?.models?.forEach((item) => {
    if (item.model) models.add(item.model);
  });
  return Array.from(models)
    .filter(Boolean)
    .sort((left, right) => left.localeCompare(right));
};

export const buildCandidateMap = (candidateSets: ModelPriceSyncCandidateSet[] = []) => {
  const map = new Map<string, ModelPriceSyncCandidate[]>();
  candidateSets.forEach((set) => {
    if (!set.model || !Array.isArray(set.candidates) || set.candidates.length === 0) return;
    map.set(set.model, set.candidates);
  });
  return map;
};

export const buildModelPriceRows = (
  summary: ModelPriceUsageSummaryResponse | null,
  prices: Record<string, ModelPrice>,
  candidateSets: ModelPriceSyncCandidateSet[] = [],
  runtimeModels: string[] = []
): ModelPriceRow[] => {
  const rowMap = new Map<string, ModelPriceRow>();
  const candidateMap = buildCandidateMap(candidateSets);

  const ensureRow = (model: string): ModelPriceRow => {
    const existing = rowMap.get(model);
    if (existing) return existing;
    const price = resolveConfiguredModelPrice(model, prices);
    const row: ModelPriceRow = {
      model,
      calls: 0,
      requestedCalls: 0,
      resolvedCalls: 0,
      hasPrice: Boolean(price),
      price,
      candidateCount: candidateMap.get(model)?.length ?? 0,
    };
    rowMap.set(model, row);
    return row;
  };

  Object.keys(prices).forEach(ensureRow);
  candidateMap.forEach((_candidates, model) => ensureRow(model));
  runtimeModels.forEach((model) => {
    const trimmed = typeof model === 'string' ? model.trim() : '';
    if (trimmed) ensureRow(trimmed);
  });

  summary?.models?.forEach((item) => {
    if (!item.model) return;
    const row = ensureRow(item.model);
    row.calls += Number(item.calls) || 0;
    row.requestedCalls += Number(item.requested_calls) || 0;
    row.resolvedCalls += Number(item.resolved_calls) || 0;
  });

  return Array.from(rowMap.values()).sort(
    (left, right) =>
      Number(left.hasPrice) - Number(right.hasPrice) ||
      right.candidateCount - left.candidateCount ||
      right.calls - left.calls ||
      left.model.localeCompare(right.model)
  );
};

export const buildModelPriceSummary = (rows: ModelPriceRow[]): ModelPriceSummary => {
  const saved = rows.filter((row) => row.hasPrice).length;
  const candidates = rows.filter((row) => !row.hasPrice && row.candidateCount > 0).length;
  return {
    total: rows.length,
    saved,
    missing: rows.length - saved,
    candidates,
  };
};

export const filterModelPriceRows = (
  rows: ModelPriceRow[],
  filter: ModelPriceFilter,
  search: string
) => {
  const query = search.trim().toLowerCase();
  return rows.filter((row) => {
    if (filter === 'missing' && row.hasPrice) return false;
    if (filter === 'saved' && !row.hasPrice) return false;
    if (filter === 'candidates' && (row.hasPrice || row.candidateCount === 0)) return false;
    if (!query) return true;
    return (
      row.model.toLowerCase().includes(query) ||
      row.price?.sourceModelId?.toLowerCase().includes(query) ||
      row.price?.source?.toLowerCase().includes(query)
    );
  });
};

export const formatPriceUnit = (value: number | undefined) => {
  const num = Number(value);
  return Number.isFinite(num) ? `$${num.toFixed(4)}/1M` : '--';
};

export const resolveContextTierDisplayPrice = (
  price: ModelPrice | undefined,
  tier: ModelPriceContextTier
) => ({
  prompt: tier.promptConfigured ? tier.prompt : price?.prompt,
  completion: tier.completionConfigured ? tier.completion : price?.completion,
});

export const resolveServiceTierDisplayPrice = (
  price: ModelPrice | undefined,
  tier: ModelPriceServiceTier
) => ({
  prompt: tier.promptConfigured ? tier.prompt : price?.prompt,
  completion: tier.completionConfigured ? tier.completion : price?.completion,
});

const WEEKDAY_LABELS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

export const formatPeakWindow = (window: ModelPricePeakWindow) => {
  const hours = `${String(window.startHour).padStart(2, '0')}-${String(window.endHour).padStart(2, '0')}`;
  const days = window.days.join(',');
  const label =
    days === '1,2,3,4,5'
      ? 'Mon-Fri'
      : days === '0,1,2,3,4,5,6'
        ? 'Daily'
        : window.days.map((day) => WEEKDAY_LABELS[day]).join(',');
  return `${label} ${hours} UTC`;
};

export const formatServiceTierRule = (tier: ModelPriceServiceTier) => {
  const mode = tier.mode.trim();
  const serviceTier = tier.serviceTier.trim();
  if (!mode) return serviceTier || '--';
  if (!serviceTier || mode.toLowerCase() === serviceTier.toLowerCase()) return mode;
  return `${mode}/${serviceTier}`;
};

export const formatContextThreshold = (value: number) => {
  const tokens = Number(value);
  if (!Number.isFinite(tokens) || tokens <= 0) return '--';
  if (tokens >= 1_000_000 && tokens % 1_000_000 === 0) return `${tokens / 1_000_000}M`;
  if (tokens >= 1_000 && tokens % 1_000 === 0) return `${tokens / 1_000}K`;
  return tokens.toLocaleString('en-US', { maximumFractionDigits: 0 });
};
