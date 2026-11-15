/**
 * Circus — "And now for something completely different".
 *
 * A cut-out paper collage in the spirit of 1970s British surreal animation,
 * drawn with plain DOM + CSS (no WebGL/WebGPU), so it runs everywhere and
 * costs next to nothing. The egg gentleman is the session's agent; tools play
 * little collage gags, subagents walk on stage and bow out, and errors bring
 * the giant foot down from the clouds.
 *
 * Everything moves "on twos": one 12 fps ticker steps every cut-out, and
 * each cut-out jitters a pixel every few frames, like paper under a rostrum
 * camera. The ticker stops in hidden tabs and when the scene is unmounted;
 * with prefers-reduced-motion the collage is static and gags are skipped.
 */
import './circus.css'
import { getZenMusic } from '../../useZenMusic'
import { hash01, type ZenToolEvent } from './types'
import {
  gagForEvent, gagsToEvict, freeWalkerSlot, lettersFrom,
  MAX_GAGS, MAX_WALKERS, STOMP_COOLDOWN_MS, CIRCUS_FPS, BEAT_WRITE_FPS, type CircusGag,
  pickAmbient, ambientKey, hatFor, BANNER_LINES, AMBIENT_IDLE_AFTER_MS,
  type AmbientPick, type CrossingProp,
} from './circusGags'
import { pickLetterMessage, newActivity, noteActivity, wrapMessage, type LetterMessage } from './circusMessages'
import { backdropSvg, backdropInfo, type CircusBackdropId } from './circusBackdrops'
import { beatEnvelopes, calmEnvelopes, BEAT_MOTION, SILENT_CLOCK, type BeatClock } from '../../../utils/beatClock'
import { HUD_SELECTORS, hudBoxes, pickLane, pickSpot, shuffled, type Box } from './circusLanes'
import { circusCharacterFor, circusEggArt, EGG_ASPECT, type CircusKind } from '../../../utils/agents/circusCharacters'
import { HAT_ART, CROSSING_ART, TEAPOT_ART, TEACUP_ART, STALK_HEAD_ART, COCONUT_ART } from './circusProps'

const ART = '/zen/circus/'
const INK = '#2b1d10'
// Hand-tinted, muted like the engraved art: dusty rose, ochre, verdigris, cream, sage
const PALETTE = ['#b8606e', '#c79a3e', '#5f8f86', '#f1e6cb', '#a9c2b6']

/** Ground line, as a fraction of the stage height from the top. */
const GROUND = 0.84
/** Where the announcer banner crosses the stage, as a fraction of the stage height */
const BANNER_Y = 0.2

interface Gag {
  id: number
  kind: CircusGag
  el: HTMLElement
  start: number
  /** Duration in frames */
  dur: number
  step(f: number, p: number, t: number): void
  /** Optional: called when removed early or at the end */
  done?(): void
}

type WalkerState = 'in' | 'idle' | 'bow' | 'out'
interface Walker {
  id: string
  slot: number
  el: HTMLElement
  state: WalkerState
  since: number
  x: number
  fromX: number
  hue: number
}

export interface CircusScene {
  mount(el: HTMLElement): void
  /** Take the scene down; with `el`, only if it is mounted in that element */
  unmount(el?: HTMLElement | null): void
  readonly mounted: boolean
  reset(): void
  setStatus(status: string): void
  setProjectColor(hex: string): void
  toolEvent(e: ZenToolEvent): void
  sendPulse(): void
  agentSpawn(id: string): void
  agentComplete(id: string): void
  /** Which backdrop is painted (can change while mounted) */
  setBackdrop(id: CircusBackdropId): void
  /** Let an element outside the stage follow the downbeat (--cc-accent); returns the undo. */
  followDownbeat(el: HTMLElement): () => void
  /** The session on stage: seeds the egg's hat and the cast, so a session keeps its look */
  setSession(id: string): void
  /** Make the walkers match these running subagents: missing ones are standing there already
   *  (no march-on), others leave quietly (ones already bowing out finish that). Safe to call again and again. */
  agentSync(runningIds: string[]): void
}

/** Cheap deterministic noise in [-1, 1] for stop-motion jitter. */
function jitter(seed: number, frame: number, every = 3): number {
  const q = Math.floor(frame / every)
  const x = Math.sin(seed * 12.9898 + q * 78.233) * 43758.5453
  return (x - Math.floor(x)) * 2 - 1
}

function div(cls: string, html = ''): HTMLDivElement {
  const d = document.createElement('div')
  d.className = cls
  if (html) d.innerHTML = html
  return d
}

function img(src: string, cls: string): HTMLImageElement {
  const i = document.createElement('img')
  i.src = ART + src
  i.alt = ''
  i.draggable = false
  i.className = cls
  i.decoding = 'async'
  return i
}

// ── Static collage (built once per mount) ───────────────────────────────────

/** An engraved cloud cut-out (shadow baked in); circus.css tints it per backdrop. */
const CLOUD_HTML = '<img class="cc-cloud-img" src="/zen/circus/backdrops/cloud.webp" alt="" draggable="false" decoding="async">'

const BUNTING_SVG = (accent: string) => {
  const flags: string[] = []
  const n = 22
  for (let i = 0; i < n; i++) {
    const x = 30 + i * 70
    const y = 18 + Math.sin((i / (n - 1)) * Math.PI) * 26
    const c = i % 4 === 3 ? accent : PALETTE[i % 3]
    flags.push(`<path class="cc-flag" style="--i:${i}" d="M${x} ${y} L${x + 52} ${y + 2} L${x + 26} ${y + 44} Z" fill="${c}" stroke="${INK}" stroke-width="2" stroke-linejoin="round"/>`)
  }
  return `<svg class="cc-bunting" viewBox="0 0 1600 80" preserveAspectRatio="none" aria-hidden="true">
    <path d="M0 14 Q800 70 1600 14" fill="none" stroke="${INK}" stroke-width="2"/>
    ${flags.join('')}
  </svg>`
}

const CURTAIN_SVG = (side: 'l' | 'r') => `
<svg class="cc-curtain cc-curtain-${side}" viewBox="0 0 120 900" preserveAspectRatio="none" aria-hidden="true">
  <path d="${side === 'l'
    ? 'M0 0 L120 0 C96 220 104 520 70 760 C60 820 30 860 0 900 Z'
    : 'M120 0 L0 0 C24 220 16 520 50 760 C60 820 90 860 120 900 Z'}"
        fill="#8c3a4a" stroke="${INK}" stroke-width="3"/>
  ${[22, 48, 76].map(x => `<path d="M${side === 'l' ? x : 120 - x} 0 C${side === 'l' ? x - 6 : 126 - x} 300 ${side === 'l' ? x + 4 : 116 - x} 600 ${side === 'l' ? x - 14 : 134 - x} 860" fill="none" stroke="${INK}" stroke-width="1.6" opacity="0.5"/>`).join('')}
</svg>`

// ── Gag artwork (inline SVG cut-outs) ───────────────────────────────────────

