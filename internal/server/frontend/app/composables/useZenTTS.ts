/**
 * Zen Mode TTS (Text-to-Speech) Composable
 *
 * Kokoro (browser, 82M model, WebGPU/WASM) is the only voice. If it fails it is
 * retried (reloading the model, WebGPU -> WASM); when every attempt fails the
 * composable stays silent and shows a message. It never falls back to the
 * browser/OS speech synthesis or to a server-side system voice.
 *
 * A reply is split into short chunks (utils/speechChunks.ts, the first one
 * shortest) and synthesised chunk by chunk in the Kokoro worker. The first
 * chunk plays as soon as it is ready; later ones are scheduled back to back on
 * one AudioContext with a short pause between them, while the worker keeps at
 * most a few chunks ahead. Stopping cancels the worker's remaining chunks at
 * once; `fadeOut()` (used when the session changes) fades the voice out first.
 */

import { ref } from 'vue'
import { getKokoroTTS, KOKORO_VOICES, type KokoroVoice, type KokoroRun } from './useKokoroTTS'
import { chunkForSpeech } from '../utils/speechChunks'

// Re-export for UI
export { KOKORO_VOICES }
export type { KokoroVoice }

const VOICE_UNAVAILABLE = 'Voice unavailable, Kokoro could not load.'
const RETRY_DELAYS_MS = [300, 1000, 2500]
/**
 * How long the Kokoro model stays loaded after Zen is left. The model (~330 MB as
 * fp32 on WebGPU, plus the ONNX runtime) used to stay in memory for the rest of the
 * session; coming back within this window keeps it, later it is loaded again.
 */
export const KOKORO_RELEASE_AFTER_MS = 2 * 60_000
/** Longest wait for the first audio of a reading (load + first chunk). */
export const FIRST_AUDIO_TIMEOUT_MS = 30000
/** Longest wait for the next chunk once everything synthesised so far has played. */
export const STALL_TIMEOUT_MS = 30000
/** Pause between chunks (seconds). */
export const SPEECH_GAP_S = 0.12
/** Fade used when the session changes or Zen is left. */
export const SPEECH_FADE_MS = 400
const SPEECH_LEVEL = 0.85

interface GlitchChain { input: AudioNode; output: AudioNode; teardown(): void }

/** One reading: its own gain (for the fade), its scheduled chunks and its worker run. */
interface Reading {
  gain: GainNode
  glitch: GlitchChain | null
  sources: Set<AudioBufferSourceNode>
  nextStart: number
  run: KokoroRun | null
  /** Called when nothing is scheduled any more (or the reading was released). */
  onIdle: (() => void) | null
  released: boolean
}

