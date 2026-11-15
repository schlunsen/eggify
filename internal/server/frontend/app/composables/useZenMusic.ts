/**
 * Zen Mode Background Music
 *
 * Two sources: the looping music from the Eggify reel (default, public/zen/audio/reel.mp3)
 * and a generated ambient piano-like arpeggio (Web Audio API), which is also the
 * fallback if the reel file cannot be loaded. Designed to play behind TTS
 * reading without competing for attention (it ducks while TTS speaks).
 * An analyser tap on the output feeds `readBeat()`, which the Circus egg
 * dances to.
 *
 * Features:
 * - Arpeggiated chord progressions (I -> vi -> IV -> V)
 * - Soft piano-like tones with reverb
 * - Randomized timing for natural feel
 * - Independent volume control
 * - localStorage persistence
 */

import { ref } from 'vue'
import { beatStateAt, isBeatGrid, outputLatencySeconds, SILENT_CLOCK, type BeatClock, type BeatGrid } from '~/utils/beatClock'

// Note frequencies for chord tones
const CHORDS = [
  // C major: C4, E4, G4, C5
  [261.63, 329.63, 392.00, 523.25],
  // A minor: A3, C4, E4, A4
  [220.00, 261.63, 329.63, 440.00],
  // F major: F3, A3, C4, F4
  [174.61, 220.00, 261.63, 349.23],
  // G major: G3, B3, D4, G4
  [196.00, 246.94, 293.66, 392.00],
]

export const REEL_URL = '/zen/audio/reel.mp3'
/** Looping tracks for the "reel" source. A different one starts each time the music does. */
export const ZEN_TRACKS = [
  REEL_URL,
  '/zen/audio/spyfunk-groove.mp3',
  '/zen/audio/spyfunk-groove2.mp3',
  '/zen/audio/spyfunk-stakeout.mp3',
  '/zen/audio/spyfunk-chase.mp3',
]
export type ZenMusicSource = 'reel' | 'ambient'
const DUCK_LEVEL = 0.35
export const DEFAULT_VOLUME = 0.1
const BAG_KEY = 'zenMusicBag'

