/**
 * Web Worker for Kokoro TTS (kokoro-js, ONNX Runtime Web).
 *
 * Synthesis used to run on the page's main thread. Without WebGPU (the desktop
 * app's WKWebView, or after the WebGPU -> WASM fallback) each sentence is a
 * single-threaded WASM inference that blocks the thread for a second or more,
 * so auto read-aloud of a long reply froze the page (no input, no rendering,
 * no WebSocket reads). Here the inference runs off the main thread and only
 * the PCM samples come back (transferred, not copied).
 *
 * The page sends the reply already split into short chunks
 * (utils/speechChunks.ts), and each chunk's audio is posted as soon as it is
 * synthesised. kokoro-js' own `stream(text)` is not used: it never closes its
 * sentence splitter, so it drops the last sentence and never finishes, which
 * blocked every later reading behind it.
 *
 * Protocol (see useKokoroTTS.ts):
 *   in:  {type:'load', id, device, dtype}
 *        | {type:'stream', id, chunks, voice, ahead}   one inference at a time, in order
 *        | {type:'ack', id, index}                     chunk played: synthesise further ahead
 *        | {type:'cancel', id}                         drop the remaining chunks now
 *   out: {type:'loaded', id, device} | {type:'chunk', id, index, samples, rate} | {type:'done', id}
 *        | {type:'error', id, message}
 */

import { runSpeechStream, type SpeechStreamRun } from '../utils/speechStream'

type In =
  | { type: 'load'; id: number; device: 'webgpu' | 'wasm'; dtype: string }
  | { type: 'stream'; id: number; chunks: string[]; voice: string; ahead?: number }
  | { type: 'ack'; id: number; index: number }
  | { type: 'cancel'; id: number }

let tts: any = null
// Streams waiting for their turn (cancelled ones are skipped) and the one running.
const cancelled = new Set<number>()
const runs = new Map<number, SpeechStreamRun>()
let queue: Promise<void> = Promise.resolve()

const post = (msg: unknown, transfer: Transferable[] = []) =>
  (self as unknown as { postMessage(m: unknown, t: Transferable[]): void }).postMessage(msg, transfer)

async function load(id: number, device: 'webgpu' | 'wasm', dtype: string) {
  try {
    // WebGPU may exist on the page but not in workers: use WASM then.
    const nav = (self as unknown as { navigator?: { gpu?: unknown } }).navigator
    const dev = device === 'webgpu' && nav?.gpu ? 'webgpu' : 'wasm'
    const dt = dev === 'webgpu' ? dtype : 'q8'
    const { KokoroTTS } = await import('kokoro-js')
    tts = await KokoroTTS.from_pretrained('onnx-community/Kokoro-82M-v1.0-ONNX', { dtype: dt as any, device: dev })
    post({ type: 'loaded', id, device: dev })
  } catch (e: any) {
    tts = null
    post({ type: 'error', id, message: e?.message || String(e) })
  }
}

async function stream(id: number, chunks: string[], voice: string, ahead?: number) {
  if (cancelled.has(id)) { cancelled.delete(id); post({ type: 'done', id }); return }
  if (!tts) { post({ type: 'error', id, message: 'Kokoro model not loaded' }); return }
  const model = tts
  const run = runSpeechStream(
    chunks,
    async (text) => {
      const raw = await model.generate(text, { voice })
      const samples = (raw?.audio ?? raw?.data) as Float32Array | undefined
      return samples?.length ? { samples, rate: raw.sampling_rate || 24000 } : null
    },
    (index, { samples, rate }) => {
      const copy = new Float32Array(samples) // own buffer, safe to transfer
      post({ type: 'chunk', id, index, samples: copy, rate }, [copy.buffer])
    },
    ahead,
  )
  runs.set(id, run)
  try {
    await run.done
    post({ type: 'done', id })
  } catch (e: any) {
    post({ type: 'error', id, message: e?.message || String(e) })
  } finally {
    runs.delete(id)
    cancelled.delete(id)
  }
}

self.onmessage = (event: MessageEvent<In>) => {
  const msg = event.data
  if (msg.type === 'load') void load(msg.id, msg.device, msg.dtype)
  else if (msg.type === 'stream') {
    // One inference at a time (an ONNX session cannot run twice at once);
    // a request cancelled while queued finishes at once.
    queue = queue.then(() => stream(msg.id, msg.chunks, msg.voice, msg.ahead))
  } else if (msg.type === 'ack') runs.get(msg.id)?.ack(msg.index)
  else if (msg.type === 'cancel') {
    const run = runs.get(msg.id)
    if (run) run.cancel()
    else cancelled.add(msg.id)
  }
}