const BOOK_SVG = `
<svg viewBox="0 0 120 70" aria-hidden="true">
  <path d="M60 18 Q34 2 6 10 L10 62 Q36 54 60 66 Z" fill="#fbf5e6" stroke="${INK}" stroke-width="2.5" stroke-linejoin="round"/>
  <path d="M60 18 Q86 2 114 10 L110 62 Q84 54 60 66 Z" fill="#f6eedb" stroke="${INK}" stroke-width="2.5" stroke-linejoin="round"/>
  <path d="M18 22 q18 -4 34 2 M18 32 q18 -4 34 2 M18 42 q18 -4 34 2 M68 24 q18 -6 34 -2 M68 34 q18 -6 34 -2 M68 44 q18 -6 34 -2"
        fill="none" stroke="${INK}" stroke-width="1.4" opacity="0.55"/>
  <path d="M60 18 L60 66" stroke="#b8606e" stroke-width="3"/>
</svg>`

const QUILL_SVG = `
<svg viewBox="0 0 60 120" aria-hidden="true">
  <path d="M8 116 L22 70 Q14 34 44 4 Q58 34 34 74 Z" fill="#fbf5e6" stroke="${INK}" stroke-width="2.5" stroke-linejoin="round"/>
  <path d="M10 112 L40 14" stroke="${INK}" stroke-width="1.6"/>
  <path d="M24 66 l-10 -6 M28 52 l-10 -8 M33 38 l-9 -9 M36 30 l10 -4 M32 46 l12 -2 M28 60 l12 0"
        stroke="${INK}" stroke-width="1.2" opacity="0.6"/>
  <path d="M8 116 L12 102" stroke="#b8606e" stroke-width="4" stroke-linecap="round"/>
</svg>`

const MAGNIFIER_SVG = `
<svg viewBox="0 0 110 110" aria-hidden="true">
  <path d="M64 64 L102 102" stroke="${INK}" stroke-width="12" stroke-linecap="round"/>
  <path d="M64 64 L100 100" stroke="#b5541c" stroke-width="7" stroke-linecap="round"/>
  <circle cx="40" cy="40" r="32" fill="#e7f4ef" fill-opacity="0.65" stroke="#c79a3e" stroke-width="7"/>
  <circle cx="40" cy="40" r="32" fill="none" stroke="${INK}" stroke-width="2.5"/>
  <path d="M22 30 q8 -12 22 -12" fill="none" stroke="#fff" stroke-width="4" stroke-linecap="round"/>
</svg>`

const PUFF_SVG = `
<svg viewBox="0 0 80 60" aria-hidden="true">
  <path d="M14 50 Q0 48 6 36 Q2 22 18 22 Q20 6 38 10 Q50 0 60 14 Q78 14 74 32 Q82 44 68 50 Z"
        fill="#fbf5e6" stroke="${INK}" stroke-width="2.5" stroke-linejoin="round"/>
</svg>`

const BURST_SVG = (c: string) => `
<svg viewBox="0 0 100 100" aria-hidden="true">
  <path d="M50 2 L60 32 L92 20 L72 46 L98 62 L66 66 L72 98 L50 74 L28 98 L34 66 L2 62 L28 46 L8 20 L40 32 Z"
        fill="${c}" stroke="${INK}" stroke-width="3" stroke-linejoin="round"/>
</svg>`

const NOTE_GLYPHS = ['♪', '♫', '♩', '♬']

// ────────────────────────────────────────────────────────────────────────────