// ─── Main composable ──────────────────────────────────────────
export function useZenTTS() {
  const isPlaying = ref(false)
  const isLoading = ref(false)
  const retrying = ref(false)
  const error = ref<string | null>(null)
  const reverbEnabled = ref(true)
  const reverbAmount = ref(0.3) // 0–1
  const glitchEnabled = ref(false)

  // Kokoro browser engine
  const kokoro = getKokoroTTS()

  let lastText = ''

  // Web Audio graph: reading gain → (glitch) → dry + reverb → master → speakers
  let audioCtx: AudioContext | null = null
  let reverbNode: ConvolverNode | null = null
  let dryGain: GainNode | null = null
  let wetGain: GainNode | null = null
  let masterOut: GainNode | null = null

  function ensureAudio(): AudioContext {
    if (!audioCtx) {
      audioCtx = new AudioContext()
      masterOut = audioCtx.createGain()
      masterOut.gain.value = SPEECH_LEVEL
      masterOut.connect(audioCtx.destination)

      dryGain = audioCtx.createGain()
      dryGain.connect(masterOut)

      wetGain = audioCtx.createGain()
      wetGain.connect(masterOut)

      // Create reverb impulse
      reverbNode = audioCtx.createConvolver()
      const len = audioCtx.sampleRate * 2.5
      const impulse = audioCtx.createBuffer(2, len, audioCtx.sampleRate)
      for (let ch = 0; ch < 2; ch++) {
        const d = impulse.getChannelData(ch)
        for (let i = 0; i < len; i++) {
          d[i] = (Math.random() * 2 - 1) * Math.pow(1 - i / len, 2.5)
        }
      }
      reverbNode.buffer = impulse
      reverbNode.connect(wetGain)

      updateReverbMix()
    }
    if (audioCtx.state === 'suspended') void audioCtx.resume()
    return audioCtx
  }

  function updateReverbMix() {
    if (!dryGain || !wetGain) return
    if (reverbEnabled.value) {
      dryGain.gain.value = 1 - reverbAmount.value * 0.4
      wetGain.gain.value = reverbAmount.value * 0.6
    } else {
      dryGain.gain.value = 1
      wetGain.gain.value = 0
    }
  }

  // ─── Glitch FX chain ──────────────────────────────────────
  // Ring modulator: multiplies audio by a low-freq oscillator → metallic robot tone
  // Bitcrusher: WaveShaperNode that quantizes signal → digital crunch
  // Micro-stutters: random gain cuts → glitchy audio drops
  // One chain per reading (not per chunk), torn down with the reading.
  function setupGlitchChain(ctx: AudioContext): GlitchChain {
    // Ring modulator — subtle metallic overtone
    const ringModOsc = ctx.createOscillator()
    ringModOsc.type = 'sine'
    ringModOsc.frequency.value = 30 // low rumble modulation
    const ringModGain = ctx.createGain()
    ringModGain.gain.value = 0 // will be modulated
    ringModOsc.connect(ringModGain.gain) // AM synthesis
    ringModOsc.start()

    // Bitcrusher via WaveShaperNode — quantize to fewer levels
    const bitcrusherNode = ctx.createWaveShaper()
    const steps = 32 // fewer = crunchier
    const n = 4096
    const curve = new Float32Array(n)
    for (let i = 0; i < n; i++) {
      const x = (i * 2) / n - 1
      curve[i] = Math.round(x * steps) / steps
    }
    bitcrusherNode.curve = curve
    bitcrusherNode.oversample = 'none'

    // Glitch stutter LFO — random gain modulation
    const glitchLFO = ctx.createOscillator()
    glitchLFO.type = 'square'
    glitchLFO.frequency.value = 0.5 // slow base rate
    const glitchLFOGain = ctx.createGain()
    glitchLFOGain.gain.value = 1.0

    // Wire: input → ringModGain → bitcrusher → glitchLFOGain → output
    ringModGain.connect(bitcrusherNode)
    bitcrusherNode.connect(glitchLFOGain)
    glitchLFO.connect(glitchLFOGain.gain)
    glitchLFO.start()

    // Random glitch bursts — occasionally spike the LFO frequency
    const glitchInterval = setInterval(() => {
      const now = ctx.currentTime
      if (Math.random() < 0.3) {
        // Glitch burst: rapid stutter + pitch ring mod spike
        glitchLFO.frequency.setValueAtTime(8 + Math.random() * 15, now)
        ringModOsc.frequency.setValueAtTime(80 + Math.random() * 200, now)
        // Return to normal after brief burst
        const dur = 0.05 + Math.random() * 0.15
        glitchLFO.frequency.setValueAtTime(0.5, now + dur)
        ringModOsc.frequency.setValueAtTime(30, now + dur)
      }
    }, 200)

    return {
      input: ringModGain,
      output: glitchLFOGain,
      teardown() {
        clearInterval(glitchInterval)
        try { ringModOsc.stop() } catch { /* not started */ }
        try { glitchLFO.stop() } catch { /* not started */ }
        for (const node of [ringModOsc, glitchLFO, ringModGain, bitcrusherNode, glitchLFOGain]) {
          try { node.disconnect() } catch { /* already disconnected */ }
        }
      },
    }
  }

  function createReading(ctx: AudioContext): Reading {
    const gain = ctx.createGain()
    gain.gain.value = 1
    const glitch = glitchEnabled.value ? setupGlitchChain(ctx) : null
    const out: AudioNode = glitch ? glitch.output : gain
    if (glitch) gain.connect(glitch.input)
    // Always through dry + reverb: the reverb toggle only changes their levels
    out.connect(dryGain!)
    out.connect(reverbNode!)
    return { gain, glitch, sources: new Set(), nextStart: 0, run: null, onIdle: null, released: false }
  }

  /** Stops everything a reading scheduled, tells the worker to stop and frees its nodes. */
  function releaseReading(r: Reading) {
    if (r.released) return
    r.released = true
    r.run?.cancel()
    r.run = null
    for (const src of r.sources) {
      src.onended = null
      try { src.stop() } catch { /* not started */ }
      try { src.disconnect() } catch { /* already disconnected */ }
    }
    r.sources.clear()
    try { r.gain.disconnect() } catch { /* already disconnected */ }
    r.glitch?.teardown()
    r.glitch = null
    const idle = r.onIdle
    r.onIdle = null
    idle?.()
  }

  // Restore preferences
  if (typeof window !== 'undefined') {
    const savedKokoroVoice = localStorage.getItem('kokoroVoice')
    if (savedKokoroVoice) kokoro.setVoice(savedKokoroVoice)
    const savedReverb = localStorage.getItem('zenTTSReverb')
    if (savedReverb !== null) reverbEnabled.value = savedReverb !== 'false'
    const savedReverbAmt = localStorage.getItem('zenTTSReverbAmount')
    if (savedReverbAmt) reverbAmount.value = parseFloat(savedReverbAmt)
    const savedGlitch = localStorage.getItem('zenTTSGlitch')
    if (savedGlitch !== null) glitchEnabled.value = savedGlitch === 'true'
  }

  // ─── State of the current reading ──────────────────────────
  let speakGeneration = 0 // monotonic counter: a stale reading's callbacks do nothing
  let current: Reading | null = null
  let clearTimers: (() => void) | null = null
  // Readings fading out (session change); a new reading starts after them.
  const fading = new Map<Reading, { timer: ReturnType<typeof setTimeout>; resolve: () => void }>()
  let fadeUntil = 0 // AudioContext time when the last fade ends

  function resetState() {
    isPlaying.value = false
    isLoading.value = false
    retrying.value = false
    error.value = null
  }

  /** Ends the current reading at once (fades keep going). */
  function stopCurrent() {
    speakGeneration++
    clearTimers?.()
    clearTimers = null
    if (current) releaseReading(current)
    current = null
  }

  // ─── Stop ──────────────────────────────────────────────────
  /** Stops reading immediately, including readings that are fading out. */
  function stop() {
    stopCurrent()
    for (const [r, f] of fading) {
      clearTimeout(f.timer)
      releaseReading(r)
      f.resolve()
    }
    fading.clear()
    fadeUntil = 0
    resetState()
  }

  /**
   * Fades the current reading out (session change, leaving Zen): the worker's
   * remaining chunks are cancelled now, the voice ramps to silence over `ms`,
   * then everything is stopped and released. A reading started meanwhile waits
   * for the fade to end. Music ducking ends at once (`isPlaying` goes false).
   */
  function fadeOut(ms = SPEECH_FADE_MS): Promise<void> {
    const r = current
    current = null
    speakGeneration++
    clearTimers?.()
    clearTimers = null
    resetState()
    if (!r) return Promise.resolve()
    r.run?.cancel()
    r.run = null
    if (!r.sources.size || !audioCtx || ms <= 0) {
      releaseReading(r)
      return Promise.resolve()
    }
    const now = audioCtx.currentTime
    const end = now + ms / 1000
    const g = r.gain.gain
    g.cancelScheduledValues(now)
    g.setValueAtTime(g.value, now)
    g.linearRampToValueAtTime(0, end)
    fadeUntil = Math.max(fadeUntil, end)
    return new Promise<void>((resolve) => {
      const timer = setTimeout(() => {
        fading.delete(r)
        releaseReading(r)
        resolve()
      }, ms)
      fading.set(r, { timer, resolve })
    })
  }

  // ─── Main speak ────────────────────────────────────────────
  async function speak(text: string) {
    if (!text || text.length < 10) return
    const chunks = chunkForSpeech(text)
    if (!chunks.length) return

    // A new reading replaces the current one (a fading one finishes its fade)
    stopCurrent()
    const gen = speakGeneration
    const live = () => speakGeneration === gen

    lastText = text
    resetState()
    isLoading.value = true

    let ctx: AudioContext
    try {
      ctx = ensureAudio()
    } catch (e) {
      console.warn('[TTS] Web Audio unavailable:', e)
      resetState()
      error.value = VOICE_UNAVAILABLE
      return
    }
    const reading = createReading(ctx)
    current = reading

    let received = 0 // chunks delivered so far, across attempts
    let firstAudio = false
    let stallTimer: ReturnType<typeof setTimeout> | null = null
    let failAttempt: ((e: Error) => void) | null = null

    // Safety: only guards the time to the first audio. A long reading that is
    // playing is never cut off by it; a stalled one is caught by the stall timer.
    const firstAudioTimer = setTimeout(() => {
      if (live() && !firstAudio) {
        console.warn('[TTS] No audio after 30 s — giving up')
        stop()
        error.value = VOICE_UNAVAILABLE
      }
    }, FIRST_AUDIO_TIMEOUT_MS)
    const clearStall = () => { if (stallTimer) { clearTimeout(stallTimer); stallTimer = null } }
    clearTimers = () => { clearTimeout(firstAudioTimer); clearStall() }

    // Everything synthesised so far has played and the next chunk is late.
    const armStall = () => {
      clearStall()
      if (!failAttempt) return
      stallTimer = setTimeout(() => failAttempt?.(new Error('Kokoro stopped producing audio')), STALL_TIMEOUT_MS)
    }

    const schedule = (samples: Float32Array, rate: number, ack: () => void) => {
      const buffer = ctx.createBuffer(1, samples.length, rate)
      if (typeof buffer.copyToChannel === 'function') buffer.copyToChannel(samples as Float32Array<ArrayBuffer>, 0)
      else buffer.getChannelData(0).set(samples)
      const src = ctx.createBufferSource()
      src.buffer = buffer
      src.connect(reading.gain)
      const when = Math.max(ctx.currentTime + 0.02, reading.nextStart, fadeUntil)
      reading.nextStart = when + buffer.duration + SPEECH_GAP_S
      reading.sources.add(src)
      src.onended = () => {
        reading.sources.delete(src)
        try { src.disconnect() } catch { /* already disconnected */ }
        ack()
        if (!reading.sources.size) {
          if (failAttempt) armStall()
          reading.onIdle?.()
        }
      }
      src.start(when)
    }

    const onChunkFrom = (base: number) => (index: number, samples: Float32Array, rate: number) => {
      const run = reading.run
      if (!live() || reading.released) return
      const abs = base + index
      if (abs < received) return
      received = abs + 1
      clearStall()
      schedule(samples, rate, () => run?.ack(index))
      if (!firstAudio) {
        firstAudio = true
        clearTimeout(firstAudioTimer)
        isLoading.value = false
        retrying.value = false
        isPlaying.value = true
      }
    }

    // One Kokoro attempt from chunk `base` on. Resolves when the worker has
    // delivered every chunk; rejects on an error or a stall.
    const attempt = async (base: number) => {
      const run = await kokoro.synthesize(chunks.slice(base), onChunkFrom(base))
      if (!run) throw new Error('Kokoro could not load')
      if (!live()) { run.cancel(); return }
      reading.run = run
      try {
        await new Promise<void>((resolve, reject) => {
          failAttempt = (e) => { run.cancel(); reject(e) }
          run.done.then(resolve, reject)
          if (firstAudio && !reading.sources.size) armStall()
        })
      } finally {
        failAttempt = null
        clearStall()
        if (reading.run === run) reading.run = null
      }
    }

    try {
      for (let n = 0; n <= RETRY_DELAYS_MS.length; n++) {
        if (!live()) return
        if (n > 0) {
          retrying.value = true
          if (!firstAudio) isLoading.value = true
          await new Promise<void>((r) => setTimeout(r, RETRY_DELAYS_MS[n - 1]))
          if (!live()) return
          kokoro.recover()
        }
        const before = received
        try {
          await attempt(received)
          if (!live()) return
          if (received > 0) break
        } catch (kokoroErr) {
          console.warn('[TTS] Kokoro error:', kokoroErr)
          if (!live()) return
          if (received >= chunks.length) break
        }
        if (received === before) console.warn(`[TTS] Kokoro attempt ${n + 1} produced no audio`)
      }
    } catch (e) {
      console.warn('[TTS] Error:', e)
    }
    if (!live()) return
    retrying.value = false

    if (received === 0) {
      // Every attempt failed: stay silent, reset state, show the message.
      clearTimers?.()
      clearTimers = null
      releaseReading(reading)
      current = null
      isLoading.value = false
      isPlaying.value = false
      error.value = VOICE_UNAVAILABLE
      return
    }

    // Let the scheduled chunks finish playing.
    if (reading.sources.size && !reading.released) {
      await new Promise<void>((resolve) => { reading.onIdle = resolve })
    }
    if (!live()) return
    clearTimers?.()
    clearTimers = null
    releaseReading(reading)
    current = null
    isPlaying.value = false
    isLoading.value = false
  }

  // Manual retry from the error message: reload the model and speak again.
  async function retry() {
    if (!lastText) return
    kokoro.recover()
    await speak(lastText)
  }

  // ─── Load Kokoro model (user-initiated) ────────────────────
  async function loadKokoroModel() {
    return kokoro.loadModel()
  }

  // ─── Setters ───────────────────────────────────────────────
  function toggleReverb() {
    reverbEnabled.value = !reverbEnabled.value
    updateReverbMix()
    if (typeof window !== 'undefined') localStorage.setItem('zenTTSReverb', String(reverbEnabled.value))
  }

  function setReverbAmount(v: number) {
    reverbAmount.value = Math.max(0, Math.min(1, v))
    updateReverbMix()
    if (typeof window !== 'undefined') localStorage.setItem('zenTTSReverbAmount', String(reverbAmount.value))
  }

  function toggleGlitch() {
    glitchEnabled.value = !glitchEnabled.value
    if (typeof window !== 'undefined') localStorage.setItem('zenTTSGlitch', String(glitchEnabled.value))
  }

  // ─── Leaving / entering Zen ───────────────────────────────
  let releaseTimer: ReturnType<typeof setTimeout> | null = null

  /** Close the read-aloud audio graph (its AudioContext keeps an audio thread running). */
  function closeAudio() {
    if (!audioCtx) return
    try { void audioCtx.close() } catch { /* already closed */ }
    audioCtx = null
    reverbNode = null; dryGain = null; wetGain = null; masterOut = null
    fadeUntil = 0 // a time on the closed context
  }

  let leaveSeq = 0

  /**
   * Zen was left: fade the reading out (cancelling the worker's remaining
   * chunks now), then close the audio context, and unload the voice model after
   * KOKORO_RELEASE_AFTER_MS unless Zen is shown again first. Coming back (or a
   * new reading) before the fade ends keeps the audio context.
   */
  function leave(releaseAfterMs = KOKORO_RELEASE_AFTER_MS, fadeMs = SPEECH_FADE_MS) {
    const seq = ++leaveSeq
    void fadeOut(fadeMs).then(() => {
      if (seq === leaveSeq && !current) closeAudio()
    })
    if (releaseTimer) clearTimeout(releaseTimer)
    releaseTimer = null
    if (kokoro.status.value === 'unloaded') return
    const release = () => {
      releaseTimer = null
      // Never cut a load short (it would end in an error state): try again later
      if (kokoro.status.value === 'loading') { releaseTimer = setTimeout(release, releaseAfterMs); return }
      kokoro.destroy()
    }
    releaseTimer = setTimeout(release, releaseAfterMs)
  }

  /** Zen is shown again: keep the voice model. */
  function enter() {
    leaveSeq++ // a pending close after the leave fade is called off
    if (releaseTimer) clearTimeout(releaseTimer)
    releaseTimer = null
  }

  function destroy() {
    stop()
    leaveSeq++
    if (releaseTimer) { clearTimeout(releaseTimer); releaseTimer = null }
    kokoro.destroy()
    closeAudio()
  }

  return {
    isPlaying,
    isLoading,
    retrying,
    error,
    reverbEnabled,
    reverbAmount,
    glitchEnabled,
    // Kokoro state
    kokoro,
    // Methods
    speak,
    stop,
    fadeOut,
    retry,
    toggleReverb,
    setReverbAmount,
    toggleGlitch,
    loadKokoroModel,
    leave,
    enter,
    destroy,
  }
}

// ─── Singleton ────────────────────────────────────────────────
let _instance: ReturnType<typeof useZenTTS> | null = null

export function getZenTTS(): ReturnType<typeof useZenTTS> {
  if (!_instance) _instance = useZenTTS()
  return _instance
}
