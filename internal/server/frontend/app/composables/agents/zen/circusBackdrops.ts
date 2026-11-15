/**
 * The ten Circus backdrops: Victorian engraved paper-collage scenery that
 * belongs with the engraved egg gentleman. Muted ink-and-wash palettes on aged
 * paper, engraved line skies, torn-paper hills with hatching, and engraved
 * cut-outs (generated art in public/zen/circus/backdrops/, see ASSETS.md).
 *
 * Every backdrop has its own wash, horizon and signature cut-out, so a session
 * can be told apart at thumbnail size. The assignment of backdrops to sessions
 * (stable per session, spread over the open ones) lives here too, free of the
 * DOM, so it can be unit tested.
 *
 * Cost: everything that never moves is painted into "plates", self-contained
 * SVG documents used as <image> data URIs, which the browser rasterises once.
 * Only the cut-outs and the few beat-hooked groups are live SVG. No filters.
 *
 * Beat hooks (CSS in circus.css reads the variables sceneCircus.ts writes):
 *   cc-b-pulse  scale + tilt on the bar          cc-b-half   slow bob
 *   cc-b-off    smaller bob                      cc-b-swing  pendulum from the top
 *   cc-b-flicker opacity follows the beat        cc-b-fall   loops once per beat (rain, snow)
 *   cc-b-flash  lightning (opacity 0..1)
 * A hooked group never carries a transform attribute (the CSS transform would replace it).
 */
import { hash01 } from './types'

const INK = '#2b1d10'
const CREAM = '#f1e6cb'

export type CircusBackdropId =
  | 'meadow' | 'dusk' | 'storm' | 'tearoom' | 'snow'
  | 'seaside' | 'desert' | 'jungle' | 'gothic' | 'space'

export interface CircusBackdropInfo {
  id: CircusBackdropId
  name: string
  /** Dark scene: loose HUD text switches to light ink */
  dark: boolean
  /** Stage boards colour */
  board: string
}

export const CIRCUS_BACKDROPS: readonly CircusBackdropInfo[] = [
  { id: 'meadow', name: 'Teal Hills', dark: false, board: '#cdb88c' },
  { id: 'dusk', name: 'Big Sunset', dark: false, board: '#a9765a' },
  { id: 'storm', name: 'Stormy Night', dark: true, board: '#6f7378' },
  { id: 'tearoom', name: 'Tea Room', dark: true, board: '#8a6448' },
  { id: 'snow', name: 'Snowy Village', dark: false, board: '#d9d6cb' },
  { id: 'seaside', name: 'Seaside Pier', dark: false, board: '#c9b48a' },
  { id: 'desert', name: 'Pyramids', dark: false, board: '#cfa66e' },
  { id: 'jungle', name: 'Greenhouse', dark: false, board: '#8e8a62' },
  { id: 'gothic', name: 'Moonlit Graveyard', dark: true, board: '#5a5560' },
  { id: 'space', name: 'Moon Landing', dark: true, board: '#9a978e' },
] as const

export const BACKDROP_COUNT = CIRCUS_BACKDROPS.length
export const backdropInfo = (id: string): CircusBackdropInfo =>
  CIRCUS_BACKDROPS.find(b => b.id === id) ?? CIRCUS_BACKDROPS[0]!
export const isBackdropId = (v: unknown): v is CircusBackdropId =>
  typeof v === 'string' && CIRCUS_BACKDROPS.some(b => b.id === v)

/** The backdrop a session id hashes to (before collision avoidance). */
export function hashBackdropIndex(sessionId: string): number {
  return Math.floor(hash01(sessionId || '', 77) * BACKDROP_COUNT) % BACKDROP_COUNT
}

/**
 * Hands out backdrops so that the sessions open together look as different as
 * possible. A session starts at the backdrop its id hashes to and moves on to
 * the next one nobody else open uses (or the least used when more than ten are
 * open). Once given, a session keeps its backdrop for as long as the assigner
 * lives; `snapshot`/`restore` let the caller persist that across reloads.
 */
