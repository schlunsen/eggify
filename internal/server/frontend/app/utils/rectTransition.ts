/**
 * "Zoom from a rect" transitions (FLIP): an element at its final place is
 * made to start out looking like another element's box (a list row), then
 * animates to where it really is, and back again on close. Transform and
 * opacity only, through the Web Animations API, so nothing re-lays out.
 *
 * The math is pure (zoomTransform); playZoom/playFade run the animations and
 * fall back to finishing at once where element.animate is missing (tests,
 * very old browsers).
 */

export interface RectLike {
  left: number
  top: number
  width: number
  height: number
}

export interface ZoomTransform {
  x: number
  y: number
  scaleX: number
  scaleY: number
}

/** How long the zoom runs, and its ease-out curve. */
export const ZOOM_MS = 240
export const ZOOM_EASING = 'cubic-bezier(0.2, 0.8, 0.2, 1)'
/** The plain fade used with reduced motion, or when the origin is gone. */
export const FADE_MS = 160
/** How long switching between items slides (out, then in). */
export const SLIDE_MS = 200

// An entering element holds its first frame until it starts, a leaving one
// its last until it is removed; neither keeps an effect once it is in place.
const fillFor = (phase: 'in' | 'out'): FillMode => (phase === 'in' ? 'backwards' : 'forwards')

/**
 * The transform (with transform-origin at the top left) that makes an element
 * whose box is `to` cover the box `from`. Scales are clamped above 0, so an
 * empty rect never yields an invisible or inverted element.
 */
export function zoomTransform(from: RectLike, to: RectLike): ZoomTransform {
  const scaleX = to.width > 0 ? Math.max(0.01, from.width / to.width) : 1
  const scaleY = to.height > 0 ? Math.max(0.01, from.height / to.height) : 1
  return { x: from.left - to.left, y: from.top - to.top, scaleX, scaleY }
}

export function transformCss(t: ZoomTransform): string {
  const r = (n: number) => Math.round(n * 1000) / 1000
  return `translate(${r(t.x)}px, ${r(t.y)}px) scale(${r(t.scaleX)}, ${r(t.scaleY)})`
}

/**
 * Whether a rect can be zoomed from: it has a size and is (at least partly)
 * on screen. A row scrolled away or hidden is not, and the modal fades instead.
 */
export function usableRect(rect: RectLike | null | undefined, viewport = currentViewport()): rect is RectLike {
  if (!rect || !(rect.width > 0) || !(rect.height > 0)) return false
  return rect.left < viewport.width && rect.top < viewport.height && rect.left + rect.width > 0 && rect.top + rect.height > 0
}

function currentViewport(): { width: number; height: number } {
  if (typeof window === 'undefined') return { width: Infinity, height: Infinity }
  return { width: window.innerWidth || Infinity, height: window.innerHeight || Infinity }
}

export function prefersReducedMotion(): boolean {
  try {
    return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  } catch {
    return false
  }
}

/** Runs keyframes on el and calls done when they end (at once without element.animate). */
function run(el: HTMLElement, keyframes: Keyframe[], options: KeyframeAnimationOptions, done: () => void): () => void {
  if (typeof el.animate !== 'function') {
    done()
    return () => {}
  }
  let finished = false
  const finish = () => { if (!finished) { finished = true; done() } }
  const animation = el.animate(keyframes, options)
  animation.onfinish = finish
  animation.oncancel = finish
  return () => animation.cancel()
}

/**
 * Zooms el between `origin` (a row's rect) and its own box. direction 'in'
 * grows it out of the origin, 'out' shrinks it back into it. Returns a cancel
 * function (cancelling also calls done).
 */
export function playZoom(el: HTMLElement, origin: RectLike, direction: 'in' | 'out', done: () => void): () => void {
  const from = transformCss(zoomTransform(origin, el.getBoundingClientRect()))
  const full: Keyframe = { transform: 'none', opacity: 1 }
  // Opacity leads the transform a little, so the stretched contents never
  // show at full strength while the frame is still row-sized.
  const frames: Keyframe[] = direction === 'in'
    ? [{ transform: from, opacity: 0.2 }, { opacity: 1, offset: 0.45 }, full]
    : [full, { opacity: 1, offset: 0.55 }, { transform: from, opacity: 0 }]
  return run(el, frames, { duration: ZOOM_MS, easing: ZOOM_EASING, fill: fillFor(direction) }, done)
}

/** A plain fade (reduced motion, or no origin to zoom from/into). */
export function playFade(el: HTMLElement, direction: 'in' | 'out', done: () => void): () => void {
  const frames: Keyframe[] = direction === 'in' ? [{ opacity: 0 }, { opacity: 1 }] : [{ opacity: 1 }, { opacity: 0 }]
  return run(el, frames, { duration: FADE_MS, easing: 'ease-out', fill: fillFor(direction) }, done)
}

/** Slides content sideways while it fades (switching items: next comes from the right). */
export function playSlide(el: HTMLElement, direction: 1 | -1, phase: 'in' | 'out', done: () => void, reduced = prefersReducedMotion()): () => void {
  // Reduced motion: switch at once.
  if (reduced) { done(); return () => {} }
  const offset = `${direction * 24}px`
  const away = `${-direction * 24}px`
  const frames: Keyframe[] = phase === 'in'
    ? [{ transform: `translateX(${offset})`, opacity: 0 }, { transform: 'none', opacity: 1 }]
    : [{ transform: 'none', opacity: 1 }, { transform: `translateX(${away})`, opacity: 0 }]
  // The old content leaves quickly, the new one takes the rest of SLIDE_MS.
  const duration = phase === 'out' ? Math.round(SLIDE_MS * 0.4) : Math.round(SLIDE_MS * 0.6)
  return run(el, frames, { duration, easing: phase === 'out' ? 'ease-in' : ZOOM_EASING, fill: fillFor(phase) }, done)
}
