/**
 * Skills of one session (GET/PUT /api/agent/sessions/:id/skills), and the
 * small rules the Skills panel uses: which skills the session gets and why,
 * the full "added" list a change sends (PUT replaces it), the note about when
 * a change takes effect, and the composer text "Use" puts in the chat.
 *
 * Approved skills reach a session in three ways: most apply everywhere; a
 * project's skills apply when the session works in that project's folder;
 * and skills can be added to a session (its EnabledSkillIDs).
 */

/** Why the session gets a skill; '' when it doesn't (another project's skill). */
export type SkillReach = 'everywhere' | 'folder' | 'added' | ''
export type SkillsRuntime = 'claude-sdk' | 'codex' | 'direct' | ''

export interface SessionSkill {
  id: string
  name: string
  description?: string
  scope: string
  argument_hint?: string
  user_invocable: boolean
  reach: SkillReach
  /** In the session's own list (removable) */
  added: boolean
}

export interface SessionSkillsState {
  session_id: string
  runtime: SkillsRuntime
  skills: SessionSkill[]
  /** The Claude CLI picks the change up before the next prompt (a restart) */
  restart_pending: boolean
  subagents_running: number
}

/** Above this many skills the panel shows a search field. */
export const SKILLS_SEARCH_THRESHOLD = 8

const REACHES = new Set<SkillReach>(['everywhere', 'folder', 'added', ''])
const RUNTIMES = new Set<SkillsRuntime>(['claude-sdk', 'codex', 'direct', ''])

/** Reads a server answer defensively (missing fields → empty). */
export function parseSessionSkills(body: any, sessionId = ''): SessionSkillsState {
  const list: any[] = Array.isArray(body?.skills) ? body.skills : []
  const skills = list
    .filter(s => s && typeof s.id === 'string' && typeof s.name === 'string')
    .map((s): SessionSkill => ({
      id: s.id,
      name: s.name,
      description: typeof s.description === 'string' && s.description ? s.description : undefined,
      scope: typeof s.scope === 'string' ? s.scope : '',
      argument_hint: typeof s.argument_hint === 'string' && s.argument_hint ? s.argument_hint : undefined,
      user_invocable: !!s.user_invocable,
      reach: REACHES.has(s.reach) ? s.reach : '',
      added: !!s.added,
    }))
  return {
    session_id: typeof body?.session_id === 'string' ? body.session_id : sessionId,
    runtime: RUNTIMES.has(body?.runtime) ? body.runtime : '',
    skills,
    restart_pending: !!body?.restart_pending,
    subagents_running: Math.max(0, Number(body?.subagents_running) || 0),
  }
}

export function isActiveSkill(skill: Pick<SessionSkill, 'reach'>): boolean {
  return skill.reach !== ''
}

/** How many skills the session gets. */
export function activeSkillCount(skills: readonly SessionSkill[] | undefined | null): number {
  return (skills || []).filter(isActiveSkill).length
}

/** The small tag saying why the session gets a skill. */
export function reachLabel(reach: SkillReach): string {
  switch (reach) {
    case 'everywhere': return 'Everywhere'
    case 'folder': return 'This project'
    case 'added': return 'Added'
    default: return ''
  }
}

/** A skill can be removed only when the session gets it because it was added. */
export function canRemoveSkill(skill: SessionSkill): boolean {
  return skill.added && skill.reach === 'added'
}

/** Name or description contains the query (case-insensitive). */
export function filterSessionSkills(skills: readonly SessionSkill[], query: string): SessionSkill[] {
  const q = query.trim().toLowerCase()
  if (!q) return [...skills]
  return skills.filter(s => s.name.toLowerCase().includes(q) || (s.description || '').toLowerCase().includes(q))
}

/** "In this session" (reach set) and "Other projects' skills" (reach ''), in server order. */
export function groupSessionSkills(skills: readonly SessionSkill[]): { active: SessionSkill[]; other: SessionSkill[] } {
  const active: SessionSkill[] = []
  const other: SessionSkill[] = []
  for (const s of skills) (isActiveSkill(s) ? active : other).push(s)
  return { active, other }
}

/** The session's added list as it is now. */
export function addedSkillIds(skills: readonly SessionSkill[]): string[] {
  return skills.filter(s => s.added).map(s => s.id)
}

/**
 * The full added list after adding or removing one skill. PUT replaces the
 * list, so every other added skill is sent along unchanged.
 */
export function nextAddedSkillIds(skills: readonly SessionSkill[], skillId: string, add: boolean): string[] {
  const current = addedSkillIds(skills)
  if (add) return current.includes(skillId) ? current : [...current, skillId]
  return current.filter(id => id !== skillId)
}

/** When a change reaches the agent; '' when there is nothing to say. */
export function sessionSkillsNote(state: Pick<SessionSkillsState, 'runtime' | 'restart_pending' | 'subagents_running'> | null | undefined): string {
  if (!state) return ''
  if (state.runtime === 'claude-sdk') {
    if (!state.restart_pending) return ''
    const n = state.subagents_running
    const restart = n > 0
      ? ` The agent restarts then, which stops ${n} running subagent${n === 1 ? '' : 's'}.`
      : ''
    return `Takes effect with your next message.${restart}`
  }
  if (state.runtime === 'codex' || state.runtime === 'direct') {
    return 'Skills are listed to this agent when its conversation starts; added skills reach it through its skill tools.'
  }
  return ''
}

/** What "Use" types: the skill as a slash command, ready for its arguments. */
export function skillSlashText(name: string): string {
  return `/${name} `
}

/**
 * The composer text after "Use": the slash command in front of what was
 * already typed, replacing a slash command that was there first.
 */
export function composerWithSkill(current: string, name: string): string {
  const slash = skillSlashText(name)
  const rest = (current || '').replace(/^\/\S*\s*/, '').trimStart()
  return rest ? slash + rest : slash
}
