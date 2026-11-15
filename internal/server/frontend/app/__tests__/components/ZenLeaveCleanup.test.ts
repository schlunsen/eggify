import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

/**
 * Leaving Zen must stop everything Zen started: the Circus ticker and beat
 * loop, gag timers, ResizeObservers (stage, name plate, response box, composer),
 * audio contexts and listeners. Anything left running keeps the page busy (and
 * the input laggy) in Live view. This mounts the real ZenMode on the Circus
 * stage, with music playing, and counts what is still alive after unmount.
 */

vi.mock('../../composables/agents/useZenAnimation', () => ({
  useZenAnimation: () => ({
    init: vi.fn(async () => {}), handleResize: vi.fn(), detach: vi.fn(),
    triggerPulse: vi.fn(), triggerBeamDrop: vi.fn(), triggerToolBeam: vi.fn(), triggerSendPulse: vi.fn(),
  }),
  preloadZenScene: vi.fn(),
}))
vi.mock('../../composables/useAuthenticatedFetch', () => ({
  useAuthenticatedFetch: () => ({ fetchWithAuth: vi.fn(async () => ({ ok: true, json: async () => ({ digests: [] }) })) }),
}))

class FakeNode {
  gain = { value: 1, setValueAtTime() {}, linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {}, cancelScheduledValues() {} }
  frequency = { value: 0, setValueAtTime() {}, linearRampToValueAtTime() {}, exponentialRampToValueAtTime() {} }
  Q = { value: 0 }
  buffer: unknown = null
  connect() { return this }
  disconnect() {}
  start() {}
  stop() {}
}

interface Live {
  raf: Set<number>
  intervals: Set<unknown>
  timeouts: Set<unknown>
  observers: Set<unknown>
  audio: Set<unknown>
}
let live: Live
const real = {
  setInterval: globalThis.setInterval, clearInterval: globalThis.clearInterval,
  setTimeout: globalThis.setTimeout, clearTimeout: globalThis.clearTimeout,
}

function fromApp() {
  const stack = new Error().stack || ''
  return stack.split('\n').slice(3).some(l => l.includes('/frontend/app/') && !l.includes('__tests__'))
}

function instrument() {
  live = { raf: new Set(), intervals: new Set(), timeouts: new Set(), observers: new Set(), audio: new Set() }
  let rafSeq = 0
  const rafTimers = new Map<number, ReturnType<typeof setTimeout>>()
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
    const id = ++rafSeq
    live.raf.add(id)
    rafTimers.set(id, real.setTimeout(() => { live.raf.delete(id); rafTimers.delete(id); cb(performance.now()) }, 16))
    return id
  })
  vi.stubGlobal('cancelAnimationFrame', (id: number) => { live.raf.delete(id); real.clearTimeout(rafTimers.get(id)); rafTimers.delete(id) })
  vi.stubGlobal('setInterval', (fn: () => void, ms?: number) => { const h = real.setInterval(fn, ms); live.intervals.add(h); return h })
  vi.stubGlobal('clearInterval', (h: any) => { live.intervals.delete(h); real.clearInterval(h) })
  vi.stubGlobal('setTimeout', (fn: () => void, ms?: number) => {
    const h = real.setTimeout(() => { live.timeouts.delete(h); fn() }, ms)
    // Only the app's own timers (not Vue's devtools hook or the test runner's)
    if (fromApp()) live.timeouts.add(h)
    return h
  })
  vi.stubGlobal('clearTimeout', (h: any) => { live.timeouts.delete(h); real.clearTimeout(h) })
  vi.stubGlobal('ResizeObserver', class {
    constructor(_cb: unknown) {}
    observe() { live.observers.add(this) }
    unobserve() {}
    disconnect() { live.observers.delete(this) }
  })
  class FakeContext {
    state = 'running'
    currentTime = 0
    sampleRate = 8000
    destination = new FakeNode()
    constructor() { live.audio.add(this) }
    addEventListener() {}
    removeEventListener() {}
    resume() { return Promise.resolve() }
    close() { live.audio.delete(this); this.state = 'closed'; return Promise.resolve() }
    createBuffer(_c: number, len: number) { return { getChannelData: () => new Float32Array(len) } }
    createGain() { return new FakeNode() }
    createOscillator() { return new FakeNode() }
    createBiquadFilter() { return new FakeNode() }
    createConvolver() { return new FakeNode() }
    createDynamicsCompressor() { return new FakeNode() }
    createAnalyser() { return Object.assign(new FakeNode(), { fftSize: 0, smoothingTimeConstant: 0, getByteFrequencyData() {} }) }
    createMediaElementSource() { return new FakeNode() }
  }
  vi.stubGlobal('AudioContext', FakeContext)
  ;(window as any).AudioContext = FakeContext
  vi.stubGlobal('Audio', class {
    loop = false; preload = ''; paused = true; currentTime = 0; volume = 1
    constructor(public src: string) {}
    addEventListener() {}
    removeEventListener() {}
    removeAttribute() {}
    play() { this.paused = false; return Promise.resolve() }
    pause() { this.paused = true }
  })
}

const wait = (ms: number) => new Promise(r => real.setTimeout(r, ms))

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('zen-viz-mode', 'circus')
  instrument()
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('Leaving Zen', () => {
  it('leaves no animation frame, interval, timer, observer or audio context running', async () => {
    const { default: ZenMode } = await import('../../components/agents/ZenMode.vue')
    const longReply = Array.from({ length: 40 }, (_, i) => `Paragraph ${i} of the reply.`).join('\n\n')
    const w = mount(ZenMode, {
      attachTo: document.body,
      props: {
        sessionId: 'sess-cleanup', sessionStatus: 'processing', messageCount: 700,
        lastAssistantMessage: longReply, lastUserMessage: 'Please fix it',
        recentAssistantMessages: [{ id: 'm1', text: longReply, timestamp: new Date(), sequence: 1 }],
        projectName: 'eggify', projectColor: '#b8606e', avatarName: 'Randy',
      },
      global: { stubs: { ZenVizPicker: true, Teleport: true } },
    })
    await flushPromises()
    await wait(300)

    // Zen is really running: the Circus stage, its ticker and observers
    expect(document.querySelector('.zen-circus')).not.toBeNull()
    expect(live.intervals.size).toBeGreaterThan(0)
    expect(live.observers.size).toBeGreaterThan(0)

    w.unmount()
    // Music fades out over ~1.8 s before its context is released
    await wait(2300)

    expect(document.querySelector('.zen-circus')).toBeNull()
    expect([...live.raf]).toEqual([])
    expect(live.intervals.size).toBe(0)
    expect(live.observers.size).toBe(0)
    expect(live.audio.size).toBe(0)
    expect(live.timeouts.size).toBe(0)
  }, 15000)
})
