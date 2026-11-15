import { createPulseGate } from '../../composables/useActivityPulse'

const clock = () => {
  let t = 0
  return { now: () => t, advance: (ms: number) => { t += ms } }
}

describe('createPulseGate', () => {
  it('lets one pulse through per interval and drops the activity in between', () => {
    const c = clock()
    const gate = createPulseGate({ interval: 10_000, jitter: 0, maxDelay: 0, random: () => 0.5, now: c.now })
    expect(gate.request()).toBe(0)
    c.advance(9_999)
    expect(gate.request()).toBeNull()
    c.advance(1)
    expect(gate.request()).toBe(0)
  })

  it('varies the gap by the jitter and delays the start by up to maxDelay', () => {
    const c = clock()
    const rolls = [1, 1] // delay roll, gap roll
    const gate = createPulseGate({ interval: 10_000, jitter: 0.3, maxDelay: 2_500, random: () => rolls.shift() ?? 0, now: c.now })
    expect(gate.request()).toBe(2_500)
    c.advance(2_500 + 13_000 - 1)
    expect(gate.request()).toBeNull()
    c.advance(1)
    expect(gate.request()).not.toBeNull()
  })

  it('keeps several rows out of step', () => {
    const seeds = [0.1, 0.5, 0.9]
    const delays = seeds.map(r => createPulseGate({ random: () => r, now: () => 0 }).request())
    expect(new Set(delays).size).toBe(3)
  })
})
