/**
 * Logic of the first-run "Start with a project" card on the agents page.
 * It shows only once the projects request has succeeded with an empty list
 * and the session list has loaded empty too (sessions don't need a project, so
 * a user with sessions is not on their first run), so existing users never
 * see a flash while either loads.
 */

export const FIRST_PROJECT_NUDGE_DISMISSED_KEY = 'eggify-first-project-nudge-dismissed'

export interface FirstProjectNudgeState {
  projectsLoaded: boolean
  projectCount: number
  sessionsLoaded: boolean
  /** All sessions, not the filtered view. */
  sessionCount: number
  dismissed: boolean
}

export function shouldShowFirstProjectNudge(s: FirstProjectNudgeState): boolean {
  return s.projectsLoaded && s.projectCount === 0 && s.sessionsLoaded && s.sessionCount === 0 && !s.dismissed
}

export function readFirstProjectNudgeDismissed(): boolean {
  try {
    return localStorage.getItem(FIRST_PROJECT_NUDGE_DISMISSED_KEY) === '1'
  } catch { return false }
}

export function writeFirstProjectNudgeDismissed(): void {
  try { localStorage.setItem(FIRST_PROJECT_NUDGE_DISMISSED_KEY, '1') } catch { /* private mode */ }
}
