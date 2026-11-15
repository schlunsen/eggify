import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { resolve } from 'node:path'
import {
  CIRCUS_BACKDROPS, BACKDROP_COUNT, BACKDROP_ART, backdropArt, backdropSvg, backdropInfo, createBackdropAssigner, hashBackdropIndex,
} from '~/composables/agents/zen/circusBackdrops'

const ART_DIR = resolve(__dirname, '../../../public/zen/circus/backdrops')

/** Width/height from a WebP header (VP8X, VP8L or VP8). */
function webpSize(file: string): [number, number] {
  const b = readFileSync(file)
  expect(b.toString('ascii', 0, 4)).toBe('RIFF')
  expect(b.toString('ascii', 8, 12)).toBe('WEBP')
  const kind = b.toString('ascii', 12, 16)
  if (kind === 'VP8X') return [1 + b.readUIntLE(24, 3), 1 + b.readUIntLE(27, 3)]
  if (kind === 'VP8L') {
    const bits = b.readUInt32LE(21)
    return [1 + (bits & 0x3fff), 1 + ((bits >> 14) & 0x3fff)]
  }
  return [b.readUInt16LE(26) & 0x3fff, b.readUInt16LE(28) & 0x3fff]
}

/** The static plates are SVG documents inside data URIs. */
const plates = (svg: string) =>
  [...svg.matchAll(/href="data:image\/svg\+xml;charset=utf-8,([^"]+)"/g)].map(m => decodeURIComponent(m[1]!))

const ids = (n: number, prefix = 'sess') => Array.from({ length: n }, (_, i) => `${prefix}-${i}-${(i * 7919) % 1000}`)

