/**
 * The Notch Theatre (desktop app): a miniature puppet theatre around the
 * MacBook notch. Every session that is doing something is one of the Circus
 * costume eggs on its stage (the same egg everywhere: `circusCharacterFor`).
 *
 * This file turns the session list and the pending requests into a scene;
 * it is pure so the stage can be tested without a window. Priority, highest
 * first, decides the mood of the whole stage:
 *
 *   needs         an approval or question waits     egg rings a bell, holds a placard
 *   oops          a turn just failed                the foot comes down, cracked egg
 *   bow           a turn just ended                 a bow and confetti
 *   working       a turn runs (or subagents do)     eggs at work, sweating on long runs
 *   intermission  only usage-limit waits remain     curtain with the countdown
 *   loop          only scheduled /loop or cron      curtains closed, hourglass
 *   asleep        nothing at all                    curtains closed, an egg naps
 */
import { circusCharacterFor, type CircusKind } from '~/utils/agents/circusCharacters'
import { hasLimitWait, type LimitWait } from '~/utils/agents/limitWait'
import { loopPhase, type ScheduledLoop } from '~/utils/agents/scheduledLoop'
import { permissionWaitLabel } from '~/utils/agents/sessionGroups'

export type NotchMood = 'needs' | 'oops' | 'bow' | 'working' | 'intermission' | 'loop' | 'asleep'
export type ActorState = 'needs' | 'cracked' | 'bowing' | 'working' | 'sweating' | 'waiting' | 'loop'

/** A turn running longer than this makes its egg sweat. */
export const SWEAT_AFTER_MS = 10 * 60_000
/** How long a bow (turn ended) or a crack (turn failed) stays on stage. */
export const CHEER_MS = 8_000
/** At most this many eggs on the collapsed stage; the rest are "+N". */
export const STAGE_SEATS = 3

export interface NotchSession {
  id: string
  status: string
  updated_at?: string
  pending_permissions?: number
  pending_questions?: number
  subagents_running?: number
  error_message?: string | null
  limit_wait?: LimitWait | null
  scheduled_loop?: ScheduledLoop | null
  selected_avatar?: { name?: string } | null
  options?: { agent_name?: string; working_directory?: string }
}

export interface PendingPermission {
  session_id: string
  session_name?: string
  permission_id: string
  tool_name: string
  details?: Record<string, string>
  asked_at?: string
}

export interface PendingQuestion {
  session_id: string
  session_name?: string
  question_id: string
  question: string
  header?: string
  options: { label: string; description?: string }[]
  multi_select?: boolean
  kind?: string
  requires_user?: boolean
  recommended?: number[]
  auto_answer_at?: string | null
  asked_at?: string
}

/** Turns that ended or failed recently, as seen by this window (sessionId → ms). */
export interface Recent {
  doneAt: Record<string, number>
  failedAt: Record<string, number>
}

/** One approval or question card in the expanded playbill. */
export interface Cue {
  key: string
  type: 'permission' | 'question'
  sessionId: string
  sessionName: string
  /** "Allow git push origin main?" / the question text. */
  title: string
  /** Options the notch can answer itself (single-select agent questions only). */
  options: { label: string; recommended: boolean }[]
  /** Too much for the notch: multi-select, loop proposals, confirmation cards. */
  openInApp: boolean
  permission?: PendingPermission
  question?: PendingQuestion
}

export interface NotchActor {
  id: string
  name: string
  kind: CircusKind
  state: ActorState
  /** Plain words for the playbill row ("Working · 14 min", "Allow Edit app.vue?"). */
  line: string
  subagents: number
}

export interface NotchScene {
  mood: NotchMood
  /** Eggs on the collapsed stage (most urgent first). */
  stage: NotchActor[]
  /** Everyone in the playbill. */
  actors: NotchActor[]
  extra: number
  cues: Cue[]
  /** Short text for the right ear ("2", "⏸ 02:01", "↻ 14m"). */
  badge: string
  /** Screen-reader / tooltip summary. */
  headline: string
}

export function sessionName(s: NotchSession): string {
  const name = s.options?.agent_name?.trim() || s.selected_avatar?.name?.trim()
  if (name) return name
  const dir = s.options?.working_directory?.split('/').filter(Boolean).pop()
  return dir || 'Session'
}

