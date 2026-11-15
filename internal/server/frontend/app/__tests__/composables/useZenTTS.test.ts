import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { FakeAudioContext } from './fakeAudioContext'

// Fake kokoro-js: behaviour is driven per test through `plan`.
const plan = {
  loads: [] as Array<{ device: string; dtype: string }>,
  // one entry per from_pretrained call; true = ok, string = throw
  load: [] as Array<true | string>,
  // one entry per generate() call (a chunk)
  gen: [] as Array<'ok' | 'empty' | 'throw' | 'hang'>,
  texts: [] as string[],
  /** Synthesis time per chunk (fake ms). */
  synthMs: 0,
  /** Audio length per chunk (seconds). */
  audioSec: 0.5,
}

vi.mock('kokoro-js', () => ({
  KokoroTTS: {
    async from_pretrained(_id: string, opts: { device: string; dtype: string }) {
      plan.loads.push(opts)
      const r = plan.load.shift() ?? true
      if (r !== true) throw new Error(r)
      return {
        list_voices: () => [],
        async *stream() { throw new Error('stream() must not be used: it never finishes') },
        async generate(text: string) {
          const n = plan.texts.push(text)
          const mode = plan.gen.shift() ?? 'ok'
          if (mode === 'hang') return new Promise(() => {})
          if (plan.synthMs) await new Promise(r => setTimeout(r, plan.synthMs))
          if (mode === 'throw') throw new Error('generation failed')
          if (mode === 'empty') return { audio: new Float32Array(0), sampling_rate: 24000 }
          // the length encodes the call number, so tests can check the order
          return { audio: new Float32Array(Math.round(plan.audioSec * 24000) + n), sampling_rate: 24000 }
        },
      }
    },
  },
}))

const realSetTimeout = globalThis.setTimeout

// kokoro-js is imported dynamically (real async), so fake timers alone cannot
// drive the flow: yield real macrotasks and advance fake time in between.
async function until(cond: () => boolean, stepMs = 0, max = 400) {
  for (let i = 0; i < max && !cond(); i++) {
    await vi.advanceTimersByTimeAsync(stepMs)
    await new Promise<void>((r) => realSetTimeout(r, 1))
  }
}
async function advance(ms: number, stepMs = 50) {
  for (let t = 0; t < ms; t += stepMs) {
    await vi.advanceTimersByTimeAsync(stepMs)
    await new Promise<void>((r) => realSetTimeout(r, 0))
  }
}
async function finish(p: Promise<unknown>) {
  let done = false
  void p.finally(() => { done = true })
  await until(() => done, 100, 2000)
  await p
}

const TEXT = 'This is a sentence long enough to be spoken aloud.'
// Six chunks: a short first sentence, then sentences that do not fit together
const SENTENCE = 'This sentence is long enough that two of them never fit in a single later chunk of speech, so each one of them stands alone here in its own chunk of speech.'
const LONG = ['Here we go.', ...Array.from({ length: 5 }, () => SENTENCE)].join(' ')

let speechSpeak: ReturnType<typeof vi.fn>
let fetchMock: ReturnType<typeof vi.fn>

async function fresh() {
  vi.resetModules()
  const mod = await import('../../composables/useZenTTS')
  return mod.useZenTTS()
}
const ctx = () => FakeAudioContext.instances[0]!
const started = () => ctx()?.sources.filter(s => s.startedAt !== null) ?? []

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] })
  plan.loads = []; plan.load = []; plan.gen = []; plan.texts = []; plan.synthMs = 0; plan.audioSec = 0.5
  FakeAudioContext.instances = []
  localStorage.clear()
  speechSpeak = vi.fn()
  fetchMock = vi.fn(async () => ({ ok: false, json: async () => ({}) }))
  vi.stubGlobal('speechSynthesis', { speak: speechSpeak, cancel: vi.fn(), getVoices: () => [] })
  vi.stubGlobal('SpeechSynthesisUtterance', class {})
  vi.stubGlobal('fetch', fetchMock)
  vi.stubGlobal('AudioContext', FakeAudioContext)
  vi.stubGlobal('Worker', undefined) // inline engine with the kokoro-js mock above
  vi.stubGlobal('navigator', { gpu: {} })
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  vi.spyOn(console, 'error').mockImplementation(() => {})
  vi.spyOn(console, 'log').mockImplementation(() => {})
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function expectNoNativeVoice() {
  expect(speechSpeak).not.toHaveBeenCalled()
  expect(fetchMock.mock.calls.some(c => String(c[0]).includes('/api/tts/generate'))).toBe(false)
}

