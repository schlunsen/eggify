import { useZenAudio } from '../../composables/useZenAudio'
import { useZenMusic, ZEN_TRACKS, DEFAULT_VOLUME } from '../../composables/useZenMusic'

let contexts: FakeContext[] = []

class FakeNode {
  gain = { value: 1, setValueAtTime() {}, linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {}, cancelScheduledValues() {} }
  frequency = { value: 0, setValueAtTime() {}, linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {} }
  Q = { value: 0 }
  buffer: unknown = null
  connect() { return this }
  disconnect() {}
  start() {}
  stop() {}
  setValueAtTime() {}
}
class FakeContext {
  state = ctxState
  listeners: Array<() => void> = []
  addEventListener(_t: string, fn: () => void) { this.listeners.push(fn) }
  removeEventListener() {}
  currentTime = 0
  sampleRate = 8000
  destination = new FakeNode()
  closed = false
  constructor() { contexts.push(this) }
  resume() { return Promise.resolve() }
  close() { this.closed = true; this.state = 'closed'; return Promise.resolve() }
  createBuffer(_c: number, len: number) { return { getChannelData: () => new Float32Array(len) } }
  createGain() { return new FakeNode() }
  createOscillator() { return new FakeNode() }
  createBiquadFilter() { return new FakeNode() }
  createConvolver() { return new FakeNode() }
  createDynamicsCompressor() { return new FakeNode() }
  createStereoPanner() { return new FakeNode() }
  createAnalyser() { return Object.assign(new FakeNode(), { fftSize: 0, smoothingTimeConstant: 0, getByteFrequencyData(a: Uint8Array) { a.fill(fakeEnergy) } }) }
  createMediaElementSource() { return new FakeNode() }
}
let fakeEnergy = 0
let ctxState = 'running'
class FakeAudio {
  static instances: FakeAudio[] = []
  loop = false
  preload = ''
  paused = true
  constructor(public src: string) { FakeAudio.instances.push(this) }
  addEventListener() {}
  removeAttribute() {}
  play() { this.paused = false; return Promise.resolve() }
  pause() { this.paused = true }
}

beforeEach(() => {
  contexts = []
  localStorage.clear()
  localStorage.setItem('zenMusicEnabled', 'true')
  FakeAudio.instances = []
  fakeEnergy = 0
  ctxState = 'running'
  vi.stubGlobal('Audio', FakeAudio)
  vi.stubGlobal('AudioContext', FakeContext)
  ;(window as any).AudioContext = FakeContext
})
afterEach(() => vi.unstubAllGlobals())