function minutes(ms: number): string {
  const m = Math.max(0, Math.round(ms / 60_000))
  if (m < 1) return 'just now'
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60)
  return m % 60 ? `${h} h ${m % 60} min` : `${h} h`
}

/** "02:01" until the time, or "now". */
export function countdown(at: number, now: number): string {
  const s = Math.max(0, Math.round((at - now) / 1000))
  if (s === 0) return 'now'
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}`
  return `${String(m).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
}

function isWorking(s: NotchSession): boolean {
  return s.status === 'processing' || (s.subagents_running || 0) > 0
}

function needsYou(s: NotchSession, cues: Cue[]): boolean {
  return cues.some(c => c.sessionId === s.id) || (s.pending_permissions || 0) > 0 || (s.pending_questions || 0) > 0
}

function since(s: NotchSession, now: number): number {
  const at = s.updated_at ? Date.parse(s.updated_at) : NaN
  return Number.isFinite(at) ? now - at : 0
}

export function buildCues(permissions: readonly PendingPermission[], questions: readonly PendingQuestion[]): Cue[] {
  const cues: Cue[] = []
  for (const q of questions) {
    const recommended = new Set(q.recommended || [])
    const openInApp = !!q.multi_select || !!q.kind || !!q.requires_user || !q.options?.length
    cues.push({
      key: `q:${q.question_id}`,
      type: 'question',
      sessionId: q.session_id,
      sessionName: q.session_name || 'Session',
      title: q.kind === 'loop_session' ? 'Start a loop session?' : q.kind === 'worker_session' ? 'Start a worker session?' : q.question,
      options: openInApp ? [] : q.options.map((o, i) => ({ label: o.label, recommended: recommended.has(i) })),
      openInApp,
      question: q,
    })
  }
  for (const p of permissions) {
    cues.push({
      key: `p:${p.permission_id}`,
      type: 'permission',
      sessionId: p.session_id,
      sessionName: p.session_name || 'Session',
      title: permissionWaitLabel({ tool: p.tool_name, details: p.details }),
      options: [],
      openInApp: false,
      permission: p,
    })
  }
  const askedAt = (c: Cue) => Date.parse(c.permission?.asked_at || c.question?.asked_at || '') || 0
  return cues.sort((a, b) => askedAt(a) - askedAt(b))
}

function actorFor(s: NotchSession, cues: Cue[], recent: Recent, now: number): NotchActor | null {
  const look = circusCharacterFor(s.id)
  const base = { id: s.id, name: sessionName(s), kind: look.kind, subagents: s.subagents_running || 0 }
  const cue = cues.find(c => c.sessionId === s.id)
  if (needsYou(s, cues)) {
    return { ...base, state: 'needs', line: cue ? cue.title : (s.pending_questions ? 'Question waiting' : 'Approval needed') }
  }
  const failed = recent.failedAt[s.id]
  if (failed && now - failed < CHEER_MS) return { ...base, state: 'cracked', line: "'Tis but a scratch" }
  if (isWorking(s)) {
    const ms = s.status === 'processing' ? since(s, now) : 0
    const sub = base.subagents ? ` · ${base.subagents} subagent${base.subagents === 1 ? '' : 's'}` : ''
    const what = s.status === 'processing' ? `Working · ${minutes(ms)}` : 'Subagents working'
    return { ...base, state: ms >= SWEAT_AFTER_MS ? 'sweating' : 'working', line: s.status === 'processing' ? what + sub : `${what}${base.subagents ? ` · ${base.subagents}` : ''}` }
  }
  const done = recent.doneAt[s.id]
  if (done && now - done < CHEER_MS) return { ...base, state: 'bowing', line: 'Done. Take a bow!' }
  if (hasLimitWait(s.limit_wait)) {
    const at = Date.parse(s.limit_wait.resume_at)
    return { ...base, state: 'waiting', line: `Intermission · resumes in ${countdown(at, now)}` }
  }
  const phase = loopPhase(s.scheduled_loop, s.status, now)
  if (phase && s.scheduled_loop) {
    const at = Date.parse(s.scheduled_loop.next_at)
    return { ...base, state: 'loop', line: phase === 'waiting' ? `Next show in ${minutes(at - now)}` : 'Next show is due' }
  }
  return null
}