describe('useZenTTS: Kokoro is the only voice', () => {
  it('plays with Kokoro on the first attempt', async () => {
    const tts = await fresh()
    await finish(tts.speak(TEXT))
    expect(plan.texts).toEqual([TEXT])
    expect(started()).toHaveLength(1)
    expect(tts.error.value).toBeNull()
    expect(tts.isPlaying.value).toBe(false)
    expect(tts.isLoading.value).toBe(false)
    expectNoNativeVoice()
  })

  it('succeeds on the 2nd attempt after a failed generation', async () => {
    plan.gen = ['throw', 'ok']
    const tts = await fresh()
    const p = tts.speak(TEXT)
    await until(() => plan.texts.length >= 1)
    expect(tts.isLoading.value).toBe(true)
    await finish(p)
    expect(plan.texts).toHaveLength(2)
    expect(tts.error.value).toBeNull()
    expect(tts.retrying.value).toBe(false)
    expectNoNativeVoice()
  })

  it('falls back from WebGPU to WASM on retry', async () => {
    plan.load = ['webgpu init failed', true]
    const tts = await fresh()
    await finish(tts.speak(TEXT))
    expect(plan.loads.map(l => l.device)).toEqual(['webgpu', 'wasm'])
    expect(plan.loads[1]!.dtype).toBe('q8')
    expect(tts.error.value).toBeNull()
    expectNoNativeVoice()
  })

  it('shows the retrying state while waiting between attempts', async () => {
    plan.gen = ['empty', 'ok']
    const tts = await fresh()
    const p = tts.speak(TEXT)
    await until(() => tts.retrying.value)
    expect(tts.retrying.value).toBe(true)
    expect(tts.isLoading.value).toBe(true)
    await finish(p)
    expect(tts.retrying.value).toBe(false)
  })

  it('a superseded request stops retrying', async () => {
    plan.gen = Array(8).fill('throw')
    const tts = await fresh()
    const first = tts.speak(TEXT)
    await until(() => tts.retrying.value) // first attempt failed, waiting to retry
    tts.stop()
    await finish(first)
    expect(plan.texts).toHaveLength(1)
    expect(tts.isLoading.value).toBe(false)
    expect(tts.error.value).toBeNull()
    expectNoNativeVoice()
  })

  it('stays silent after all retries fail, with a message and a working Retry', async () => {
    plan.load = Array(10).fill('model unavailable')
    const tts = await fresh()
    await finish(tts.speak(TEXT))
    expect(plan.loads.length).toBe(4) // 1 attempt + 3 retries
    expect(tts.error.value).toMatch(/Voice unavailable/)
    expect(tts.isLoading.value).toBe(false)
    expect(tts.isPlaying.value).toBe(false)
    expect(tts.retrying.value).toBe(false)
    expectNoNativeVoice()

    // manual Retry reloads the model and speaks
    plan.load = [true]
    await finish(tts.retry())
    expect(tts.error.value).toBeNull()
    expect(plan.loads.length).toBe(5)
    expectNoNativeVoice()
  })

  it('ignores old macOS / engine localStorage keys', async () => {
    localStorage.setItem('zenTTSEngine', 'macos')
    localStorage.setItem('zenTTSMacosVoice', 'daniel')
    const tts = await fresh()
    await finish(tts.speak(TEXT))
    expect(plan.texts).toHaveLength(1)
    expectNoNativeVoice()
  })
})

