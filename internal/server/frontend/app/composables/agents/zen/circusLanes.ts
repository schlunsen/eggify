/**
 * Keeps the Circus stage and the HUD from fighting: pure geometry for choosing
 * where a prop may cross or settle so it stays clear of the HUD cards, chips and
 * name plate. No DOM here (the scene measures the HUD and passes boxes in), so it
 * can be unit tested.
 */

/** A rectangle in stage coordinates (px from the stage's top-left). */
export interface Box { x: number; y: number; w: number; h: number }

/** Selectors of the HUD pieces a stage prop must not cover (looked up inside `.zen-mode`). */
export const HUD_SELECTORS = [
  '.zen-plate', '.zen-status-badge', '.zen-agents-summary', '.zen-last-message',
  '.zen-metrics', '.zen-top-right', '.zen-top-center', '.zen-bottom-right-controls',
  '.zen-feed', '.zen-response-panel',
] as const

/** Breathing room kept around every HUD box (px). */
export const HUD_PAD = 14

export function boxesOverlap(a: Box, b: Box): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h
}

export function padBox(b: Box, pad: number): Box {
  return { x: b.x - pad, y: b.y - pad, w: b.w + pad * 2, h: b.h + pad * 2 }
}

export const boxFree = (b: Box, hud: readonly Box[]) => !hud.some(h => boxesOverlap(b, h))

/** First candidate that touches no HUD box, or null when none is free (the gag is skipped). */
export function pickSpot(candidates: readonly Box[], hud: readonly Box[]): Box | null {
  return candidates.find(c => boxFree(c, hud)) ?? null
}

/** Fisher-Yates with an injectable random, so tests are deterministic. */
export function shuffled<T>(items: readonly T[], rand: () => number): T[] {
  const a = items.slice()
  for (let i = a.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1))
    ;[a[i], a[j]] = [a[j]!, a[i]!]
  }
  return a
}

/**
 * Full-width horizontal bands (a prop that crosses the whole stage) at heights
 * between `minY` and `maxY`, in random order; `margin` is room for bobbing above/below.
 */
export function laneCandidates(W: number, minY: number, maxY: number, h: number, margin: number, rand: () => number, steps = 12): Box[] {
  const ys: number[] = []
  for (let i = 0; i < steps; i++) ys.push(minY + ((maxY - minY) * i) / Math.max(1, steps - 1))
  return shuffled(ys, rand).map(y => ({ x: 0, y: y - margin, w: W, h: h + margin * 2 }))
}

/** A free crossing lane (its top y, margin removed), or null when the HUD blocks every one. */
export function pickLane(W: number, minY: number, maxY: number, h: number, margin: number, hud: readonly Box[], rand: () => number): number | null {
  const lane = pickSpot(laneCandidates(W, minY, maxY, h, margin, rand), hud)
  return lane ? lane.y + margin : null
}

/** Convert DOM rects to padded stage-space boxes (empty rects are dropped). */
export function hudBoxes(
  rects: readonly { left: number; top: number; width: number; height: number }[],
  origin: { left: number; top: number },
  pad = HUD_PAD,
): Box[] {
  return rects
    .filter(r => r.width > 0 && r.height > 0)
    .map(r => padBox({ x: r.left - origin.left, y: r.top - origin.top, w: r.width, h: r.height }, pad))
}
