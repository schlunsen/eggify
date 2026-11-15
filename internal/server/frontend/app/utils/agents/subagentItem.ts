/**
 * One subagent as the Subagents view shows it, derived from the agent record
 * and its output lines. Items are rebuilt only when their inputs change, so
 * cards whose agent did nothing keep the same object and skip re-rendering.
 */
import type { BackgroundAgent } from '~/composables/agents/useBackgroundAgents'
import { renderSafeMarkdown } from '~/utils/safeMarkdown'
import {
  finalReport, reportGist, subagentDisplayStatus, subagentNowAction, subagentProblem, subagentStepCount, subagentSteps,
  type NowAction, type SubagentDisplayStatus, type ToolAction
} from './subagentActivity'
import { latestTool, plainLine, subagentTimeline, type TimelineEntry, type TimelineTool } from './subagentTimeline'
import { isKeyStep, subagentProducts, type SubagentProducts } from './subagentBrief'
import { modelLabel, modelTitle } from './modelLabel'
import { homeDir } from '~/utils/homeDir'

export interface SubagentItem {
  id: string
  agent: BackgroundAgent
  title: string
  type: string
  background: boolean
  /** Short model label ("Haiku 4.5"), "" when unknown. */
  model: string
  /** Tooltip with the full model id and whether it is only requested. */
  modelTitle: string
  /** The model comes from the launch call, not yet from the agent itself. */
  modelRequested: boolean
  parentTitle: string
  /** The home folder its paths were shortened with (they are rebuilt when it becomes known). */
  home: string
  status: SubagentDisplayStatus
  lines: string[]
  /** When each line was produced (unix ms, 0 = unknown), parallel to lines; [] when none is known. */
  times: readonly number[]
  now: NowAction
  steps: ToolAction[]
  stepCount: number
  /** Every tool call (with its result) and note, oldest first. */
  timeline: TimelineEntry[]
  /** The newest tool call. */
  latest: TimelineTool | null
  /** What it was asked to do. */
  prompt: string
  /** How many steps changed something, launched an agent or failed. */
  keyStepCount: number
  /** Pull requests, commits, pushed branches and changed files. */
  products: SubagentProducts
  /** One plain line for the list: the report's gist (done) or the problem. */
  summary: string
  /** Sanitized HTML of the report's gist / the whole report (done agents only). */
  resultHtml: string
  reportHtml: string
  /** Whether the full report says more than the gist. */
  hasMoreReport: boolean
  /** A failed/interrupted agent's error in plain words, and the raw message. */
  problem: string
  rawError: string
  startedAt: number
  endedAt: number
  lastActivityAt: number
  created_at: string
  updated_at: string
  completed_at?: string
  error_message?: string
  interrupted?: boolean
  last_output?: string
  /** Set for a worker session shown like a subagent (utils/agents/workerAgent.ts): its session id. */
  sessionLink?: string
}

export function buildSubagentItem(agent: BackgroundAgent, lines: string[], parentTitle: string, times: readonly number[] = []): SubagentItem {
  const status = subagentDisplayStatus(agent)
  let resultHtml = ''
  let reportHtml = ''
  let hasMoreReport = false
  let summary = ''
  if (status === 'done') {
    const report = finalReport(agent, lines)
    const gist = reportGist(report)
    summary = plainLine(gist)
    if (gist) {
      resultHtml = renderSafeMarkdown(gist)
      hasMoreReport = report.replace(/\s+/g, ' ').trim().length > gist.replace(/\s+/g, ' ').replace(/…$/, '').trim().length + 1
      reportHtml = hasMoreReport ? renderSafeMarkdown(report) : ''
    }
  }
  const problem = status === 'failed' || status === 'interrupted' ? subagentProblem(agent) : ''
  const timeline = subagentTimeline(lines, status === 'done', times)
  const started = Date.parse(agent.created_at)
  const ended = Date.parse(agent.completed_at || agent.updated_at || '')
  return {
    id: agent.agent_id,
    agent,
    title: agent.description || agent.subagent_type || 'Subagent',
    type: agent.subagent_type || 'agent',
    background: !!agent.background,
    model: modelLabel(agent.model),
    modelTitle: modelTitle(agent.model, agent.model_source),
    modelRequested: !!agent.model && agent.model_source === 'requested',
    parentTitle,
    home: homeDir.value,
    status,
    lines,
    times,
    now: subagentNowAction(lines),
    steps: subagentSteps(lines, 5),
    stepCount: subagentStepCount(lines),
    timeline,
    latest: latestTool(timeline),
    prompt: (agent.prompt || '').trim(),
    keyStepCount: timeline.reduce((n, e) => n + (e.kind === 'tool' && isKeyStep(e) ? 1 : 0), 0),
    products: subagentProducts(timeline),
    summary: summary || problem,
    resultHtml,
    reportHtml,
    hasMoreReport,
    problem,
    rawError: agent.error_message || '',
    startedAt: started,
    endedAt: status === 'running' ? NaN : ended,
    lastActivityAt: Date.parse(agent.updated_at || agent.created_at),
    created_at: agent.created_at,
    updated_at: agent.updated_at,
    completed_at: agent.completed_at,
    error_message: agent.error_message,
    interrupted: agent.interrupted,
    last_output: agent.last_output
  }
}

/** The inputs an item depends on; a new item is built only when one changes. */
export function itemInputsChanged(item: SubagentItem, agent: BackgroundAgent, lines: string[], parentTitle: string, times: readonly number[] = []): boolean {
  return item.lines !== lines || (item.times !== times && (item.times.length > 0 || times.length > 0)) || item.agent !== agent || item.parentTitle !== parentTitle ||
    item.status !== subagentDisplayStatus(agent) || item.updated_at !== agent.updated_at ||
    item.completed_at !== agent.completed_at || item.error_message !== agent.error_message ||
    item.title !== (agent.description || agent.subagent_type || 'Subagent') ||
    item.interrupted !== agent.interrupted ||
    item.model !== modelLabel(agent.model) || item.modelRequested !== (!!agent.model && agent.model_source === 'requested') ||
    item.last_output !== agent.last_output || item.home !== homeDir.value
}