export function createBackdropAssigner(restore?: Record<string, number>) {
  const given = new Map<string, number>()
  if (restore) {
    for (const [k, v] of Object.entries(restore)) {
      if (Number.isInteger(v) && v >= 0 && v < BACKDROP_COUNT) given.set(k, v)
    }
  }
  /** `openIds` are the sessions known right now (including `id`). */
  function assign(id: string, openIds: readonly string[] = []): number {
    const have = given.get(id)
    if (have !== undefined) return have
    const open = new Set(openIds.filter(x => x && x !== id))
    const used = new Array(BACKDROP_COUNT).fill(0)
    for (const o of open) {
      const g = given.get(o)
      if (g !== undefined) used[g]++
    }
    const start = hashBackdropIndex(id)
    let best = start
    let bestUse = Infinity
    for (let k = 0; k < BACKDROP_COUNT; k++) {
      const i = (start + k) % BACKDROP_COUNT
      if (used[i] < bestUse) { bestUse = used[i]; best = i }
      if (bestUse === 0) break
    }
    given.set(id, best)
    return best
  }
  /** Give every open session a backdrop in a stable order (by hash, then id), so a fresh start spreads evenly. */
  function assignAll(openIds: readonly string[]): void {
    const order = [...openIds].filter(Boolean).sort((a, b) => hashBackdropIndex(a) - hashBackdropIndex(b) || (a < b ? -1 : 1))
    for (const id of order) assign(id, openIds)
  }
  return {
    assign,
    assignAll,
    snapshot: (): Record<string, number> => Object.fromEntries(given),
    forget: (id: string) => { given.delete(id) },
  }
}

// ── Engraved cut-outs ───────────────────────────────────────────────────────

export const BACKDROP_ART_BASE = '/zen/circus/backdrops/'

/** Pixel size of every cut-out in public/zen/circus/backdrops (keeps the aspect ratio exact). */
export const BACKDROP_ART: Record<string, readonly [number, number]> = {
  'bats': [444, 339], 'camel': [584, 471], 'castle': [491, 545], 'church': [476, 545], 'clock': [203, 585],
  'cloud': [608, 461], 'crescent': [472, 545], 'crescent-sage': [507, 585], 'deadtree': [477, 539],
  'fern': [410, 585], 'gulls': [484, 441], 'lighthouse': [342, 585], 'lighthouse-slate': [319, 545],
  'moonface': [544, 545], 'palm': [368, 545], 'pier': [724, 468], 'pines': [382, 585], 'plant': [431, 545],
  'portrait': [296, 445], 'pyramids': [724, 409], 'rocket': [217, 585], 'saturn': [584, 379], 'ship': [664, 658],
  'stormcloud': [539, 629], 'sun-ochre': [664, 660], 'sun-pale': [584, 580], 'teapot': [584, 409],
  'tombstones': [505, 545],
}

interface CutOpts { cls?: string; flip?: boolean; opacity?: number }

/** An engraved cut-out, `w` wide, centred on `cx`, standing on `bottom`. */
function cut(name: string, cx: number, bottom: number, w: number, o: CutOpts = {}): string {
  const [iw, ih] = BACKDROP_ART[name]!
  const h = Math.round((w * ih) / iw)
  const x = Math.round(cx - w / 2)
  const y = bottom - h
  const op = o.opacity !== undefined ? ` opacity="${o.opacity}"` : ''
  let el = `<image href="${BACKDROP_ART_BASE}${name}.webp" x="${x}" y="${y}" width="${w}" height="${h}" preserveAspectRatio="none"${op}/>`
  if (o.flip) el = `<g transform="translate(${cx * 2} 0) scale(-1 1)">${el}</g>`
  return o.cls ? `<g class="${o.cls}">${el}</g>` : el
}

// ── Engraving toolkit (used inside plates) ──────────────────────────────────

const rnd = (seed: number, i: number | string) => hash01(String(i), seed)
const f1 = (n: number) => Math.round(n * 10) / 10

