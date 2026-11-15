import { describe, expect, it, vi } from 'vitest'
import { playFade, playSlide, playZoom, transformCss, usableRect, zoomTransform } from '~/utils/rectTransition'

describe('zoomTransform', () => {
  it('maps the final box onto the origin row', () => {
    const row = { left: 900, top: 60, width: 300, height: 40 }
    const modal = { left: 300, top: 100, width: 600, height: 800 }
    const t = zoomTransform(row, modal)
    expect(t).toEqual({ x: 600, y: -40, scaleX: 0.5, scaleY: 0.05 })
    // Applied with transform-origin 0 0, the modal's corners land on the row's.
    expect(modal.left + t.x).toBe(row.left)
    expect(modal.top + t.y).toBe(row.top)
    expect(modal.width * t.scaleX).toBe(row.width)
    expect(modal.height * t.scaleY).toBe(row.height)
    expect(transformCss(t)).toBe('translate(600px, -40px) scale(0.5, 0.05)')
  })

  it('never collapses to zero or flips', () => {
    const t = zoomTransform({ left: 0, top: 0, width: 0, height: 0 }, { left: 10, top: 10, width: 0, height: 100 })
    expect(t.scaleX).toBe(1)
    expect(t.scaleY).toBeGreaterThan(0)
  })

  it('zooms only from rects that are on screen and have a size', () => {
    const viewport = { width: 1000, height: 800 }
    expect(usableRect({ left: 10, top: 10, width: 100, height: 20 }, viewport)).toBe(true)
    expect(usableRect({ left: 10, top: 10, width: 0, height: 20 }, viewport)).toBe(false)
    expect(usableRect({ left: 10, top: 900, width: 100, height: 20 }, viewport)).toBe(false)
    expect(usableRect({ left: -200, top: 10, width: 100, height: 20 }, viewport)).toBe(false)
    expect(usableRect(null, viewport)).toBe(false)
  })
})

describe('playing', () => {
  const box = () => {
    const el = document.createElement('div')
    el.getBoundingClientRect = () => ({ left: 300, top: 100, width: 600, height: 800 }) as DOMRect
    return el
  }

  it('finishes at once without element.animate', () => {
    const done = vi.fn()
    const plain = () => Object.assign(box(), { animate: undefined })
    playZoom(plain(), { left: 0, top: 0, width: 10, height: 10 }, 'in', done)
    playFade(plain(), 'out', done)
    expect(done).toHaveBeenCalledTimes(2)
  })

  it('animates transform and opacity only, in and back out', () => {
    const el = box()
    const animate = vi.fn(() => ({ cancel: vi.fn(), onfinish: null, oncancel: null }))
    ;(el as any).animate = animate
    playZoom(el, { left: 900, top: 60, width: 300, height: 40 }, 'in', () => {})
    playZoom(el, { left: 900, top: 60, width: 300, height: 40 }, 'out', () => {})
    const [inFrames, inOpts] = animate.mock.calls[0] as unknown as [Keyframe[], KeyframeAnimationOptions]
    const [outFrames] = animate.mock.calls[1] as unknown as [Keyframe[]]
    expect(inFrames[0]!.transform).toBe('translate(600px, -40px) scale(0.5, 0.05)')
    expect(inFrames.at(-1)).toEqual({ transform: 'none', opacity: 1 })
    expect(outFrames.at(-1)).toMatchObject({ transform: 'translate(600px, -40px) scale(0.5, 0.05)', opacity: 0 })
    expect(inOpts.duration).toBeGreaterThanOrEqual(220)
    expect(inOpts.duration).toBeLessThanOrEqual(280)
    for (const frame of [...inFrames, ...outFrames]) {
      expect(Object.keys(frame).every(k => ['transform', 'opacity', 'offset'].includes(k))).toBe(true)
    }
  })

  it('slides with the direction of travel, and not at all with reduced motion', () => {
    const el = box()
    const animate = vi.fn(() => ({ cancel: vi.fn(), onfinish: null, oncancel: null }))
    ;(el as any).animate = animate
    playSlide(el, 1, 'in', () => {}, false)
    playSlide(el, 1, 'out', () => {}, false)
    playSlide(el, -1, 'in', () => {}, false)
    const first = (i: number) => (animate.mock.calls[i] as unknown as [Keyframe[]])[0]
    expect(first(0)[0]!.transform).toBe('translateX(24px)') // next comes in from the right
    expect(first(1).at(-1)!.transform).toBe('translateX(-24px)') // and the old one leaves left
    expect(first(2)[0]!.transform).toBe('translateX(-24px)')

    const done = vi.fn()
    animate.mockClear()
    playSlide(el, 1, 'in', done, true)
    expect(animate).not.toHaveBeenCalled()
    expect(done).toHaveBeenCalled()
  })
})