export function useZenMusic() {
  // Music is part of the sound mix, which starts on: it plays when Zen opens,
  // unless the music (or sound) was switched off.
  const isEnabled = ref(true)
  // Moderate by default (about -20 dB on the master gain, so the track sits well below
  // speech and normal listening); a saved volume always wins.
  const volume = ref(DEFAULT_VOLUME)
  const isPlaying = ref(false)
  const source = ref<ZenMusicSource>('reel')

  // Restore from localStorage
  if (typeof window !== 'undefined') {
    try {
      const saved = localStorage.getItem('zenMusicEnabled')
      if (saved === 'false') isEnabled.value = false
      const savedVol = localStorage.getItem('zenMusicVolume')
      const v = savedVol ? parseFloat(savedVol) : NaN
      if (Number.isFinite(v)) volume.value = Math.max(0, Math.min(1, v))
      if (localStorage.getItem('zenMusicSource') === 'ambient') source.value = 'ambient'
    } catch { /* storage unavailable */ }
  }
  let ducked = false
  const targetGain = () => volume.value * (ducked ? DUCK_LEVEL : 1)

  let ctx: AudioContext | null = null
  let masterGain: GainNode | null = null
  let reverb: ConvolverNode | null = null
  let reverbGain: GainNode | null = null
  let dryGain: GainNode | null = null
  let reelGain: GainNode | null = null
  let analyser: AnalyserNode | null = null
  let reelEl: HTMLAudioElement | null = null
  /** The playing element's graph node: disconnected when the element goes, or every track leaked one. */
  let reelSource: AudioNode | null = null
  let reelStopTimer: ReturnType<typeof setTimeout> | null = null
  /** Pending teardown after leaving Zen (the music fades out first). */
  let leaveTimer: ReturnType<typeof setTimeout> | null = null
  let lastTrack = ''
  /** The file playing now, and the tempo grids loaded so far (null = none for that file) */
  let currentTrackUrl = ''
  const grids = new Map<string, BeatGrid | null>()
  let bag: string[] = []
  try {
    const saved = JSON.parse(sessionStorage.getItem(BAG_KEY) || 'null')
    if (saved && Array.isArray(saved.bag)) {
      bag = saved.bag.filter((t: unknown) => typeof t === 'string' && ZEN_TRACKS.includes(t))
      if (typeof saved.lastTrack === 'string') lastTrack = saved.lastTrack
    }
  } catch { /* storage unavailable or corrupt: start a fresh bag */ }
  let schedulerInterval: ReturnType<typeof setInterval> | null = null
  let currentChord = 0
  let noteIndex = 0
  let nextNoteTime = 0

  function ensureContext(): AudioContext {
    if (!ctx) {
      ctx = new AudioContext()

      masterGain = ctx.createGain()
      masterGain.gain.value = volume.value
      masterGain.connect(ctx.destination)

      // Beat tap: both sources feed it (it is not connected to the output)
      analyser = ctx.createAnalyser()
      analyser.fftSize = 512
      analyser.smoothingTimeConstant = 0.35
      reelGain = ctx.createGain()
      reelGain.gain.value = 1
      reelGain.connect(masterGain)
      reelGain.connect(analyser)

      // Dry path
      dryGain = ctx.createGain()
      dryGain.gain.value = 0.3
      dryGain.connect(masterGain)
      dryGain.connect(analyser)

      // Reverb path — lush, long tail
      reverb = ctx.createConvolver()
      const len = ctx.sampleRate * 3.5
      const impulse = ctx.createBuffer(2, len, ctx.sampleRate)
      for (let ch = 0; ch < 2; ch++) {
        const d = impulse.getChannelData(ch)
        for (let i = 0; i < len; i++) {
          d[i] = (Math.random() * 2 - 1) * Math.pow(1 - i / len, 2.0)
        }
      }
      reverb.buffer = impulse

      reverbGain = ctx.createGain()
      reverbGain.gain.value = 0.7
      reverb.connect(reverbGain)
      reverbGain.connect(masterGain)
    }
    if (ctx.state === 'suspended') ctx.resume()
    return ctx
  }

  function playNote(freq: number, time: number) {
    if (!ctx || !dryGain || !reverb) return

    const osc = ctx.createOscillator()
    osc.type = 'sine'
    osc.frequency.value = freq

    // Soft harmonic — subtle overtone
    const osc2 = ctx.createOscillator()
    osc2.type = 'sine'
    osc2.frequency.value = freq * 2

    const env = ctx.createGain()
    env.gain.setValueAtTime(0, time)
    env.gain.linearRampToValueAtTime(0.12, time + 0.02) // quick attack
    env.gain.exponentialRampToValueAtTime(0.001, time + 2.5) // slow decay

    const env2 = ctx.createGain()
    env2.gain.setValueAtTime(0, time)
    env2.gain.linearRampToValueAtTime(0.03, time + 0.015)
    env2.gain.exponentialRampToValueAtTime(0.001, time + 1.5)

    // Lowpass to soften
    const filter = ctx.createBiquadFilter()
    filter.type = 'lowpass'
    filter.frequency.value = 2000
    filter.Q.value = 0.5

    osc.connect(env)
    osc2.connect(env2)
    env.connect(filter)
    env2.connect(filter)
    filter.connect(dryGain)
    filter.connect(reverb)

    osc.start(time)
    osc.stop(time + 3)
    osc2.start(time)
    osc2.stop(time + 2)
  }

  function scheduleNotes() {
    if (!ctx || !isPlaying.value) return

    const now = ctx.currentTime

    // Schedule notes ahead (look-ahead scheduling)
    while (nextNoteTime < now + 0.2) {
      const chord = CHORDS[currentChord]
      const freq = chord[noteIndex % chord.length]

      // Humanize timing slightly
      const humanize = (Math.random() - 0.5) * 0.04
      playNote(freq, nextNoteTime + humanize)

      noteIndex++

      // Move to next note — tempo ~100-140ms between arpeggio notes
      const baseInterval = 0.3 + Math.random() * 0.25
      nextNoteTime += baseInterval

      // After playing through chord tones, advance to next chord
      if (noteIndex >= chord.length) {
        noteIndex = 0
        currentChord = (currentChord + 1) % CHORDS.length
        // Longer pause between chords
        nextNoteTime += 0.8 + Math.random() * 1.2
      }
    }
  }

  function startAmbient(audioCtx: AudioContext) {
    currentChord = 0
    noteIndex = 0
    nextNoteTime = audioCtx.currentTime + 0.1
    schedulerInterval = setInterval(scheduleNotes, 100)
  }

  function stopReelElement() {
    if (reelStopTimer) { clearTimeout(reelStopTimer); reelStopTimer = null }
    if (reelSource) {
      try { reelSource.disconnect() } catch { /* already disconnected */ }
      reelSource = null
    }
    if (reelEl) {
      try { reelEl.pause() } catch { /* already gone */ }
      reelEl.removeAttribute('src')
      try { reelEl.load() } catch { /* release the decoder and network buffers */ }
      reelEl = null
    }
  }

  /**
   * The next track, as a shuffle bag: every track plays once, in random order, before any repeats,
   * and a new bag never starts with the track that just played. The bag survives a reload
   * (sessionStorage) so reopening Zen does not restart the same few tracks.
   */
  function pickTrack(): string {
    if (!bag.length) {
      bag = [...ZEN_TRACKS]
      for (let i = bag.length - 1; i > 0; i--) {
        const j = Math.floor(Math.random() * (i + 1))
        ;[bag[i], bag[j]] = [bag[j]!, bag[i]!]
      }
      // Don't open the new round with the track that just played.
      if (bag.length > 1 && bag[bag.length - 1] === lastTrack) {
        ;[bag[0], bag[bag.length - 1]] = [bag[bag.length - 1]!, bag[0]!]
      }
    }
    const next = bag.pop() ?? REEL_URL
    lastTrack = next
    try { sessionStorage.setItem(BAG_KEY, JSON.stringify({ bag, lastTrack })) } catch { /* storage unavailable */ }
    return next
  }

  /** Fetch <track>.beats.json once; a missing or malformed file leaves the live detector in charge. */
  function loadGrid(url: string) {
    if (grids.has(url) || typeof fetch === 'undefined') return
    grids.set(url, null)
    fetch(url.replace(/\.[a-z0-9]+$/i, '.beats.json'))
      .then(r => (r.ok ? r.json() : null))
      .then(j => { if (isBeatGrid(j)) grids.set(url, j) })
      .catch(() => { /* no grid: live detection */ })
  }

  function startReel(audioCtx: AudioContext) {
    stopReelElement()
    try {
      currentTrackUrl = pickTrack()
      loadGrid(currentTrackUrl)
      const el = new Audio(currentTrackUrl)
      el.loop = false
      el.preload = 'auto'
      // When a track ends, move on to the next one in the shuffle.
      el.addEventListener('ended', () => {
        if (reelEl === el && isPlaying.value && ctx) startReel(ctx)
      }, { once: true })
      // If the file is missing or cannot be decoded, fall back to the synth.
      el.addEventListener('error', () => {
        if (reelEl !== el || !isPlaying.value) return
        stopReelElement()
        if (ctx) startAmbient(ctx)
      }, { once: true })
      const node = audioCtx.createMediaElementSource(el)
      node.connect(reelGain!)
      reelSource = node
      reelEl = el
      void el.play().catch(() => {
        // Autoplay refused or load failed: use the synth instead.
        if (reelEl === el && isPlaying.value && ctx) { stopReelElement(); startAmbient(ctx) }
      })
    } catch {
      startAmbient(audioCtx)
    }
  }

  function start() {
    // Back before a fade-out finished (Zen left and re-entered, or toggled off
    // and on): keep the context and the fading track and fade it back in,
    // instead of starting a second one.
    if (leaveTimer) { clearTimeout(leaveTimer); leaveTimer = null }
    const fading = !isPlaying.value && !!ctx && !!reelEl && !!reelStopTimer && source.value === 'reel'
    const audioCtx = ensureContext()
    if (isPlaying.value) return

    isPlaying.value = true

    // Fade in master
    if (masterGain) {
      const now = audioCtx.currentTime
      masterGain.gain.cancelScheduledValues(now)
      masterGain.gain.setValueAtTime(fading ? masterGain.gain.value : 0, now)
      masterGain.gain.linearRampToValueAtTime(targetGain(), now + (fading ? 0.8 : 2))
    }

    if (fading) {
      if (reelStopTimer) { clearTimeout(reelStopTimer); reelStopTimer = null }
      if (reelEl?.paused) void reelEl.play().catch(() => { /* resumed by the next start */ })
      return
    }
    if (source.value === 'reel') startReel(audioCtx)
    else startAmbient(audioCtx)
  }

  /** Fade the master gain to 0 over `fadeMs`, then stop the file (unless music started again). */
  function stop(fadeMs = 1500) {
    isPlaying.value = false
    if (schedulerInterval) {
      clearInterval(schedulerInterval)
      schedulerInterval = null
    }
    // Fade out
    if (ctx && masterGain) {
      const now = ctx.currentTime
      masterGain.gain.cancelScheduledValues(now)
      masterGain.gain.setValueAtTime(masterGain.gain.value, now)
      masterGain.gain.linearRampToValueAtTime(0, now + fadeMs / 1000)
    }
    // Let the fade finish, then stop the file
    if (reelEl) {
      if (reelStopTimer) clearTimeout(reelStopTimer)
      reelStopTimer = setTimeout(() => { reelStopTimer = null; if (!isPlaying.value) stopReelElement() }, fadeMs + 200)
    }
  }

  /** How long leaving Zen fades the music out before it is released. */
  const LEAVE_FADE_MS = 1800

  /**
   * Leave Zen: stop following the view, fade the music out, and only then
   * pause and release the element, its graph node and the AudioContext.
   * Coming back during the fade (arm/start) cancels the release and fades the
   * same track back in. destroy() is the immediate version.
   */
  function leave(fadeMs = LEAVE_FADE_MS) {
    disarm()
    if (isPlaying.value) interrupted = true
    if (!ctx) return
    if (!isPlaying.value && !reelEl) { release(); return }
    stop(fadeMs)
    if (leaveTimer) clearTimeout(leaveTimer)
    leaveTimer = setTimeout(() => {
      leaveTimer = null
      if (!isPlaying.value) release()
    }, fadeMs + 250)
  }

  /** Free everything: the element, its node and the context. */
  function release() {
    if (leaveTimer) { clearTimeout(leaveTimer); leaveTimer = null }
    stopReelElement()
    if (ctx) {
      try { void ctx.close() } catch { /* already closed */ }
      ctx = null
    }
  }

  /** Switch between the reel's track and the generated ambient music. */
  function setSource(next: ZenMusicSource) {
    if (source.value === next) return
    source.value = next
    persist('zenMusicSource', next)
    if (!isPlaying.value || !ctx) return
    if (schedulerInterval) { clearInterval(schedulerInterval); schedulerInterval = null }
    stopReelElement()
    if (next === 'reel') startReel(ctx)
    else startAmbient(ctx)
  }

  /** Lower the music while something is speaking over it. */
  function duck(on: boolean) {
    ducked = on
    if (masterGain && ctx && isPlaying.value) {
      const now = ctx.currentTime
      masterGain.gain.cancelScheduledValues(now)
      masterGain.gain.setValueAtTime(masterGain.gain.value, now)
      masterGain.gain.linearRampToValueAtTime(targetGain(), now + 0.4)
    }
  }

  // ─── Beat detection (for the dancing egg) ────────────────────
  const spectrum = new Uint8Array(256)
  let avgEnergy = 0
  let sinceBeat = 99
  let level = 0

  /**
   * Poll from an animation tick. `level` is the smoothed low-frequency energy
   * (0..1) and `beat` is true on the poll where a new beat was detected.
   */
  function readBeat(): { playing: boolean; level: number; beat: boolean } {
    if (!isPlaying.value || !analyser || !ctx || ctx.state !== 'running') {
      level = 0; avgEnergy = 0
      return { playing: false, level: 0, beat: false }
    }
    analyser.getByteFrequencyData(spectrum)
    // Bass and low mids: about 90-560 Hz
    let sum = 0
    for (let i = 1; i <= 6; i++) sum += spectrum[i]!
    const energy = sum / 6
    sinceBeat++
    const beat = energy > 40 && energy > avgEnergy * 1.22 + 6 && sinceBeat >= 3
    if (beat) sinceBeat = 0
    avgEnergy = avgEnergy === 0 ? energy : avgEnergy * 0.9 + energy * 0.1
    level = level * 0.6 + Math.min(1, energy / 200) * 0.4
    return { playing: true, level, beat }
  }

  // Live fallback clock: the analyser's onsets stand in for a grid
  let liveIndex = 0
  let liveLastBeat = 0
  let liveInterval = 0.5

  /**
   * Where we are in the music right now. With a tempo grid for the playing
   * track this is exact: the element's currentTime minus the output latency.
   * Without one (ambient synth, grid still loading or missing) it follows the
   * analyser's onsets. Silent when music is not playing.
   */
  function beatClock(): BeatClock {
    if (!isPlaying.value || !ctx || ctx.state !== 'running') return SILENT_CLOCK
    const grid = grids.get(currentTrackUrl) ?? null
    if (grid && reelEl && !reelEl.paused && source.value === 'reel') {
      const s = beatStateAt(grid, reelEl.currentTime, outputLatencySeconds(ctx))
      return { ...s, playing: true, source: 'grid', track: currentTrackUrl }
    }
    const r = readBeat()
    if (!r.playing) return SILENT_CLOCK
    const now = performance.now() / 1000
    if (r.beat) {
      if (liveLastBeat > 0) {
        const d = now - liveLastBeat
        if (d > 0.25 && d < 1.2) liveInterval = liveInterval * 0.7 + d * 0.3
      }
      liveLastBeat = now
      liveIndex++
    }
    const since = liveLastBeat ? now - liveLastBeat : 0
    const phase = Math.min(0.999, since / liveInterval)
    return {
      playing: true, source: 'live', track: currentTrackUrl,
      beatIndex: liveIndex, phase, beatInBar: liveIndex % 4, bar: Math.floor(liveIndex / 4),
      toNextBeat: Math.max(0, liveInterval - since), toNextDownbeat: Math.max(0, liveInterval * (4 - (liveIndex % 4)) - since),
      bpm: 60 / liveInterval,
    }
  }

  function toggle() {
    isEnabled.value = !isEnabled.value
    persist('zenMusicEnabled', String(isEnabled.value))

    if (isEnabled.value) {
      start()
    } else {
      stop()
    }
  }

  function setVolume(v: number) {
    volume.value = Math.max(0, Math.min(1, v))
    if (masterGain && ctx) {
      const now = ctx.currentTime
      masterGain.gain.cancelScheduledValues(now)
      masterGain.gain.setValueAtTime(masterGain.gain.value, now)
      masterGain.gain.linearRampToValueAtTime(targetGain(), now + 0.1)
    }
    persist('zenMusicVolume', String(volume.value))
  }

  function persist(key: string, value: string) {
    if (typeof window !== 'undefined') {
      localStorage.setItem(key, value)
    }
  }

  // Set when destroy() cut off playing music, so the Zen view can bring it
  // back the next time it is shown.
  let interrupted = false

  function destroy() {
    disarm()
    if (isPlaying.value) interrupted = true
    stop()
    release()
  }

  // ─── Autostart (autoplay policy, hidden tabs) ─────────────────
  let wanted: () => boolean = () => false
  let armed = false
  let gestureListening = false
  let stateWatched: AudioContext | null = null
  let pausedWhileHidden = false
  const GESTURES = ['pointerdown', 'keydown'] as const

  function onGesture() {
    removeGestureListeners()
    try { startIfWanted(true) } catch { /* never throw from an input handler */ }
  }
  function addGestureListeners() {
    if (gestureListening || typeof window === 'undefined') return
    gestureListening = true
    for (const t of GESTURES) window.addEventListener(t, onGesture, { capture: true, passive: true })
  }
  function removeGestureListeners() {
    if (!gestureListening || typeof window === 'undefined') return
    gestureListening = false
    for (const t of GESTURES) window.removeEventListener(t, onGesture, { capture: true } as EventListenerOptions)
  }
  function onVisibility() {
    try {
      if (document.hidden) {
        if (isPlaying.value) { pausedWhileHidden = true; stop() }
      } else if (pausedWhileHidden) {
        pausedWhileHidden = false
        startIfWanted()
      }
    } catch { /* ignore */ }
  }

  /**
   * Start the music if the mix wants it. Browsers keep an AudioContext suspended until a user
   * gesture; then wait for the first pointer/key interaction (or the context becoming
   * runnable, as in the desktop webview) instead of starting a file that would be refused.
   */
  function startIfWanted(fromGesture = false) {
    if (!armed || isPlaying.value || !isEnabled.value || !wanted()) return
    if (typeof document !== 'undefined' && document.hidden) { pausedWhileHidden = true; return }
    try {
      const c = ensureContext()
      if (fromGesture || c.state === 'running') {
        removeGestureListeners()
        start()
        return
      }
      addGestureListeners()
      // One statechange listener per context (this runs on every arm/visibility change)
      if (stateWatched === c) return
      stateWatched = c
      const onState = () => {
        if (ctx !== c) { c.removeEventListener?.('statechange', onState); return }
        if (c.state === 'running') {
          c.removeEventListener?.('statechange', onState)
          if (stateWatched === c) stateWatched = null
          startIfWanted()
        }
      }
      c.addEventListener?.('statechange', onState)
    } catch {
      // No Web Audio at all: stay silent.
    }
  }

  /** Call when Zen is shown: music will start now or on the first gesture, and follow tab visibility. */
  function arm(isWanted: () => boolean) {
    wanted = isWanted
    if (!armed) {
      armed = true
      if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility)
    }
    startIfWanted()
  }

  function disarm() {
    armed = false
    pausedWhileHidden = false
    removeGestureListeners()
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility)
  }

  function resumeIfInterrupted() {
    if (!interrupted) return
    interrupted = false
    if (isEnabled.value) start()
  }

  return {
    isEnabled,
    isPlaying,
    volume,
    source,
    setSource,
    duck,
    readBeat,
    beatClock,
    toggle,
    start,
    stop,
    setVolume,
    resumeIfInterrupted,
    arm,
    startIfWanted,
    destroy,
    leave,
  }
}

// Singleton
let _instance: ReturnType<typeof useZenMusic> | null = null
export function getZenMusic(): ReturnType<typeof useZenMusic> {
  if (!_instance) _instance = useZenMusic()
  return _instance
}
