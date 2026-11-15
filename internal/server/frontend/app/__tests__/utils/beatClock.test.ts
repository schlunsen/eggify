import { describe, it, expect } from 'vitest'
import { beatStateAt, beatEnvelopes, calmEnvelopes, barAccents, BEAT_MOTION, hash01, isBeatGrid, outputLatencySeconds, type BeatGrid } from '~/utils/beatClock'

const grid: BeatGrid = { bpm: 120, firstBeat: 0.25, beatsPerBar: 4, downbeatOffset: 1, duration: 10 }
// 120 bpm = a beat every 0.5 s: beats at 0.25, 0.75, 1.25, ...; bars start at beat index 1, 5, 9 (0.75, 2.75, ...)

describe('beat clock maths', () => {
  it('is on the beat at the grid times', () => {
    const s = beatStateAt(grid, 1.25)
    expect(s.beatIndex).toBe(2)
    expect(s.phase).toBeCloseTo(0, 6)
    expect(s.toNextBeat).toBeCloseTo(0.5, 6)
  })

  it('gives the phase between beats and the time to the next one', () => {
    const s = beatStateAt(grid, 1.5)
    expect(s.beatIndex).toBe(2)
    expect(s.phase).toBeCloseTo(0.5, 6)
    expect(s.toNextBeat).toBeCloseTo(0.25, 6)
  })

  it('counts the bar from the downbeat offset', () => {
    // Beat index 1 (t = 0.75) starts a bar
    expect(beatStateAt(grid, 0.75).beatInBar).toBe(0)
    expect(beatStateAt(grid, 1.25).beatInBar).toBe(1)
    expect(beatStateAt(grid, 2.25).beatInBar).toBe(3)
    expect(beatStateAt(grid, 2.75).beatInBar).toBe(0)
    expect(beatStateAt(grid, 2.75).bar).toBe(1)
    // Before the first downbeat the beat is the last of bar -1
    expect(beatStateAt(grid, 0.3).beatInBar).toBe(3)
    expect(beatStateAt(grid, 0.3).bar).toBe(-1)
  })

  it('knows when the next downbeat lands', () => {
    // At t = 1.25 (beat 1 of the bar): downbeat at 2.75
    expect(beatStateAt(grid, 1.25).toNextDownbeat).toBeCloseTo(1.5, 6)
    expect(beatStateAt(grid, 0.75).toNextDownbeat).toBeCloseTo(2, 6)
  })

  it('handles time before the first beat (negative beat index, phase still 0..1)', () => {
    const s = beatStateAt(grid, 0.1)
    expect(s.beatIndex).toBe(-1)
    expect(s.phase).toBeGreaterThanOrEqual(0)
    expect(s.phase).toBeLessThan(1)
    expect(s.phase).toBeCloseTo(0.7, 6)
  })

  it('follows a seek: the state depends only on the position', () => {
    const a = beatStateAt(grid, 7.6)
    beatStateAt(grid, 1)
    const b = beatStateAt(grid, 7.6)
    expect(b).toEqual(a)
    expect(a.beatIndex).toBe(14)
    expect(a.phase).toBeCloseTo(0.7, 6)
  })

  it('compensates the output latency: what you hear is older than the element time', () => {
    // The element is at 1.30 s but the speakers play 80 ms behind: we hear t = 1.22 (just before beat 2 at 1.25)
    const s = beatStateAt(grid, 1.3, 0.08)
    expect(s.beatIndex).toBe(1)
    expect(s.phase).toBeCloseTo(0.94, 6)
    // With no latency we would already be past the beat
    expect(beatStateAt(grid, 1.3).beatIndex).toBe(2)
  })

  it('a track change restarts from the new grid and position', () => {
    const next: BeatGrid = { bpm: 90, firstBeat: 0.1, beatsPerBar: 4, downbeatOffset: 0 }
    const s = beatStateAt(next, 0.1 + 60 / 90)
    expect(s.beatIndex).toBe(1)
    expect(s.phase).toBeCloseTo(0, 6)
    expect(s.bpm).toBe(90)
  })

  it('wraps at the duration when the track loops', () => {
    const looped = beatStateAt(grid, 10.25 + 0.5, 0, true)
    const start = beatStateAt(grid, 0.75)
    expect(looped.beatIndex).toBe(start.beatIndex)
    expect(looped.phase).toBeCloseTo(start.phase, 6)
  })

  it('validates grids', () => {
    expect(isBeatGrid(grid)).toBe(true)
    expect(isBeatGrid(null)).toBe(false)
    expect(isBeatGrid({ ...grid, bpm: 5 })).toBe(false)
    expect(isBeatGrid({ ...grid, downbeatOffset: 4 })).toBe(false)
    expect(isBeatGrid({ ...grid, firstBeat: -1 })).toBe(false)
  })

  it('reads the output latency from the audio context, capped', () => {
    expect(outputLatencySeconds(null)).toBe(0)
    expect(outputLatencySeconds({ outputLatency: 0.03, baseLatency: 0.01 })).toBeCloseTo(0.04, 6)
    expect(outputLatencySeconds({ baseLatency: 0.01 })).toBeCloseTo(0.01, 6)
    expect(outputLatencySeconds({ outputLatency: 5 })).toBe(0.25)
    expect(outputLatencySeconds({ outputLatency: Number.NaN })).toBe(0)
  })
})

