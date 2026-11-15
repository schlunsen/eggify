import { describe, expect, it } from 'vitest'
import { subagentsWorkingCount, subagentsWorkingLabel } from '~/utils/agents/subagentsWorking'

describe('subagentsWorkingCount', () => {
  const live = [{ status: 'running' }, { status: 'completed' }, { status: 'running' }]

  it('counts running live agents when the whole list is loaded', () => {
    expect(subagentsWorkingCount(live, true, 5)).toBe(2)
    expect(subagentsWorkingCount([], true, 5)).toBe(0)
  })

  it('uses the listed count while only some agents are known', () => {
    expect(subagentsWorkingCount(live, false, 3)).toBe(3)
    expect(subagentsWorkingCount([], false, undefined)).toBe(0)
  })
})

describe('subagentsWorkingLabel', () => {
  it('says how many are working', () => {
    expect(subagentsWorkingLabel(1)).toBe('1 subagent working')
    expect(subagentsWorkingLabel(3)).toBe('3 subagents working')
  })
})
