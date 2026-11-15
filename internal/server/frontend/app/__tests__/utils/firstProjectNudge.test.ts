import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  FIRST_PROJECT_NUDGE_DISMISSED_KEY, readFirstProjectNudgeDismissed, shouldShowFirstProjectNudge,
  writeFirstProjectNudgeDismissed,
} from '../../utils/projects/firstProjectNudge'

describe('shouldShowFirstProjectNudge', () => {
  // projectsLoaded, projectCount, sessionsLoaded, sessionCount, dismissed → shown
  it.each([
    [true, 0, true, 0, false, true],
    [false, 0, true, 0, false, false], // projects loading or failed
    [true, 1, true, 0, false, false], // a project exists
    [true, 0, false, 0, false, false], // session list still loading
    [true, 0, true, 2, false, false], // existing user with sessions
    [true, 0, true, 0, true, false], // dismissed
    [false, 0, false, 0, false, false],
  ])('projects loaded=%s n=%s, sessions loaded=%s n=%s, dismissed=%s → %s', (projectsLoaded, projectCount, sessionsLoaded, sessionCount, dismissed, want) => {
    expect(shouldShowFirstProjectNudge({ projectsLoaded, projectCount, sessionsLoaded, sessionCount, dismissed })).toBe(want)
  })
})

describe('dismissal storage', () => {
  afterEach(() => { vi.unstubAllGlobals(); localStorage.clear() })

  it('remembers the dismissal', () => {
    expect(readFirstProjectNudgeDismissed()).toBe(false)
    writeFirstProjectNudgeDismissed()
    expect(localStorage.getItem(FIRST_PROJECT_NUDGE_DISMISSED_KEY)).toBe('1')
    expect(readFirstProjectNudgeDismissed()).toBe(true)
  })

  it('survives a throwing localStorage', () => {
    const boom = () => { throw new Error('denied') }
    vi.stubGlobal('localStorage', { getItem: boom, setItem: boom })
    expect(readFirstProjectNudgeDismissed()).toBe(false)
    expect(() => writeFirstProjectNudgeDismissed()).not.toThrow()
  })
})