describe('useZenTTS: leaving Zen frees the voice', () => {
  it('unloads the Kokoro model a while after Zen is left', async () => {
    const tts = await fresh()
    await finish(tts.speak(TEXT))
    expect(tts.kokoro.status.value).toBe('ready')
    tts.leave(1000)
    expect(tts.isPlaying.value).toBe(false)
    await vi.advanceTimersByTimeAsync(999)
    expect(tts.kokoro.status.value).toBe('ready')
    await vi.advanceTimersByTimeAsync(1)
    expect(tts.kokoro.status.value).toBe('unloaded')
  })

  it('keeps the model when Zen is shown again before that', async () => {
    const tts = await fresh()
    await finish(tts.speak(TEXT))
    tts.leave(1000)
    await vi.advanceTimersByTimeAsync(500)
    tts.enter()
    await vi.advanceTimersByTimeAsync(5000)
    expect(tts.kokoro.status.value).toBe('ready')
  })

  it('leaves no timer behind when no model was loaded', async () => {
    const tts = await fresh()
    tts.leave(1000)
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('useZenTTS: streaming playback', () => {
  it('reads cleaned chunks, never the markdown', async () => {
    const tts = await fresh()
    await finish(tts.speak('Here is the plan:\n\n1. **Fix** the `use_zen_tts` code\n\n```ts\nconst secret = 1\n```'))
    expect(plan.texts.join(' ')).toBe('Here is the plan. One. Fix the use zen tts code. Code omitted.')
  })

  it('plays chunk 1 before later chunks are synthesised', async () => {
    plan.synthMs = 1000
    const tts = await fresh()
    const p = tts.speak(LONG)
    await until(() => started().length > 0, 50, 2000)
    expect(plan.texts[0]).toBe('Here we go.')
    expect(started()).toHaveLength(1)
    expect(plan.texts.length).toBeLessThanOrEqual(2) // the second is still being synthesised
    expect(tts.isLoading.value).toBe(false)
    expect(tts.isPlaying.value).toBe(true)
    tts.stop()
    await finish(p)
  })

  it('plays every chunk in order, back to back with a short gap', async () => {
    plan.audioSec = 1
    const tts = await fresh()
    await finish(tts.speak(LONG))
    const srcs = ctx().sources
    expect(srcs).toHaveLength(6)
    const lengths = srcs.map(s => s.buffer!.length)
    expect(lengths).toEqual([...lengths].sort((a, b) => a - b)) // call order == play order
    for (let i = 1; i < srcs.length; i++) {
      const gap = srcs[i]!.when! - (srcs[i - 1]!.when! + srcs[i - 1]!.buffer!.duration)
      expect(gap).toBeGreaterThanOrEqual(0.08)
      expect(gap).toBeLessThanOrEqual(0.15 + 1e-6)
    }
    expect(tts.isPlaying.value).toBe(false)
  })

  it('keeps at most 3 chunks synthesised ahead of playback', async () => {
    plan.audioSec = 5
    const tts = await fresh()
    const p = tts.speak(LONG)
    await until(() => started().length > 0)
    await advance(1000)
    // chunk 1 playing, 2 and 3 waiting: the worker waits for an ack
    expect(plan.texts).toHaveLength(3)
    await advance(5200)
    expect(plan.texts).toHaveLength(4)
    tts.stop()
    await finish(p)
  })
})

describe('useZenTTS: cancellation', () => {
  it('stop plays nothing further and synthesises nothing further', async () => {
    plan.audioSec = 2
    plan.synthMs = 500
    const tts = await fresh()
    const p = tts.speak(LONG)
    await until(() => started().length > 0, 50, 2000)
    const synthesised = plan.texts.length
    tts.stop()
    expect(ctx().sources.every(s => s.stopped || s.ended)).toBe(true)
    await advance(20000, 250)
    await finish(p)
    expect(started()).toHaveLength(1)
    expect(plan.texts.length).toBeLessThanOrEqual(synthesised + 1) // at most the one already in flight
    expect(tts.isPlaying.value).toBe(false)
    expect(tts.isLoading.value).toBe(false)
  })

  it('a new reading supersedes the old one', async () => {
    plan.audioSec = 2
    const tts = await fresh()
    const first = tts.speak(LONG)
    await until(() => started().length > 0)
    const oldSources = [...ctx().sources]
    const second = tts.speak('A different reply that should be read instead.')
    expect(oldSources.every(s => s.stopped || s.ended)).toBe(true)
    await finish(second)
    await finish(first)
    expect(plan.texts.at(-1)).toBe('A different reply that should be read instead.')
    const newSources = ctx().sources.filter(s => !oldSources.includes(s))
    expect(newSources).toHaveLength(1)
    expect(newSources[0]!.startedAt).not.toBeNull()
    expect(tts.isPlaying.value).toBe(false)
  })
})

describe('useZenTTS: safety timeout', () => {
  it('gives up when no audio arrives within 30 s', async () => {
    plan.gen = ['hang']
    const tts = await fresh()
    const p = tts.speak(TEXT)
    await until(() => plan.texts.length > 0)
    expect(tts.isLoading.value).toBe(true)
    await advance(29000, 1000)
    expect(tts.error.value).toBeNull()
    await advance(2000, 500)
    expect(tts.error.value).toMatch(/Voice unavailable/)
    expect(tts.isLoading.value).toBe(false)
    void p
  })

  it('never cuts off a long reading that is playing', async () => {
    plan.audioSec = 12 // 6 chunks × 12 s: over a minute of speech
    const tts = await fresh()
    const p = tts.speak(LONG)
    await until(() => started().length > 0)
    await advance(45000, 500)
    expect(tts.error.value).toBeNull()
    expect(tts.isPlaying.value).toBe(true)
    await finish(p)
    expect(started()).toHaveLength(6)
    expect(tts.error.value).toBeNull()
  })
})

describe('useZenTTS: fade out (session change)', () => {
  it('ramps the voice to silence, then stops it and cancels the rest', async () => {
    plan.audioSec = 4
    const tts = await fresh()
    const p = tts.speak(LONG)
    await until(() => started().length > 0)
    await advance(500)
    const synthesised = plan.texts.length
    const now = ctx().currentTime
    const faded = tts.fadeOut()
    expect(tts.isPlaying.value).toBe(false) // music un-ducks right away

    // the reading's own gain ramps to 0 over ~400 ms
    const ramp = ctx().gains.flatMap(g => g.gain.calls).find(c => c.method === 'linearRampToValueAtTime')
    expect(ramp?.args[0]).toBe(0)
    expect(ramp!.args[1]! - now).toBeCloseTo(0.4, 2)
    const playing = ctx().sources.filter(s => s.startedAt !== null && !s.ended)
    expect(playing.every(s => !s.stopped)).toBe(true) // still audible during the fade

    await advance(450)
    await faded
    expect(ctx().sources.every(s => s.stopped || s.ended)).toBe(true)
    await advance(20000, 500)
    await finish(p)
    expect(started()).toHaveLength(1) // nothing more from the old session
    expect(plan.texts.length).toBe(synthesised)
  })

  it('a reading started during the fade begins after it', async () => {
    plan.audioSec = 4
    const tts = await fresh()
    const first = tts.speak(LONG)
    await until(() => started().length > 0)
    const fadeEnd = ctx().currentTime + 0.4
    void tts.fadeOut()
    const second = tts.speak('The new session has its own reply to read.')
    await finish(second)
    await finish(first)
    const last = ctx().sources.at(-1)!
    expect(last.when!).toBeGreaterThanOrEqual(fadeEnd - 1e-6)
    expect(last.startedAt).not.toBeNull()
  })

  it('leaving Zen fades the reading out first, then closes the audio context', async () => {
    plan.audioSec = 4
    const tts = await fresh()
    const p = tts.speak(LONG)
    await until(() => started().length > 0)
    const synthesised = plan.texts.length
    tts.leave(60_000)
    const ramp = ctx().gains.flatMap(g => g.gain.calls).find(c => c.method === 'linearRampToValueAtTime')
    expect(ramp?.args[0]).toBe(0)
    expect(ctx().closed).toBe(false) // still fading
    await advance(450)
    expect(ctx().sources.every(s => s.stopped || s.ended)).toBe(true)
    expect(ctx().closed).toBe(true)
    await advance(10000, 500)
    await finish(p)
    expect(started()).toHaveLength(1)
    expect(plan.texts.length).toBe(synthesised)
  })

  it('coming back to Zen during the leave fade keeps the audio context', async () => {
    plan.audioSec = 4
    const tts = await fresh()
    const p = tts.speak(LONG)
    await until(() => started().length > 0)
    tts.leave(60_000)
    tts.enter()
    await advance(450)
    expect(ctx().closed).toBe(false)
    await finish(p)
  })

  it('does nothing when nothing is being read', async () => {
    const tts = await fresh()
    await tts.fadeOut()
    expect(tts.isPlaying.value).toBe(false)
  })
})

describe('source tree', () => {
  it('has no native speech synthesis or macOS TTS references in app/', () => {
    const root = join(__dirname, '..', '..')
    const hits: string[] = []
    const walk = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const full = join(dir, name)
        if (statSync(full).isDirectory()) {
          if (name === '__tests__' || name === 'node_modules') continue
          walk(full)
        } else if (/\.(ts|vue|js)$/.test(name)) {
          const src = readFileSync(full, 'utf8')
          if (/speechSynthesis|SpeechSynthesisUtterance|\/api\/tts\/generate|MACOS_VOICES|macosVoice/.test(src)) hits.push(full)
        }
      }
    }
    walk(root)
    expect(hits).toEqual([])
  })
})
