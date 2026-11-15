/**
 * Fly mode, shared by every zen scene that has one (Waves, Galaxy).
 *
 * The visible scene registers itself as the fly target; the zen HUD (Fly
 * button, ⌥F, flight panel) talks to whichever scene is registered and reads
 * `zenFlyState`.
 */
import { reactive } from 'vue'

export interface ZenFlyState {
  /** The visible scene has a fly mode */
  available: boolean
  active: boolean
  speed: number
  throttle: number
  /** Name of something nearby (Galaxy: nearest file star) */
  near: string
  score: number
  best: number
  combo: number
  /** Increments on every pickup (the HUD animates on change) */
  pickups: number
  /** Points from the last pickup (for the "+50" pop) */
  lastPoints: number
}

const BEST_KEY = 'eggify:zen-fly-best'

function readBest(): number {
  try { return Number(localStorage.getItem(BEST_KEY)) || 0 } catch { return 0 }
}

export const zenFlyState = reactive<ZenFlyState>({
  available: false, active: false, speed: 0, throttle: 0, near: '',
  score: 0, best: typeof window === 'undefined' ? 0 : readBest(), combo: 1, pickups: 0, lastPoints: 0,
})

export function saveFlyBest(score: number) {
  if (score <= zenFlyState.best) return
  zenFlyState.best = score
  try { localStorage.setItem(BEST_KEY, String(score)) } catch { /* private mode */ }
}

type FlyTarget = (on: boolean) => void
let target: FlyTarget | null = null
let owner: unknown = null

/** The visible scene offers fly mode (call with null to withdraw). */
export function registerFlyTarget(who: unknown, fn: FlyTarget | null) {
  if (fn) {
    owner = who
    target = fn
    zenFlyState.available = true
  } else if (owner === who) {
    if (zenFlyState.active) target?.(false)
    owner = null
    target = null
    zenFlyState.available = false
    zenFlyState.active = false
  }
}

export function setZenFlying(on: boolean) {
  target?.(on)
}