const STATE_RANK: Record<ActorState, number> = { needs: 0, cracked: 1, bowing: 2, sweating: 3, working: 3, waiting: 4, loop: 5 }

export function buildScene(
  sessions: readonly NotchSession[],
  permissions: readonly PendingPermission[],
  questions: readonly PendingQuestion[],
  recent: Recent,
  now: number,
): NotchScene {
  const live = sessions.filter(s => s.status !== 'ended')
  const known = new Set(live.map(s => s.id))
  const cues = buildCues(permissions, questions).filter(c => known.has(c.sessionId))
  const actors = live
    .map(s => actorFor(s, cues, recent, now))
    .filter((a): a is NotchActor => !!a)
    .sort((a, b) => STATE_RANK[a.state] - STATE_RANK[b.state])

  const has = (st: ActorState) => actors.some(a => a.state === st)
  let mood: NotchMood = 'asleep'
  if (has('needs')) mood = 'needs'
  else if (has('cracked')) mood = 'oops'
  else if (has('bowing')) mood = 'bow'
  else if (has('working') || has('sweating')) mood = 'working'
  else if (has('waiting')) mood = 'intermission'
  else if (has('loop')) mood = 'loop'

  const working = actors.filter(a => a.state === 'working' || a.state === 'sweating').length
  const needs = actors.filter(a => a.state === 'needs').length
  let badge = ''
  let headline = 'Nothing running. The troupe is resting, not dead.'
  switch (mood) {
    case 'needs':
      badge = '!'
      headline = needs === 1 ? `${actors[0]!.name}: ${actors[0]!.line}` : `${needs} sessions need you`
      break
    case 'oops':
      badge = '✕'
      headline = `${actors[0]!.name} hit a snag`
      break
    case 'bow':
      badge = '✓'
      headline = `${actors[0]!.name} is done`
      break
    case 'working':
      badge = String(working || actors.filter(a => a.subagents > 0).length)
      headline = working === 1 ? `${actors.find(a => a.state === 'working' || a.state === 'sweating')!.name} is working` : `${working} sessions working`
      break
    case 'intermission': {
      const first = live
        .filter(s => hasLimitWait(s.limit_wait))
        .map(s => Date.parse(s.limit_wait!.resume_at))
        .sort((a, b) => a - b)[0]!
      badge = `⏸ ${countdown(first, now)}`
      headline = `Intermission: usage limit, resumes in ${countdown(first, now)}`
      break
    }
    case 'loop': {
      const next = live
        .filter(s => s.scheduled_loop)
        .map(s => Date.parse(s.scheduled_loop!.next_at))
        .sort((a, b) => a - b)[0]!
      const m = Math.max(0, Math.round((next - now) / 60_000))
      badge = m > 0 ? `↻ ${m}m` : '↻'
      headline = m > 0 ? `Next show in ${minutes(next - now)}` : 'Next show is due'
      break
    }
  }

  return {
    mood,
    stage: actors.slice(0, STAGE_SEATS),
    actors,
    extra: Math.max(0, actors.length - STAGE_SEATS),
    cues,
    badge,
    headline,
  }
}

/**
 * Updates `recent` from one poll to the next: a session that was processing
 * and now is not ended its turn (failed when it carries a new error).
 * Returns the ids that just ended, so the caller can play a cue.
 */
export function noteTransitions(
  previous: ReadonlyMap<string, { status: string; error?: string | null }>,
  sessions: readonly NotchSession[],
  recent: Recent,
  now: number,
): { done: string[]; failed: string[] } {
  const done: string[] = []
  const failed: string[] = []
  for (const s of sessions) {
    const before = previous.get(s.id)
    if (!before || before.status !== 'processing' || s.status === 'processing') continue
    if (s.status === 'error' || (s.error_message && s.error_message !== before.error)) {
      recent.failedAt[s.id] = now
      failed.push(s.id)
    } else {
      recent.doneAt[s.id] = now
      done.push(s.id)
    }
  }
  for (const map of [recent.doneAt, recent.failedAt]) {
    for (const id of Object.keys(map)) if (now - map[id]! > CHEER_MS) delete map[id]
  }
  return { done, failed }
}