describe('Zen audio lifecycle', () => {
  it('starts with sound on when nothing is saved', () => {
    localStorage.clear()
    const audio = useZenAudio()
    expect(audio.isEnabled.value).toBe(true)
    expect(useZenMusic().isEnabled.value).toBe(true)
    expect(useZenMusic().volume.value).toBe(DEFAULT_VOLUME)
    expect(DEFAULT_VOLUME).toBeLessThanOrEqual(0.12)
    expect(audio.droneEnabled.value).toBe(false)
    expect(contexts).toHaveLength(0) // nothing sounds before Zen is shown
  })

  it('keeps a saved mute, and saves the choice when toggled', () => {
    localStorage.setItem('zenAudioEnabled', 'false')
    const audio = useZenAudio()
    expect(audio.isEnabled.value).toBe(false)
    audio.setPresented(true)
    audio.playToolBip('Read')
    expect(contexts).toHaveLength(0)
    audio.toggle()
    expect(localStorage.getItem('zenAudioEnabled')).toBe('true')
    audio.toggle()
    expect(localStorage.getItem('zenAudioEnabled')).toBe('false')
    audio.setPresented(false)
  })

  it('keeps a saved volume', () => {
    localStorage.setItem('zenMusicVolume', '0.4')
    localStorage.setItem('zenAudioVolume', '0.6')
    expect(useZenMusic().volume.value).toBe(0.4)
    expect(useZenAudio().volume.value).toBe(0.6)
  })

  it('does not start music for a user who muted it', () => {
    localStorage.setItem('zenMusicEnabled', 'false')
    const music = useZenMusic()
    music.arm(() => music.isEnabled.value)
    expect(music.isPlaying.value).toBe(false)
    music.destroy()
  })

  it('starts music when armed and the context may run', () => {
    const music = useZenMusic()
    music.arm(() => true)
    expect(music.isPlaying.value).toBe(true)
    expect(FakeAudio.instances).toHaveLength(1)
    music.arm(() => true) // armed again: no second instance
    music.startIfWanted()
    expect(FakeAudio.instances.filter(a => !a.paused)).toHaveLength(1)
    music.destroy()
  })

  it('waits for the first gesture when autoplay is blocked, then starts once', () => {
    ctxState = 'suspended'
    const music = useZenMusic()
    music.arm(() => true)
    expect(music.isPlaying.value).toBe(false)
    expect(FakeAudio.instances).toHaveLength(0)
    window.dispatchEvent(new Event('pointerdown'))
    expect(music.isPlaying.value).toBe(true)
    window.dispatchEvent(new Event('keydown'))
    window.dispatchEvent(new Event('pointerdown'))
    expect(FakeAudio.instances.filter(a => !a.paused)).toHaveLength(1)
    music.destroy()
  })

  it('stops when the tab is hidden and resumes when it is visible again', () => {
    const music = useZenMusic()
    music.arm(() => true)
    expect(music.isPlaying.value).toBe(true)
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
    document.dispatchEvent(new Event('visibilitychange'))
    expect(music.isPlaying.value).toBe(false)
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
    document.dispatchEvent(new Event('visibilitychange'))
    expect(music.isPlaying.value).toBe(true)
    expect(FakeAudio.instances.filter(a => !a.paused)).toHaveLength(1)
    music.destroy()
  })

  it('reopening Zen never leaves two tracks playing', () => {
    const music = useZenMusic()
    music.arm(() => true)
    music.destroy()
    music.arm(() => true)
    expect(FakeAudio.instances.filter(a => !a.paused)).toHaveLength(1)
    music.destroy()
    expect(FakeAudio.instances.filter(a => !a.paused)).toHaveLength(0)
  })

  it('plays effects only while the Zen view is presented', () => {
    const audio = useZenAudio()
    audio.playToolBip('Read')
    audio.playSendChime()
    expect(contexts).toHaveLength(0)

    audio.setPresented(true)
    audio.playToolBip('Read')
    expect(contexts).toHaveLength(1)
  })

  it('closes the audio context when the Zen view goes away', () => {
    const audio = useZenAudio()
    audio.setPresented(true)
    audio.playToolBip('Read')
    audio.setPresented(false)
    expect(contexts[0]!.closed).toBe(true)

    audio.playToolBip('Read')
    expect(contexts).toHaveLength(1)
  })

  it('stops music when destroyed and brings it back on resume', () => {
    const music = useZenMusic()
    music.start()
    expect(music.isPlaying.value).toBe(true)

    music.destroy()
    expect(music.isPlaying.value).toBe(false)
    expect(contexts[0]!.closed).toBe(true)

    music.resumeIfInterrupted()
    expect(music.isPlaying.value).toBe(true)
    music.destroy()
  })

  it('does not resume music that was not playing', () => {
    const music = useZenMusic()
    music.destroy()
    music.resumeIfInterrupted()
    expect(music.isPlaying.value).toBe(false)
  })

  it('plays one of the looping tracks by default and the synth when asked', () => {
    const music = useZenMusic()
    expect(music.source.value).toBe('reel')
    music.start()
    expect(FakeAudio.instances).toHaveLength(1)
    expect(ZEN_TRACKS).toContain(FakeAudio.instances[0]!.src)
    expect(FakeAudio.instances[0]!.loop).toBe(false)
    music.setSource('ambient')
    expect(FakeAudio.instances[0]!.paused).toBe(true)
    expect(localStorage.getItem('zenMusicSource')).toBe('ambient')
    music.destroy()
  })

  it('plays every track once, in random order, before any repeats', () => {
    sessionStorage.clear()
    const music = useZenMusic()
    for (let round = 0; round < 3; round++) {
      const seen: string[] = []
      for (let i = 0; i < ZEN_TRACKS.length; i++) {
        music.start()
        seen.push(FakeAudio.instances[FakeAudio.instances.length - 1]!.src)
        music.stop()
        music.destroy()
      }
      expect([...seen].sort()).toEqual([...ZEN_TRACKS].sort())
    }
  })

  it('never opens a new round with the track that just played', () => {
    sessionStorage.clear()
    const music = useZenMusic()
    let last = ''
    for (let i = 0; i < 60; i++) {
      music.start()
      const src = FakeAudio.instances[FakeAudio.instances.length - 1]!.src
      expect(src).not.toBe(last)
      last = src
      music.stop()
      music.destroy()
    }
  })

  it('music is on unless it was switched off, and the drone starts off', () => {
    localStorage.setItem('zenMusicEnabled', 'false')
    expect(useZenMusic().isEnabled.value).toBe(false)
    localStorage.removeItem('zenMusicEnabled')
    expect(useZenMusic().isEnabled.value).toBe(true)
    localStorage.setItem('zenAudioDrone', 'true') // a stale value from the old key
    const audio = useZenAudio()
    audio.setPresented(true)
    audio.setProject('p1')
    expect(audio.isDroneActive.value).toBe(false)
    audio.setPresented(false)
  })

  it('reports beats from the analyser, and nothing while stopped', () => {
    const music = useZenMusic()
    expect(music.readBeat().playing).toBe(false)
    music.start()
    fakeEnergy = 20
    for (let i = 0; i < 6; i++) music.readBeat()
    fakeEnergy = 150
    const hit = music.readBeat()
    expect(hit.playing).toBe(true)
    expect(hit.beat).toBe(true)
    expect(music.readBeat().beat).toBe(false) // not twice in a row
    music.destroy()
  })
})
