/**
 * Beat clock maths for the Zen music: pure functions, no DOM or audio.
 *
 * A track has a constant-tempo grid (generated offline by scripts/zen-beatgrid.mjs):
 * beat k lands at `firstBeat + k * 60 / bpm`. Given the playing element's
 * currentTime (and how long the output takes to reach the speakers) the clock
 * says where in the beat we are *now*.
 */

export interface BeatGrid {
  bpm: number
  /** Seconds from the start of the file to the first beat */
  firstBeat: number
  beatsPerBar: number
  /** Beat index (0..beatsPerBar-1, counted from firstBeat) that starts a bar */
  downbeatOffset: number
  duration?: number
}

export interface BeatState {
  /** Beat number since the grid origin (negative before the first beat) */
  beatIndex: number
  /** 0 on the beat, rising to 1 just before the next one */
  phase: number
  /** Position inside the bar: 0 is the downbeat */
  beatInBar: number
  /** Bar number (floor of the beat index over the bar length, after the downbeat shift) */
  bar: number
  /** Seconds until the next beat / the next downbeat */
  toNextBeat: number
  toNextDownbeat: number
  bpm: number
}

/** What the scene reads: a state plus whether a grid drives it or the live analyser does. */
export interface BeatClock extends BeatState {
  playing: boolean
  /** 'grid' = tempo map of the track; 'live' = analyser onsets; 'none' = silence */
  source: 'grid' | 'live' | 'none'
  track: string
}

export const SILENT_CLOCK: BeatClock = {
  playing: false, source: 'none', track: '',
  beatIndex: 0, phase: 0, beatInBar: 0, bar: 0, toNextBeat: 0, toNextDownbeat: 0, bpm: 0,
}

export function isBeatGrid(v: unknown): v is BeatGrid {
  const g = v as BeatGrid | null
  return !!g && typeof g === 'object'
    && Number.isFinite(g.bpm) && g.bpm >= 40 && g.bpm <= 240
    && Number.isFinite(g.firstBeat) && g.firstBeat >= 0
    && Number.isInteger(g.beatsPerBar) && g.beatsPerBar >= 1 && g.beatsPerBar <= 16
    && Number.isInteger(g.downbeatOffset) && g.downbeatOffset >= 0 && g.downbeatOffset < g.beatsPerBar
}

const mod = (a: number, n: number) => ((a % n) + n) % n

/**
 * Beat state at `time` seconds into the file. `latency` is the output delay in
 * seconds: what you hear now is what the element played `latency` ago.
 * With `loop` the time wraps at the grid's duration.
 */
export function beatStateAt(grid: BeatGrid, time: number, latency = 0, loop = false): BeatState {
  let t = time - Math.max(0, latency)
  if (loop && grid.duration && grid.duration > 0) t = mod(t, grid.duration)
  const period = 60 / grid.bpm
  const beatsF = (t - grid.firstBeat) / period
  const beatIndex = Math.floor(beatsF)
  const phase = beatsF - beatIndex
  const inBar = mod(beatIndex - grid.downbeatOffset, grid.beatsPerBar)
  const toNextBeat = (1 - phase) * period
  return {
    beatIndex,
    phase,
    beatInBar: inBar,
    bar: Math.floor((beatIndex - grid.downbeatOffset) / grid.beatsPerBar),
    toNextBeat,
    toNextDownbeat: toNextBeat + (grid.beatsPerBar - 1 - inBar) * period,
    bpm: grid.bpm,
  }
}

/** Output latency in seconds from an AudioContext-like object (0 if unknown), capped to a sane range. */
export function outputLatencySeconds(ctx: { outputLatency?: number; baseLatency?: number } | null | undefined): number {
  if (!ctx) return 0
  const v = (Number.isFinite(ctx.outputLatency) && ctx.outputLatency! > 0 ? ctx.outputLatency! : 0)
    + (Number.isFinite(ctx.baseLatency) && ctx.baseLatency! > 0 ? ctx.baseLatency! : 0)
  return Math.min(0.25, v)
}

/**
 * Envelopes for the animation, all 0..1, from a beat state: a hard attack on
 * the beat with an eased release (`pulse`), the same only on the downbeat
 * (`accent`), only on every other beat (`half`), and a bump halfway between
 * beats (`off`). `swing` is -1..1, one swing per beat (pendulum).
 */