export function createCircusScene(): CircusScene {
  let root: HTMLElement | null = null
  let host: HTMLElement | null = null
  let stage: HTMLElement | null = null
  let fx: HTMLElement | null = null
  let eggWrap: HTMLElement | null = null
  let eggTalk: HTMLElement | null = null
  let bunting: HTMLElement | null = null
  let clouds: { el: HTMLElement; x: number; y: number; speed: number; seed: number; w: number }[] = []
  let ro: ResizeObserver | null = null
  let timer: ReturnType<typeof setInterval> | null = null

  let W = 1, H = 1
  let frame = 0
  let status = 'idle'
  let accent = '#b8606e'
  let reduced = false
  let gagSeq = 0
  let gags: Gag[] = []
  let walkers = new Map<string, Walker>()
  /** Subagents running while the stage was full: counted, walk on later. */
  let waitingWalkers: string[] = []
  let lastStomp = -Infinity
  const activity = newActivity(Date.now())
  let squashUntil = -1
  let talkUntil = -1
  let shakeUntil = -1
  let backdrop: CircusBackdropId = 'meadow'
  let seed = 0.5
  let turn = 0
  let eggBeat: HTMLElement | null = null
  let hatEl: HTMLElement | null = null
  let lastAmbient: Record<string, number> = {}
  let lastAnyAmbient = 0
  let nextFlash = 40
  let flashUntil = -1
  let steam: { el: HTMLElement; machine: HTMLElement; puffs: { el: HTMLElement; start: number; seed: number }[]; lastPuff: number; y: number; gag: Gag } | null = null

  // Egg geometry, recomputed on resize
  const egg = { x: 0, h: 0, w: 0, ground: 0 }

  function layout() {
    if (!root) return
    W = root.clientWidth || 1
    H = root.clientHeight || 1
    egg.ground = H * GROUND
    egg.h = Math.min(H * 0.42, 380)
    egg.w = egg.h * (346 / 600)
    // Stage left of the centre column (status, latest response), clear of the curtain
    const curtain = Math.max(28, Math.min(96, W * 0.06))
    egg.x = Math.max(curtain + 8, W * 0.24 - egg.w / 2)
    if (eggWrap) {
      eggWrap.style.width = `${egg.w}px`
      eggWrap.style.height = `${egg.h}px`
    }
    for (const c of clouds) c.el.style.width = `${c.w * Math.max(0.6, Math.min(1.3, W / 1400))}px`
    for (const w of walkers.values()) sizeWalker(w)
  }

  // ── Ticker ──
  function running() {
    return !!root && !reduced && typeof document !== 'undefined' && !document.hidden
  }

  function startTicker() {
    if (timer || !running()) return
    timer = setInterval(tick, 1000 / CIRCUS_FPS)
  }

  function stopTicker() {
    if (timer) clearInterval(timer)
    timer = null
  }

  function onVisibility() {
    if (document.hidden) { stopTicker(); stopBeatLoop() }
    else startTicker()
  }

  // ── The beat ──
  // A requestAnimationFrame loop reads the music's beat clock and writes the
  // envelopes as CSS variables (--cc-pulse, --cc-accent, --cc-half, --cc-off,
  // --cc-swing, --cc-dir, --cc-phase); the CSS moves the egg, the walkers, the
  // sun, clouds and bunting from them. Beat timing is exact (60 fps); only the
  // *style* of the paper stays on twos. It runs only while music plays, the tab
  // is visible and motion is allowed.
  let pulse = 0    // 1 on a beat, easing to 0 (read by the 12 fps code)
  let accentEnv = 0
  let beating = false
  let beatRaf = 0
  let lastBeatWrite = -Infinity
  let lastClock: BeatClock = SILENT_CLOCK
  const vars: Record<string, string> = {}

  /** Elements outside the stage that follow the downbeat (the name plate). */
  const accentTargets = new Set<HTMLElement>()

  function setVar(name: string, value: string) {
    if (vars[name] === value || !root) return
    vars[name] = value
    root.style.setProperty(name, value)
    if (name === '--cc-accent') for (const t of accentTargets) t.style.setProperty(name, value)
  }

  function writeBeat(env: ReturnType<typeof beatEnvelopes> | null, phase: number, calm: ReturnType<typeof calmEnvelopes> | null = null) {
    // Two decimals: finer steps are invisible and only cause more restyles
    const f = (n: number) => n.toFixed(2)
    setVar('--cc-pulse', f(env?.pulse ?? 0))
    setVar('--cc-accent', f(env?.accent ?? 0))
    setVar('--cc-half', f(env?.half ?? 0))
    setVar('--cc-off', f(env?.off ?? 0))
    setVar('--cc-squash', f(env?.squash ?? 0))
    setVar('--cc-swing', f(env?.swing ?? 0))
    setVar('--cc-dir', String(env?.dir ?? 1))
    setVar('--cc-phase', f(phase))
    // The relaxed motion of the egg, walkers and scenery (see BEAT_MOTION)
    setVar('--cc-sway', f(calm?.sway ?? 0))
    setVar('--cc-breath', f(calm?.breath ?? 0))
    setVar('--cc-nod', f(calm?.nod ?? 0))
    setVar('--cc-bar', f(calm?.bar ?? 0))
    setVar('--cc-step', f(calm?.step ?? 0))
    setVar('--cc-stepdir', String(calm?.stepDir ?? 1))
    pulse = env?.pulse ?? 0
    accentEnv = env?.accent ?? 0
  }

  function musicPlaying(): boolean {
    try { return getZenMusic().isPlaying.value } catch { return false }
  }

  function beatFrame(now: number = performance.now()) {
    beatRaf = 0
    if (!root || !running()) return
    // Every write restyles the whole stage (inherited custom properties), and
    // requestAnimationFrame runs at 120 Hz on ProMotion displays: that kept
    // the main thread about half busy on a loaded machine (4x CPU throttle:
    // 21% of the time in style recalculation alone). The paper moves on twos
    // anyway, so write at most BEAT_WRITE_FPS times a second.
    if (beating && now - lastBeatWrite < 1000 / BEAT_WRITE_FPS - 2) {
      beatRaf = requestAnimationFrame(beatFrame)
      return
    }
    lastBeatWrite = now
    let clock: BeatClock = SILENT_CLOCK
    try { clock = getZenMusic().beatClock() } catch { /* no audio available */ }
    lastClock = clock
    if (!clock.playing) {
      if (beating) { beating = false; root.classList.remove('cc-dancing'); writeBeat(null, 0) }
      return // the 12 fps ticker restarts this when music starts again
    }
    if (!beating) { beating = true; root.classList.add('cc-dancing') }
    writeBeat(beatEnvelopes(clock), clock.phase, calmEnvelopes(clock))
    beatRaf = requestAnimationFrame(beatFrame)
  }

  function ensureBeatLoop() {
    if (!beatRaf && running() && musicPlaying()) beatRaf = requestAnimationFrame(beatFrame)
  }

  function stopBeatLoop() {
    if (beatRaf) cancelAnimationFrame(beatRaf)
    beatRaf = 0
    if (beating) { beating = false; root?.classList.remove('cc-dancing') }
    writeBeat(null, 0)
  }

  /** Free-running motion when no music is playing (rain, snow, pendulum): not tied to any beat. */
  function stepAmbientVars() {
    if (beating) return
    setVar('--cc-phase', ((frame % 9) / 9).toFixed(3))
    setVar('--cc-swing', (Math.sin(frame / 7) * 0.7).toFixed(3))
  }

  /** Run `fn` on the next beat (or downbeat) of the music; at once when there is none. */
  const pendingBeats = new Set<ReturnType<typeof setTimeout>>()
  function onBeat(fn: () => void, downbeat = false) {
    const c = lastClock
    if (!c.playing || c.source !== 'grid') { fn(); return }
    const wait = Math.max(0, (downbeat ? c.toNextDownbeat : c.toNextBeat) * 1000 - 30)
    if (wait > 6000) { fn(); return }
    const h = setTimeout(() => { pendingBeats.delete(h); if (root) fn() }, wait)
    pendingBeats.add(h)
  }

  function tick() {
    frame++
    ensureBeatLoop()
    stepAmbientVars()
    stepFlash()
    maybeAmbient()
    stepClouds()
    stepEgg()
    stepWalkers()
    stepSteam()
    for (const g of [...gags]) {
      const p = (frame - g.start) / g.dur
      if (p >= 1) { removeGag(g); continue }
      g.step(frame, p, frame - g.start)
    }
    if (root) {
      if (frame < shakeUntil) {
        const a = (shakeUntil - frame) * 2.2
        root.style.transform = `translate(${jitter(1, frame, 1) * a}px, ${jitter(2, frame, 1) * a}px)`
      } else if (root.style.transform) {
        root.style.transform = ''
      }
    }
  }

  // ── Clouds ──
  function buildClouds() {
    const specs = [
      { x: 0.08, y: 0.17, w: 230, speed: 0.6 },
      { x: 0.46, y: 0.27, w: 170, speed: 0.4 },
      { x: 0.56, y: 0.08, w: 260, speed: 0.5 },
      { x: 0.9, y: 0.36, w: 150, speed: 0.35 },
    ]
    clouds = specs.map((s, i) => {
      const el = div('cc-cloud', CLOUD_HTML)
      el.style.width = `${s.w}px`
      stage!.appendChild(el)
      return { el, x: s.x, y: s.y, speed: s.speed, seed: i + 11, w: s.w }
    })
    placeClouds()
  }

  function placeClouds() {
    for (const c of clouds) {
      const px = c.x * (W + 300) - 150
      const jx = reduced ? 0 : jitter(c.seed, frame, 4)
      const jy = reduced ? 0 : jitter(c.seed + 5, frame, 4)
      c.el.style.transform = `translate(${px + jx}px, ${c.y * H + jy}px)`
    }
  }

  function stepClouds() {
    // Clouds advance a whole pixel-step every few frames (paper slid by hand)
    if (frame % 3 === 0) {
      for (const c of clouds) {
        c.x += (c.speed * (status === 'processing' ? 2 : 1)) / Math.max(400, W)
        if (c.x > 1.05) c.x = -0.05
      }
    }
    placeClouds()
  }

  // ── The egg gentleman ──
  function buildEgg() {
    eggWrap = div('cc-egg')
    const shadow = div('cc-egg-shadow')
    eggWrap.appendChild(shadow)
    eggBeat = div('cc-egg-beat')
    eggBeat.appendChild(img('egg.webp', 'cc-egg-img cc-cutout'))
    hatEl = div('cc-hat cc-cutout')
    eggBeat.appendChild(hatEl)
    eggTalk = div('cc-egg-talk', `<svg viewBox="0 0 40 40" aria-hidden="true"><path d="M4 10 L18 4 M6 20 L22 20 M4 30 L18 36" stroke="${INK}" stroke-width="3" stroke-linecap="round"/></svg>`)
    eggBeat.appendChild(eggTalk)
    eggWrap.appendChild(eggBeat)
    stage!.appendChild(eggWrap)
    dressEgg()
  }

  /** The egg gentleman keeps his own bowler unless this turn's hat is another one. */
  function dressEgg() {
    if (!hatEl) return
    const hat = hatFor(seed, turn)
    hatEl.innerHTML = hat === 'bowler' ? '' : HAT_ART[hat].svg
    hatEl.dataset.hat = hat
  }

  function stepEgg() {
    if (!eggWrap) return
    const talking = status === 'processing' || frame < talkUntil
    setVar('--cc-energy', String(status === 'processing' ? BEAT_MOTION.energyWorking : BEAT_MOTION.energyIdle))
    let bob = 0
    let rot = 0
    let sy = 1
    if (frame < squashUntil) {
      const left = squashUntil - frame
      sy = left > 3 ? 0.5 : left > 1 ? 1.18 : 1
    } else if (talking) {
      // Bob on twos, nod while talking: on the clock, not the music, so it never doubles
      // up with the beat sway (.cc-egg-beat)
      const beat = Math.floor(frame / 2) % 2
      bob = beat ? 7 : 0
      rot = beat ? -2.5 : 1.5
    } else if (beating) {
      // The sway on the music is CSS (.cc-egg-beat); no extra lean here
    } else {
      // Idle: lean on the cane, a slow two-pose breath
      rot = Math.floor(frame / 9) % 2 ? -1 : 0.6
    }
    const jx = jitter(3, frame) * 1.2
    const jy = jitter(4, frame) * 1.2
    eggWrap.style.transform = `translate(${egg.x + jx}px, ${egg.ground - egg.h - bob + jy}px) rotate(${rot}deg) scaleY(${sy})`
    if (eggTalk) eggTalk.style.opacity = talking && Math.floor(frame / 2) % 2 === 0 ? '1' : '0'
  }

  function placeEggStatic() {
    if (!eggWrap) return
    eggWrap.style.transform = `translate(${egg.x}px, ${egg.ground - egg.h}px)`
    if (eggTalk) eggTalk.style.opacity = '0'
  }

  // ── Gags ──
  function addGag(kind: CircusGag, el: HTMLElement, dur: number, step: Gag['step'], done?: () => void): Gag {
    for (const id of gagsToEvict(gags.map(g => g.id), MAX_GAGS)) {
      const old = gags.find(g => g.id === id)
      if (old) removeGag(old)
    }
    const g: Gag = { id: ++gagSeq, kind, el, start: frame, dur, step, done }
    fx!.appendChild(el)
    gags.push(g)
    step(frame, 0, 0)
    return g
  }

  function removeGag(g: Gag) {
    const i = gags.indexOf(g)
    if (i >= 0) gags.splice(i, 1)
    g.el.remove()
    g.done?.()
  }

  const has = (kind: CircusGag) => gags.some(g => g.kind === kind)

  /**
   * The HUD (chips, cards, name plate) in stage coordinates, measured when a gag is
   * about to be placed so it follows resizes and cards that grew or shrank.
   */
  function measureHud(): Box[] {
    if (!root) return []
    const zen = root.closest('.zen-mode')
    if (!zen) return []
    const o = root.getBoundingClientRect()
    const rects = [...zen.querySelectorAll(HUD_SELECTORS.join(','))].map(e => e.getBoundingClientRect())
    return hudBoxes(rects, o)
  }

  function gagBook() {
    const el = div('cc-gag cc-book cc-cutout', BOOK_SVG)
    const s = hash01(String(gagSeq), 3)
    const y0 = H * (0.16 + s * 0.16)
    addGag('book', el, 40, (f, p) => {
      const x = W + 80 - p * (W + 240)
      const y = y0 + Math.sin(p * Math.PI * 3) * H * 0.04
      const flap = Math.floor(f / 2) % 2 ? 0.45 : 1
      el.style.transform = `translate(${x}px, ${y}px) rotate(${jitter(s * 100, f) * 6 - 8}deg) scaleY(${flap})`
    })
  }

  function gagQuill() {
    const el = div('cc-gag cc-quill-wrap')
    const quill = div('cc-quill cc-cutout', QUILL_SVG)
    el.appendChild(quill)
    const scraps = Array.from({ length: 6 }, (_, i) => {
      const sc = div('cc-scrap')
      sc.style.background = PALETTE[i % PALETTE.length]!
      sc.style.clipPath = ['polygon(0 0,100% 20%,80% 100%,10% 80%)', 'polygon(20% 0,100% 40%,60% 100%,0 60%)', 'polygon(0 30%,70% 0,100% 100%,30% 90%)'][i % 3]!
      el.appendChild(sc)
      return { el: sc, vx: (hash01(`${gagSeq}q${i}`) - 0.5) * 18, vy: -10 - hash01(`${gagSeq}v${i}`) * 10 }
    })
    // Above the hat: the centre column (latest response) sits right of the egg
    const bx = egg.x + egg.w * 0.2
    const by = egg.ground - egg.h * 1.02
    addGag('quill', el, 34, (f, p, local) => {
      el.style.transform = `translate(${bx}px, ${by}px)`
      if (p < 0.55) {
        const zig = Math.floor(f / 2) % 2 ? 14 : -10
        quill.style.transform = `translate(${zig + local * 2}px, ${(local % 4) * 3}px) rotate(${zig > 0 ? 8 : -6}deg)`
        quill.style.opacity = '1'
        for (const s of scraps) s.el.style.opacity = '0'
      } else {
        quill.style.opacity = local % 2 ? '1' : '0.0'
        const t = local - 18
        for (const s of scraps) {
          s.el.style.opacity = '1'
          s.el.style.transform = `translate(${s.vx * t}px, ${s.vy * t + 1.6 * t * t}px) rotate(${t * 40 * Math.sign(s.vx)}deg)`
        }
      }
    })
  }

  function stepSteam() {
    if (!steam) return
    const s = steam
    const age = frame - s.lastPuff
    // Rise in 3 steps, sink in 3 steps after the last puff has gone
    const mh = s.machine.clientHeight || H * 0.3
    const target = egg.ground - mh * 0.96
    const sinking = age > 40
    const k = sinking ? Math.min(1, (age - 40) / 3) : Math.min(1, (frame - (s.gag.start)) / 3)
    const y = sinking ? target + (H - target) * Math.ceil(k * 3) / 3 : H - (H - target) * Math.ceil(k * 3) / 3
    s.y = y
    const x = W * 0.86 - (s.machine.clientWidth || 0) / 2
    s.el.style.transform = `translate(${x + jitter(7, frame) }px, ${y + jitter(8, frame)}px)`
    for (const pf of [...s.puffs]) {
      const t = frame - pf.start
      if (t > 22) { pf.el.remove(); s.puffs.splice(s.puffs.indexOf(pf), 1); continue }
      const sc = 0.5 + Math.ceil(t / 3) * 0.12
      pf.el.style.transform = `translate(${(s.machine.clientWidth || 100) * 0.62 + t * 2 + jitter(pf.seed, frame) * 3}px, ${-t * 6}px) scale(${sc})`
      pf.el.style.opacity = t > 15 ? String(1 - Math.ceil((t - 15) / 2) * 0.25) : '1'
    }
    if (sinking && k >= 1) removeGag(s.gag)
  }

  function gagSteam() {
    if (steam) {
      puff()
      return
    }
    const el = div('cc-gag cc-steam')
    const machine = img('metrics.webp', 'cc-machine cc-cutout')
    // Scaled to the stage so it does not swallow a narrow (windowed) zen view
    machine.style.height = `${Math.max(100, Math.min(H * 0.3, W * 0.28, 280))}px`
    el.appendChild(machine)
    const g = addGag('steam', el, 100000, () => {}, () => { steam = null })
    steam = { el, machine, puffs: [], lastPuff: frame, y: H, gag: g }
    puff()
  }

  function puff() {
    if (!steam) return
    steam.lastPuff = frame
    if (steam.puffs.length >= 4) return
    const p = div('cc-puff cc-cutout', PUFF_SVG)
    steam.el.appendChild(p)
    steam.puffs.push({ el: p, start: frame, seed: frame % 97 })
  }

  function gagMagnifier() {
    if (has('magnifier')) return
    const el = div('cc-gag cc-magnifier cc-cutout', MAGNIFIER_SVG)
    addGag('magnifier', el, 36, (f, p) => {
      const x = W * 0.08 + p * W * 0.8
      const y = H * 0.62 + Math.sin(p * Math.PI * 4) * H * 0.03
      el.style.transform = `translate(${x}px, ${y}px) rotate(${jitter(31, f) * 5}deg)`
    })
  }

  function notes(parent: HTMLElement, n: number) {
    const out: HTMLElement[] = []
    for (let i = 0; i < n; i++) {
      const s = document.createElement('span')
      s.className = 'cc-note'
      s.textContent = NOTE_GLYPHS[(gagSeq + i) % NOTE_GLYPHS.length]!
      parent.appendChild(s)
      out.push(s)
    }
    return out
  }

  function gagCherub(fanfare: boolean) {
    if (fanfare && has('fanfare')) return
    if (!fanfare && has('cherub')) return
    const el = div('cc-gag cc-cherub-wrap')
    const ch = img('cherub.webp', 'cc-cherub cc-cutout')
    el.appendChild(ch)
    const ns = notes(el, 4)
    const size = Math.min(H * 0.24, 200)
    ch.style.width = `${size}px`
    const y0 = H * 0.08
    addGag(fanfare ? 'fanfare' : 'cherub', el, fanfare ? 36 : 44, (f, p) => {
      let x: number, y: number
      if (fanfare) {
        // Swoop down beside the egg, toot, swoop off
        const hx = egg.x + egg.w * 1.05
        const hy = egg.ground - egg.h * 1.25
        if (p < 0.25) { const k = Math.ceil((p / 0.25) * 4) / 4; x = W + 40 - (W + 40 - hx) * k; y = -size + (hy + size) * k }
        else if (p < 0.7) { x = hx; y = hy }
        else { const k = Math.ceil(((p - 0.7) / 0.3) * 4) / 4; x = hx + k * W * 0.4; y = hy - k * (hy + size * 1.4) }
      } else {
        x = -size - 20 + p * (W + size * 2)
        y = y0 + Math.sin(p * Math.PI * 2) * H * 0.04
      }
      const flap = Math.floor(f / 2) % 2
      ch.style.transform = `translate(${x}px, ${y + (flap ? -5 : 0)}px) rotate(${flap ? -4 : 3}deg)`
      const tooting = !fanfare || (p >= 0.25 && p < 0.7)
      ns.forEach((n, i) => {
        const t = (f + i * 4) % 16
        n.style.opacity = tooting ? String(1 - t / 16) : '0'
        n.style.transform = `translate(${x + size * 0.92 + t * 4}px, ${y + size * 0.1 - t * 3 + (i % 2) * 10}px)`
      })
    })
  }

  /** A summary or joke spelled across up to two short lines of tiles beside the egg. */
  function gagMessage(m: LetterMessage) {
    talkUntil = frame + 20
    const lines = wrapMessage(m.text)
    const el = div('cc-gag cc-letters')
    const bannerW = Math.min(W * 0.36, 420)
    const perLine = Math.max(...lines.map(l => l.length), 1)
    const tile = Math.max(11, Math.min(24, bannerW / perLine))
    const tiles: { s: HTMLElement; tx: number; ty: number }[] = []
    // Settle beside the egg, clear of the HUD cards: try the egg's own spot, then
    // nudge sideways and up; nothing free means no banner this time.
    const spanW = perLine * tile
    const spanH = lines.length * tile * 1.6
    const baseCx = Math.max(bannerW / 2 + 8, egg.x + egg.w / 2)
    const baseTop = Math.max(8, egg.ground - egg.h - spanH - 6)
    const spot = pickSpot(
      [0, -1, 1, -2, 2].flatMap(dx => [0, -0.1, -0.2, 0.1].map(dy => ({
        x: Math.min(W - spanW - 8, Math.max(8, baseCx + dx * spanW * 0.6 - spanW / 2)),
        y: Math.max(8, baseTop + dy * H),
        w: spanW,
        h: spanH,
      }))),
      measureHud(),
    )
    if (!spot) return
    const cx = spot.x + spanW / 2
    const top = spot.y
    lines.forEach((line, row) => {
      const x0 = cx - (line.length * tile) / 2
      ;[...line].forEach((ch, col) => {
        if (ch === ' ') return
        const s = document.createElement('span')
        s.className = 'cc-letter cc-letter-sm'
        s.textContent = ch
        s.style.fontSize = `${Math.round(tile * 0.85)}px`
        s.style.background = PALETTE[(tiles.length + gagSeq) % PALETTE.length]!
        s.style.fontFamily = tiles.length % 2 ? "'Rye', serif" : "'Courier Prime', monospace"
        el.appendChild(s)
        tiles.push({ s, tx: x0 + col * tile, ty: top + row * tile * 1.6 })
      })
    })
    const ox = egg.x + egg.w * 0.75
    const oy = egg.ground - egg.h * 0.78
    addGag('letters', el, 30 + tiles.length * 2, (_f, p, local) => {
      tiles.forEach((t, i) => {
        const k = Math.ceil(Math.max(0, local - i * 2) / 2) * 2 // on twos
        const e = Math.min(1, k / 10)
        const ease = 1 - (1 - e) * (1 - e)
        const wobble = (i % 2 ? 1 : -1) * (1 - e) * 40
        t.s.style.opacity = k <= 0 ? '0' : p > 0.85 ? '0.4' : '1'
        t.s.style.transform = `translate(${ox + (t.tx - ox) * ease}px, ${oy + (t.ty - oy) * ease}px) rotate(${wobble + ((i % 3) - 1) * 2 * e}deg)`
      })
    })
  }

  function gagLetters(label: string) {
    if (has('letters')) return
    const m = pickLetterMessage({ activity, status, now: Date.now() })
    if (m.kind !== 'word' && m.text) return gagMessage(m)
    talkUntil = frame + 14
    // The tiles fly up and to the right of the egg's mouth: skip when that path is under the HUD
    const flight: Box = { x: egg.x + egg.w * 0.75, y: egg.ground - egg.h * 0.78 - 130, w: 200, h: 150 }
    if (!pickSpot([flight], measureHud())) return
    const el = div('cc-gag cc-letters')
    const letters = lettersFrom(label).map((ch, i) => {
      const s = document.createElement('span')
      s.className = 'cc-letter'
      s.textContent = ch
      s.style.background = PALETTE[(i + gagSeq) % PALETTE.length]!
      s.style.fontFamily = i % 2 ? "'Rye', serif" : "'Courier Prime', monospace"
      el.appendChild(s)
      return s
    })
    const ox = egg.x + egg.w * 0.75
    const oy = egg.ground - egg.h * 0.78
    addGag('letters', el, 26, (_f, p, local) => {
      letters.forEach((s, i) => {
        const t = Math.max(0, local - i * 2)
        const k = Math.ceil(t / 2) * 2
        s.style.opacity = t <= 0 ? '0' : p > 0.8 ? '0.4' : '1'
        s.style.transform = `translate(${ox + k * (5 + i * 2.5)}px, ${oy - k * 4.5 - i * 4}px) rotate(${(i % 2 ? 1 : -1) * (6 + k)}deg)`
      })
    })
  }

  function gagStomp() {
    if (frame - lastStomp < STOMP_COOLDOWN_MS / (1000 / CIRCUS_FPS)) return
    if (has('stomp')) return
    lastStomp = frame
    const el = div('cc-gag cc-foot-wrap')
    const foot = img('foot.webp', 'cc-foot cc-cutout')
    el.appendChild(foot)
    const fh = egg.ground + H * 0.02
    const fw = fh * (716 / 900)
    foot.style.height = `${fh}px`
    foot.style.width = `${fw}px`
    // The foot is on tiptoe: the ball of the foot (right of the image) lands on the egg
    const x = egg.x + egg.w * 0.5 - fw * 0.78
    let landed = false
    addGag('stomp', el, 30, (f, _p, t) => {
      let y: number
      if (t < 4) y = -fh + (fh * Math.ceil(t + 1)) / 4 - fh * 0.02 // down in 4 hard steps
      else if (t < 20) y = -fh * 0.02 + (egg.ground - fh)
      else y = (egg.ground - fh) - (fh * Math.ceil((t - 19) * 1.0)) / 8
      if (t >= 3 && !landed) {
        landed = true
        squashUntil = frame + 14
        shakeUntil = frame + 6
      }
      el.style.transform = `translate(${x + jitter(41, f) * 1.5}px, ${Math.min(y, egg.ground - fh)}px)`
    })
  }

  function gagPop() {
    if (gags.filter(g => g.kind === 'pop').length >= 2) return
    const el = div('cc-gag cc-pop cc-cutout', BURST_SVG(PALETTE[gagSeq % 3]!))
    const s = hash01(String(gagSeq), 9)
    const x = egg.x + egg.w * (1.2 + s * 1.2)
    const y = egg.ground - egg.h * (0.5 + s * 0.6)
    addGag('pop', el, 10, (f, p) => {
      const sc = p < 0.3 ? 0.5 : p < 0.8 ? 1 : 0.7
      el.style.transform = `translate(${x}px, ${y}px) scale(${sc}) rotate(${jitter(s * 50, f, 2) * 10}deg)`
      el.style.opacity = p > 0.8 ? '0.5' : '1'
    })
  }

  function play(kind: CircusGag, label = '') {
    if (!root || !running()) return
    switch (kind) {
      case 'book': return gagBook()
      case 'quill': return gagQuill()
      case 'steam': return gagSteam()
      case 'magnifier': return gagMagnifier()
      case 'cherub': return gagCherub(false)
      case 'letters': return gagLetters(label)
      case 'stomp': return gagStomp()
      case 'pop': return gagPop()
      default: return
    }
  }

  // ── Ambient gags: rare, on a cooldown, entering on a beat ──
  const sec = (n: number) => Math.round(n * CIRCUS_FPS)
  const BANNER_COOLDOWN_MS = 180_000
  const STALK_LINES = ['Form 27b, if you please', 'Please queue here', 'Your hat is in the wrong form', 'Next!', 'Stamped, signed, and slightly damp', 'Is this the department of tea?']

  function hopVars(el: HTMLElement, hy: number, hr: number, hd: number) {
    el.style.setProperty('--hy', `${hy}px`)
    el.style.setProperty('--hr', `${hr}deg`)
    el.style.setProperty('--hd', String(hd))
  }

  function gagBanner() {
    if (has('banner') || gags.length >= MAX_GAGS - 1) return
    const el = div('cc-gag cc-banner')
    const inner = div('cc-banner-in cc-hop')
    inner.textContent = BANNER_LINES[Math.floor(Math.random() * BANNER_LINES.length)]!
    hopVars(inner, -5, 1.2, 1)
    el.appendChild(inner)
    const w0 = el.offsetWidth || 360
    const rest0 = Math.max(W * 0.5 - w0 / 2, W * 0.12)
    // Rest where no HUD box is (the card column sits mid-stage): try a few heights
    const bh = 44
    const spot = pickSpot(
      [BANNER_Y, 0.1, 0.3, 0.4, 0.5, 0.58].map(f => ({ x: rest0, y: H * f, w: w0, h: bh })),
      measureHud(),
    )
    if (!spot) return
    const y = spot.y
    addGag('banner', el, sec(8), (_f, p) => {
      const w = el.offsetWidth || 360
      const rest = Math.max(W * 0.5 - w / 2, W * 0.12)
      const kin = Math.min(1, p / 0.1)
      const kout = p > 0.9 ? (p - 0.9) / 0.1 : 0
      const x = kout > 0 ? rest + (W - rest) * (Math.ceil(kout * 4) / 4) : -w + (rest + w) * (Math.ceil(kin * 4) / 4)
      el.style.transform = `translate(${x}px, ${y}px)`
    })
  }

  function gagTea() {
    if (has('tea') || gags.length >= MAX_GAGS - 1) return
    const el = div('cc-gag cc-tea')
    const parts = [TEAPOT_ART, TEACUP_ART, TEACUP_ART, TEACUP_ART]
    const h = Math.min(H * 0.1, 84)
    const items = parts.map((a, i) => {
      const it = div('cc-tea-item')
      const inn = div('cc-hop cc-cutout', a.svg)
      inn.style.width = `${h * (a.w / a.h)}px`
      hopVars(inn, -16, 7, i % 2 ? -1 : 1)
      it.appendChild(inn)
      el.appendChild(it)
      return { it, w: h * (a.w / a.h) }
    })
    const gap = h * 1.5
    const total = items.length * gap
    addGag('tea', el, sec(11), (_f, p) => {
      const lead = W + 40 - p * (W + total + 120)
      items.forEach((m, i) => {
        m.it.style.transform = `translate(${lead + i * gap}px, ${egg.ground - h * 0.9}px)`
      })
    })
  }

  function gagStalk() {
    if (has('stalk') || gags.length >= MAX_GAGS - 1) return
    const h = Math.min(H * 0.34, 260)
    const w = h * (STALK_HEAD_ART.w / STALK_HEAD_ART.h)
    const el = div('cc-gag cc-stalk')
    const clip = div('cc-stalk-clip')
    clip.style.width = `${w}px`
    clip.style.height = `${h}px`
    const inn = div('cc-stalk-in cc-hop cc-cutout', STALK_HEAD_ART.svg)
    hopVars(inn, -6, 5, 1)
    clip.appendChild(inn)
    el.appendChild(clip)
    const balloon = div('cc-balloon')
    balloon.textContent = STALK_LINES[Math.floor(Math.random() * STALK_LINES.length)]!
    el.appendChild(balloon)
    const baseY = egg.ground - H * 0.05
    const hud = measureHud()
    const xs = shuffled([0.6, 0.66, 0.72, 0.78, 0.84, 0.9, 0.12, 0.3, 0.4], Math.random)
    const spot = pickSpot(xs.map(f => ({ x: W * f - w / 2, y: baseY - h, w, h })), hud)
    if (!spot) return
    const x = spot.x
    addGag('stalk', el, sec(8), (_f, p) => {
      const rise = p < 0.12 ? Math.ceil((p / 0.12) * 3) / 3 : p > 0.88 ? 1 - Math.ceil(((p - 0.88) / 0.12) * 3) / 3 : 1
      el.style.transform = `translate(${x}px, ${baseY - h}px)`
      inn.style.marginTop = `${(1 - rise) * h}px`
      balloon.style.opacity = rise >= 1 ? '1' : '0'
    })
  }

  function gagCoconut() {
    if (has('coconut') || gags.length >= MAX_GAGS - 1) return
    const el = div('cc-gag cc-coconut')
    const w = Math.min(H * 0.1, 78)
    for (let i = 0; i < 2; i++) {
      const c = div('cc-coco cc-hop cc-cutout', COCONUT_ART.svg)
      c.style.width = `${w}px`
      c.style.left = `${i * w * 1.25}px`
      hopVars(c, -18, i ? -9 : 9, i ? -1 : 1)
      c.style.setProperty('--hd', i ? '-1' : '1')
      el.appendChild(c)
    }
    el.appendChild(div('cc-clop', 'clip-clop'))
    const x = egg.x + egg.w * 1.45
    addGag('coconut', el, sec(10), (_f, p) => {
      el.style.opacity = p < 0.06 || p > 0.94 ? '0.4' : '1'
      el.style.transform = `translate(${x}px, ${egg.ground - w * 0.5}px)`
    })
  }

  interface CrossCfg { h: number; secs: number; lane: 'sky' | 'ground' | 'horizon' | 'diag'; hy: number; hr: number; wave?: number }
  const CROSS: Record<CrossingProp, CrossCfg> = {
    balloon: { h: 0.3, secs: 18, lane: 'sky', hy: -6, hr: 3, wave: 0.03 },
    bicycle: { h: 0.19, secs: 9, lane: 'ground', hy: -8, hr: 2 },
    train: { h: 0.17, secs: 10, lane: 'horizon', hy: -4, hr: 0.6 },
    fish: { h: 0.15, secs: 12, lane: 'sky', hy: -10, hr: 6, wave: 0.07 },
    cat: { h: 0.42, secs: 14, lane: 'ground', hy: -8, hr: 1.5 },
    shootingstar: { h: 0.06, secs: 1.8, lane: 'diag', hy: 0, hr: 0 },
    gramophone: { h: 0.18, secs: 10, lane: 'ground', hy: -14, hr: 6 },
    rabbit: { h: 0.17, secs: 8, lane: 'ground', hy: -34, hr: 8 },
    bird: { h: 0.11, secs: 10, lane: 'sky', hy: -8, hr: 10, wave: 0.06 },
  }

  function gagCrossing(prop: CrossingProp) {
    if (has('crossing') || gags.length >= MAX_GAGS - 1) return
    const cfg = CROSS[prop]
    const a = CROSSING_ART[prop]
    const h = H * cfg.h
    const w = h * (a.w / a.h)
    const dir = prop === 'shootingstar' ? 1 : Math.random() < 0.5 ? 1 : -1
    const el = div('cc-gag cc-cross')
    const inn = div('cc-hop cc-cutout', a.svg)
    inn.style.width = `${w}px`
    hopVars(inn, cfg.hy, cfg.hr, 1)
    el.appendChild(inn)
    // Sky lanes keep clear of the HUD cards/chips/plate (skipped when none is free); ground,
    // horizon and shooting-star lanes run low or fast and pass behind the HUD, which sits above the stage.
    let sky = H * (0.1 + Math.random() * 0.14)
    if (cfg.lane === 'sky') {
      const margin = H * (cfg.wave ?? 0.03) + 8
      const lane = pickLane(W, H * 0.06 + margin, Math.max(H * 0.06 + margin, H * 0.6 - h - margin), h, margin, measureHud(), Math.random)
      if (lane === null) return
      sky = lane
    }
    addGag('crossing', el, sec(cfg.secs), (f, p) => {
      const x = dir > 0 ? -w + p * (W + w * 2) : W - p * (W + w * 2)
      let y: number
      if (cfg.lane === 'sky') y = sky + Math.sin(p * Math.PI * 4 + seed * 6) * H * (cfg.wave ?? 0.03)
      else if (cfg.lane === 'horizon') y = H * 0.72 - h
      else if (cfg.lane === 'diag') y = H * 0.04 + p * H * 0.3
      else y = egg.ground - h * (prop === 'cat' ? 0.78 : 0.97)
      const flip = dir < 0 && cfg.lane !== 'diag' ? -1 : 1
      el.style.transform = `translate(${x}px, ${y + jitter(w, f, 4)}px) scaleX(${flip})`
    })
  }

  function runAmbient(p: AmbientPick) {
    if (!root || !running()) return
    switch (p.kind) {
      case 'banner': return gagBanner()
      case 'tea': return gagTea()
      case 'stalk': return gagStalk()
      case 'coconut': return gagCoconut()
      case 'crossing': return p.prop ? gagCrossing(p.prop) : undefined
    }
  }

  function playAmbient(p: AmbientPick) {
    const now = performance.now()
    lastAmbient[ambientKey(p)] = now
    lastAnyAmbient = now
    onBeat(() => runAmbient(p), p.onDownbeat)
  }

  /** Once a second, on a slow random timer: maybe one rare gag. */
  function maybeAmbient() {
    if (frame % CIRCUS_FPS !== 0 || !root || !running()) return
    const now = performance.now()
    if (now - lastAnyAmbient < AMBIENT_IDLE_AFTER_MS || Math.random() > 0.05) return
    const pick = pickAmbient({ now, last: lastAmbient, lastAny: lastAnyAmbient, onStage: gags.length, playing: musicPlaying(), backdrop, rand: Math.random })
    if (pick) playAmbient(pick)
  }

  /** A new turn starts: the announcer banner slides in on the next downbeat. */
  function turnStarted() {
    turn++
    dressEgg()
    const now = performance.now()
    if (now - (lastAmbient.banner ?? -Infinity) < BANNER_COOLDOWN_MS || gags.length >= 3) return
    playAmbient({ kind: 'banner', onDownbeat: true })
  }

  /** The storm backdrop's lightning: on a downbeat when music plays, else on a timer. */
  function stepFlash() {
    if (backdrop !== 'storm' || !root) return
    if (frame < flashUntil) { setVar('--cc-flash', (flashUntil - frame) % 2 ? '1' : '0.35'); return }
    setVar('--cc-flash', '0')
    if (frame >= nextFlash && (!beating || accentEnv > 0.85)) {
      flashUntil = frame + 5
      nextFlash = frame + sec(8 + Math.random() * 10)
    }
  }

  function applyBackdrop() {
    if (!root) return
    root.querySelector('.cc-backdrop')?.remove()
    root.insertAdjacentHTML('afterbegin', backdropSvg(backdrop))
    root.className = root.className.replace(/\bcc-bd-\w+/g, '').trim()
    root.classList.add(`cc-bd-${backdrop}`)
    root.dataset.backdrop = backdrop
    root.classList.toggle('cc-dark', backdropInfo(backdrop).dark)
  }

  // ── Subagent walkers ──
  function walkerSlotX(slot: number) {
    // Two rows of little eggs to the right of the gentleman, left of the steam machine
    const startX = egg.x + egg.w * 1.5
    const span = Math.max(egg.w, W * 0.74 - startX)
    return startX + (span * (slot + 0.5)) / MAX_WALKERS
  }

  function sizeWalker(w: Walker) {
    const h = egg.h * 0.42
    w.el.style.height = `${h}px`
    w.el.style.width = `${h * (EGG_ASPECT[(w.el.dataset.kind as CircusKind) || 'egg'] ?? 346 / 600)}px`
  }

  function spawnWalker(id: string, instant = false) {
    if (!root || walkers.has(id)) return
    const slot = freeWalkerSlot(new Set([...walkers.values()].map(w => w.slot)))
    if (slot < 0) {
      if (!waitingWalkers.includes(id)) waitingWalkers.push(id)
      return
    }
    const el = div('cc-walker')
    const hue = Math.round(hash01(id) * 360)
    // The subagent's engraved egg in costume, the same one the subagents view shows
    const kind = circusCharacterFor(id).kind
    const im = document.createElement('img')
    im.className = 'cc-walker-img'
    im.src = circusEggArt(kind)
    im.alt = ''
    im.draggable = false
    el.dataset.kind = kind
    const beat = div('cc-walker-beat')
    beat.appendChild(im)
    el.appendChild(beat)
    el.style.setProperty('--wdir', slot % 2 ? '-1' : '1')
    stage!.appendChild(el)
    const w: Walker = { id, slot, el, state: reduced || instant ? 'idle' : 'in', since: frame, x: reduced || instant ? walkerSlotX(slot) : W + 40, fromX: W + 40, hue }
    sizeWalker(w)
    walkers.set(id, w)
    placeWalker(w, 0, 0)
  }

  function placeWalker(w: Walker, bob: number, rot: number) {
    const h = egg.h * 0.42
    const row = w.slot % 2 ? H * 0.035 : 0
    w.el.classList.toggle('cc-silly', w.state === 'in' || w.state === 'out')
    w.el.style.transform = `translate(${w.x}px, ${egg.ground - h + row - bob}px) rotate(${rot}deg)`
    w.el.style.zIndex = String(w.slot % 2 ? 3 : 2)
  }

  function stepWalkers() {
    for (const w of [...walkers.values()]) {
      const t = frame - w.since
      const target = walkerSlotX(w.slot)
      const seed = w.hue + 1
      let bob = 0, rot = 0
      if (w.state === 'in') {
        // March on in steps
        const dur = 24
        const k = Math.min(1, t / dur)
        w.x = w.fromX + (target - w.fromX) * k
        bob = beating ? 0 : Math.floor(frame / 2) % 2 ? 6 : 0
        rot = beating ? 0 : bob ? -5 : 4
        if (k >= 1) { w.state = 'idle'; w.since = frame }
      } else if (w.state === 'idle') {
        w.x = target
        rot = jitter(seed, frame, 7) * 3
        bob = beating ? 0 : Math.floor((frame + seed) / 18) % 4 === 0 ? 5 : 0
      } else if (w.state === 'bow') {
        w.x = target
        rot = Math.floor(t / 4) % 2 === 0 ? 24 : 0
        if (t > 14) { w.state = 'out'; w.since = frame; w.fromX = w.x }
      } else {
        const k = t / 20
        w.x = w.fromX + (W + 60 - w.fromX) * k
        bob = beating ? 0 : Math.floor(frame / 2) % 2 ? 6 : 0
        rot = beating ? 0 : bob ? 5 : -4
        if (k >= 1) { removeWalker(w); continue }
      }
      placeWalker(w, bob, rot + jitter(seed, frame) * 0.8)
    }
  }

  function removeWalker(w: Walker) {
    w.el.remove()
    walkers.delete(w.id)
    const next = waitingWalkers.shift()
    if (next) spawnWalker(next)
  }

  function completeWalker(id: string) {
    const wi = waitingWalkers.indexOf(id)
    if (wi >= 0) { waitingWalkers.splice(wi, 1); return }
    const w = walkers.get(id)
    if (!w) return
    if (!running()) { removeWalker(w); return }
    if (w.state === 'bow' || w.state === 'out') return
    w.state = 'bow'
    w.since = frame
  }

  // ── Mount / unmount ──
  function mount(el: HTMLElement) {
    if (root && host === el) return
    if (root) unmount()
    host = el
    reduced = typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    root = div('zen-circus')
    root.setAttribute('aria-hidden', 'true')
    if (reduced) root.classList.add('cc-reduced')
    lastAnyAmbient = performance.now()
    applyBackdrop()
    stage = div('cc-stage')
    fx = div('cc-fx')
    root.appendChild(stage)
    bunting = div('cc-bunting-wrap', BUNTING_SVG(accent))
    root.appendChild(bunting)
    root.insertAdjacentHTML('beforeend', CURTAIN_SVG('l') + CURTAIN_SVG('r'))
    root.appendChild(fx)
    root.appendChild(div('cc-grain'))
    el.prepend(root)

    buildClouds()
    buildEgg()
    layout()
    placeClouds()
    placeEggStatic()

    ro = new ResizeObserver(() => { layout(); if (!timer) { placeClouds(); placeEggStatic() } })
    ro.observe(root)
    document.addEventListener('visibilitychange', onVisibility)
    // Fade in on the next frame (the canvas scenes do the same)
    requestAnimationFrame(() => root?.classList.add('cc-shown'))
    startTicker()
  }

  function clearActors() {
    for (const g of [...gags]) removeGag(g)
    gags = []
    for (const w of walkers.values()) w.el.remove()
    walkers.clear()
    waitingWalkers = []
    steam = null
    for (const h of pendingBeats) clearTimeout(h)
    pendingBeats.clear()
    squashUntil = talkUntil = shakeUntil = -1
  }

  function unmount(el?: HTMLElement | null) {
    if (el !== undefined && el !== host) return
    stopTicker()
    document.removeEventListener('visibilitychange', onVisibility)
    ro?.disconnect()
    ro = null
    stopBeatLoop()
    clearActors()
    root?.remove()
    root = host = stage = fx = eggWrap = eggBeat = hatEl = eggTalk = bunting = null
    for (const k of Object.keys(vars)) delete vars[k]
    clouds = []
  }

  return {
    mount,
    unmount,
    get mounted() { return !!root },
    reset() {
      clearActors()
      lastStomp = -Infinity
    },
    setStatus(s: string) {
      const next = s || 'idle'
      const started = next === 'processing' && status !== 'processing' && !!root
      status = next
      if (started) turnStarted()
    },
    setBackdrop(id: CircusBackdropId) {
      if (id === backdrop) return
      backdrop = id
      nextFlash = frame + sec(4)
      applyBackdrop()
    },
    followDownbeat(el: HTMLElement) {
      accentTargets.add(el)
      return () => { accentTargets.delete(el); el.style.removeProperty('--cc-accent') }
    },
    setSession(id: string) {
      seed = hash01(id || '', 31)
      turn = 0
      dressEgg()
    },
    setProjectColor(hex: string) {
      const next = /^#[0-9a-f]{6}$/i.test(hex?.trim() || '') ? hex.trim() : '#b8606e'
      if (next === accent) return
      accent = next
      if (bunting) bunting.innerHTML = BUNTING_SVG(accent)
    },
    toolEvent(e: ZenToolEvent) {
      play(gagForEvent(e), e.label)
      noteActivity(activity, e, Date.now())
    },
    sendPulse() {
      if (!root || !running()) return
      gagCherub(true)
      bunting?.classList.remove('cc-bunting-hop')
      void bunting?.offsetWidth
      bunting?.classList.add('cc-bunting-hop')
    },
    agentSpawn(id: string) { if (id) spawnWalker(id) },
    agentComplete(id: string) { if (id) completeWalker(id) },
    agentSync(runningIds: string[]) {
      if (!root) return
      const want = new Set(runningIds.filter(Boolean))
      waitingWalkers = waitingWalkers.filter(id => want.has(id))
      for (const w of [...walkers.values()]) if (!want.has(w.id) && w.state !== 'bow' && w.state !== 'out') { w.el.remove(); walkers.delete(w.id) }
      for (const id of want) spawnWalker(id, true)
    },
  }
}

/** Shared instance: the scene follows ZenMode's single canvas container. */
let shared: CircusScene | null = null
export function getCircusScene(): CircusScene {
  if (!shared) shared = createCircusScene()
  return shared
}
