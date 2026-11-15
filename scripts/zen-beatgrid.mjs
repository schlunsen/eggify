#!/usr/bin/env node
/**
 * Offline beat-grid generator for the Zen music tracks.
 *
 *   node scripts/zen-beatgrid.mjs                 # every mp3 in public/zen/audio
 *   node scripts/zen-beatgrid.mjs a.mp3 b.mp3     # only these
 *
 * Needs ffmpeg on PATH (used only to decode to mono PCM). Writes
 * <name>.beats.json next to each mp3: { bpm, firstBeat, beatsPerBar,
 * downbeatOffset, duration, confidence, version }.
 *
 * Method: log-magnitude spectral flux -> onset envelope; tempo from the
 * autocorrelation (70-180 bpm, weighted towards 110 so octave errors lose);
 * then a comb search over bpm (+-4 %) and phase that maximises the onset energy
 * on the beat grid; the downbeat is the bar position (of 4) whose beats carry
 * the most low-frequency (kick) energy. Deterministic: same file, same output.
 */
import { spawnSync } from 'node:child_process'
import { readdirSync, writeFileSync, existsSync } from 'node:fs'
import { basename, dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const SAMPLE_RATE = 22050
const FRAME = 1024
const HOP = 128
const BEATS_PER_BAR = 4
/**
 * The flux peaks about 30 ms before the onset it belongs to is "centred" in the
 * analysis window (measured on synthetic click tracks), so grid times are shifted by this.
 */
const ONSET_BIAS_S = 0.03

/** Decode any audio file to mono float PCM with ffmpeg. */
export function decodeWithFfmpeg(file, sampleRate = SAMPLE_RATE) {
  const r = spawnSync('ffmpeg', ['-v', 'error', '-i', file, '-ac', '1', '-ar', String(sampleRate), '-f', 'f32le', '-'], {
    maxBuffer: 1 << 30,
  })
  if (r.status !== 0) throw new Error(`ffmpeg failed for ${file}: ${r.stderr?.toString() || r.error}`)
  const buf = r.stdout
  return new Float32Array(buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.byteLength - (buf.byteLength % 4)))
}

/** In-place radix-2 FFT. */
function fft(re, im) {
  const n = re.length
  for (let i = 1, j = 0; i < n; i++) {
    let bit = n >> 1
    for (; j & bit; bit >>= 1) j ^= bit
    j ^= bit
    if (i < j) { [re[i], re[j]] = [re[j], re[i]]; [im[i], im[j]] = [im[j], im[i]] }
  }
  for (let len = 2; len <= n; len <<= 1) {
    const ang = (-2 * Math.PI) / len
    const wr = Math.cos(ang), wi = Math.sin(ang)
    for (let i = 0; i < n; i += len) {
      let cr = 1, ci = 0
      for (let k = 0; k < len / 2; k++) {
        const a = i + k, b = a + len / 2
        const tr = re[b] * cr - im[b] * ci
        const ti = re[b] * ci + im[b] * cr
        re[b] = re[a] - tr; im[b] = im[a] - ti
        re[a] += tr; im[a] += ti
        const nr = cr * wr - ci * wi
        ci = cr * wi + ci * wr
        cr = nr
      }
    }
  }
}

/**
 * Onset envelopes at one value per HOP samples: `full` (spectral flux up to
 * ~5 kHz) and `low` (flux below ~200 Hz, the kick).
 */