describe('circus backdrops', () => {
  it('has ten distinct, named backdrops that all draw', () => {
    expect(BACKDROP_COUNT).toBe(10)
    expect(new Set(CIRCUS_BACKDROPS.map(b => b.id)).size).toBe(10)
    expect(new Set(CIRCUS_BACKDROPS.map(b => b.name)).size).toBe(10)
    const svgs = CIRCUS_BACKDROPS.map(b => backdropSvg(b.id))
    expect(new Set(svgs).size).toBe(10)
    for (const svg of svgs) {
      expect(svg.startsWith('<svg')).toBe(true)
      expect(svg.endsWith('</svg>')).toBe(true)
      // balanced groups, so a typo in one backdrop cannot swallow the stage boards
      expect((svg.match(/<g[ >]/g) || []).length).toBe((svg.match(/<\/g>/g) || []).length)
    }
  })

  it('every backdrop paints a stage and none moves more than a few things on the beat', () => {
    for (const b of CIRCUS_BACKDROPS) {
      const svg = backdropSvg(b.id)
      expect(svg).toContain(b.board)
      const beatHooks = (svg.match(/class="cc-b-[a-z]+/g) || []).length
      expect(beatHooks).toBeLessThanOrEqual(6)
    }
  })

  it('has beat hooks on each backdrop so something moves with the music', () => {
    for (const b of CIRCUS_BACKDROPS) expect(backdropSvg(b.id)).toMatch(/cc-b-(pulse|half|off|swing|flicker|fall|flash)/)
  })

  it('paints its static layers as self-contained SVG plates', () => {
    for (const b of CIRCUS_BACKDROPS) {
      const docs = plates(backdropSvg(b.id))
      expect(docs.length).toBeGreaterThan(0)
      for (const d of docs) {
        expect(d.startsWith('<svg xmlns="http://www.w3.org/2000/svg"')).toBe(true)
        expect(d.endsWith('</svg>')).toBe(true)
        expect((d.match(/<g[ >]/g) || []).length).toBe((d.match(/<\/g>/g) || []).length)
        // a plate is an image: it cannot load anything else
        expect(d).not.toMatch(/href="(?!#)/)
      }
      // the stage boards are painted in a plate
      expect(docs.some(d => d.includes(`fill="${b.board}"`))).toBe(true)
    }
  })

  it('beat-hooked groups carry no transform attribute (the CSS transform would replace it)', () => {
    for (const b of CIRCUS_BACKDROPS) {
      expect(backdropSvg(b.id)).not.toMatch(/<g class="cc-b-[^"]*"[^>]*transform=/)
    }
  })

  it('falls back to the first backdrop for an unknown id', () => {
    expect(backdropInfo('nope').id).toBe('meadow')
  })
})

describe('engraved cut-out art', () => {
  it('keeps the ids, names and order of the ten backdrops', () => {
    expect(CIRCUS_BACKDROPS.map(b => b.id)).toEqual(['meadow', 'dusk', 'storm', 'tearoom', 'snow', 'seaside', 'desert', 'jungle', 'gothic', 'space'])
    expect(CIRCUS_BACKDROPS.map(b => b.name)).toEqual(['Teal Hills', 'Big Sunset', 'Stormy Night', 'Tea Room', 'Snowy Village', 'Seaside Pier', 'Pyramids', 'Greenhouse', 'Moonlit Graveyard', 'Moon Landing'])
  })

  it('every backdrop uses engraved cut-outs, and each scene has a signature piece of its own', () => {
    const sets = CIRCUS_BACKDROPS.map(b => backdropArt(b.id))
    for (const s of sets) expect(s.length).toBeGreaterThanOrEqual(2)
    for (let i = 0; i < sets.length; i++) {
      const others = new Set(sets.filter((_, j) => j !== i).flat())
      expect(sets[i]!.some(a => !others.has(a))).toBe(true)
    }
  })

  it('every referenced file exists, matches its recorded size and stays in budget', () => {
    const used = new Set(CIRCUS_BACKDROPS.flatMap(b => backdropArt(b.id)))
    used.add('cloud') // the scene's drifting clouds
    for (const name of used) {
      expect(BACKDROP_ART[name], name).toBeDefined()
      const file = resolve(ART_DIR, `${name}.webp`)
      expect(statSync(file).size, name).toBeLessThan(120 * 1024)
      const [w, h] = webpSize(file)
      expect([w, h], name).toEqual([...BACKDROP_ART[name]!])
      expect(Math.max(w, h), name).toBeLessThanOrEqual(760)
    }
    const files = readdirSync(ART_DIR).filter(f => f.endsWith('.webp'))
    // nothing unused ships, and the whole set stays small
    expect(files.map(f => f.replace('.webp', '')).sort()).toEqual([...used].sort())
    const total = files.reduce((n, f) => n + statSync(resolve(ART_DIR, f)).size, 0)
    expect(total).toBeLessThan(4 * 1024 * 1024)
  })

  it('documents every file in ASSETS.md and never a key', () => {
    const doc = readFileSync(resolve(ART_DIR, 'ASSETS.md'), 'utf8')
    for (const f of readdirSync(ART_DIR).filter(f => f.endsWith('.webp'))) expect(doc).toContain(f)
    expect(doc).not.toMatch(/apikey-|Bearer [A-Za-z0-9]/)
  })
})

describe('session to backdrop mapping', () => {
  it('is stable per session id', () => {
    for (const id of ids(30)) expect(hashBackdropIndex(id)).toBe(hashBackdropIndex(id))
    const a = createBackdropAssigner()
    const first = a.assign('abc', ['abc'])
    // other sessions come and go: the first one keeps its backdrop
    a.assign('def', ['abc', 'def'])
    a.assign('ghi', ['abc', 'def', 'ghi'])
    expect(a.assign('abc', ['abc', 'def', 'ghi'])).toBe(first)
    expect(a.assign('abc')).toBe(first)
  })

  it('covers all ten backdrops across many ids', () => {
    const seen = new Set(ids(300).map(hashBackdropIndex))
    expect(seen.size).toBe(BACKDROP_COUNT)
    // and spreads reasonably evenly
    const counts = new Array(BACKDROP_COUNT).fill(0)
    for (const id of ids(1000, 'x')) counts[hashBackdropIndex(id)]++
    for (const c of counts) expect(c).toBeGreaterThan(50)
  })

  it('gives ten open sessions ten distinct backdrops, whatever order they appear in', () => {
    for (let trial = 0; trial < 20; trial++) {
      const open = ids(10, `t${trial}`)
      const a = createBackdropAssigner()
      // sessions are discovered one at a time, in a shuffled order
      const order = [...open].sort((x, y) => hashBackdropIndex(y + 'z') - hashBackdropIndex(x + 'z'))
      const got = order.map(id => a.assign(id, open))
      expect(new Set(got).size).toBe(10)
    }
  })

  it('assignAll spreads a fresh start evenly and keeps earlier choices', () => {
    const open = ids(10, 'all')
    const a = createBackdropAssigner()
    a.assignAll(open)
    const snap = a.snapshot()
    expect(new Set(Object.values(snap)).size).toBe(10)
    a.assignAll([...open, 'late-comer'])
    for (const id of open) expect(a.snapshot()[id]).toBe(snap[id])
  })

  it('with more than ten sessions every backdrop is used about equally', () => {
    const open = ids(20, 'many')
    const a = createBackdropAssigner()
    a.assignAll(open)
    const counts = new Array(10).fill(0)
    for (const id of open) counts[a.snapshot()[id]!]++
    expect(Math.max(...counts)).toBeLessThanOrEqual(2)
    expect(Math.min(...counts)).toBeGreaterThanOrEqual(2)
  })

  it('restores a saved mapping and ignores junk', () => {
    const a = createBackdropAssigner({ a: 3, b: 99, c: -1, d: 1.5 as number })
    expect(a.snapshot()).toEqual({ a: 3 })
    expect(a.assign('a')).toBe(3)
  })
})