/** Shared engraving patterns, defined once per plate. */
function plateDefs(ink = INK): string {
  return `<defs>
  <pattern id="h1" width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(38)"><path d="M0 0 V6" stroke="${ink}" stroke-width="0.9"/></pattern>
  <pattern id="h2" width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(-30)"><path d="M0 0 V6 M0 0 H6" stroke="${ink}" stroke-width="0.8"/></pattern>
  <pattern id="sky" width="520" height="9" patternUnits="userSpaceOnUse"><path d="M0 2 Q130 1.2 260 2.2 T520 2" fill="none" stroke="${ink}" stroke-width="0.75"/><path d="M0 6.5 Q120 7.3 250 6.4 T520 6.5" fill="none" stroke="${ink}" stroke-width="0.45"/></pattern>
  <pattern id="stip" width="23" height="19" patternUnits="userSpaceOnUse"><g fill="${ink}"><circle cx="3" cy="4" r="0.9"/><circle cx="14" cy="2" r="0.7"/><circle cx="9" cy="11" r="1"/><circle cx="19" cy="9" r="0.8"/><circle cx="4" cy="16" r="0.7"/><circle cx="16" cy="16" r="0.9"/></g></pattern>
  <pattern id="water" width="90" height="13" patternUnits="userSpaceOnUse"><path d="M2 6 q9 -4 18 0 t18 0 M48 11 q8 -3 16 0 t16 0" fill="none" stroke="${ink}" stroke-width="1.1"/><path d="M40 3 q6 -2 12 0" fill="none" stroke="${ink}" stroke-width="0.6"/></pattern>
  <pattern id="wood" width="260" height="22" patternUnits="userSpaceOnUse"><path d="M0 6 C60 3 90 10 150 6 S230 3 260 6 M0 15 C40 18 110 12 170 16 S240 18 260 15" fill="none" stroke="${ink}" stroke-width="0.9"/><ellipse cx="118" cy="11" rx="9" ry="3" fill="none" stroke="${ink}" stroke-width="0.8"/></pattern>
  <linearGradient id="fade" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff"/><stop offset="1" stop-color="#fff" stop-opacity="0.15"/></linearGradient>
  <mask id="skym"><rect width="1600" height="900" fill="url(#fade)"/></mask>
  <radialGradient id="fox"><stop offset="0" stop-color="#8a5a2a" stop-opacity="0.32"/><stop offset="0.6" stop-color="#8a5a2a" stop-opacity="0.12"/><stop offset="1" stop-color="#8a5a2a" stop-opacity="0"/></radialGradient>
</defs>`
}

/** Aged paper with an uneven watercolour wash, engraved line sky and foxing. */
function skyPlate(seed: number, paper: string, wash: string, top: string, lines: number, dark = false): string {
  const blot = (c: string, n: number, y0: number, y1: number, o: number) => Array.from({ length: n }, (_, i) => {
    const cx = f1(rnd(seed, `bx${c}${i}`) * 1700 - 50)
    const cy = f1(y0 + rnd(seed, `by${c}${i}`) * (y1 - y0))
    const rx = f1(260 + rnd(seed, `br${c}${i}`) * 420)
    return `<ellipse cx="${cx}" cy="${cy}" rx="${rx}" ry="${f1(rx * 0.45)}" fill="url(#w${c})" opacity="${o}"/>`
  }).join('')
  const fox = Array.from({ length: 9 }, (_, i) =>
    `<circle cx="${f1(rnd(seed, `fx${i}`) * 1600)}" cy="${f1(rnd(seed, `fy${i}`) * 700)}" r="${f1(8 + rnd(seed, `fr${i}`) * 34)}" fill="url(#fox)"/>`).join('')
  return `<defs>
    <radialGradient id="wa"><stop offset="0" stop-color="${wash}"/><stop offset="1" stop-color="${wash}" stop-opacity="0"/></radialGradient>
    <radialGradient id="wb"><stop offset="0" stop-color="${top}"/><stop offset="1" stop-color="${top}" stop-opacity="0"/></radialGradient></defs>
  <rect width="1600" height="900" fill="${paper}"/>
  ${blot('a', 7, 120, 760, 0.75)}${blot('b', 6, -80, 260, 0.85)}
  <rect width="1600" height="900" fill="url(#sky)" mask="url(#skym)" opacity="${lines}"/>
  ${dark ? '' : fox}`
}

function ridge(seed: number, y0: number, amp: number, wl: number): Array<[number, number]> {
  const pts: Array<[number, number]> = []
  for (let x = -20; x <= 1620; x += 9) {
    const t = x / wl
    const y = y0 - amp * (0.6 * Math.sin(t + seed) + 0.28 * Math.sin(t * 2.3 + seed * 1.7) + 0.12 * Math.sin(t * 5.1 + seed * 2.9))
      + (rnd(seed, x) - 0.5) * 3.4
    pts.push([x, f1(y)])
  }
  return pts
}
const edgeD = (pts: Array<[number, number]>, dy = 0) => 'M' + pts.map(([x, y]) => `${x} ${f1(y + dy)}`).join(' L')
const fillD = (pts: Array<[number, number]>, dy = 0) => `${edgeD(pts, dy)} L1620 920 L-20 920 Z`

interface StripOpts { hatch?: 'h1' | 'h2' | 'stip' | 'water'; hatchOp?: number; shade?: number; ink?: string }

