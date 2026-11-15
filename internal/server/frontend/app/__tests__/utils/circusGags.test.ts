import { describe, it, expect } from 'vitest'
import { gagForEvent, gagsToEvict, freeWalkerSlot, lettersFrom, MAX_GAGS, MAX_WALKERS } from '~/composables/agents/zen/circusGags'
import { toolCategory } from '~/composables/agents/zen/types'

const ev = (tool: string, isError = false) => ({ category: toolCategory(tool, isError), isError })

describe('circus gags', () => {
  it('maps tool events to collage gags', () => {
    expect(gagForEvent(ev('Read'))).toBe('book')
    expect(gagForEvent(ev('Edit'))).toBe('quill')
    expect(gagForEvent(ev('Write'))).toBe('quill')
    expect(gagForEvent(ev('Bash'))).toBe('steam')
    expect(gagForEvent(ev('Grep'))).toBe('magnifier')
    expect(gagForEvent(ev('Glob'))).toBe('magnifier')
    expect(gagForEvent(ev('WebFetch'))).toBe('cherub')
    expect(gagForEvent(ev('WebSearch'))).toBe('cherub')
    expect(gagForEvent(ev('Message'))).toBe('letters')
    expect(gagForEvent(ev('mcp__x__y'))).toBe('pop')
  })

  it('leaves Task/Agent to the subagent walkers', () => {
    expect(gagForEvent(ev('Task'))).toBeNull()
    expect(gagForEvent(ev('Agent'))).toBeNull()
  })

  it('brings the foot down for any error or interrupt', () => {
    expect(gagForEvent(ev('Error', true))).toBe('stomp')
    expect(gagForEvent(ev('Interrupt', true))).toBe('stomp')
    expect(gagForEvent({ category: 'read', isError: true })).toBe('stomp')
  })

  it('evicts the oldest gags so a new one fits under the cap', () => {
    expect(gagsToEvict([], 3)).toEqual([])
    expect(gagsToEvict([1, 2], 3)).toEqual([])
    expect(gagsToEvict([1, 2, 3], 3)).toEqual([1])
    expect(gagsToEvict([1, 2, 3, 4, 5], 3)).toEqual([1, 2, 3])
    const many = Array.from({ length: 50 }, (_, i) => i)
    expect(many.length - gagsToEvict(many).length + 1).toBe(MAX_GAGS)
  })

  it('hands out walker slots until the stage is full', () => {
    expect(freeWalkerSlot(new Set())).toBe(0)
    expect(freeWalkerSlot(new Set([0, 1, 3]))).toBe(2)
    const full = new Set(Array.from({ length: MAX_WALKERS }, (_, i) => i))
    expect(freeWalkerSlot(full)).toBe(-1)
  })

  it('cuts letters out of a label', () => {
    expect(lettersFrom('hello world')).toEqual(['H', 'E', 'L', 'L', 'O'])
    expect(lettersFrom('a-b', 3)).toEqual(['A', 'B'])
    expect(lettersFrom('')).toEqual(['B', 'L', 'A', 'H'])
    expect(lettersFrom('…')).toEqual(['B', 'L', 'A', 'H'])
  })
})