describe('beat envelopes', () => {
  it('snaps to full on the beat and eases out', () => {
    const on = beatEnvelopes({ phase: 0, beatIndex: 2, beatInBar: 1 })
    const mid = beatEnvelopes({ phase: 0.5, beatIndex: 2, beatInBar: 1 })
    expect(on.pulse).toBe(1)
    expect(mid.pulse).toBeLessThan(0.2)
    expect(on.pulse).toBeGreaterThan(mid.pulse)
  })

  it('accents only the downbeat, halves only every other beat', () => {
    expect(beatEnvelopes({ phase: 0, beatIndex: 1, beatInBar: 0 }).accent).toBe(1)
    expect(beatEnvelopes({ phase: 0, beatIndex: 2, beatInBar: 1 }).accent).toBe(0)
    expect(beatEnvelopes({ phase: 0, beatIndex: 2, beatInBar: 1 }).half).toBe(1)
    expect(beatEnvelopes({ phase: 0, beatIndex: 3, beatInBar: 2 }).half).toBe(0)
  })

  it('bumps between the beats and alternates side', () => {
    expect(beatEnvelopes({ phase: 0.5, beatIndex: 0, beatInBar: 0 }).off).toBeCloseTo(1, 6)
    expect(beatEnvelopes({ phase: 0, beatIndex: 0, beatInBar: 0 }).off).toBeCloseTo(0, 6)
    expect(beatEnvelopes({ phase: 0, beatIndex: 0, beatInBar: 0 }).dir).toBe(1)
    expect(beatEnvelopes({ phase: 0, beatIndex: 1, beatInBar: 1 }).dir).toBe(-1)
  })

  it('swings one way on a beat and the other on the next', () => {
    const a = beatEnvelopes({ phase: 0, beatIndex: 0, beatInBar: 0 }).swing
    const b = beatEnvelopes({ phase: 0, beatIndex: 1, beatInBar: 1 }).swing
    expect(a).toBeCloseTo(-b, 6)
    expect(Math.abs(a)).toBeCloseTo(1, 6)
  })

  it('crouches only just before the beat', () => {
    expect(beatEnvelopes({ phase: 0.5, beatIndex: 0, beatInBar: 0 }).squash).toBe(0)
    expect(beatEnvelopes({ phase: 0.99, beatIndex: 0, beatInBar: 0 }).squash).toBeGreaterThan(0.5)
  })
})