/** A torn-paper strip of land (or sea): drop shadow, cream cut edge, wash, hatching, inked edge of varying weight. */
function strip(seed: number, y: number, amp: number, wl: number, fill: string, o: StripOpts = {}): string {
  const pts = ridge(seed, y, amp, wl)
  const d = fillD(pts)
  const e = edgeD(pts)
  const ink = o.ink ?? INK
  const dash = `${30 + Math.round(rnd(seed, 'd1') * 50)} ${20 + Math.round(rnd(seed, 'd2') * 40)} ${8 + Math.round(rnd(seed, 'd3') * 20)} ${50 + Math.round(rnd(seed, 'd4') * 60)}`
  return `<path d="${d}" transform="translate(5 7)" fill="${INK}" opacity="0.26"/>
  <path d="${d}" fill="${CREAM}" stroke="${CREAM}" stroke-width="9" stroke-linejoin="round"/>
  <path d="${d}" fill="${fill}"/>
  <path d="${d}" fill="url(#${o.hatch ?? 'h1'})" opacity="${o.hatchOp ?? 0.32}"/>
  ${contours(pts, ink)}
  ${o.shade ? `<path d="${fillD(pts, 34)}" fill="url(#h2)" opacity="${o.shade}"/><path d="${fillD(pts, 80)}" fill="url(#h2)" opacity="${o.shade}"/>` : ''}
  <path d="${e}" fill="none" stroke="${ink}" stroke-width="1.2" stroke-linejoin="round"/>
  <path d="${e}" fill="none" stroke="${ink}" stroke-width="3" stroke-dasharray="${dash}" opacity="0.6"/>`
}

/** Engraver's contour lines: the ridge repeated below itself, thinning and fading. */
function contours(pts: Array<[number, number]>, ink: string): string {
  return Array.from({ length: 7 }, (_, k) =>
    `<path d="${edgeD(pts, 6 + k * 7 + k * k * 0.8)}" fill="none" stroke="${ink}" stroke-width="${f1(1 - k * 0.1)}" opacity="${f1(0.55 - k * 0.07)}"/>`).join('')
}

/** The stage: worn boards with wood grain, perspective joints of uneven weight, footlight shells. */
function boardsPlate(board: string): string {
  const joints = Array.from({ length: 17 }, (_, i) =>
    `<path d="M${i * 100} 756 L${f1((i * 100 - 800) * 1.35 + 800)} 900" stroke="${INK}" stroke-width="${f1(0.8 + rnd(3, i) * 1.4)}" opacity="${f1(0.35 + rnd(4, i) * 0.3)}"/>`).join('')
  const shells = Array.from({ length: 9 }, (_, i) => {
    const x = 120 + i * 170
    return `<path d="M${x} 900 q14 -28 30 0 Z" fill="${CREAM}" stroke="${INK}" stroke-width="1.6"/><path d="M${x + 6} 900 l9 -18 M${x + 15} 900 v-21 M${x + 24} 900 l-9 -18" stroke="${INK}" stroke-width="0.7" opacity="0.6"/>`
  }).join('')
  return `<path d="M0 756 H1600 V900 H0 Z" fill="${board}"/>
  <path d="M0 756 H1600 V900 H0 Z" fill="url(#wood)" opacity="0.38"/>
  <path d="M0 756 H1600 V900 H0 Z" fill="url(#h1)" opacity="0.14"/>
  <path d="M0 756 H1600 V772 H0 Z" fill="${INK}" opacity="0.22"/>
  ${joints}
  <path d="M0 800 L1600 800" stroke="${INK}" stroke-width="1.6" opacity="0.4"/><path d="M0 852 L1600 852" stroke="${INK}" stroke-width="1" opacity="0.3"/>
  <path d="M0 756 L1600 756" stroke="${INK}" stroke-width="2.6"/>
  ${shells}`
}

/** Wrap static engraving into a self-contained SVG image (painted once, cached as a bitmap). */
function plate(inner: string, ink = INK): string {
  const doc = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1600 900" width="1600" height="900">${plateDefs(ink)}${inner}</svg>`
  return `<image class="cc-plate" href="data:image/svg+xml;charset=utf-8,${encodeURIComponent(doc)}" x="0" y="0" width="1600" height="900" preserveAspectRatio="none"/>`
}

