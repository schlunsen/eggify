/**
 * A minimal Web Audio fake for read-aloud tests. Time comes from Date.now(), so
 * tests fake 'Date' together with setTimeout. A buffer source "plays" from its
 * start time for its buffer's duration and then fires onended.
 */

export interface FakeSource {
  buffer: { duration: number; length: number } | null
  when: number | null
  startedAt: number | null
  stopped: boolean
  ended: boolean
  onended: (() => void) | null
}

export interface FakeParamCall { method: string; args: number[] }

export class FakeAudioContext {
  static instances: FakeAudioContext[] = []
  state = 'running'
  sampleRate = 24000
  destination = { kind: 'destination' }
  sources: FakeSource[] = []
  gains: Array<{ gain: { value: number; calls: FakeParamCall[] } }> = []
  closed = false
  private t0 = Date.now()

  constructor() { FakeAudioContext.instances.push(this) }

  get currentTime() { return (Date.now() - this.t0) / 1000 }
  resume() { this.state = 'running'; return Promise.resolve() }
  close() { this.closed = true; return Promise.resolve() }

  private node<T extends object>(extra: T) {
    return Object.assign({ connect() {}, disconnect() {} }, extra)
  }

  private param(value: number) {
    const calls: FakeParamCall[] = []
    return {
      value,
      calls,
      setValueAtTime(...args: number[]) { calls.push({ method: 'setValueAtTime', args }) },
      linearRampToValueAtTime(...args: number[]) { calls.push({ method: 'linearRampToValueAtTime', args }) },
      cancelScheduledValues(...args: number[]) { calls.push({ method: 'cancelScheduledValues', args }) },
    }
  }

  createGain() {
    const g = this.node({ gain: this.param(1) })
    this.gains.push(g)
    return g
  }
  createConvolver() { return this.node({ buffer: null as unknown }) }
  createOscillator() { return this.node({ type: 'sine', frequency: this.param(0), start() {}, stop() {} }) }
  createWaveShaper() { return this.node({ curve: null as unknown, oversample: 'none' }) }
  createBuffer(_channels: number, length: number, rate: number) {
    const data = new Float32Array(length)
    return { duration: length / rate, length, sampleRate: rate, getChannelData: () => data, copyToChannel(src: Float32Array) { data.set(src.subarray(0, length)) } }
  }
  createBufferSource() {
    const ctx = this
    const src = this.node({
      buffer: null as FakeSource['buffer'],
      when: null as number | null,
      startedAt: null as number | null,
      stopped: false,
      ended: false,
      onended: null as (() => void) | null,
      timers: [] as ReturnType<typeof setTimeout>[],
      start(when = 0) {
        src.when = when
        const delay = Math.max(0, when - ctx.currentTime) * 1000
        src.timers.push(setTimeout(() => { if (!src.stopped) src.startedAt = ctx.currentTime }, delay))
        src.timers.push(setTimeout(() => {
          if (src.stopped) return
          src.ended = true
          src.onended?.()
        }, delay + (src.buffer?.duration ?? 0) * 1000))
      },
      stop() {
        src.stopped = true
        for (const t of src.timers) clearTimeout(t)
      },
    })
    this.sources.push(src)
    return src
  }
}
