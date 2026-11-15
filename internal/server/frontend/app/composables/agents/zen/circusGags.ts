/**
 * Pure logic for the Circus zen scene: which collage gag a session event
 * plays, and how many cut-outs may be on stage at once. Kept free of DOM so it
 * can be unit tested.
 */
import type { ToolCategory, ZenToolEvent } from './types'

export type CircusGag =
  | 'book'      // Read: a book flutters across the sky
  | 'quill'     // Edit/Write: a quill scribbles, paper scraps fly
  | 'steam'     // Bash: the steam machine rises and puffs
  | 'magnifier' // Grep/Glob: a magnifying glass sweeps the hills
  | 'cherub'    // Web: a cherub with a trumpet flies across
  | 'fanfare'   // Prompt sent: the cherub swoops in and toots
  | 'letters'   // Assistant text: cut-out letters float up from the egg
  | 'stomp'     // Error/interrupt: the giant foot comes down
  | 'pop'       // Anything else: a little paper burst
  | 'banner'    // New turn: a theatre banner slides in on a downbeat
  | 'tea'       // Idle: a parade of teacups and a teapot crosses the stage
  | 'stalk'     // A bureaucrat's head on a long stalk pops out of the hills
  | 'coconut'   // Coconut-clop percussion on the beat
  | 'crossing'  // A prop (balloon, bicycle, train, fish...) crosses the stage
  | null        // No gag (the subagent walkers cover Task/Agent)

const BY_CATEGORY: Record<ToolCategory, CircusGag> = {
  read: 'book',
  edit: 'quill',
  bash: 'steam',
  search: 'magnifier',
  web: 'cherub',
  agent: null,
  message: 'letters',
  error: 'stomp',
  other: 'pop',
}

export function gagForEvent(e: Pick<ZenToolEvent, 'category' | 'isError'>): CircusGag {
  if (e.isError) return 'stomp'
  return e.category in BY_CATEGORY ? BY_CATEGORY[e.category] : 'pop'
}

/** Most gags (excluding the egg and walkers) on stage at once. */
export const MAX_GAGS = 6
/** Most subagent walkers drawn at once; the rest are counted, not drawn. */
export const MAX_WALKERS = 6
/** The foot may not stomp more often than this (ms). */
export const STOMP_COOLDOWN_MS = 2500
/** Stop-motion frame rate: cut-out animation runs "on twos" (12 fps). */
export const CIRCUS_FPS = 12
/** How often the beat loop writes its CSS variables (the rAF itself may run at 120 Hz). */
export const BEAT_WRITE_FPS = 30

/**
 * Given the ids of gags currently on stage (oldest first), return the ids to
 * remove so that one more gag fits under `max`.
 */
export function gagsToEvict(onStage: readonly number[], max = MAX_GAGS): number[] {
  const over = onStage.length + 1 - max
  return over > 0 ? onStage.slice(0, over) : []
}

/** First free walker slot (0..max-1), or -1 when the stage is full. */
export function freeWalkerSlot(taken: ReadonlySet<number>, max = MAX_WALKERS): number {
  for (let i = 0; i < max; i++) if (!taken.has(i)) return i
  return -1
}

/** Letters for the cut-out "talking" gag: up to `n` visible characters of the label. */
export function lettersFrom(label: string, n = 5): string[] {
  const chars = [...(label || '').replace(/[^\p{L}\p{N}?!]/gu, '')]
  const picked = chars.slice(0, n).map(c => c.toUpperCase())
  return picked.length ? picked : ['B', 'L', 'A', 'H']
}

// ── Ambient gags: rare, on a cooldown, landing on the beat ──────────────────

export type CrossingProp = 'balloon' | 'bicycle' | 'train' | 'fish' | 'cat' | 'shootingstar' | 'gramophone' | 'rabbit' | 'bird'
export type AmbientKind = 'banner' | 'tea' | 'stalk' | 'coconut' | 'crossing'

export interface AmbientPick { kind: AmbientKind; prop?: CrossingProp; onDownbeat: boolean }

interface AmbientRule {
  kind: AmbientKind
  prop?: CrossingProp
  /** Minimum time between two plays of this one (ms) */
  cooldownMs: number
  weight: number
  needsMusic?: boolean
  /** Only on these backdrops (all when omitted) */
  backdrops?: readonly string[]
  downbeat?: boolean
}

const DARK_SKIES = ['dusk', 'storm', 'gothic', 'space'] as const

