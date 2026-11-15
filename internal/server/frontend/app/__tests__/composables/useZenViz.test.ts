import { describe, it, expect, beforeEach, vi } from 'vitest'

const KEY = 'zen-viz-mode'

async function freshModule() {
  vi.resetModules()
  return await import('~/composables/agents/useZenViz')
}

describe('useZenViz', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('defaults to the Circus scene when nothing is stored', async () => {
    const m = await freshModule()
    expect(m.DEFAULT_ZEN_VIZ).toBe('circus')
    expect(m.useZenViz().activeViz.value).toBe('circus')
  })

  it('lists Circus first, with its label, description and pink accent', async () => {
    const { ZEN_VIZ_MODES } = await freshModule()
    expect(ZEN_VIZ_MODES.map(m => m.id)).toEqual(['circus', 'waves', 'galaxy', 'ink'])
    const circus = ZEN_VIZ_MODES[0]!
    expect(circus.label).toBe('Circus')
    expect(circus.description).toMatch(/something completely different/i)
    expect(circus.color).toBe('#e8458b')
    expect(circus.icon.length).toBeGreaterThan(10)
  })

  it('keeps a stored valid choice', async () => {
    localStorage.setItem(KEY, 'galaxy')
    const m = await freshModule()
    expect(m.useZenViz().activeViz.value).toBe('galaxy')
  })

  it('falls back to the default for unknown stored values', async () => {
    localStorage.setItem(KEY, 'lava-lamp')
    const m = await freshModule()
    expect(m.loadZenVizFromStorage()).toBe('circus')
    expect(m.useZenViz().activeViz.value).toBe('circus')
  })

  it('falls back to the default when storage throws', async () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('denied') })
    try {
      const m = await freshModule()
      expect(m.loadZenVizFromStorage()).toBe('circus')
    } finally {
      spy.mockRestore()
    }
  })

  it('persists a new choice', async () => {
    const m = await freshModule()
    m.useZenViz().setViz('ink')
    expect(localStorage.getItem(KEY)).toBe('ink')
  })
})
