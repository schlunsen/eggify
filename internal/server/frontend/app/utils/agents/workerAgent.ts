/**
 * A worker session in the shape the subagent pipeline reads (BackgroundAgent),
 * so the parent's Subagents card shows its workers next to its subagents: the
 * live Now line, steps, report and status, from the lines `worker_activity`
 * events carry (composables/agents/useWorkerActivity.ts).
 *
 * A worker is a whole session, not a subagent: it never fails or finishes the
 * way a subagent does. Working → running; idle, waiting or ended → done, with
 * its last update as the end.
 */
import type { BackgroundAgent } from '~/composables/agents/useBackgroundAgents'
import { workerMode, workerOf, workerParentId, workerStatus, WORKER_ROLE_LABELS, type TeamSession } from './team'

/** "Build worker", "Review worker": the item's type line. */
export function workerAgentType(session: TeamSession): string {
  return `${WORKER_ROLE_LABELS[workerMode(session)].toLowerCase()} worker`
}

/** The pseudo agent of a worker session, or null when it is not a worker. */
export function workerAgent(session: TeamSession, lines: readonly string[]): BackgroundAgent | null {
  const worker = workerOf(session)
  if (!worker) return null
  const running = workerStatus(session) === 'working'
  const created = session.created_at || session.updated_at || ''
  const updated = session.updated_at || created
  const task = (worker.task || '').trim()
  const model = (session.options?.model || session.model_name || '').trim()
  return {
    agent_id: session.id,
    parent_session_id: workerParentId(session),
    subagent_type: workerAgentType(session),
    description: firstLine(task) || `${WORKER_ROLE_LABELS[workerMode(session)]} worker`,
    prompt: task,
    status: running ? 'running' : 'completed',
    progress: running ? 0 : 1,
    last_output: lines.length ? lines[lines.length - 1]! : '',
    output_lines: lines.length,
    created_at: created,
    updated_at: updated,
    completed_at: running ? undefined : updated,
    background: true,
    model: model || undefined,
    model_source: model ? 'actual' : undefined,
  }
}

function firstLine(text: string): string {
  const line = text.split('\n').find(l => l.trim())?.trim() || ''
  return line.length > 120 ? `${line.slice(0, 119)}…` : line
}

/**
 * Keeps one pseudo agent per worker while its inputs stay the same, so the
 * subagent items built from it (identity-compared) are rebuilt only on change.
 */
export function createWorkerAgentCache() {
  const cache = new Map<string, { key: string; agent: BackgroundAgent }>()
  return {
    get(session: TeamSession, lines: readonly string[]): BackgroundAgent | null {
      const worker = workerOf(session)
      if (!worker) { cache.delete(session.id); return null }
      const key = [
        workerStatus(session), session.updated_at, session.created_at, worker.task, worker.mode,
        session.options?.model, session.model_name, lines.length, lines[lines.length - 1],
      ].join('\u0000')
      const old = cache.get(session.id)
      if (old && old.key === key) return old.agent
      const agent = workerAgent(session, lines)!
      cache.set(session.id, { key, agent })
      return agent
    },
    /** Forgets workers no longer listed. */
    keep(ids: Set<string>) {
      for (const id of cache.keys()) if (!ids.has(id)) cache.delete(id)
    },
    clear() { cache.clear() },
  }
}
