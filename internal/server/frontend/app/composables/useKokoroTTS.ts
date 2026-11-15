/**
 * Kokoro TTS — Browser-based text-to-speech via kokoro-js
 *
 * 82M parameter model running entirely in browser via ONNX Runtime Web.
 * Supports WebGPU (fast) and WASM (fallback).
 * 24 English voices, 24kHz output.
 */

import { ref } from 'vue'
import { runSpeechStream, DEFAULT_SPEECH_AHEAD } from '../utils/speechStream'

export type KokoroStatus = 'unloaded' | 'loading' | 'ready' | 'error' | 'generating'

export interface KokoroVoice {
  id: string
  label: string
  gender: '♀' | '♂'
  accent: 'US' | 'UK'
  grade?: string
}

export const KOKORO_VOICES: KokoroVoice[] = [
  { id: 'af_heart', label: 'Heart', gender: '♀', accent: 'US', grade: 'A' },
  { id: 'af_bella', label: 'Bella', gender: '♀', accent: 'US', grade: 'A-' },
  { id: 'af_sky', label: 'Sky', gender: '♀', accent: 'US' },
  { id: 'af_nicole', label: 'Nicole', gender: '♀', accent: 'US' },
  { id: 'af_sarah', label: 'Sarah', gender: '♀', accent: 'US' },
  { id: 'am_michael', label: 'Michael', gender: '♂', accent: 'US' },
  { id: 'am_adam', label: 'Adam', gender: '♂', accent: 'US' },
  { id: 'am_eric', label: 'Eric', gender: '♂', accent: 'US' },
  { id: 'bf_emma', label: 'Emma', gender: '♀', accent: 'UK', grade: 'B-' },
  { id: 'bm_george', label: 'George', gender: '♂', accent: 'UK' },
  { id: 'bm_fable', label: 'Fable', gender: '♂', accent: 'UK' },
]

// ─── Engines ──────────────────────────────────────────────────
// Synthesis runs in a Web Worker (workers/kokoro-worker.ts) so a WASM
// inference never blocks the page. The inline engine (same thread) is only
// used where workers do not exist (unit tests, very old webviews).
type Device = 'webgpu' | 'wasm'

/** A reading in progress: chunks arrive through the chunk handler, in order. */
export interface KokoroRun {
  /** Resolves when every chunk was delivered (or the run was cancelled); rejects on a synthesis error. */
  done: Promise<void>
  /** Chunk `index` finished playing: the engine may synthesise further ahead. */
  ack(index: number): void
  /** Drop the remaining chunks now (the worker is told at once). */
  cancel(): void
}

export type KokoroChunkHandler = (index: number, samples: Float32Array, rate: number) => void

interface KokoroEngine {
  /** Resolves with the device actually used. */
  load(device: Device, dtype: string): Promise<Device>
  start(chunks: string[], voice: string, onChunk: KokoroChunkHandler, ahead: number): KokoroRun
  dispose(): void
}

async function generateChunk(model: any, text: string, voice: string) {
  const raw = await model.generate(text, { voice })
  const samples = (raw?.audio ?? raw?.data) as Float32Array | undefined
  return samples?.length ? { samples, rate: raw.sampling_rate || 24000 } : null
}

function createInlineEngine(): KokoroEngine {
  let tts: any = null
  return {
    async load(device, dtype) {
      const { KokoroTTS } = await import('kokoro-js')
      tts = await KokoroTTS.from_pretrained('onnx-community/Kokoro-82M-v1.0-ONNX', { dtype: dtype as any, device })
      return device
    },
    start(chunks, voice, onChunk, ahead) {
      const model = tts
      if (!model) return { done: Promise.reject(new Error('Kokoro model not loaded')), ack() {}, cancel() {} }
      const run = runSpeechStream(chunks, text => generateChunk(model, text, voice), (index, r) => onChunk(index, r.samples, r.rate), ahead)
      return { done: run.done, ack: run.ack, cancel: run.cancel }
    },
    dispose() { tts = null },
  }
}

function createWorkerEngine(): KokoroEngine {
  const worker = new Worker(new URL('../workers/kokoro-worker.ts', import.meta.url), { type: 'module' })
  let seq = 0
  type Pending = {
    resolve: (v?: any) => void
    reject: (e: Error) => void
    onChunk?: KokoroChunkHandler
  }
  const pending = new Map<number, Pending>()
  worker.onmessage = (event: MessageEvent<any>) => {
    const m = event.data
    const p = pending.get(m?.id)
    if (!p) return // cancelled or finished: late chunks are dropped
    if (m.type === 'chunk') {
      p.onChunk?.(m.index, m.samples, m.rate)
    } else if (m.type === 'loaded') {
      pending.delete(m.id); p.resolve(m.device)
    } else if (m.type === 'done') {
      pending.delete(m.id); p.resolve()
    } else if (m.type === 'error') {
      pending.delete(m.id); p.reject(new Error(m.message))
    }
  }
  worker.onerror = (event) => {
    const err = new Error(`Kokoro worker error: ${(event as ErrorEvent).message || 'unknown'}`)
    for (const p of pending.values()) p.reject(err)
    pending.clear()
  }
  return {
    load(device, dtype) {
      const id = ++seq
      return new Promise<Device>((resolve, reject) => {
        pending.set(id, { resolve, reject })
        worker.postMessage({ type: 'load', id, device, dtype })
      })
    },
    start(chunks, voice, onChunk, ahead) {
      const id = ++seq
      let finish: () => void = () => {}
      const done = new Promise<void>((resolve, reject) => {
        finish = resolve
        pending.set(id, { resolve, reject, onChunk })
      })
      worker.postMessage({ type: 'stream', id, chunks, voice, ahead })
      return {
        done,
        ack(index) { if (pending.has(id)) worker.postMessage({ type: 'ack', id, index }) },
        cancel() {
          if (!pending.has(id)) return
          pending.delete(id)
          worker.postMessage({ type: 'cancel', id })
          finish()
        },
      }
    },
    dispose() {
      for (const p of pending.values()) p.reject(new Error('Kokoro worker stopped'))
      pending.clear()
      worker.terminate()
    },
  }
}

