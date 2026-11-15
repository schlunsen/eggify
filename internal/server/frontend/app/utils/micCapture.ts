/**
 * Helpers for microphone capture that behave the same in Chrome and in the
 * desktop app's WKWebView (Safari engine).
 */

/** Container types to try for MediaRecorder, best first. Safari has no audio/webm before 18.4. */
const RECORDER_MIME_TYPES = ['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4', 'audio/ogg;codecs=opus']

/**
 * A MediaRecorder mime type this webview supports. '' lets the browser choose
 * its default (always valid), which is what Safari needs when nothing matches.
 */
export function pickRecorderMimeType(
  recorder: { isTypeSupported?: (t: string) => boolean } | undefined = typeof MediaRecorder !== 'undefined' ? MediaRecorder : undefined
): string {
  if (!recorder || typeof recorder.isTypeSupported !== 'function') return ''
  for (const t of RECORDER_MIME_TYPES) {
    try {
      if (recorder.isTypeSupported(t)) return t
    } catch { /* try the next one */ }
  }
  return ''
}

/**
 * True in Safari and the desktop app's WKWebView (WebKit), false in Chrome,
 * Edge and Firefox (whose UAs also say "AppleWebKit"/"Gecko").
 */
export function isWebKitEngine(ua: string = typeof navigator !== 'undefined' ? navigator.userAgent : ''): boolean {
  return /AppleWebKit/.test(ua) && !/(Chrome|Chromium|CriOS|Edg|OPR|Firefox|FxiOS)\//.test(ua)
}

/**
 * Audio constraints for dictation.
 *
 * WebKit (Safari, the desktop app's WKWebView) with echo cancellation on runs
 * the input through Apple's voice processing, which only reads the device's
 * first input channel. On a multi-input interface (e.g. a UA Volt 2 with the
 * mic on input 2) that is pure digital silence: the recording "works" and
 * Whisper hallucinates "you". With processing off WebKit delivers every input
 * channel, and callers mix them down with `mixToMono`. Chrome mixes the
 * inputs itself, so it keeps the requested processing.
 */
export function dictationAudioConstraints(
  preferred: MediaTrackConstraints,
  webkit: boolean = isWebKitEngine()
): MediaTrackConstraints {
  if (!webkit) return preferred
  const { echoCancellation: _ec, noiseSuppression: _ns, autoGainControl: _agc, channelCount: _cc, sampleRate: _sr, ...rest } = preferred
  return {
    ...rest,
    echoCancellation: false,
    noiseSuppression: false,
    autoGainControl: false,
    channelCount: { ideal: 2 },
  }
}

/** Channels quieter than this fraction of the loudest one count as unplugged. */
const SILENT_CHANNEL_RATIO = 0.05

/**
 * Mix the channels into one. A mic on input 2 of a stereo interface lands in
 * channel 1 only, so reading channel 0 alone would give silence. Channels that
 * are near-silent next to the loudest (an empty input) are left out, so they
 * don't halve the level; the rest are averaged.
 */
export function mixToMono(channels: Float32Array[]): Float32Array {
  if (channels.length === 0) return new Float32Array(0)
  if (channels.length === 1) return channels[0]!
  const len = Math.min(...channels.map(c => c.length))
  const peaks = channels.map((ch) => {
    let p = 0
    for (let i = 0; i < len; i++) { const a = Math.abs(ch[i]!); if (a > p) p = a }
    return p
  })
  const loudest = Math.max(...peaks)
  const used = channels.filter((_, c) => peaks[c]! >= loudest * SILENT_CHANNEL_RATIO)
  if (used.length === 1) return used[0]!.length === len ? used[0]! : used[0]!.slice(0, len)
  const out = new Float32Array(len)
  const n = used.length
  for (const ch of used) {
    for (let i = 0; i < len; i++) out[i]! += ch[i]! / n
  }
  return out
}

/** All channels of an AudioBuffer mixed to mono. */
export function audioBufferToMono(buf: { numberOfChannels: number; getChannelData(c: number): Float32Array }): Float32Array {
  const channels: Float32Array[] = []
  for (let c = 0; c < buf.numberOfChannels; c++) channels.push(buf.getChannelData(c))
  return mixToMono(channels)
}

/** Why getUserMedia is unavailable, or null when it can be called. */
export function micUnavailableReason(): string | null {
  if (typeof navigator === 'undefined' || !navigator.mediaDevices || typeof navigator.mediaDevices.getUserMedia !== 'function') {
    return 'This window cannot use the microphone (it needs a secure page such as https:// or localhost).'
  }
  return null
}

/** A message a person can act on, instead of "Permission denied" or "Requested device not found". */
export function describeMicError(err: unknown): string {
  const name = (err as { name?: string } | null)?.name ?? ''
  const raw = err instanceof Error ? err.message : typeof err === 'string' ? err : ''
  switch (name) {
    case 'NotAllowedError':
    case 'SecurityError':
    case 'PermissionDeniedError':
      return 'Microphone access was blocked. Allow Eggify under System Settings → Privacy & Security → Microphone (or the browser\'s site settings), then try again.'
    case 'NotFoundError':
    case 'DevicesNotFoundError':
      return 'No microphone was found. Connect one or choose an input device in System Settings → Sound.'
    case 'NotReadableError':
    case 'TrackStartError':
    case 'AbortError':
      return 'The microphone is in use by another app or could not be started. Close the other app and try again.'
    case 'OverconstrainedError':
      return 'The selected microphone is not available. Pick another input device and try again.'
    case 'NotSupportedError':
      return 'This window cannot record audio in the format needed. Update the app or try another browser.'
  }
  return raw || 'Could not start the microphone.'
}

/** Resolves when `p` settles or after `ms` (to `fallback`), whichever is first. */
export function withTimeout<T>(p: Promise<T>, ms: number, fallback: T): Promise<T> {
  return new Promise<T>((resolve) => {
    const t = setTimeout(() => resolve(fallback), ms)
    p.then((v) => { clearTimeout(t); resolve(v) }, () => { clearTimeout(t); resolve(fallback) })
  })
}
