import { describe, it, expect } from 'vitest'
import { existsSync, readFileSync, readdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
// @ts-expect-error plain .mjs script without type declarations
import { analyse, decodeWithFfmpeg, SAMPLE_RATE } from '../../../../../../scripts/zen-beatgrid.mjs'
import { isBeatGrid } from '../../utils/beatClock'

/** A click track: a short low thump with a noisy edge on every beat, the bar's first one louder. */
function clickTrack(bpm: number, offset: number, seconds: number): Float32Array {
  const n = Math.floor(seconds * SAMPLE_RATE)
  const out = new Float32Array(n)
  const period = 60 / bpm
  let seed = 12345
  const rnd = () => { seed = (seed * 1664525 + 1013904223) >>> 0; return (seed / 4294967296) * 2 - 1 }
  for (let k = 0; offset + k * period < seconds - 0.1; k++) {
    const t0 = Math.round((offset + k * period) * SAMPLE_RATE)
    const amp = k % 4 === 0 ? 1 : 0.6
    for (let i = 0; i < 0.06 * SAMPLE_RATE && t0 + i < n; i++) {
      const t = i / SAMPLE_RATE
      out[t0 + i] += amp * Math.exp(-t / 0.015) * (0.8 * Math.sin(2 * Math.PI * 80 * t) + 0.5 * rnd())
    }
  }
  return out
}

/** Smallest signed distance between two beat phases, in seconds */
function phaseError(found: number, expected: number, bpm: number): number {
  const p = 60 / bpm
  return ((((found - expected) + p / 2) % p) + p) % p - p / 2
}

describe('zen beat grid script', () => {
  it.each([
    [118, 0.31],
    [96.4, 0.12],
    [127.7, 0.05],
    [104, 0.5],
  ])('recovers %s bpm (offset %s s) within 1 percent and 30 ms', (bpm, offset) => {
    const g = analyse(clickTrack(bpm, offset, 45))
    expect(Math.abs(g.bpm / bpm - 1)).toBeLessThan(0.01)
    expect(Math.abs(phaseError(g.firstBeat, offset, bpm))).toBeLessThan(0.03)
    expect(g.beatsPerBar).toBe(4)
    // The first click is the loud one: it is the downbeat
    expect(g.downbeatOffset).toBe(0)
  })

  it('finds the louder beat of the bar as the downbeat', () => {
    // Shift the accent: start the track one beat before the loud click
    const bpm = 110
    const period = 60 / bpm
    const track = clickTrack(bpm, 0.2, 45)
    const shifted = new Float32Array(track.length)
    shifted.set(track.subarray(Math.round(period * SAMPLE_RATE)), 0) // drop the first (loud) beat
    const g = analyse(shifted)
    // Beats now start at 0.2 s and the loud ones are at beat index 3 (k=4 is 4 beats after the dropped one)
    expect(g.downbeatOffset).toBe(3)
  })

  it('writes a grid the runtime accepts for every shipped track', () => {
    const dir = join(__dirname, '../../../public/zen/audio')
    const mp3s = readdirSync(dir).filter(f => f.endsWith('.mp3'))
    expect(mp3s.length).toBeGreaterThan(0)
    for (const f of mp3s) {
      const grid = join(dir, f.replace(/\.mp3$/, '.beats.json'))
      expect(existsSync(grid), `${f} has a .beats.json`).toBe(true)
      expect(isBeatGrid(JSON.parse(readFileSync(grid, 'utf8'))), `${f} grid is valid`).toBe(true)
    }
  })

  const ffmpeg = spawnSync('ffmpeg', ['-version']).status === 0
  it.skipIf(!ffmpeg)('decodes a file through ffmpeg and gets the same answer', () => {
    const dir = mkdtempSync(join(tmpdir(), 'zenbeat-'))
    const samples = clickTrack(100, 0.25, 30)
    const wav = Buffer.alloc(44 + samples.length * 2)
    wav.write('RIFF', 0); wav.writeUInt32LE(36 + samples.length * 2, 4); wav.write('WAVEfmt ', 8)
    wav.writeUInt32LE(16, 16); wav.writeUInt16LE(1, 20); wav.writeUInt16LE(1, 22)
    wav.writeUInt32LE(SAMPLE_RATE, 24); wav.writeUInt32LE(SAMPLE_RATE * 2, 28); wav.writeUInt16LE(2, 32); wav.writeUInt16LE(16, 34)
    wav.write('data', 36); wav.writeUInt32LE(samples.length * 2, 40)
    samples.forEach((v, i) => wav.writeInt16LE(Math.max(-32767, Math.min(32767, Math.round(v * 20000))), 44 + i * 2))
    const file = join(dir, 'click.wav')
    writeFileSync(file, wav)
    const g = analyse(decodeWithFfmpeg(file))
    expect(Math.abs(g.bpm / 100 - 1)).toBeLessThan(0.01)
    expect(Math.abs(phaseError(g.firstBeat, 0.25, 100))).toBeLessThan(0.03)
  })
})