export const AMBIENT_RULES: readonly AmbientRule[] = [
  { kind: 'tea', cooldownMs: 240_000, weight: 3, downbeat: true },
  { kind: 'stalk', cooldownMs: 150_000, weight: 3 },
  { kind: 'coconut', cooldownMs: 200_000, weight: 3, needsMusic: true, downbeat: true },
  { kind: 'crossing', prop: 'balloon', cooldownMs: 180_000, weight: 2, backdrops: ['meadow', 'snow', 'seaside', 'desert'] },
  { kind: 'crossing', prop: 'bicycle', cooldownMs: 180_000, weight: 2 },
  { kind: 'crossing', prop: 'train', cooldownMs: 240_000, weight: 2, backdrops: ['meadow', 'snow', 'desert', 'dusk', 'storm'] },
  { kind: 'crossing', prop: 'fish', cooldownMs: 200_000, weight: 3, backdrops: ['seaside', 'jungle', 'dusk'] },
  { kind: 'crossing', prop: 'cat', cooldownMs: 300_000, weight: 2 },
  { kind: 'crossing', prop: 'shootingstar', cooldownMs: 120_000, weight: 3, backdrops: DARK_SKIES },
  { kind: 'crossing', prop: 'gramophone', cooldownMs: 240_000, weight: 2, needsMusic: true, backdrops: ['tearoom', 'meadow', 'snow'] },
  { kind: 'crossing', prop: 'rabbit', cooldownMs: 220_000, weight: 2 },
  { kind: 'crossing', prop: 'bird', cooldownMs: 160_000, weight: 2 },
]

/** The quietest the stage may be between two ambient gags, and how full it may be to add one. */
export const AMBIENT_MIN_GAP_MS = 25_000
export const AMBIENT_MAX_ON_STAGE = 3
/** How long the stage must have been quiet (no ambient gag) before an idle one is due */
export const AMBIENT_IDLE_AFTER_MS = 45_000

const ruleKey = (r: Pick<AmbientRule, 'kind' | 'prop'>) => (r.prop ? `${r.kind}:${r.prop}` : r.kind)

/**
 * Choose an ambient gag, or null when it is too soon / the stage is full.
 * `last` maps rule keys (`stalk`, `crossing:cat`) to the time they last played.
 */
export function pickAmbient(o: {
  now: number
  last: Readonly<Record<string, number>>
  lastAny: number
  onStage: number
  playing: boolean
  backdrop: string
  rand: () => number
}): AmbientPick | null {
  if (o.onStage >= AMBIENT_MAX_ON_STAGE) return null
  if (o.now - o.lastAny < AMBIENT_MIN_GAP_MS) return null
  const ok = AMBIENT_RULES.filter(r =>
    (!r.needsMusic || o.playing)
    && (!r.backdrops || r.backdrops.includes(o.backdrop))
    && o.now - (o.last[ruleKey(r)] ?? -Infinity) >= r.cooldownMs)
  const total = ok.reduce((n, r) => n + r.weight, 0)
  if (!total) return null
  let x = o.rand() * total
  for (const r of ok) {
    x -= r.weight
    if (x <= 0) return { kind: r.kind, prop: r.prop, onDownbeat: !!r.downbeat }
  }
  const r = ok[ok.length - 1]!
  return { kind: r.kind, prop: r.prop, onDownbeat: !!r.downbeat }
}
export const ambientKey = (p: Pick<AmbientPick, 'kind' | 'prop'>) => ruleKey(p)

/** A banner line for a new turn (original wording, in the spirit of an announcer card). */
export const BANNER_LINES = [
  'And now, something rather different',
  'A brief pause for tea',
  'Please remain calmly seated',
  'The egg will now think',
  'Meanwhile, in a different hat',
  'Entirely unrelated, but important',
  'Stand by for paperwork',
  'Nobody panic, it is only code',
] as const

/** Hats for the egg gentleman; the one he wears depends on the session and on the turn. */
export const HATS = ['tophat', 'bowler', 'crown', 'teacosy', 'mitre', 'chef', 'general', 'bandsman'] as const
export type Hat = typeof HATS[number]

export function hatFor(sessionSeed: number, turn: number): Hat {
  // Same session, same hat for a run of turns; changes every few turns
  return HATS[Math.floor((sessionSeed * HATS.length + Math.floor(turn / 3)) % HATS.length)]!
}