export function beatEnvelopes(s: Pick<BeatState, 'phase' | 'beatIndex' | 'beatInBar'>) {
  const release = (x: number, k: number) => Math.pow(Math.max(0, 1 - x), k)
  const pulse = release(s.phase, 3)
  const accent = s.beatInBar === 0 ? release(s.phase, 2.2) : 0
  const half = s.beatIndex % 2 === 0 ? release(s.phase, 2) : 0
  const off = Math.max(0, Math.sin(Math.PI * Math.max(0, Math.min(1, s.phase))) ** 2)
  const dir = s.beatIndex % 2 === 0 ? 1 : -1
  const swing = Math.cos(Math.PI * (s.beatIndex + s.phase)) * -1
  // A short crouch just before the beat, so the hop has an anticipation
  const squash = s.phase > 0.86 ? (s.phase - 0.86) / 0.14 : 0
  return { pulse, accent, half, off, dir, swing, squash }
}

/**
 * How much the stage reacts to the music. The characters and scenery breathe
 * on a slow cycle instead of hopping on every beat; amplitudes live in
 * circus.css (--cc-sway, --cc-breath, --cc-nod, --cc-step, --cc-bar) and are
 * about 30-40% of the old per-beat hop. Everything here is a pure function of
 * the beat index, so a given beat always looks the same (no Math.random).
 */
export const BEAT_MOTION = {
  /** Beats per half sway: the egg leans one way, then the other, every `swayBeats` beats. */
  swayBeats: 2,
  /** The soft accent (a small eased lift) lands on the downbeat of every `accentEveryBars`-th bar... */
  accentEveryBars: 2,
  /** ...and is skipped for this share of those bars, picked from the bar number, so it is not mechanical. */
  accentSkip: 0.25,
  /** How long the accent takes to rise and settle, in beats (sine in, sine out: no snap). */
  accentBeats: 2,
  /** The bar-level bump of the bunting and curtains takes this many beats. */
  barBeats: 2,
  /** Walkers step once per this many beats (2 = half tempo), with a smooth bounce. */
  walkerDivisor: 2,
  /** Sway amplitude varies per half-cycle between this and 1, so it is not a metronome. */
  swayMinAmp: 0.65,
  /** Multiplier on the egg's beat motion: idle vs while the agent is working. */
  energyIdle: 1,
  energyWorking: 1.3,
}

/** Deterministic 0..1 value from an integer (Knuth multiplicative hash). */
export function hash01(n: number): number {
  return ((Math.imul(Math.floor(n) | 0, 2654435761) >>> 0) % 1000) / 1000
}

/** True when the bar's downbeat carries the soft accent. */
export function barAccents(bar: number, m: Pick<typeof BEAT_MOTION, 'accentEveryBars' | 'accentSkip'> = BEAT_MOTION): boolean {
  const every = Math.max(1, m.accentEveryBars)
  if (((bar % every) + every) % every !== 0) return false
  return hash01(bar * 7 + 3) >= m.accentSkip
}

const bump = (x: number) => (x > 0 && x < 1 ? Math.sin(Math.PI * x) ** 2 : 0)

/**
 * The relaxed envelopes. `sway` is -1..1 (slow lean, one half-cycle per
 * `swayBeats`), `breath` 0..1 once per bar, `nod` 0..1 a soft accent on the
 * downbeat of some bars, `bar` 0..1 a bump on every downbeat, `step` 0..1 and
 * `stepDir` +-1 for walkers at half tempo.
 */
export function calmEnvelopes(
  s: Pick<BeatState, 'phase' | 'beatIndex' | 'beatInBar' | 'bar'>,
  beatsPerBar = 4,
  m: typeof BEAT_MOTION = BEAT_MOTION,
) {
  const t = s.beatIndex + s.phase
  const x = t / m.swayBeats
  const k = Math.floor(x)
  const sway = Math.sin(Math.PI * (x - k)) * (((k % 2) + 2) % 2 ? -1 : 1) * (m.swayMinAmp + (1 - m.swayMinAmp) * hash01(k))
  const inBar = s.beatInBar + s.phase
  const breath = 0.5 - 0.5 * Math.cos((2 * Math.PI * inBar) / Math.max(1, beatsPerBar))
  const nod = barAccents(s.bar, m) ? bump(inBar / m.accentBeats) : 0
  const bar = bump(inBar / m.barBeats)
  const div = Math.max(1, m.walkerDivisor)
  const stepIdx = Math.floor(s.beatIndex / div)
  const step = bump(((((s.beatIndex % div) + div) % div) + s.phase) / div)
  const stepDir = ((stepIdx % 2) + 2) % 2 ? -1 : 1
  return { sway, breath, nod, bar, step, stepDir }
}
