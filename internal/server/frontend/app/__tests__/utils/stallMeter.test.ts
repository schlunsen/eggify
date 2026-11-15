import { describe, it, expect } from 'vitest'
import { createStallMeter } from '~/utils/perfReporter'

describe('createStallMeter', () => {
  it('measures stalls from a late timer where there is no Long Tasks API (WebKit)', () => {
    let t = 0
    let tick: () => void = () => {}
    let hidden = false
    const m = createStallMeter({
      now: () => t,
      hidden: () => hidden,
      observer: undefined,
      setInterval: (fn) => { tick = fn; return 1 },
      clearInterval: () => {},
    })
    t += 200; tick() // on time
    t += 230; tick() // 30 ms late: not a long task
    t += 2200; tick() // the thread was busy for ~2 s
    t += 300; tick() // 100 ms late
    expect(m.take()).toEqual({ longTasks: 2, maxStallMs: 2000 })
    expect(m.take()).toEqual({ longTasks: 0, maxStallMs: 0 })

    hidden = true // throttled timers in a hidden tab are not freezes
    t += 60_000; tick()
    expect(m.take()).toEqual({ longTasks: 0, maxStallMs: 0 })
    m.stop()
  })

  it('uses the Long Tasks API when it exists', () => {
    let cb: ((list: { getEntries(): Array<{ duration: number }> }) => void) | null = null
    class FakePO {
      static supportedEntryTypes = ['longtask']
      constructor(fn: any) { cb = fn }
      observe() {}
      disconnect() {}
    }
    let intervals = 0
    const m = createStallMeter({ hidden: () => false, observer: FakePO as any, setInterval: () => { intervals++; return 1 } })
    cb!({ getEntries: () => [{ duration: 80 }, { duration: 1500 }] })
    expect(m.take()).toEqual({ longTasks: 2, maxStallMs: 1500 })
    expect(intervals).toBe(0)
    m.stop()
  })
})
