/**
 * Which Circus backdrop a session gets: an optional manual choice (saved like
 * the other Zen preferences) or, in "auto", a stable per-session assignment
 * that spreads the open sessions over the ten backdrops.
 */
import { computed, ref } from 'vue'
import {
  CIRCUS_BACKDROPS, backdropInfo, createBackdropAssigner, isBackdropId,
  type CircusBackdropId,
} from './circusBackdrops'

export const BACKDROP_PREF_KEY = 'eggify-zen-circus-backdrop'
const ASSIGNED_KEY = 'eggify-zen-circus-backdrops'
const MAX_REMEMBERED = 200

export type BackdropPref = 'auto' | CircusBackdropId

function readPref(): BackdropPref {
  try {
    const v = localStorage.getItem(BACKDROP_PREF_KEY)
    return isBackdropId(v) ? v : 'auto'
  } catch { return 'auto' }
}

function readAssigned(): Record<string, number> {
  try {
    const j = JSON.parse(localStorage.getItem(ASSIGNED_KEY) || '{}')
    return j && typeof j === 'object' ? j : {}
  } catch { return {} }
}

const pref = ref<BackdropPref>(readPref())
const assigner = createBackdropAssigner(readAssigned())

function persistAssigned() {
  try {
    const snap = assigner.snapshot()
    const keys = Object.keys(snap)
    if (keys.length > MAX_REMEMBERED) for (const k of keys.slice(0, keys.length - MAX_REMEMBERED)) assigner.forget(k)
    localStorage.setItem(ASSIGNED_KEY, JSON.stringify(assigner.snapshot()))
  } catch { /* storage unavailable */ }
}

/** The backdrop for `sessionId`, given the ids of the sessions known right now. */
export function resolveCircusBackdrop(sessionId: string, openIds: readonly string[] = []): CircusBackdropId {
  if (pref.value !== 'auto') return pref.value
  if (!sessionId) return CIRCUS_BACKDROPS[0]!.id
  const before = JSON.stringify(assigner.snapshot()[sessionId])
  assigner.assignAll([...openIds, sessionId])
  const idx = assigner.assign(sessionId, [...openIds, sessionId])
  if (before !== JSON.stringify(idx)) persistAssigned()
  return CIRCUS_BACKDROPS[idx]!.id
}

export function useCircusBackdropPref() {
  function setPref(v: BackdropPref) {
    pref.value = v
    try { localStorage.setItem(BACKDROP_PREF_KEY, v) } catch { /* storage unavailable */ }
  }
  /** auto -> each backdrop in turn -> auto */
  function cyclePref() {
    const ids = CIRCUS_BACKDROPS.map(b => b.id)
    const i = pref.value === 'auto' ? -1 : ids.indexOf(pref.value)
    setPref(i + 1 >= ids.length ? 'auto' : ids[i + 1]!)
  }
  return { pref, setPref, cyclePref, label: computed(() => (pref.value === 'auto' ? 'Auto' : backdropInfo(pref.value).name)) }
}
