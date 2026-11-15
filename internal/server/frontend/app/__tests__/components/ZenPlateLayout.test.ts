import { mount } from '@vue/test-utils'
import { describe, it, expect, afterEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import ZenNamePlate from '../../components/agents/ZenNamePlate.vue'

/**
 * The name plate sits absolutely under the bunting with a variable height; the
 * status/subagent chips and message cards are a centred column (.zen-overlay).
 * The plate reports its height, ZenMode puts it in --zen-plate-h, and the column's
 * top padding is at least plate top + plate height, so the two never overlap.
 * happy-dom has no layout, so this checks the wiring, not pixels.
 */

const zenMode = readFileSync(resolve(__dirname, '../../components/agents/ZenMode.vue'), 'utf8')
const plateSrc = readFileSync(resolve(__dirname, '../../components/agents/ZenNamePlate.vue'), 'utf8')

describe('Zen name plate / HUD column layout', () => {
  const originalRO = globalThis.ResizeObserver
  afterEach(() => {
    globalThis.ResizeObserver = originalRO
    vi.restoreAllMocks()
  })

  it('reports its height on mount, on resize, and 0 on unmount', () => {
    let height = 64
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(() => height)
    let fire: (() => void) | null = null
    let disconnected = false
    globalThis.ResizeObserver = class {
      constructor(cb: () => void) { fire = cb }
      observe() {}
      unobserve() {}
      disconnect() { disconnected = true }
    } as unknown as typeof ResizeObserver

    const w = mount(ZenNamePlate, { props: { name: 'Randy Catnaps', summary: 'A long summary' } })
    expect(w.emitted('height')?.[0]).toEqual([64])

    height = 131.4
    fire!()
    expect(w.emitted('height')?.at(-1)).toEqual([132])

    // Same height again: no duplicate event
    const count = w.emitted('height')!.length
    fire!()
    expect(w.emitted('height')!.length).toBe(count)

    w.unmount()
    expect(disconnected).toBe(true)
    expect(w.emitted('height')?.at(-1)).toEqual([0])
  })

  it('positions the plate from the shared --zen-plate-top variable', () => {
    expect(plateSrc).toMatch(/top:\s*var\(--zen-plate-top,\s*58px\)/)
  })

  it('ZenMode feeds the plate height into --zen-plate-h on the stage root', () => {
    expect(zenMode).toMatch(/<ZenNamePlate[\s\S]*?@height="plateHeight = \$event"/)
    expect(zenMode).toMatch(/'--zen-plate-h': `\$\{plateHeight\}px`/)
    expect(zenMode).toMatch(/'zen-has-plate': activeViz === 'circus' && plateHeight > 0/)
    expect(zenMode).toMatch(/--zen-plate-top:\s*58px/)
  })

  it('the chip column starts below the plate (top padding = plate top + plate height)', () => {
    const rule = zenMode.match(/\.zen-mode\.zen-has-plate \.zen-overlay\s*\{([^}]*)\}/)
    expect(rule).not.toBeNull()
    expect(rule![1]).toMatch(/padding-top:\s*max\(32px,\s*calc\(var\(--zen-plate-top\)\s*\+\s*var\(--zen-plate-h, 0px\)\s*\+\s*\d+px\)\)/)
  })

  it('status chip, subagent chip and message cards share the one .zen-overlay column', () => {
    const start = zenMode.indexOf('<div class="zen-overlay">')
    expect(start).toBeGreaterThan(-1)
    const after = zenMode.slice(start)
    const badge = after.indexOf('class="zen-status-badge"')
    const agents = after.indexOf('class="zen-agents-summary"')
    expect(badge).toBeGreaterThan(0)
    expect(agents).toBeGreaterThan(badge)
    // The plate is outside the column (absolute) and comes before it in the template
    expect(zenMode.indexOf('<ZenNamePlate')).toBeLessThan(start)
  })
})
