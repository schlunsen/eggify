import { describe, it, expect } from 'vitest'
import { createReconnectBackoff, reconnectDelay } from '~/utils/wsReconnect'

describe('reconnectDelay', () => {
  it('grows exponentially, stays within [exp/2, exp] and caps at maxMs', () => {
    const lo = { baseMs: 1000, maxMs: 30_000, random: () => 0 }
    const hi = { baseMs: 1000, maxMs: 30_000, random: () => 1 }
    expect(reconnectDelay(0, lo)).toBe(500)
    expect(reconnectDelay(0, hi)).toBe(1000)
    expect(reconnectDelay(3, lo)).toBe(4000)
    expect(reconnectDelay(3, hi)).toBe(8000)
    expect(reconnectDelay(10, hi)).toBe(30_000)
    expect(reconnectDelay(1000, hi)).toBe(30_000)
  })
})

describe('createReconnectBackoff', () => {
  it('backs off on repeated failures', () => {
    const b = createReconnectBackoff({ random: () => 1, now: () => 0 })
    expect([b.nextDelay(), b.nextDelay(), b.nextDelay(), b.nextDelay()]).toEqual([1000, 2000, 4000, 8000])
  })

  it('does not reset for a connection that flaps right after authenticating', () => {
    let t = 0
    const b = createReconnectBackoff({ random: () => 1, now: () => t })
    const delays: number[] = []
    for (let i = 0; i < 6; i++) {
      b.connected()
      t += 200 // closed 200 ms later (e.g. the server dropped a broken socket)
      delays.push(b.nextDelay())
      t += delays[delays.length - 1]!
    }
    expect(delays).toEqual([1000, 2000, 4000, 8000, 16000, 30000])
    // At most a handful of reconnects in a minute, not one every few seconds
    const inFirstMinute = delays.reduce((acc, d) => (acc.sum + d <= 60_000 ? { sum: acc.sum + d, n: acc.n + 1 } : acc), { sum: 0, n: 0 }).n
    expect(inFirstMinute).toBeLessThanOrEqual(5)
  })

  it('starts over after a connection that stayed up', () => {
    let t = 0
    const b = createReconnectBackoff({ random: () => 1, now: () => t, stableMs: 30_000 })
    b.nextDelay(); b.nextDelay(); b.nextDelay()
    b.connected()
    t += 60_000
    expect(b.nextDelay()).toBe(1000)
  })
})
