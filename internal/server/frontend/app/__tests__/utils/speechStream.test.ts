import { describe, it, expect } from 'vitest'
import { runSpeechStream, type SynthResult } from '../../utils/speechStream'

const tick = () => new Promise<void>(r => setTimeout(r, 0))

function controllableSynth() {
  const calls: string[] = []
  const pending: Array<(r: SynthResult | null) => void> = []
  const synth = (text: string) => new Promise<SynthResult | null>((resolve) => { calls.push(text); pending.push(resolve) })
  const finishNext = async () => { pending.shift()!({ samples: new Float32Array(10), rate: 24000 }); await tick() }
  return { calls, synth, finishNext, pending }
}

describe('runSpeechStream', () => {
  it('emits each chunk as soon as it is synthesised, in order', async () => {
    const s = controllableSynth()
    const emitted: number[] = []
    const run = runSpeechStream(['a', 'b', 'c'], s.synth, i => emitted.push(i), 3)
    await tick()
    expect(s.calls).toEqual(['a']) // one inference at a time
    await s.finishNext()
    expect(emitted).toEqual([0]) // chunk 0 is out before chunk 1 is done
    expect(s.calls).toEqual(['a', 'b'])
    await s.finishNext()
    await s.finishNext()
    await run.done
    expect(emitted).toEqual([0, 1, 2])
  })

  it('stays at most `ahead` chunks ahead of what was acknowledged', async () => {
    const s = controllableSynth()
    const emitted: number[] = []
    const run = runSpeechStream(['a', 'b', 'c', 'd', 'e'], s.synth, i => emitted.push(i), 2)
    await tick()
    await s.finishNext()
    await s.finishNext()
    expect(emitted).toEqual([0, 1])
    expect(s.calls).toEqual(['a', 'b']) // waits: two unplayed chunks buffered
    run.ack(0)
    await tick()
    expect(s.calls).toEqual(['a', 'b', 'c'])
    await s.finishNext()
    expect(s.calls).toHaveLength(3)
    run.ack(1); run.ack(2)
    await tick()
    await s.finishNext()
    await s.finishNext()
    await run.done
    expect(emitted).toEqual([0, 1, 2, 3, 4])
  })

  it('cancel stops before the next chunk and drops the one in flight', async () => {
    const s = controllableSynth()
    const emitted: number[] = []
    const run = runSpeechStream(['a', 'b', 'c'], s.synth, i => emitted.push(i), 3)
    await tick()
    run.cancel()
    await s.finishNext()
    await run.done
    expect(emitted).toEqual([])
    expect(s.calls).toEqual(['a'])
  })

  it('cancel wakes a run waiting for acknowledgements', async () => {
    const s = controllableSynth()
    const run = runSpeechStream(['a', 'b'], s.synth, () => {}, 1)
    await tick()
    await s.finishNext()
    run.cancel()
    await run.done
    expect(s.calls).toEqual(['a'])
  })

  it('rejects on a synthesis error', async () => {
    const run = runSpeechStream(['a'], async () => { throw new Error('boom') }, () => {})
    await expect(run.done).rejects.toThrow('boom')
  })
})