function createEngine(): KokoroEngine {
  if (typeof Worker !== 'undefined') {
    try { return createWorkerEngine() } catch (e) { console.warn('[Kokoro] Worker unavailable, synthesising inline:', e) }
  }
  return createInlineEngine()
}

// ─── Main composable ─────────────────────────────────────────
export function useKokoroTTS() {
  const status = ref<KokoroStatus>('unloaded')
  const loadProgress = ref(0)
  const error = ref<string | null>(null)
  const selectedVoice = ref('af_sky')

  let engine: KokoroEngine | null = null
  // Set after a WebGPU load/generation failure: the same model and voices on WASM (q8).
  let forceWasm = false
  let currentDevice: Device | null = null

  // Restore voice preference
  if (typeof window !== 'undefined') {
    const saved = localStorage.getItem('kokoroVoice')
    if (saved) selectedVoice.value = saved
  }

  function hasWebGPU(): boolean {
    return typeof navigator !== 'undefined' && !!navigator.gpu
  }

  // ─── Load model ────────────────────────────────────────────
  async function loadModel(): Promise<boolean> {
    if (status.value === 'ready') return true
    if (status.value === 'loading') return false

    status.value = 'loading'
    loadProgress.value = 10
    error.value = null

    try {
      const device: Device = hasWebGPU() && !forceWasm ? 'webgpu' : 'wasm'
      const dtype = device === 'webgpu' ? 'fp32' : 'q8'
      currentDevice = device

      console.log(`[Kokoro] Loading model (device=${device}, dtype=${dtype})...`)
      loadProgress.value = 30

      engine?.dispose()
      engine = createEngine()
      currentDevice = await engine.load(device, dtype)

      loadProgress.value = 100
      status.value = 'ready'
      console.log(`[Kokoro] Model loaded (device=${currentDevice})`)
      return true
    } catch (e: any) {
      console.error('[Kokoro] Failed to load:', e)
      error.value = e.message || 'Failed to load Kokoro model'
      status.value = 'error'
      return false
    }
  }

  // Put the engine back into a loadable state after a failure. When the failed
  // attempt ran on WebGPU, the next load uses WASM (same model, same voices).
  function recover() {
    if (status.value === 'loading') return
    if (currentDevice === 'webgpu') forceWasm = true
    engine?.dispose()
    engine = null
    currentDevice = null
    status.value = 'unloaded'
    loadProgress.value = 0
  }

  // ─── Synthesise chunk by chunk ─────────────────────────────
  // The reply arrives already split into short chunks (utils/speechChunks.ts).
  // Each chunk's audio is handed to `onChunk` as soon as it is synthesised, so
  // playback starts with the first one; the engine stays at most `ahead`
  // chunks ahead of what was acknowledged (played). Returns null when the
  // model could not be loaded.
  async function synthesize(
    chunks: string[],
    onChunk: KokoroChunkHandler,
    options: { voice?: string; ahead?: number } = {},
  ): Promise<KokoroRun | null> {
    // 'generating' means loaded (a previous reply is still being synthesised): never reload the model for that
    if (status.value !== 'ready' && !(status.value === 'generating' && engine)) {
      const ok = await loadModel()
      if (!ok) return null
    }
    if (!engine) return null

    status.value = 'generating'
    const run = engine.start(chunks, options.voice || selectedVoice.value, onChunk, options.ahead ?? DEFAULT_SPEECH_AHEAD)
    const settle = () => { if (status.value === 'generating') status.value = 'ready' }
    run.done.then(settle, settle)
    return run
  }

  function setVoice(voice: string) {
    selectedVoice.value = voice
    if (typeof window !== 'undefined') {
      localStorage.setItem('kokoroVoice', voice)
    }
  }

  function destroy() {
    engine?.dispose()
    engine = null
    forceWasm = false
    currentDevice = null
    status.value = 'unloaded'
    loadProgress.value = 0
  }

  return {
    status,
    loadProgress,
    error,
    selectedVoice,
    hasWebGPU,
    loadModel,
    recover,
    synthesize,
    setVoice,
    destroy,
  }
}

// ─── Singleton ───────────────────────────────────────────────
let _instance: ReturnType<typeof useKokoroTTS> | null = null

export function getKokoroTTS(): ReturnType<typeof useKokoroTTS> {
  if (!_instance) {
    _instance = useKokoroTTS()
  }
  return _instance
}
