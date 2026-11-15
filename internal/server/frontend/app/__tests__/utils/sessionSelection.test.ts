import {
  groupCheckState,
  pruneSelected,
  selectRange,
  sessionCountLabel,
  summarizeSelection,
  toggleGroupSelected,
  toggleSelected,
} from '../../utils/agents/sessionSelection'

const order = ['a', 'b', 'c', 'd', 'e']

it('toggles one id without touching the input set', () => {
  const start = new Set(['a'])
  expect([...toggleSelected(start, 'b')]).toEqual(['a', 'b'])
  expect([...toggleSelected(start, 'a')]).toEqual([])
  expect([...start]).toEqual(['a'])
})

it('shift-click selects (or clears) every row between the anchor and the clicked one', () => {
  expect([...selectRange(new Set(['b']), order, 'b', 'd')].sort()).toEqual(['b', 'c', 'd'])
  expect([...selectRange(new Set(), order, 'e', 'c')].sort()).toEqual(['c', 'd', 'e'])
  // The clicked row was selected: the range is cleared
  expect([...selectRange(new Set(['a', 'b', 'c', 'd']), order, 'a', 'c')]).toEqual(['d'])
  // No anchor on screen: a plain toggle
  expect([...selectRange(new Set(), order, 'gone', 'c')]).toEqual(['c'])
  expect([...selectRange(new Set(), order, null, 'c')]).toEqual(['c'])
})

it('reports and toggles a group checkbox', () => {
  expect(groupCheckState(['a', 'b'], new Set())).toBe('none')
  expect(groupCheckState(['a', 'b'], new Set(['a']))).toBe('some')
  expect(groupCheckState(['a', 'b'], new Set(['a', 'b', 'x']))).toBe('all')
  expect([...toggleGroupSelected(new Set(['a', 'x']), ['a', 'b'])].sort()).toEqual(['a', 'b', 'x'])
  expect([...toggleGroupSelected(new Set(['a', 'b', 'x']), ['a', 'b'])]).toEqual(['x'])
})

it('drops ids that left the list and keeps the same set when nothing changed', () => {
  const sel = new Set(['a', 'b'])
  expect(pruneSelected(sel, ['a', 'b', 'c'])).toBe(sel)
  expect([...pruneSelected(sel, ['b'])]).toEqual(['b'])
})

it('summarises what End and Delete would act on', () => {
  const sessions = [
    { id: 'a', status: 'ended' },
    { id: 'b', status: 'idle' },
    { id: 'c', status: 'processing' },
    { id: 'd', status: 'idle', pending_questions: 1 },
    { id: 'e', status: 'idle' },
  ]
  expect(summarizeSelection(sessions, new Set(['a', 'b', 'c', 'd', 'gone']))).toEqual({
    ids: ['a', 'b', 'c', 'd'],
    endable: ['b', 'c', 'd'],
    busy: 2,
  })
  expect(sessionCountLabel(1)).toBe('1 session')
  expect(sessionCountLabel(3)).toBe('3 sessions')
})