export function onsetEnvelopes(samples, sampleRate = SAMPLE_RATE) {
  const frames = Math.max(0, Math.floor((samples.length - FRAME) / HOP))
  const win = new Float32Array(FRAME)
  for (let i = 0; i < FRAME; i++) win[i] = 0.5 - 0.5 * Math.cos((2 * Math.PI * i) / (FRAME - 1))
  const binHz = sampleRate / FRAME
  const hi = Math.min(FRAME / 2, Math.floor(5000 / binHz))
  const lowHi = Math.max(2, Math.floor(200 / binHz))
  const re = new Float64Array(FRAME), im = new Float64Array(FRAME)
  let prev = new Float64Array(hi)
  const full = new Float64Array(frames), low = new Float64Array(frames)
  for (let f = 0; f < frames; f++) {
    const o = f * HOP
    for (let i = 0; i < FRAME; i++) { re[i] = samples[o + i] * win[i]; im[i] = 0 }
    fft(re, im)
    const cur = new Float64Array(hi)
    let sf = 0, sl = 0
    for (let k = 1; k < hi; k++) {
      cur[k] = Math.log1p(100 * Math.hypot(re[k], im[k]) / FRAME * 8)
      const d = cur[k] - prev[k]
      if (d > 0) { sf += d; if (k <= lowHi) sl += d }
    }
    full[f] = sf; low[f] = sl
    prev = cur
  }
  return { full: normalise(full), low: normalise(low), hopSeconds: HOP / sampleRate }
}

/** Subtract a moving average, half-wave rectify, scale to unit mean. */
function normalise(x) {
  const n = x.length, out = new Float64Array(n)
  const r = 40 // ~0.23 s
  let sum = 0
  const pre = new Float64Array(n + 1)
  for (let i = 0; i < n; i++) { sum += x[i]; pre[i + 1] = sum }
  let tot = 0
  for (let i = 0; i < n; i++) {
    const a = Math.max(0, i - r), b = Math.min(n, i + r + 1)
    const m = (pre[b] - pre[a]) / (b - a)
    out[i] = Math.max(0, x[i] - m)
    tot += out[i]
  }
  const mean = tot / Math.max(1, n) || 1
  for (let i = 0; i < n; i++) out[i] /= mean
  return out
}

/** Tempo guess (bpm) from the autocorrelation of the onset envelope. */
export function estimateTempo(env, hopSeconds, minBpm = 70, maxBpm = 180, prior = 110) {
  const n = env.length
  const minLag = Math.floor(60 / maxBpm / hopSeconds)
  const maxLag = Math.ceil(60 / minBpm / hopSeconds)
  const ac = new Float64Array(maxLag + 2)
  for (let lag = minLag - 1; lag <= maxLag + 1; lag++) {
    let s = 0
    for (let i = 0; i + lag < n; i++) s += env[i] * env[i + lag]
    ac[lag] = s / (n - lag)
  }
  let best = -1, bestScore = -Infinity
  for (let lag = minLag; lag <= maxLag; lag++) {
    const bpm = 60 / (lag * hopSeconds)
    // Strong at the lag and at its double (a bar of two beats) helps pick the beat, not the half beat
    const dbl = lag * 2 <= maxLag + 1 ? ac[lag * 2] : 0
    const w = Math.exp(-0.5 * Math.pow(Math.log2(bpm / prior) / 0.55, 2))
    const score = (ac[lag] + 0.5 * dbl) * w
    if (score > bestScore) { bestScore = score; best = lag }
  }
  return 60 / (best * hopSeconds)
}

function smooth(env, r = 2) {
  const out = new Float64Array(env.length)
  for (let i = 0; i < env.length; i++) {
    let s = 0, wsum = 0
    for (let k = -r; k <= r; k++) {
      const j = i + k
      if (j < 0 || j >= env.length) continue
      const w = r + 1 - Math.abs(k)
      s += env[j] * w; wsum += w
    }
    out[i] = s / wsum
  }
  return out
}

function sampleAt(env, pos) {
  const i = Math.floor(pos)
  if (i < 0 || i + 1 >= env.length) return 0
  const f = pos - i
  return env[i] * (1 - f) + env[i + 1] * f
}

/** Score of a (bpm, phase) grid: mean onset energy on the beats. */
function gridScore(env, hopSeconds, bpm, phase) {
  const period = 60 / bpm / hopSeconds
  const start = phase / hopSeconds
  let s = 0, c = 0
  for (let p = start; p < env.length - 1; p += period) { s += sampleAt(env, p); c++ }
  return c ? s / c : 0
}

