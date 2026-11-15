import { pickRecorderMimeType, describeMicError, withTimeout, isWebKitEngine, dictationAudioConstraints, mixToMono, audioBufferToMono } from '../../utils/micCapture'

describe('pickRecorderMimeType', () => {
  it('prefers webm when supported', () => {
    expect(pickRecorderMimeType({ isTypeSupported: () => true })).toBe('audio/webm;codecs=opus')
  })
  it('falls back to audio/mp4 in WKWebView (no webm)', () => {
    expect(pickRecorderMimeType({ isTypeSupported: t => t === 'audio/mp4' })).toBe('audio/mp4')
  })
  it('returns "" (browser default) when nothing matches or MediaRecorder is missing', () => {
    expect(pickRecorderMimeType({ isTypeSupported: () => false })).toBe('')
    expect(pickRecorderMimeType(undefined)).toBe('')
  })
})

describe('describeMicError', () => {
  it('explains a denied permission', () => {
    expect(describeMicError(Object.assign(new Error('Permission denied'), { name: 'NotAllowedError' }))).toMatch(/System Settings/)
  })
  it('explains a missing device and a busy one', () => {
    expect(describeMicError({ name: 'NotFoundError' })).toMatch(/No microphone/)
    expect(describeMicError({ name: 'NotReadableError' })).toMatch(/in use/)
  })
  it('keeps unknown messages', () => {
    expect(describeMicError(new Error('boom'))).toBe('boom')
    expect(describeMicError(null)).toMatch(/microphone/)
  })
})

describe('withTimeout', () => {
  it('gives the fallback when the promise never settles (AudioContext.resume without a gesture)', async () => {
    vi.useFakeTimers()
    const p = withTimeout(new Promise<string>(() => {}), 1500, 'timeout')
    await vi.advanceTimersByTimeAsync(1500)
    expect(await p).toBe('timeout')
    vi.useRealTimers()
  })
  it('gives the value when it settles first', async () => {
    expect(await withTimeout(Promise.resolve('ok'), 1000, 'timeout')).toBe('ok')
  })
})

const WKWEBVIEW_UA = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)'
const SAFARI_UA = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15'
const CHROME_UA = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36'
const EDGE_UA = CHROME_UA + ' Edg/140.0.0.0'
const FIREFOX_UA = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:140.0) Gecko/20100101 Firefox/140.0'

describe('isWebKitEngine', () => {
  it('is true for the desktop WKWebView and Safari', () => {
    expect(isWebKitEngine(WKWEBVIEW_UA)).toBe(true)
    expect(isWebKitEngine(SAFARI_UA)).toBe(true)
  })
  it('is false for Chrome, Edge and Firefox', () => {
    expect(isWebKitEngine(CHROME_UA)).toBe(false)
    expect(isWebKitEngine(EDGE_UA)).toBe(false)
    expect(isWebKitEngine(FIREFOX_UA)).toBe(false)
  })
})

describe('dictationAudioConstraints', () => {
  const whisper = { echoCancellation: true, noiseSuppression: true, sampleRate: 16000 }
  it('keeps the requested processing outside WebKit', () => {
    expect(dictationAudioConstraints(whisper, false)).toEqual(whisper)
  })
  it('turns voice processing off and asks for every input channel in WebKit', () => {
    // Voice processing only reads input 1: a mic on input 2 of an interface was silent
    expect(dictationAudioConstraints(whisper, true)).toEqual({
      echoCancellation: false, noiseSuppression: false, autoGainControl: false, channelCount: { ideal: 2 },
    })
    expect(dictationAudioConstraints({ channelCount: 1, deviceId: { exact: 'mic' } }, true)).toMatchObject({
      channelCount: { ideal: 2 }, deviceId: { exact: 'mic' },
    })
  })
})

describe('mixToMono', () => {
  it('passes a mono channel through', () => {
    const ch = new Float32Array([0.1, -0.2])
    expect(mixToMono([ch])).toBe(ch)
  })
  it('uses the live channel when the other input is empty (mic on input 2)', () => {
    const dead = new Float32Array([0, 0.0001, 0, 0])
    const mic = new Float32Array([0.3, -0.4, 0.2, 0])
    expect(Array.from(mixToMono([dead, mic]))).toEqual(Array.from(mic))
  })
  it('averages channels that both carry signal', () => {
    const out = mixToMono([new Float32Array([0.2, 0.4]), new Float32Array([0.4, 0.2])])
    expect(out[0]).toBeCloseTo(0.3)
    expect(out[1]).toBeCloseTo(0.3)
  })
  it('handles silence and no channels', () => {
    expect(Array.from(mixToMono([new Float32Array(3), new Float32Array(3)]))).toEqual([0, 0, 0])
    expect(mixToMono([]).length).toBe(0)
  })
  it('reads every channel of an AudioBuffer', () => {
    const chans = [new Float32Array([0, 0]), new Float32Array([0.5, -0.5])]
    const buf = { numberOfChannels: 2, getChannelData: (c: number) => chans[c]! }
    expect(Array.from(audioBufferToMono(buf))).toEqual([0.5, -0.5])
  })
})
