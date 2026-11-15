import { describe, expect, it } from 'vitest'
import { machineChipMode, machineChipRoom } from '~/utils/machineChipFit'

describe('machineChipRoom', () => {
  it('is twice the smaller distance from the middle, minus the gaps', () => {
    // 1200 px bar: logo side ends at 300, controls start at 800.
    expect(machineChipRoom({ center: 600, leftEnd: 300, rightStart: 800 })).toBe(2 * (200 - 12))
  })
  it('is never negative when a side reaches past the middle', () => {
    expect(machineChipRoom({ center: 400, leftEnd: 200, rightStart: 380 })).toBe(0)
  })
})

describe('machineChipMode', () => {
  it('shows the name, then the dot, then nothing', () => {
    expect(machineChipMode(200, 120, 32)).toBe('full')
    expect(machineChipMode(100, 120, 32)).toBe('dot')
    expect(machineChipMode(20, 120, 32)).toBe('hidden')
  })
  it('does not claim the full chip fits before it was measured', () => {
    expect(machineChipMode(500, 0, 32)).toBe('dot')
  })
})