/** Inline (live) patterns for the few things that move: rain and snow. */
const LIVE_DEFS = `<defs>
  <pattern id="cc-rain" width="46" height="46" patternUnits="userSpaceOnUse" patternTransform="rotate(12)"><path d="M8 2 V22 M30 20 V44 M20 30 V36" stroke="${INK}" stroke-width="1" opacity="0.55"/><path d="M9 2 V22 M31 20 V44" stroke="#e9e3d2" stroke-width="0.8" opacity="0.5"/></pattern>
  <pattern id="cc-flakes" width="120" height="120" patternUnits="userSpaceOnUse"><g fill="#f6f1e4" stroke="${INK}" stroke-width="0.9"><circle cx="14" cy="18" r="3.6"/><circle cx="70" cy="40" r="2.8"/><circle cx="104" cy="10" r="2.4"/><circle cx="40" cy="74" r="3.2"/><circle cx="92" cy="96" r="3.8"/><circle cx="10" cy="108" r="2.4"/><circle cx="55" cy="8" r="1.6"/><circle cx="31" cy="44" r="1.8"/><circle cx="84" cy="66" r="1.5"/><circle cx="114" cy="58" r="2"/><circle cx="62" cy="112" r="1.4"/></g></pattern>
</defs>`

/** Engraved wave crests (live, they bob on the beat). */
function crests(seed: number, y0: number, y1: number, n: number): string {
  return Array.from({ length: n }, (_, i) => {
    const x = f1(rnd(seed, `cx${i}`) * 1500 + 40)
    const y = f1(y0 + rnd(seed, `cy${i}`) * (y1 - y0))
    const w = f1(30 + rnd(seed, `cw${i}`) * 50)
    return `<path d="M${x} ${y} q${f1(w / 4)} -${f1(w / 7)} ${f1(w / 2)} 0 q${f1(w / 4)} -${f1(w / 9)} ${f1(w / 2)} 0" fill="none" stroke="${INK}" stroke-width="${f1(0.8 + rnd(seed, `cs${i}`) * 1.6)}" stroke-linecap="round"/>`
  }).join('')
}

function starsPlate(seed: number, n: number, maxY: number, color: string): string {
  return Array.from({ length: n }, (_, i) => {
    const x = f1(rnd(seed, `s${i}`) * 1560 + 20)
    const y = f1(rnd(seed, `t${i}`) * maxY + 10)
    const r = f1(0.8 + rnd(seed, `r${i}`) * 1.8)
    const big = rnd(seed, `b${i}`) > 0.86
    return big
      ? `<path d="M${x} ${f1(y - r * 4)} L${f1(x + r * 0.7)} ${y} L${x} ${f1(y + r * 4)} L${f1(x - r * 0.7)} ${y} Z M${f1(x - r * 4)} ${y} L${x} ${f1(y - r * 0.7)} L${f1(x + r * 4)} ${y} L${x} ${f1(y + r * 0.7)} Z" fill="${color}"/>`
      : `<circle cx="${x}" cy="${y}" r="${r}" fill="${color}"/>`
  }).join('')
}

// ── The ten scenes ──────────────────────────────────────────────────────────

