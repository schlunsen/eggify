import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import {
  boxesOverlap, boxFree, pickSpot, pickLane, laneCandidates, hudBoxes, shuffled, HUD_PAD, type Box,
} from '~/composables/agents/zen/circusLanes'

// A central card column and a name plate, like the real HUD
const HUD: Box[] = [
  { x: 400, y: 60, w: 440, h: 70 },   // plate
  { x: 450, y: 200, w: 340, h: 40 },  // chips
  { x: 380, y: 280, w: 480, h: 130 }, // cards
]
let seed = 1
const rand = () => { seed = (seed * 16807) % 2147483647; return seed / 2147483647 }

describe('circus lanes', () => {
  it('detects overlap and touching edges', () => {
    expect(boxesOverlap({ x: 0, y: 0, w: 10, h: 10 }, { x: 5, y: 5, w: 10, h: 10 })).toBe(true)
    expect(boxesOverlap({ x: 0, y: 0, w: 10, h: 10 }, { x: 10, y: 0, w: 10, h: 10 })).toBe(false)
  })

  it('never returns a lane that intersects a HUD box', () => {
    for (let n = 0; n < 200; n++) {
      const h = 60 + rand() * 120
      const margin = 10
      const lane = pickLane(1280, 40, 560, h, margin, HUD, rand)
      if (lane === null) continue
      const band: Box = { x: 0, y: lane - margin, w: 1280, h: h + margin * 2 }
      expect(boxFree(band, HUD)).toBe(true)
    }
  })

  it('finds a lane when one exists', () => {
    expect(pickLane(1280, 40, 560, 60, 10, HUD, rand)).not.toBeNull()
  })

  it('skips the gag (null) when the HUD blocks every lane', () => {
    const wall: Box[] = [{ x: 500, y: 0, w: 100, h: 1000 }]
    expect(pickLane(1280, 40, 560, 60, 10, wall, rand)).toBeNull()
    expect(pickSpot([{ x: 0, y: 0, w: 50, h: 50 }], [{ x: 10, y: 10, w: 5, h: 5 }])).toBeNull()
  })

  it('picks the first free spot', () => {
    const a: Box = { x: 420, y: 290, w: 100, h: 40 } // under the cards
    const b: Box = { x: 20, y: 290, w: 100, h: 40 }
    expect(pickSpot([a, b], HUD)).toBe(b)
  })

  it('shuffles without losing candidates', () => {
    const c = laneCandidates(1000, 0, 100, 20, 0, rand, 8)
    expect(c).toHaveLength(8)
    expect(shuffled([1, 2, 3, 4], rand).sort()).toEqual([1, 2, 3, 4])
  })

  it('turns DOM rects into padded stage boxes and drops empty ones', () => {
    const boxes = hudBoxes(
      [{ left: 110, top: 220, width: 100, height: 40 }, { left: 0, top: 0, width: 0, height: 10 }],
      { left: 10, top: 20 },
    )
    expect(boxes).toEqual([{ x: 100 - HUD_PAD, y: 200 - HUD_PAD, w: 100 + HUD_PAD * 2, h: 40 + HUD_PAD * 2 }])
  })
})

describe('stage below HUD (stylesheet)', () => {
  const vue = readFileSync(resolve(__dirname, '../../components/agents/ZenMode.vue'), 'utf8')

  it('keeps the stage in an isolated layer under the HUD', () => {
    const canvas = vue.match(/\.zen-canvas-container \{([^}]*)\}/)![1]!
    expect(canvas).toMatch(/z-index:\s*0/)
    expect(canvas).toMatch(/isolation:\s*isolate/)
    const overlay = vue.match(/\.zen-overlay \{([^}]*)\}/)![1]!
    expect(Number(overlay.match(/z-index:\s*(\d+)/)![1])).toBeGreaterThan(0)
  })

  it('gives Circus cards and chips a solid base so props never show through', () => {
    const m = vue.match(/(\.zen-mode\.zen-viz-circus [^{]*zen-last-message[^{]*)\{([^}]*)\}/g) ?? []
    const solid = m.find(r => /background-color:\s*rgb\(28, 22, 16\)/.test(r))
    expect(solid).toBeTruthy()
    expect(solid).toContain('.zen-status-badge')
    expect(solid).toContain('.zen-agents-summary')
  })
})
