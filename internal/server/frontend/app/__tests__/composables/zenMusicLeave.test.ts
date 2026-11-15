import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useZenMusic } from '../../composables/useZenMusic'

// Every gain automation, MediaElementSource disconnect and element pause goes
// into one ordered log, so the test can check what happened before what.
let log: string[] = []
let contexts: FakeContext[] = []

class FakeParam {
  value = 1
  constructor(private name: string) {}
  setValueAtTime(v: number) { this.value = v; log.push(`${this.name}.set(${v})`) }
  linearRampToValueAtTime(v: number, t: number) { this.value = v; log.push(`${this.name}.ramp(${v}@${t})`) }
  exponentialRampToValueAtTime() {}
  cancelScheduledValues() {}
}
class FakeNode {
  gain: FakeParam
  frequency = new FakeParam('freq')
  Q = { value: 0 }
  buffer: unknown = null
  constructor(name = 'node') { this.gain = new FakeParam(name) }
  connect() { return this }
  disconnect() {}
  start() {}
  stop() {}
}
class FakeSource extends FakeNode {
  static live = 0
  constructor() { super('source'); FakeSource.live++ }
  override disconnect() { FakeSource.live--; log.push('source.disconnect') }
}
class FakeContext {
  state = 'running'
  currentTime = 0
  sampleRate = 8000
  destination = new FakeNode()
  closed = false
  gains = 0
  constructor() { contexts.push(this) }
  addEventListener() {}
  removeEventListener() {}
  resume() { return Promise.resolve() }
  close() { this.closed = true; this.state = 'closed'; log.push('ctx.close'); return Promise.resolve() }
  createBuffer(_c: number, len: number) { return { getChannelData: () => new Float32Array(len) } }
  // The first gain node is the master gain.
  createGain() { return new FakeNode(this.gains++ === 0 ? 'master' : 'gain') }
  createOscillator() { return new FakeNode() }
  createBiquadFilter() { return new FakeNode() }
  createConvolver() { return new FakeNode() }
  createDynamicsCompressor() { return new FakeNode() }
  createAnalyser() { return Object.assign(new FakeNode(), { fftSize: 0, smoothingTimeConstant: 0, getByteFrequencyData() {} }) }
  createMediaElementSource() { return new FakeSource() }
}
class FakeAudio {
  static instances: FakeAudio[] = []
  loop = false
  preload = ''
  paused = true
  constructor(public src: string) { FakeAudio.instances.push(this) }
  addEventListener() {}
  removeAttribute() { log.push('audio.release') }
  load() {}
  play() { this.paused = false; return Promise.resolve() }
  pause() { this.paused = true; log.push('audio.pause') }
}

beforeEach(() => {
  vi.useFakeTimers()
  log = []
  contexts = []
  FakeAudio.instances = []
  FakeSource.live = 0
  localStorage.clear()
  vi.stubGlobal('Audio', FakeAudio)
  vi.stubGlobal('AudioContext', FakeContext)
  ;(window as any).AudioContext = FakeContext
  vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('no grid'))))
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('leaving Zen fades the music out', () => {
  it('ramps the master gain to 0 before the element is paused and released', () => {
    const music = useZenMusic()
    music.arm(() => true)
    expect(FakeAudio.instances).toHaveLength(1)
    log = []

    music.leave()
    expect(music.isPlaying.value).toBe(false)
    // Right after leaving: only the fade is scheduled, the track keeps playing
    expect(log.some(l => l.startsWith('master.ramp(0@1.8'))).toBe(true)
    expect(log).not.toContain('audio.pause')
    expect(FakeAudio.instances[0]!.paused).toBe(false)

    vi.advanceTimersByTime(1000)
    expect(FakeAudio.instances[0]!.paused).toBe(false) // still fading

    vi.advanceTimersByTime(1500)
    const ramp = log.findIndex(l => l.startsWith('master.ramp(0'))
    const pause = log.indexOf('audio.pause')
    expect(pause).toBeGreaterThan(ramp)
    expect(log.indexOf('source.disconnect')).toBeGreaterThan(ramp)
    expect(log).toContain('ctx.close')
    expect(FakeSource.live).toBe(0)
  })

  it('re-entering during the fade fades the same track back in, without a second instance', () => {
    const music = useZenMusic()
    music.arm(() => true)
    music.leave()
    vi.advanceTimersByTime(800) // mid-fade

    music.arm(() => true)
    expect(music.isPlaying.value).toBe(true)
    expect(FakeAudio.instances).toHaveLength(1)
    expect(contexts).toHaveLength(1)
    expect(log.filter(l => l.startsWith('master.ramp(')).pop()).not.toMatch(/^master\.ramp\(0@/)

    // The cancelled cleanup never runs
    vi.advanceTimersByTime(5000)
    expect(FakeAudio.instances[0]!.paused).toBe(false)
    expect(contexts[0]!.closed).toBe(false)
    expect(FakeAudio.instances.filter(a => !a.paused)).toHaveLength(1)
    music.destroy()
  })

  it('disconnects each track graph node when the next track starts', () => {
    const music = useZenMusic()
    music.start()
    for (let i = 0; i < 5; i++) {
      music.setSource('ambient')
      music.setSource('reel')
    }
    expect(FakeSource.live).toBe(1)
    music.destroy()
    expect(FakeSource.live).toBe(0)
  })
})
