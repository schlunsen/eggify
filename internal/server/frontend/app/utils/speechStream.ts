/**
 * Synthesises speech chunks one after another with bounded look-ahead.
 *
 * Used by the Kokoro worker (and the inline fallback engine). Each chunk is
 * emitted as soon as it is synthesised; at most `ahead` chunks may be emitted
 * but not yet acknowledged (played), so a long reply never piles up audio
 * buffers. `cancel()` stops before the next chunk (an inference that already
 * started cannot be interrupted, its result is dropped).
 */

export interface SynthResult { samples: Float32Array; rate: number }
export type SynthFn = (text: string) => Promise<SynthResult | null>

export interface SpeechStreamRun {
  /** Resolves when every chunk was emitted or the run was cancelled; rejects on a synthesis error. */
  done: Promise<void>
  /** The consumer finished with chunk `index` (played or dropped). */
  ack(index: number): void
  cancel(): void
  readonly cancelled: boolean
}

export const DEFAULT_SPEECH_AHEAD = 3

export function runSpeechStream(
  chunks: string[],
  synth: SynthFn,
  emit: (index: number, result: SynthResult) => void,
  ahead = DEFAULT_SPEECH_AHEAD,
): SpeechStreamRun {
  let cancelled = false
  let consumed = 0 // chunks acknowledged
  const acked = new Set<number>()
  let wake: (() => void) | null = null
  const notify = () => { const w = wake; wake = null; w?.() }

  const done = (async () => {
    for (let i = 0; i < chunks.length; i++) {
      while (!cancelled && i - consumed >= Math.max(1, ahead)) {
        await new Promise<void>((resolve) => { wake = resolve })
      }
      if (cancelled) return
      const result = await synth(chunks[i]!)
      if (cancelled) return
      if (result && result.samples.length) emit(i, result)
      else acknowledge(i) // nothing to play: does not count against look-ahead
    }
  })()

  function acknowledge(index: number) {
    if (acked.has(index)) return
    acked.add(index)
    while (acked.has(consumed)) consumed++
    notify()
  }

  return {
    done,
    ack: acknowledge,
    cancel() { cancelled = true; notify() },
    get cancelled() { return cancelled },
  }
}
