/** The Subagents Zen stage: the Flying Circus collage by default, or the neon synthwave city. */
export type ZenStageName = 'circus' | 'synthwave'

export const ZEN_STAGE_KEY = 'eggify-zen-stage'
export const DEFAULT_ZEN_STAGE: ZenStageName = 'circus'

export function loadZenStage(): ZenStageName {
  try {
    const stored = localStorage.getItem(ZEN_STAGE_KEY)
    if (stored === 'circus' || stored === 'synthwave') return stored
  } catch {}
  return DEFAULT_ZEN_STAGE
}

export function saveZenStage(stage: ZenStageName) {
  try { localStorage.setItem(ZEN_STAGE_KEY, stage) } catch {}
}