describe('relaxed beat motion', () => {
  const at = (t: number) => { const st = beatStateAt(grid, t); return { st, e: calmEnvelopes(st) } }
  const bpb = 4
  const downbeatBars = (n: number) => Array.from({ length: n }, (_, b) => b).filter(b => barAccents(b))

  it('accents only every other bar, with a deterministic skip', () => {
    const bars = downbeatBars(200)
    expect(bars.every(b => b % BEAT_MOTION.accentEveryBars === 0)).toBe(true)
    // about (1 - skip) of the eligible bars
    expect(bars.length).toBeGreaterThan(100 * 0.6)
    expect(bars.length).toBeLessThan(100 * 0.9)
    expect(downbeatBars(200)).toEqual(bars)
    expect(hash01(5)).toBe(hash01(5))
  })

  it('nods softly on an accented downbeat and never on other beats', () => {
    const b = downbeatBars(20)[0]!
    const beatIdx = b * bpb + grid.downbeatOffset
    const t = (i: number, ph: number) => grid.firstBeat + (i + ph) * 0.5
    expect(at(t(beatIdx, 0)).e.nod).toBe(0)                  // eases in: no snap on the beat
    expect(at(t(beatIdx, 0.5)).e.nod).toBeGreaterThan(0.2)
    expect(at(t(beatIdx, 0.99)).e.nod).toBeLessThan(1.01)
    expect(at(t(beatIdx + 1, 0.5)).e.nod).toBeGreaterThanOrEqual(0)
    expect(at(t(beatIdx + 2, 0.5)).e.nod).toBe(0)
    expect(at(t(beatIdx + 3, 0.5)).e.nod).toBe(0)
  })

  it('has no snap: every envelope is continuous across a beat', () => {
    for (let i = 0; i < 40; i++) {
      const before = at(grid.firstBeat + (i + 0.999) * 0.5).e
      const after = at(grid.firstBeat + (i + 1.001) * 0.5).e
      for (const k of ['sway', 'breath', 'nod', 'bar', 'step'] as const) {
        expect(Math.abs(before[k] - after[k])).toBeLessThan(0.05)
      }
    }
  })

  it('steps walkers at half tempo: one bounce per two beats, alternating side', () => {
    const peaks: number[] = []
    let prev = 0, prev2 = 0
    for (let i = 0; i <= 16 * 50; i++) {
      const st = beatStateAt(grid, grid.firstBeat + (i / 50) * 0.5)
      const v = calmEnvelopes(st).step
      if (prev > prev2 && prev >= v && prev > 0.9) peaks.push(st.beatIndex + st.phase)
      prev2 = prev; prev = v
    }
    expect(peaks.length).toBeGreaterThanOrEqual(7)
    expect(peaks.length).toBeLessThanOrEqual(8)
    expect(peaks[1]! - peaks[0]!).toBeCloseTo(2, 1)
    expect(calmEnvelopes({ phase: 0.5, beatIndex: 0, beatInBar: 0, bar: 0 }).stepDir)
      .toBe(-calmEnvelopes({ phase: 0.5, beatIndex: 2, beatInBar: 2, bar: 0 }).stepDir)
  })

  it('leans every two beats, slower and softer than a per-beat swing, and breathes once a bar', () => {
    const lean = (i: number) => calmEnvelopes({ phase: 0.5, beatIndex: i, beatInBar: i % 4, bar: Math.floor(i / 4) }).sway
    expect(Math.sign(lean(0))).toBe(-Math.sign(lean(2)))
    expect(Math.sign(lean(0))).toBe(Math.sign(lean(1)))
    const bre = (inBar: number) => calmEnvelopes({ phase: 0, beatIndex: inBar, beatInBar: inBar, bar: 0 }).breath
    expect(bre(0)).toBeCloseTo(0, 6)
    expect(bre(2)).toBeCloseTo(1, 6)
  })
})
