import { describe, it, expect } from 'vitest'
import { pickAmbient, hatFor, HATS, AMBIENT_MIN_GAP_MS, AMBIENT_MAX_ON_STAGE, AMBIENT_RULES, ambientKey } from '~/composables/agents/zen/circusGags'

describe('ambient gag scheduler', () => {
  const base = { now: 1_000_000, last: {}, lastAny: 0, onStage: 0, playing: true, backdrop: 'meadow', rand: () => 0.3 }

  it('stays quiet right after another ambient gag', () => {
    expect(pickAmbient({ ...base, lastAny: base.now - AMBIENT_MIN_GAP_MS + 1 })).toBeNull()
    expect(pickAmbient({ ...base, lastAny: base.now - AMBIENT_MIN_GAP_MS })).not.toBeNull()
  })

  it('does not add a gag to a busy stage', () => {
    expect(pickAmbient({ ...base, onStage: AMBIENT_MAX_ON_STAGE })).toBeNull()
    expect(pickAmbient({ ...base, onStage: AMBIENT_MAX_ON_STAGE - 1 })).not.toBeNull()
  })

  it("respects each gag's own cooldown", () => {
    const last: Record<string, number> = {}
    for (const r of AMBIENT_RULES) last[ambientKey(r)] = base.now - 1000
    expect(pickAmbient({ ...base, last })).toBeNull()
  })

  it('needs music for the beat gags and picks props that fit the backdrop', () => {
    const seen = new Set<string>()
    for (let i = 0; i < 200; i++) {
      const p = pickAmbient({ ...base, playing: false, backdrop: 'seaside', rand: () => i / 200 })
      if (p) seen.add(ambientKey(p))
    }
    expect(seen.has('coconut')).toBe(false)
    expect(seen.has('crossing:gramophone')).toBe(false)
    expect(seen.has('crossing:fish')).toBe(true)
    expect(seen.has('crossing:shootingstar')).toBe(false)
    const space = new Set<string>()
    for (let i = 0; i < 200; i++) {
      const p = pickAmbient({ ...base, backdrop: 'space', rand: () => i / 200 })
      if (p) space.add(ambientKey(p))
    }
    expect(space.has('crossing:shootingstar')).toBe(true)
    expect(space.has('crossing:balloon')).toBe(false)
  })

  it('has a big enough cast of gags', () => {
    expect(new Set(AMBIENT_RULES.map(ambientKey)).size).toBeGreaterThanOrEqual(10)
  })
})

describe('hats', () => {
  it('stays stable for a session and turn run, and changes over turns', () => {
    expect(hatFor(0.42, 0)).toBe(hatFor(0.42, 0))
    expect(hatFor(0.42, 1)).toBe(hatFor(0.42, 0))
    const seen = new Set(Array.from({ length: 40 }, (_, t) => hatFor(0.42, t)))
    expect(seen.size).toBeGreaterThan(4)
    for (const h of seen) expect(HATS).toContain(h)
  })

  it('differs between sessions', () => {
    const seen = new Set([0.05, 0.2, 0.35, 0.5, 0.65, 0.8, 0.95].map(s => hatFor(s, 0)))
    expect(seen.size).toBeGreaterThan(3)
  })
})