/** Best (bpm, phase seconds) around a tempo guess. */
export function fitGrid(env, hopSeconds, bpmGuess, span = 0.04) {
  const sm = smooth(env, 2)
  let best = { score: -1, bpm: bpmGuess, phase: 0 }
  const step = 0.0004 // relative bpm step (0.04 %)
  for (let rel = -span; rel <= span + 1e-9; rel += step) {
    const bpm = bpmGuess * (1 + rel)
    const period = 60 / bpm
    for (let ph = 0; ph < period; ph += hopSeconds) {
      const sc = gridScore(sm, hopSeconds, bpm, ph)
      if (sc > best.score) best = { score: sc, bpm, phase: ph }
    }
  }
  // Fine phase search (quarter hop) at the winning tempo
  let fine = best
  for (let ph = best.phase - hopSeconds; ph <= best.phase + hopSeconds; ph += hopSeconds / 8) {
    const sc = gridScore(sm, hopSeconds, best.bpm, ph)
    if (sc > fine.score) fine = { score: sc, bpm: best.bpm, phase: ph }
  }
  return fine
}

/** Which of the 4 beats of a bar carries the kick: returns the beat index (0..3) that starts a bar. */
export function findDownbeat(low, hopSeconds, bpm, firstBeat, beatsPerBar = BEATS_PER_BAR) {
  const period = 60 / bpm
  const sums = new Array(beatsPerBar).fill(0)
  const cnt = new Array(beatsPerBar).fill(0)
  const sm = smooth(low, 2)
  for (let k = 0; ; k++) {
    const t = firstBeat + k * period
    if (t / hopSeconds >= sm.length - 1) break
    sums[k % beatsPerBar] += sampleAt(sm, t / hopSeconds)
    cnt[k % beatsPerBar]++
  }
  let best = 0, bs = -1
  for (let i = 0; i < beatsPerBar; i++) {
    const v = sums[i] / Math.max(1, cnt[i])
    if (v > bs) { bs = v; best = i }
  }
  return best
}

/** Full analysis of mono PCM. */
export function analyse(samples, sampleRate = SAMPLE_RATE) {
  const { full, low, hopSeconds } = onsetEnvelopes(samples, sampleRate)
  const guess = estimateTempo(full, hopSeconds)
  const fit = fitGrid(full, hopSeconds, guess)
  const period = 60 / fit.bpm
  const downbeatOffset = findDownbeat(low, hopSeconds, fit.bpm, fit.phase)
  const firstBeat = (fit.phase + ONSET_BIAS_S) % period
  return {
    version: 1,
    bpm: Math.round(fit.bpm * 1000) / 1000,
    firstBeat: Math.round(firstBeat * 1000) / 1000,
    beatsPerBar: BEATS_PER_BAR,
    downbeatOffset,
    duration: Math.round((samples.length / sampleRate) * 1000) / 1000,
    confidence: Math.round(fit.score * 1000) / 1000,
    tempoGuess: Math.round(guess * 100) / 100,
    period: Math.round(period * 10000) / 10000,
  }
}

function main() {
  const here = dirname(fileURLToPath(import.meta.url))
  const dir = resolve(here, '../internal/server/frontend/public/zen/audio')
  const args = process.argv.slice(2)
  const files = args.length ? args : readdirSync(dir).filter(f => f.endsWith('.mp3')).map(f => join(dir, f))
  for (const f of files) {
    if (!existsSync(f)) { console.error(`missing ${f}`); process.exitCode = 1; continue }
    const grid = analyse(decodeWithFfmpeg(f))
    const out = f.replace(/\.[^.]+$/, '') + '.beats.json'
    writeFileSync(out, JSON.stringify(grid, null, 2) + '\n')
    console.log(`${basename(f)}: ${grid.bpm} bpm (guess ${grid.tempoGuess}), first beat ${grid.firstBeat}s, downbeat +${grid.downbeatOffset}, confidence ${grid.confidence}`)
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) main()