const BACKDROPS: Record<CircusBackdropId, (board: string) => string> = {
  // 1. Dusty teal sky, sage hills with a castle, ochre fields, engraved sun
  meadow: board => `
  ${plate(`${skyPlate(11, '#e9e0c7', '#9fbcae', '#86a99c', 0.5)}
    ${strip(1.3, 590, 46, 260, '#9fb5a0', { hatchOp: 0.3 })}`)}
  ${cut('castle', 1120, 610, 250)}
  ${cut('sun-pale', 1330, 290, 210, { cls: 'cc-b-pulse cc-sun-rays' })}
  ${plate(`${strip(4.1, 690, 32, 210, '#cdb071', { hatchOp: 0.34, shade: 0.12 })}${boardsPlate(board)}`)}`,

  // 2. Rust-ochre sky, a huge engraved sun sinking behind plum hills, a ship on the estuary, crescent moon
  dusk: board => `
  ${plate(`${skyPlate(22, '#e7cba0', '#d79a68', '#8a5a6a', 0.55)}`)}
  ${cut('crescent', 300, 300, 170, { cls: 'cc-b-off' })}
  ${cut('sun-ochre', 1000, 790, 600, { cls: 'cc-b-half' })}
  ${plate(`${strip(2.2, 600, 40, 230, '#8e6577', { hatchOp: 0.4, shade: 0.14 })}
    ${strip(7.7, 668, 6, 300, '#b88a6a', { hatch: 'water', hatchOp: 0.45 })}`)}
  ${cut('ship', 1340, 735, 270, { cls: 'cc-b-off' })}
  ${plate(`${strip(5.5, 728, 14, 180, '#7a5560', { hatchOp: 0.42, shade: 0.16 })}${boardsPlate(board)}`)}`,

  // 3. Slate night, engraved thunderclouds, a lighthouse on the headland, rain and lightning
  storm: board => `
  ${plate(`${skyPlate(33, '#8e95a0', '#6d7888', '#4b5466', 0.75, true)}
    <rect width="1600" height="900" fill="url(#h2)" opacity="0.18"/>
    ${strip(3.3, 620, 26, 240, '#59626e', { hatchOp: 0.45, shade: 0.2 })}`)}
  ${cut('lighthouse-slate', 1390, 640, 170)}
  <g class="cc-b-flicker"><path d="M1384 368 L880 300 L880 450 Z" fill="#f1e6c4" opacity="0.5"/><path d="M1384 368 L880 300 M1384 368 L880 450" stroke="${INK}" stroke-width="1" opacity="0.5"/></g>
  ${cut('stormcloud', 420, 430, 360, { cls: 'cc-b-half' })}
  ${cut('stormcloud', 1190, 330, 320, { cls: 'cc-b-off', flip: true })}
  <g class="cc-b-flash"><rect width="1600" height="900" fill="#efe6cc" opacity="0.28"/><path d="M1180 140 L1120 290 L1168 290 L1100 470 L1220 260 L1172 260 L1230 140 Z" fill="#f6efd6" stroke="${INK}" stroke-width="2.4" stroke-linejoin="round"/></g>
  ${plate(`${strip(6.6, 700, 10, 160, '#4f5964', { hatch: 'water', hatchOp: 0.6, shade: 0.2 })}${boardsPlate(board)}`)}
  ${LIVE_DEFS}<rect class="cc-b-fall" x="-80" y="-80" width="1760" height="1060" fill="url(#cc-rain)"/>`,

  // 4. Faded damask wallpaper, panelled wainscot, a grandfather clock, a swinging walrus portrait, tea
  tearoom: board => `
  ${plate(`<defs><pattern id="damask" width="120" height="150" patternUnits="userSpaceOnUse">
      <path d="M60 14 C82 40 92 58 60 75 C28 58 38 40 60 14 Z M60 75 C80 92 90 120 60 136 C30 120 40 92 60 75 Z" fill="none" stroke="${INK}" stroke-width="1.1"/>
      <path d="M60 30 C70 44 72 54 60 62 C48 54 50 44 60 30 Z" fill="url(#h1)" stroke="${INK}" stroke-width="0.7"/>
      <path d="M0 75 q14 -10 22 0 q-8 10 -22 0 M120 75 q-14 -10 -22 0 q8 10 22 0" fill="none" stroke="${INK}" stroke-width="0.9"/>
      <path d="M0 0 V150 M120 0 V150" stroke="${INK}" stroke-width="0.5" opacity="0.6"/></pattern></defs>
    ${skyPlate(44, '#c9a69a', '#a8747a', '#8a5a62', 0.12)}
    <rect width="1600" height="560" fill="url(#damask)" opacity="0.42"/>
    <rect y="520" width="1600" height="236" fill="#8a6448"/>
    <rect y="520" width="1600" height="236" fill="url(#wood)" opacity="0.4"/>
    ${Array.from({ length: 8 }, (_, i) => `<rect x="${60 + i * 195}" y="560" width="150" height="170" fill="url(#h1)" opacity="0.22" stroke="${INK}" stroke-width="${f1(1.2 + rnd(5, i) * 1.4)}"/><path d="M${64 + i * 195} 726 V564 H${206 + i * 195}" fill="none" stroke="${CREAM}" stroke-width="1.4" opacity="0.5"/>`).join('')}
    <path d="M0 512 H1600 V530 H0 Z" fill="#b48a52" stroke="${INK}" stroke-width="1.6"/><path d="M0 524 H1600" stroke="${INK}" stroke-width="0.8"/>
    <g transform="translate(150 610)"><path d="M-100 0 H100 V14 H-100 Z" fill="#9a7048" stroke="${INK}" stroke-width="1.6"/><path d="M-100 0 H100 V14 H-100 Z" fill="url(#wood)" opacity="0.5"/>
      <path d="M-80 14 C-84 60 -70 110 -88 146 M80 14 C84 60 70 110 88 146 M0 14 V146" fill="none" stroke="${INK}" stroke-width="7" stroke-linecap="round"/>
      <path d="M-80 14 C-84 60 -70 110 -88 146 M80 14 C84 60 70 110 88 146 M0 14 V146" fill="none" stroke="#9a7048" stroke-width="4" stroke-linecap="round"/></g>
    ${boardsPlate(board)}`)}
  ${cut('teapot', 190, 616, 210)}
  ${cut('clock', 1330, 762, 190, { cls: 'cc-b-pulse' })}
  <g class="cc-b-swing"><path d="M1010 92 L962 172 M1010 92 L1058 172" stroke="${INK}" stroke-width="1.6" fill="none"/><circle cx="1010" cy="92" r="5" fill="#b48a52" stroke="${INK}" stroke-width="1.4"/>${cut('portrait', 1010, 440, 200)}</g>`,

  // 5. Bone-blue winter sky, a snowy church between pines, falling snow
  snow: board => `
  ${plate(`${skyPlate(55, '#e3e1d6', '#b9c3c4', '#9fabb2', 0.42)}
    ${strip(5.1, 590, 34, 250, '#e8e4d8', { hatch: 'stip', hatchOp: 0.5 })}`)}
  ${cut('sun-pale', 1270, 290, 170, { cls: 'cc-b-pulse cc-sun-rays', opacity: 0.85 })}
  ${cut('church', 1080, 680, 280)}
  ${cut('pines', 1420, 740, 250)}
  ${cut('pines', 170, 730, 210, { flip: true })}
  ${plate(`${strip(8.4, 700, 18, 190, '#f0ece1', { hatch: 'stip', hatchOp: 0.4, shade: 0.06 })}${boardsPlate(board)}`)}
  ${LIVE_DEFS}<rect class="cc-b-fall" x="-120" y="-120" width="1840" height="1140" fill="url(#cc-flakes)"/>`,

  // 6. Slate-teal sky, engraved sea, a long pier, a lighthouse and gulls
  seaside: board => `
  ${plate(`${skyPlate(66, '#e4e0cf', '#a9bdbc', '#8aa3aa', 0.5)}
    ${strip(6.2, 520, 30, 200, '#9aa58f', { hatchOp: 0.35 })}
    ${strip(9.1, 548, 2, 400, '#7e9aa0', { hatch: 'water', hatchOp: 0.5, shade: 0.08 })}`)}
  ${cut('sun-pale', 1080, 300, 190, { cls: 'cc-b-pulse cc-sun-rays' })}
  ${cut('gulls', 640, 420, 220, { cls: 'cc-b-off' })}
  <g class="cc-b-half">${crests(66, 570, 740, 26)}</g>
  ${cut('pier', 1010, 715, 560)}
  ${cut('lighthouse', 1440, 720, 180)}
  <g class="cc-b-flicker"><path d="M1460 425 L1580 380 L1580 480 Z" fill="#f4ead0" opacity="0.6"/></g>
  ${plate(`${strip(2.8, 740, 8, 260, '#d4c39a', { hatch: 'stip', hatchOp: 0.45 })}${boardsPlate(board)}`)}`,

  // 7. Ochre haze, the pyramids, a palm and a camel in a bowler hat (well clear of the egg)
  desert: board => `
  ${plate(`${skyPlate(77, '#ecdcb6', '#dcb57c', '#c58f62', 0.45)}
    ${strip(7.3, 630, 18, 280, '#d8b77e', { hatch: 'stip', hatchOp: 0.45 })}`)}
  ${cut('sun-pale', 1240, 290, 220, { cls: 'cc-b-pulse cc-sun-rays' })}
  ${cut('pyramids', 950, 660, 560)}
  ${cut('palm', 170, 742, 200)}
  ${plate(`${strip(3.9, 712, 22, 230, '#c99b62', { hatch: 'stip', hatchOp: 0.5, shade: 0.12 })}${boardsPlate(board)}`)}
  ${cut('camel', 1390, 772, 300, { cls: 'cc-b-half' })}`,

  // 8. Olive glasshouse: iron ribs, hatched glass, shafts of light, ferns and a hungry plant
  jungle: board => `
  ${plate(`${skyPlate(88, '#dfdcc0', '#a7ad7e', '#8c9466', 0.45)}
    <g fill="none" stroke="${INK}">
      ${Array.from({ length: 9 }, (_, i) => `<path d="M${i * 200} 760 V250 Q${i * 200 + 100} 120 ${i * 200 + 200} 250" stroke-width="${i % 2 ? 2.2 : 3.6}"/>`).join('')}
      <path d="M0 250 H1600 M0 470 H1600" stroke-width="2"/>
      ${Array.from({ length: 16 }, (_, i) => `<path d="M${i * 100 + 50} 250 V760" stroke-width="0.8" opacity="0.7"/>`).join('')}
    </g>
    <rect y="250" width="1600" height="510" fill="url(#h1)" opacity="0.12"/>
    ${strip(8.8, 700, 14, 200, '#6f7a4c', { hatchOp: 0.4, shade: 0.14 })}`)}
  <g class="cc-b-off" opacity="0.32"><path d="M520 0 L700 0 L580 760 L320 760 Z M1000 0 L1120 0 L1070 760 L860 760 Z" fill="#f6eed0"/></g>
  ${cut('plant', 1130, 742, 260, { cls: 'cc-b-pulse' })}
  ${cut('fern', 1440, 770, 300, { cls: 'cc-b-half', flip: true })}
  ${cut('fern', 140, 770, 250)}
  ${plate(boardsPlate(board))}`,

  // 9. Plum night, a melancholy moon in a nightcap, bats, tombstones and a dead tree
  gothic: board => `
  ${plate(`${skyPlate(99, '#7a6f80', '#5f5470', '#3f3650', 0.8, true)}
    <rect width="1600" height="900" fill="url(#h2)" opacity="0.2"/>
    ${starsPlate(9, 30, 300, '#e9dfc4')}
    ${strip(9.9, 640, 28, 260, '#4c4656', { hatchOp: 0.5, shade: 0.22 })}`)}
  ${cut('moonface', 1130, 390, 280, { cls: 'cc-b-half' })}
  ${cut('bats', 760, 380, 230, { cls: 'cc-b-pulse' })}
  ${cut('tombstones', 920, 735, 300)}
  ${cut('deadtree', 1420, 762, 340)}
  ${plate(`${strip(1.9, 730, 10, 220, '#433f4c', { hatchOp: 0.5, shade: 0.2 })}${boardsPlate(board)}`)}`,

  // 10. Engraved night sky with stipple stars, a ringed planet, the crescent moon, a riveted rocket on bone-grey dust
  space: board => `
  ${plate(`${skyPlate(10, '#55566a', '#454863', '#2f3048', 0.7, true)}
    <rect width="1600" height="900" fill="url(#h2)" opacity="0.22"/>`)}
  <g class="cc-b-off">${plate(starsPlate(3, 70, 560, '#efe6cc'))}</g>
  ${cut('saturn', 330, 330, 320, { cls: 'cc-b-pulse' })}
  ${cut('crescent-sage', 1180, 330, 170, { cls: 'cc-b-half' })}
  ${plate(`${strip(4.4, 690, 22, 240, '#b9b4a6', { hatch: 'stip', hatchOp: 0.6, shade: 0.14 })}
    ${[[260, 735, 70], [900, 722, 90], [1180, 745, 60]].map(([x, y, r]) => `<ellipse cx="${x}" cy="${y}" rx="${r}" ry="${f1(r! * 0.2)}" fill="#9a958a" stroke="${INK}" stroke-width="1.4"/><ellipse cx="${x}" cy="${y! - 2}" rx="${r}" ry="${f1(r! * 0.16)}" fill="url(#h2)" opacity="0.5"/>`).join('')}
    <path d="M700 700 V560" stroke="${INK}" stroke-width="3"/><path d="M700 560 H780 L768 578 L780 596 H700 Z" fill="#c9a07a" stroke="${INK}" stroke-width="1.6"/><path d="M700 560 H780 L768 578 L780 596 H700 Z" fill="url(#h1)" opacity="0.4"/>
    ${boardsPlate(board)}`)}
  ${cut('rocket', 1380, 770, 140, { cls: 'cc-b-half' })}`,
}

/** The whole backdrop: plates (static, cached), cut-outs and the few beat-hooked layers. */
export function backdropSvg(id: CircusBackdropId): string {
  const info = backdropInfo(id)
  return `<svg class="cc-backdrop" data-board="${info.board}" viewBox="0 0 1600 900" preserveAspectRatio="xMidYMax slice" aria-hidden="true">${BACKDROPS[info.id](info.board)}</svg>`
}

/** The cut-out files a backdrop uses (for tests and preloading). */
export function backdropArt(id: CircusBackdropId): string[] {
  const svg = backdropSvg(id)
  return [...new Set([...svg.matchAll(/backdrops\/([a-z-]+)\.webp/g)].map(m => m[1]!))]
}
