/**
 * What the workers of a session are doing, live.
 *
 * A worker is another session, so its messages never reach the page showing
 * its parent. The server sends the parent's connections `worker_activity`
 * events instead: the worker's tool calls, results and words in the subagent
 * line format (`TOOL:<name>: <what>`, `RESULT_START` … `RESULT_END`, text),
 * and keeps the newest lines per worker for `GET /api/agent/sessions/:id/workers`.
 * The lines feed the same pipeline subagents use (utils/agents/workerAgent.ts).
 */
import { shallowRef, triggerRef, markRaw } from 'vue'
import { alignTimes, MAX_OUTPUT_LINES } from './useBackgroundAgents'

export interface WorkerActivityEvent {
  type: string
  /** The session that started the worker. */
  session_id?: string
  worker_session_id?: string
  /** The last line of the event. */
  output?: string
  is_error?: boolean
  /** Every line, when the event carries more than one. */
  lines?: string[]
  /** When the (last) line was produced, unix ms. */
  at?: number
  lines_at?: number[]
}

/** One worker in the answer of GET /api/agent/sessions/:id/workers. */
export interface WorkerActivityRow {
  session_id: string
  lines: string[]
  lines_at: number[]
}

// Singleton state shared across components. Like subagent output: append-only
// text kept out of deep reactivity, replaced (never edited) on every change.
const lineHistory = shallowRef<Map<string, string[]>>(new Map())
const lineTimes = new Map<string, number[]>()
const NO_LINES: string[] = markRaw([])
const NO_TIMES: number[] = markRaw([])
// Parents whose workers' kept lines were asked for.
const loadedParents = new Set<string>()

function setLines(workerId: string, lines: string[], times?: readonly number[]) {
  lineTimes.set(workerId, markRaw(alignTimes(times, lines.length)))
  lineHistory.value.set(workerId, markRaw(lines))
  triggerRef(lineHistory)
}

function appendLines(workerId: string, lines: string[], times?: readonly number[]) {
  const history = lineHistory.value.get(workerId) || []
  const historyTimes = lineTimes.get(workerId) || []
  const keepOld = Math.max(0, MAX_OUTPUT_LINES - lines.length)
  const kept = history.length > keepOld ? history.slice(history.length - keepOld) : history
  const keptTimes = alignTimes(historyTimes, history.length).slice(history.length - kept.length)
  const added = lines.length > MAX_OUTPUT_LINES ? lines.slice(-MAX_OUTPUT_LINES) : lines
  // Lines without a server time get the time they arrived.
  const now = Date.now()
  const addedTimes = alignTimes(times, lines.length).slice(lines.length - added.length).map(t => t || now)
  setLines(workerId, kept.concat(added), keptTimes.concat(addedTimes))
}

export function useWorkerActivity() {
  /** The worker's kept lines, oldest first. Reading it tracks the times too. */
  const getWorkerLines = (workerId: string): string[] => lineHistory.value.get(workerId) || NO_LINES

  /** When each line of getWorkerLines(workerId) was produced (unix ms, 0 = unknown). */
  const getWorkerTimes = (workerId: string): number[] => lineTimes.get(workerId) || NO_TIMES

  const processWebSocketMessage = (message: WorkerActivityEvent) => {
    if (message.type !== 'worker_activity' || !message.worker_session_id) return
    const many = Array.isArray(message.lines) && message.lines.length > 0
    if (!many && !message.output) return
    const lines = many ? message.lines! : [message.output!]
    const times = many ? message.lines_at : message.at ? [message.at] : undefined
    appendLines(message.worker_session_id, lines, times)
  }

  /**
   * The server's kept lines for the workers of a session, e.g. after a reload.
   * They are older than anything live, so a worker with live lines keeps them.
   */
  const applyKeptLines = (workers: WorkerActivityRow[] | undefined | null) => {
    for (const worker of workers || []) {
      if (!worker?.session_id || !Array.isArray(worker.lines) || !worker.lines.length) continue
      if (getWorkerLines(worker.session_id).length) continue
      setLines(worker.session_id, worker.lines.slice(-MAX_OUTPUT_LINES), worker.lines_at)
    }
  }

  /**
   * Loads the kept lines of parentId's workers, once per parent (a failed
   * load is tried again next time). `fetcher` is the page's authenticated fetch.
   */
  const loadWorkerActivity = async (
    parentId: string,
    fetcher: (url: string) => Promise<{ ok: boolean; json: () => Promise<any> }>,
  ) => {
    if (!parentId || loadedParents.has(parentId)) return
    loadedParents.add(parentId)
    try {
      const response = await fetcher(`/api/agent/sessions/${parentId}/workers`)
      if (!response.ok) {
        loadedParents.delete(parentId)
        return
      }
      applyKeptLines((await response.json())?.workers)
    } catch {
      // Live events still arrive.
      loadedParents.delete(parentId)
    }
  }

  /** Forgets everything (tests, sign-out). */
  const resetWorkerActivity = () => {
    lineHistory.value.clear()
    lineTimes.clear()
    loadedParents.clear()
    triggerRef(lineHistory)
  }

  return {
    getWorkerLines,
    getWorkerTimes,
    processWebSocketMessage,
    applyKeptLines,
    loadWorkerActivity,
    resetWorkerActivity,
  }
}
