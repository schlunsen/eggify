import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

/**
 * Closing Zen's text cards: the reply stays hidden until the next reply (a new
 * message id; more streamed text of the same reply keeps it closed), "Your
 * message" until you send another one. "Show reply" brings it back early.
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

async function mountZen() {
  const { default: ZenMode } = await import('../../components/agents/ZenMode.vue')
  const w = mount(ZenMode, {
    attachTo: document.body,
    props: {
      sessionId: 'sess-close', sessionStatus: 'processing', messageCount: 10,
      lastAssistantMessage: 'First reply', lastUserMessage: 'Please fix it',
      recentAssistantMessages: [{ id: 'm1', text: 'First reply', timestamp: new Date(), sequence: 1 }],
      projectName: 'eggify', projectColor: '#b8606e', avatarName: 'Randy',
    },
    global: { stubs: { ZenVizPicker: true, Teleport: true } },
  })
  await flushPromises()
  return w
}
const has = (w: any, id: string) => w.find(`[data-testid="${id}"]`).exists()

describe('Closing Zen cards', () => {
  it('a closed reply stays closed while it streams and comes back with the next reply', async () => {
    const w = await mountZen()
    expect(has(w, 'zen-latest-response')).toBe(true)

    await w.find('[data-testid="zen-resp-close"]').trigger('click')
    await wait(400)
    expect(has(w, 'zen-latest-response')).toBe(false)
    expect(has(w, 'zen-resp-reopen')).toBe(true)

    // More of the same reply streams in: still closed
    await w.setProps({
      lastAssistantMessage: 'First reply, now longer',
      recentAssistantMessages: [{ id: 'm1', text: 'First reply, now longer', timestamp: new Date(), sequence: 1 }],
    })
    await wait(400)
    expect(has(w, 'zen-latest-response')).toBe(false)

    // A new reply arrives: shown again
    await w.setProps({
      lastAssistantMessage: 'Second reply',
      recentAssistantMessages: [{ id: 'm2', text: 'Second reply', timestamp: new Date(), sequence: 2 }],
    })
    await wait(400)
    expect(has(w, 'zen-latest-response')).toBe(true)
    expect(has(w, 'zen-resp-reopen')).toBe(false)
    w.unmount()
  }, 15000)

  it('"Show reply" brings a closed reply back', async () => {
    const w = await mountZen()
    await w.find('[data-testid="zen-resp-close"]').trigger('click')
    await wait(400)
    await w.find('[data-testid="zen-resp-reopen"]').trigger('click')
    await wait(400)
    expect(has(w, 'zen-latest-response')).toBe(true)
    w.unmount()
  }, 15000)

  it('"Your message" stays closed until you send another one', async () => {
    const w = await mountZen()
    expect(has(w, 'zen-user-message')).toBe(true)
    await w.find('[data-testid="zen-user-close"]').trigger('click')
    await wait(400)
    expect(has(w, 'zen-user-message')).toBe(false)

    await w.setProps({ lastUserMessage: 'And the tests too' })
    await wait(400)
    expect(has(w, 'zen-user-message')).toBe(true)
    w.unmount()
  }, 15000)
})
