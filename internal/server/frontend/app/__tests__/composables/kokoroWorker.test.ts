import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// If synthesis ever ran on the page again, this mock would count it. The worker
// tests below use it too (the worker module imports kokoro-js).
const model = { loads: 0, generated: [] as string[], streams: 0, pending: [] as Array<() => void> }
vi.mock('kokoro-js', () => ({
  KokoroTTS: {
    async from_pretrained() {
      model.loads++
      return {
        // kokoro-js' stream(text) never finishes (its splitter is never closed)
        async *stream() { model.streams++; await new Promise(() => {}) },
        generate(text: string) {
          model.generated.push(text)
          return new Promise((resolve) => {
            model.pending.push(() => resolve({ audio: new Float32Array(10), sampling_rate: 24000 }))
          })
        },
      }
    },
  },
}))

const tick = () => new Promise<void>(r => setTimeout(r, 0))

// A fake worker speaking the kokoro-worker.ts protocol: one chunk per macrotask.
class FakeWorker {
  static instances: FakeWorker[] = []
  posted: any[] = []
  onmessage: ((e: MessageEvent) => void) | null = null
  onerror: ((e: ErrorEvent) => void) | null = null
  terminated = false
  constructor(public url: URL | string, public opts?: WorkerOptions) { FakeWorker.instances.push(this) }
  reply(data: any) { this.onmessage?.({ data } as MessageEvent) }
  postMessage(m: any) {
    this.posted.push(m)
    if (m.type === 'load') queueMicrotask(() => this.reply({ type: 'loaded', id: m.id, device: 'wasm' }))
  }
  terminate() { this.terminated = true }
}

beforeEach(() => {
  vi.resetModules()
  model.loads = 0
  model.generated = []
  model.streams = 0
  model.pending = []
  FakeWorker.instances = []
  vi.stubGlobal('Worker', FakeWorker)
})
afterEach(() => vi.unstubAllGlobals())

describe('Kokoro runs in a Web Worker', () => {
  it('loads and synthesises in the worker, never on the page', async () => {
    const { useKokoroTTS } = await import('../../composables/useKokoroTTS')
    const k = useKokoroTTS()
    const got: number[] = []
    const run = await k.synthesize(['One.', 'Two.', 'Three.'], i => got.push(i))
    const w = FakeWorker.instances[0]!
    expect(String(w.url)).toContain('kokoro-worker')
    expect(w.opts?.type).toBe('module')
    const stream = w.posted.find(m => m.type === 'stream')
    expect(stream).toMatchObject({ chunks: ['One.', 'Two.', 'Three.'], ahead: 3 })
    for (let i = 0; i < 3; i++) w.reply({ type: 'chunk', id: stream.id, index: i, samples: new Float32Array(240), rate: 24000 })
    w.reply({ type: 'done', id: stream.id })
    await run!.done
    expect(got).toEqual([0, 1, 2])
    expect(model).toMatchObject({ loads: 0, generated: [], streams: 0 })
    expect(k.status.value).toBe('ready')
  })

  it('acks played chunks and cancels at once, dropping late chunks', async () => {
    const { useKokoroTTS } = await import('../../composables/useKokoroTTS')
    const k = useKokoroTTS()
    const got: number[] = []
    const run = (await k.synthesize(['One.', 'Two.', 'Three.'], i => got.push(i)))!
    const w = FakeWorker.instances[0]!
    const id = w.posted.find(m => m.type === 'stream').id
    w.reply({ type: 'chunk', id, index: 0, samples: new Float32Array(240), rate: 24000 })
    run.ack(0)
    expect(w.posted.at(-1)).toEqual({ type: 'ack', id, index: 0 })
    run.cancel()
    expect(w.posted.at(-1)).toEqual({ type: 'cancel', id }) // sent now, not on the next chunk
    await run.done // resolves without waiting for the worker
    w.reply({ type: 'chunk', id, index: 1, samples: new Float32Array(240), rate: 24000 })
    expect(got).toEqual([0])
  })

  it('terminates the worker on recover and destroy', async () => {
    const { useKokoroTTS } = await import('../../composables/useKokoroTTS')
    const k = useKokoroTTS()
    await k.loadModel()
    k.recover()
    expect(FakeWorker.instances[0]!.terminated).toBe(true)
    await k.loadModel()
    k.destroy()
    expect(FakeWorker.instances[1]!.terminated).toBe(true)
  })
})

describe('kokoro-worker.ts', () => {
  async function loadWorker() {
    const out: any[] = []
    const fakeSelf: any = { postMessage: (m: any) => out.push(m), navigator: {} }
    vi.stubGlobal('self', fakeSelf)
    await import('../../workers/kokoro-worker')
    const send = async (data: any) => { fakeSelf.onmessage({ data }); await tick() }
    await send({ type: 'load', id: 1, device: 'wasm', dtype: 'q8' })
    await vi.waitFor(() => expect(out.some(m => m.type === 'loaded')).toBe(true))
    return { out, send }
  }
  const finishNext = async () => { model.pending.shift()!(); await tick(); await tick() }

  it('synthesises chunk by chunk with generate(), posting each one as it is ready', async () => {
    const { out, send } = await loadWorker()
    await send({ type: 'stream', id: 2, chunks: ['A.', 'B.'], voice: 'af_sky', ahead: 3 })
    expect(model.generated).toEqual(['A.'])
    await finishNext()
    expect(out.filter(m => m.type === 'chunk').map(m => m.index)).toEqual([0])
    expect(model.generated).toEqual(['A.', 'B.'])
    await finishNext()
    await vi.waitFor(() => expect(out.at(-1)).toEqual({ type: 'done', id: 2 }))
    expect(model.streams).toBe(0) // kokoro-js stream() never finishes: never used
  })

  it('waits for acks beyond the look-ahead and stops on cancel', async () => {
    const { out, send } = await loadWorker()
    await send({ type: 'stream', id: 3, chunks: ['A.', 'B.', 'C.'], voice: 'af_sky', ahead: 1 })
    await finishNext()
    expect(model.generated).toEqual(['A.']) // waiting for chunk 0 to be played
    await send({ type: 'ack', id: 3, index: 0 })
    expect(model.generated).toEqual(['A.', 'B.'])
    await send({ type: 'cancel', id: 3 })
    await finishNext() // the inference in flight finishes, its audio is dropped
    await vi.waitFor(() => expect(out.at(-1)).toEqual({ type: 'done', id: 3 }))
    expect(out.filter(m => m.type === 'chunk' && m.id === 3).map(m => m.index)).toEqual([0])
    expect(model.generated).toEqual(['A.', 'B.'])

    // the next reading is not blocked behind the cancelled one
    await send({ type: 'stream', id: 4, chunks: ['D.'], voice: 'af_sky', ahead: 3 })
    expect(model.generated.at(-1)).toBe('D.')
  })

  it('a reading cancelled while queued never synthesises', async () => {
    const { out, send } = await loadWorker()
    await send({ type: 'stream', id: 5, chunks: ['A.'], voice: 'af_sky', ahead: 3 })
    await send({ type: 'stream', id: 6, chunks: ['B.'], voice: 'af_sky', ahead: 3 })
    await send({ type: 'cancel', id: 6 })
    await finishNext()
    await vi.waitFor(() => expect(out.filter(m => m.type === 'done').map(m => m.id)).toEqual([5, 6]))
    expect(model.generated).toEqual(['A.'])
  })
})
