/**
 * Shared microphone level meter.
 *
 * Voice capture (Whisper batch recording and Parakeet streaming dictation)
 * hands its MediaStream here; anything that wants to react to the user's
 * voice — the zen scene, the zen recording bar — reads the level.
 *
 * Module-level singleton: one analyser at a time, driven by its own rAF loop
 * while a stream is attached. `getMicLevel()` is a cheap non-reactive read for
 * per-frame render loops; `micLevel` / `micActive` are reactive for the UI.
 */
import { ref, readonly } from 'vue'

const level = ref(0)
const active = ref(false)

let ctx: AudioContext | null = null
let source: MediaStreamAudioSourceNode | null = null
let analyser: AnalyserNode | null = null
let buf: Float32Array | null = null
let attached: MediaStream | null = null
let raf: number | null = null
let smoothed = 0
let lastUiUpdate = 0

// Speech sits roughly between -55 dBFS (soft) and -12 dBFS (loud)
const FLOOR_DB = -55
const CEIL_DB = -12

function sample(now: number) {
  if (!analyser || !buf) return
  analyser.getFloatTimeDomainData(buf as any)
  let sum = 0
  for (let i = 0; i < buf.length; i++) sum += buf[i]! * buf[i]!
  const rms = Math.sqrt(sum / buf.length)
  const db = 20 * Math.log10(Math.max(rms, 1e-8))
  const target = Math.min(1, Math.max(0, (db - FLOOR_DB) / (CEIL_DB - FLOOR_DB)))
  // Fast attack, slower release, so syllables punch and then glow out
  smoothed += (target - smoothed) * (target > smoothed ? 0.55 : 0.12)
  if (now - lastUiUpdate > 50) {
    level.value = smoothed
    lastUiUpdate = now
  }
  raf = requestAnimationFrame(sample)
}

/** Start metering a live microphone stream. Replaces any previous stream. */
export function attachMicStream(stream: MediaStream) {
  detachMicStream()
  try {
    const Ctor = window.AudioContext || (window as any).webkitAudioContext
    if (!Ctor || !stream.getAudioTracks().length) return
    ctx = new Ctor()
    if (ctx.state === 'suspended') ctx.resume().catch(() => {})
    source = ctx.createMediaStreamSource(stream)
    analyser = ctx.createAnalyser()
    analyser.fftSize = 1024
    analyser.smoothingTimeConstant = 0
    buf = new Float32Array(analyser.fftSize)
    source.connect(analyser) // analyser only — never routed to the speakers
    attached = stream
    active.value = true
    raf = requestAnimationFrame(sample)
  } catch (err) {
    console.warn('[micLevel] could not meter microphone:', err)
    detachMicStream()
  }
}

/** Stop metering. With a stream argument, only detaches if it is the current one. */
export function detachMicStream(stream?: MediaStream | null) {
  if (stream && attached && stream !== attached) return
  if (raf !== null) cancelAnimationFrame(raf)
  raf = null
  try { source?.disconnect() } catch { /* already gone */ }
  ctx?.close().catch(() => {})
  ctx = null
  source = null
  analyser = null
  buf = null
  attached = null
  smoothed = 0
  level.value = 0
  active.value = false
}

/** Current smoothed level, 0..1. Non-reactive — for render loops. */
export function getMicLevel(): number {
  return smoothed
}

/** True while a microphone stream is being metered. */
export function isMicActive(): boolean {
  return attached !== null
}

export function useMicLevel() {
  return { level: readonly(level), active: readonly(active) }
}
