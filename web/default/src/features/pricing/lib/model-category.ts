import type { PricingModel } from '../types'

// ----------------------------------------------------------------------------
// Model Categories
// ----------------------------------------------------------------------------
// The model square filters by five broad categories (text / image / video /
// audio / music) instead of by individual capability tags — tags are tab-level
// (text-to-image, digital human, ...), numerous, and only meaningful inside the
// playground, which makes them far too granular to filter on.
//
// A model's category is derived from the same four playground model configs the
// operator maintains in the admin console: whichever config a model is listed
// in decides both which playground page shows it and which category it belongs
// to. No second source of truth is introduced.
//
// Keys mirror `PLAYGROUND_CATEGORIES` in the classic frontend
// (`web/classic/src/constants/playgroundAdmin.constants.js`) — keep both in sync.

export const MODEL_CATEGORIES = [
  { key: 'playground', labelKey: 'Text Models', configKey: null },
  { key: 'image', labelKey: 'Image Models', configKey: 'ImageModelSizeConfig' },
  { key: 'video', labelKey: 'Video Models', configKey: 'VideoModelConfig' },
  { key: 'audio', labelKey: 'Audio Models', configKey: 'AudioModelConfig' },
  { key: 'music', labelKey: 'Music Models', configKey: 'MusicModelConfig' },
] as const

export type ModelCategoryKey = (typeof MODEL_CATEGORIES)[number]['key']

/** Text models have no model config (they are what is left over) — the fallback. */
export const MODEL_CATEGORY_TEXT: ModelCategoryKey = 'playground'

/**
 * Models that are listed but never wired into the playground have no config to
 * look up, so fall back to their endpoint types. `audio-speech` must be checked
 * before `openai-video`: TTS models declare both.
 */
const ENDPOINT_CATEGORY_FALLBACK: [string, ModelCategoryKey][] = [
  ['image-generation', 'image'],
  ['audio-speech', 'audio'],
  ['openai-video', 'video'],
]

type ModelConfig = { models?: Record<string, unknown> }

function parseModelConfig(raw: unknown): ModelConfig | null {
  if (!raw) return null
  if (typeof raw === 'object') return raw as ModelConfig
  if (typeof raw !== 'string') return null
  try {
    return JSON.parse(raw) as ModelConfig
  } catch {
    return null
  }
}

/** Build a `model name -> category key` index from the `/api/status` payload. */
export function buildModelCategoryIndex(
  status: Record<string, unknown> | undefined
): Map<string, ModelCategoryKey> {
  const index = new Map<string, ModelCategoryKey>()
  for (const category of MODEL_CATEGORIES) {
    if (!category.configKey) continue
    const parsed = parseModelConfig(status?.[category.configKey])
    for (const name of Object.keys(parsed?.models ?? {})) {
      if (!index.has(name)) index.set(name, category.key)
    }
  }
  return index
}

export function resolveModelCategory(
  model: PricingModel,
  index: Map<string, ModelCategoryKey>
): ModelCategoryKey {
  const hit = index.get(model.model_name)
  if (hit) return hit

  const types = model.supported_endpoint_types ?? []
  const fallback = ENDPOINT_CATEGORY_FALLBACK.find(([endpoint]) =>
    types.includes(endpoint)
  )
  return fallback ? fallback[1] : MODEL_CATEGORY_TEXT
}
