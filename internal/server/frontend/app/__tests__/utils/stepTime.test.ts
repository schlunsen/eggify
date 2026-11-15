import { describe, expect, it } from 'vitest'
import { clockTime, stepDuration, stepTimeTitle } from '~/utils/agents/stepTime'

describe('step times', () => {
  const at = new Date(2026, 9, 11, 9, 5, 7).getTime() // local time

  it('shows the local clock time, nothing when unknown', () => {
    expect(clockTime(at)).toBe('09:05:07')
    expect(clockTime(undefined)).toBe('')
    expect(clockTime(0)).toBe('')
  })

  it('titles a step with the full date and how long ago', () => {
    const title = stepTimeTitle(at, at + 3 * 60_000)
    expect(title).toContain('2026')
    expect(title).toMatch(/· 3m ago$/)
    expect(stepTimeTitle(undefined, at)).toBe('')
  })

  it('says how long a step took', () => {
    expect(stepDuration(850)).toBe('850 ms')
    expect(stepDuration(2300)).toBe('2.3 s')
    expect(stepDuration(41_000)).toBe('41 s')
    expect(stepDuration(185_000)).toBe('3 min 5 s')
    expect(stepDuration(120_000)).toBe('2 min')
    expect(stepDuration(3_840_000)).toBe('1 h 4 min')
    expect(stepDuration(undefined)).toBe('')
    expect(stepDuration(-5)).toBe('')
  })
})
