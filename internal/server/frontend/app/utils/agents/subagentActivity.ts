/**
 * What a subagent is working on and doing, in plain words, for the Subagents
 * view: the action of a `TOOL:<name>: <detail>` line ("Reading ZenMode.vue"),
 * a status that tells interrupted agents from failed ones, the gist of a final
 * report, grouping/filtering, and a frame batcher for live output.
 *
 * Built only from data the server already sends (recent_output, live
 * background_agent_output lines, status, error_message, last_output).
 */
import { parseToolLine } from './subagentOutput'
import { cleanCommand } from './commandText'

// ---------------------------------------------------------------------------
// Tool actions
// ---------------------------------------------------------------------------

/** What a tool's detail is: decides how it is shortened for display. */
export type ToolTarget = 'file' | 'command' | 'pattern' | 'url' | 'text' | 'none'

export interface ToolActionSpec {
  /** Human verb, e.g. "Reading". The target follows it. */
  verb: string
  icon: string
  target: ToolTarget
}

/**
 * Tool name → verb, icon and target kind. Plain data so other clients (the iOS
 * app's SubagentsSheet) can mirror it.
 */
export const SUBAGENT_TOOL_ACTIONS: Record<string, ToolActionSpec> = {
  Read: { verb: 'Reading', icon: '▤', target: 'file' },
  NotebookRead: { verb: 'Reading', icon: '▤', target: 'file' },
  Edit: { verb: 'Editing', icon: '✎', target: 'file' },
  MultiEdit: { verb: 'Editing', icon: '✎', target: 'file' },
  NotebookEdit: { verb: 'Editing', icon: '✎', target: 'file' },
  Write: { verb: 'Writing', icon: '✎', target: 'file' },
  FileChange: { verb: 'Changing', icon: '✎', target: 'file' },
  Bash: { verb: 'Running', icon: '>_', target: 'command' },
  BashOutput: { verb: 'Checking shell output', icon: '>_', target: 'none' },
  KillShell: { verb: 'Stopping a shell', icon: '>_', target: 'none' },
  KillBash: { verb: 'Stopping a shell', icon: '>_', target: 'none' },
  Grep: { verb: 'Searching for', icon: '⌕', target: 'pattern' },
  Glob: { verb: 'Finding files', icon: '⌕', target: 'pattern' },
  LS: { verb: 'Listing', icon: '⌕', target: 'file' },
  WebFetch: { verb: 'Fetching', icon: '◍', target: 'url' },
  WebSearch: { verb: 'Searching the web for', icon: '◍', target: 'text' },
  Task: { verb: 'Launching a subagent:', icon: '✦', target: 'text' },
  Agent: { verb: 'Launching a subagent:', icon: '✦', target: 'text' },
  TodoWrite: { verb: 'Updating the to-do list', icon: '☑', target: 'none' },
  Skill: { verb: 'Using skill', icon: '⚙', target: 'text' },
  AskUserQuestion: { verb: 'Asking you a question', icon: '?', target: 'none' }
}

export interface ToolAction {
  /** The tool as written in the line, e.g. "Read" or "mcp__n0__n0_tasks_list". */
  tool: string
  icon: string
  verb: string
  /** The shortened target ("ZenMode.vue"), or '' */
  target: string
  /** Verb and target: "Reading ZenMode.vue" */
  text: string
  /** The untouched line detail, for a tooltip. */
  full: string
}

function flatten(text: string): string {
  return text.replace(/\s+/g, ' ').trim()
}

export function clip(text: string, max: number): string {
  const flat = flatten(text)
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
}

function baseName(path: string): string {
  const trimmed = path.trim().replace(/[\\/]+$/, '')
  const parts = trimmed.split(/[\\/]/)
  return parts[parts.length - 1] || trimmed
}

function shortUrl(url: string): string {
  try {
    const parsed = new URL(url.trim())
    return clip(parsed.host + (parsed.pathname === '/' ? '' : parsed.pathname), 60)
  } catch {
    return clip(url, 60)
  }
}

function shortTarget(kind: ToolTarget, detail: string): string {
  if (!detail.trim() || kind === 'none') return ''
  switch (kind) {
    // A file detail can carry more than a path (e.g. "a.go (lines 1-20)"); keep the name.
    case 'file': return clip(detail.includes(' ') && !/^[\\/~.]/.test(detail) ? detail : baseName(detail), 60)
    case 'command': return clip(cleanCommand(detail).command, 140)
    case 'url': return shortUrl(detail)
    case 'pattern': return clip(detail, 60)
    default: return clip(detail, 80)
  }
}

/** Turns a tool call into a human action: Read + /a/b/ZenMode.vue → "Reading ZenMode.vue". */
export function describeToolAction(tool: string, detail = ''): ToolAction {
  let spec = SUBAGENT_TOOL_ACTIONS[tool]
  let verb: string
  if (spec) {
    verb = spec.verb
  } else if (tool.startsWith('mcp__')) {
    // mcp__<server>__<tool>: a known tool behind an MCP server keeps its verb.
    const parts = tool.split('__')
    const name = parts[parts.length - 1] || tool
    const server = parts.slice(1, -1).join('__')
    spec = SUBAGENT_TOOL_ACTIONS[name]
    verb = spec ? spec.verb : `Calling ${name}${server ? ` (${server})` : ''}`
    spec = spec || { verb, icon: '⚙', target: 'text' }
  } else {
    verb = `Using ${tool || 'a tool'}`
    spec = { verb, icon: '⚙', target: 'text' }
  }
  const target = shortTarget(spec.target, detail)
  return { tool, icon: spec.icon, verb, target, text: target ? `${verb} ${target}` : verb, full: flatten(detail) }
}

// ---------------------------------------------------------------------------
// Now and steps
// ---------------------------------------------------------------------------

export interface NowAction {
  kind: 'tool' | 'thinking' | 'starting'
  icon: string
  text: string
  /** Longer text for a tooltip: the full detail or the subagent's own words. */
  full: string
}

function insideResult(lines: string[], i: number): boolean {
  return lines.lastIndexOf('RESULT_START', i) > lines.lastIndexOf('RESULT_END', i)
}

/**
 * What a running subagent is doing right now: its latest tool call, or
 * "Thinking…" when the newest line is its own text.
 */
export function subagentNowAction(lines: string[]): NowAction {
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i] ?? ''
    const tool = parseToolLine(line)
    if (tool) {
      const action = describeToolAction(tool.name, tool.detail)
      return { kind: 'tool', icon: action.icon, text: action.text, full: action.full || action.text }
    }
    if (!line.trim() || line === 'RESULT_START' || line === 'RESULT_END') continue
    // Tool output belongs to the call above it: keep walking back to that call.
    if (insideResult(lines, i)) continue
    return { kind: 'thinking', icon: '…', text: 'Thinking…', full: clip(line, 300) }
  }
  return { kind: 'starting', icon: '…', text: 'Starting…', full: '' }
}

/** The last `count` tool calls as actions, oldest first. */
export function subagentSteps(lines: string[], count = 5): ToolAction[] {
  const steps: ToolAction[] = []
  for (let i = lines.length - 1; i >= 0 && steps.length < count; i--) {
    const tool = parseToolLine(lines[i] ?? '')
    if (tool) steps.unshift(describeToolAction(tool.name, tool.detail))
  }
  return steps
}

/** How many tool calls the kept output holds. */
export function subagentStepCount(lines: string[]): number {
  let count = 0
  for (const line of lines) if (line.startsWith('TOOL:')) count++
  return count
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

export type SubagentDisplayStatus = 'running' | 'done' | 'failed' | 'interrupted'

/**
 * Error messages of agents that were cut off rather than failing on their own.
 * Keep in sync with subagentInterruptionRE in internal/server/agents/background_agents.go.
 */
const INTERRUPTION_RE = /^(app restarted|agent (stopped|killed|interrupted|paused|cancell?ed|aborted)\b|sub-agent .{0,200}? (stopped|interrupted|cancell?ed)\b|session (ended|closed|stopped))|\[request interrupted/i

export function isInterruptionMessage(message: string | undefined | null): boolean {
  return !!message && INTERRUPTION_RE.test(message.trim())
}

interface StatusLike {
  status: string
  error_message?: string
  interrupted?: boolean
}

export function subagentDisplayStatus(agent: StatusLike): SubagentDisplayStatus {
  // Already classified (a SubagentItem's status).
  if (agent.status === 'done' || agent.status === 'interrupted') return agent.status
  if (agent.status === 'completed') return 'done'
  if (agent.status === 'failed') return agent.interrupted || isInterruptionMessage(agent.error_message) ? 'interrupted' : 'failed'
  return 'running' // running, waiting, or anything a newer server adds
}

export const STATUS_LABELS: Record<SubagentDisplayStatus, string> = {
  running: 'Running',
  done: 'Done',
  failed: 'Failed',
  interrupted: 'Interrupted'
}

/** A failed or interrupted agent's error in plain words. */
export function subagentProblem(agent: StatusLike): string {
  const message = (agent.error_message || '').trim()
  if (subagentDisplayStatus(agent) === 'interrupted') {
    if (/^agent paused: usage limit/i.test(message)) return 'The Claude usage limit stopped it. Its session continues once the limit resets.'
    if (/^app restarted/i.test(message)) return 'Eggify restarted while it was running, so it stopped before finishing.'
    if (/\[request interrupted/i.test(message)) return 'The turn was interrupted before it finished.'
    if (/killed|stopped|cancell?ed|aborted/i.test(message)) return 'It was stopped before it finished.'
    return 'It was interrupted before it finished.'
  }
  const firstLine = message
    .replace(/^(agent failed|sub-agent \S+ failed)\s*:?\s*/i, '')
    .split('\n').map(l => l.trim()).find(Boolean)
  return firstLine ? clip(firstLine, 240) : 'It failed without an error message.'
}

// ---------------------------------------------------------------------------
// Final report
// ---------------------------------------------------------------------------

/** The subagent's text after its last tool call: usually its final report. */
export function trailingText(lines: string[]): string {
  const text: string[] = []
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i] ?? ''
    if (line.startsWith('TOOL:') || line === 'RESULT_END' || line === 'RESULT_START') break
    text.unshift(line)
  }
  return text.join('\n').trim()
}

interface ReportLike extends StatusLike {
  last_output?: string
}

/** A finished agent's final report: the completion output, or its last words. */
export function finalReport(agent: ReportLike, lines: string[]): string {
  const last = (agent.last_output || '').trim()
  const usable = last && !last.startsWith('TOOL:') && last !== 'RESULT_END' && last !== 'RESULT_START' ? last : ''
  const tail = trailingText(lines)
  return usable.length >= tail.length ? usable : tail
}

const HEADING_RE = /^#{1,6}\s/
const RULE_RE = /^([-*_]\s*){3,}$/
// "**Summary**", "Summary:", "## Result" style labels carry no content of their own.
const LABEL_RE = /^(\*\*|__)?[\w ,'&/-]{1,40}(\*\*|__)?:?(\*\*|__)?$/

/**
 * The first meaningful sentence(s) of a report, as markdown: skips headings,
 * rules, bare labels and code, takes the first paragraph (or the first few
 * list items) and cuts it at a sentence end near `max` characters.
 */
export function reportGist(report: string, max = 320): string {
  const lines = report.replace(/\r\n/g, '\n').split('\n')
  const picked: string[] = []
  let inCode = false
  for (const raw of lines) {
    const line = raw.trim()
    if (line.startsWith('```')) {
      inCode = !inCode
      if (picked.length) break
      continue
    }
    if (inCode) continue
    if (!line) {
      if (picked.length) break
      continue
    }
    if (HEADING_RE.test(line) || RULE_RE.test(line)) {
      if (picked.length) break
      continue
    }
    if (!picked.length && LABEL_RE.test(line) && (line.endsWith(':') || /^(\*\*|__)/.test(line))) continue
    picked.push(line)
    if (picked.length >= 4) break
  }
  const isList = picked.length > 0 && /^([-*+]|\d+[.)])\s/.test(picked[0]!)
  let gist = isList ? picked.slice(0, 3).join('\n') : picked.join(' ')
  if (gist.length > max) {
    const cut = gist.slice(0, max)
    const end = Math.max(cut.lastIndexOf('. '), cut.lastIndexOf('! '), cut.lastIndexOf('? '))
    gist = end > max * 0.4 ? cut.slice(0, end + 1) : `${cut.slice(0, cut.lastIndexOf(' ') > 0 ? cut.lastIndexOf(' ') : max).trimEnd()}…`
  }
  return gist
}

// ---------------------------------------------------------------------------
// Grouping and filters
// ---------------------------------------------------------------------------

export type SubagentFilter = 'all' | SubagentDisplayStatus

export const SUBAGENT_FILTERS: SubagentFilter[] = ['all', 'running', 'done', 'failed', 'interrupted']

interface GroupLike extends StatusLike {
  created_at: string
  updated_at?: string
  completed_at?: string
}

function finishedAt(agent: GroupLike): number {
  return Date.parse(agent.completed_at || agent.updated_at || agent.created_at) || 0
}

export interface SubagentGroups<T> {
  /** Running agents, oldest first so cards keep their place. */
  running: T[]
  /** Finished agents (done, failed, interrupted), newest first. */
  finished: T[]
  counts: Record<SubagentFilter, number>
}

/** Splits agents into running and finished, applying a status filter; counts ignore the filter. */
export function groupSubagents<T extends GroupLike>(agents: T[], filter: SubagentFilter = 'all'): SubagentGroups<T> {
  const counts: Record<SubagentFilter, number> = { all: agents.length, running: 0, done: 0, failed: 0, interrupted: 0 }
  const running: T[] = []
  const finished: T[] = []
  for (const agent of agents) {
    const status = subagentDisplayStatus(agent)
    counts[status]++
    if (filter !== 'all' && filter !== status) continue
    if (status === 'running') running.push(agent)
    else finished.push(agent)
  }
  running.sort((a, b) => (Date.parse(a.created_at) || 0) - (Date.parse(b.created_at) || 0))
  finished.sort((a, b) => finishedAt(b) - finishedAt(a))
  return { running, finished, counts }
}

// ---------------------------------------------------------------------------
// Time
// ---------------------------------------------------------------------------

/** After this long without output, a running agent is flagged as quiet. */
export const STALE_AFTER_MS = 2 * 60 * 1000

export function formatDuration(ms: number): string {
  const seconds = Math.max(0, Math.round(ms / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return minutes < 60 ? `${minutes}m ${seconds % 60}s` : `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}

/** "just now", "12s ago", "3m ago". */
export function activityAgo(lastActivity: number, now: number): string {
  if (!Number.isFinite(lastActivity)) return ''
  const seconds = Math.max(0, Math.round((now - lastActivity) / 1000))
  if (seconds < 5) return 'just now'
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  return minutes < 60 ? `${minutes}m ago` : `${Math.floor(minutes / 60)}h ${minutes % 60}m ago`
}

// ---------------------------------------------------------------------------
// Batching
// ---------------------------------------------------------------------------

export interface FrameBatcher {
  /** Asks for one flush on the next frame; extra requests before it are merged. */
  request(): void
  /** Flushes now if a flush is pending. */
  flush(): void
  cancel(): void
}

/**
 * Coalesces many updates (one per WebSocket output event) into one call per
 * animation frame. Falls back to a timer where requestAnimationFrame is missing.
 */
export function createFrameBatcher(run: () => void, schedule?: (cb: () => void) => unknown, unschedule?: (handle: any) => void): FrameBatcher {
  const raf = typeof requestAnimationFrame === 'function'
  const sched = schedule || (raf ? (cb: () => void) => requestAnimationFrame(cb) : (cb: () => void) => setTimeout(cb, 16))
  const unsched = unschedule || (raf ? (h: number) => cancelAnimationFrame(h) : (h: any) => clearTimeout(h))
  let handle: unknown = null
  let pending = false
  const fire = () => {
    handle = null
    if (!pending) return
    pending = false
    run()
  }
  return {
    request() {
      if (pending) return
      pending = true
      handle = sched(fire)
    },
    flush() {
      if (handle !== null) unsched(handle)
      fire()
    },
    cancel() {
      if (handle !== null) unsched(handle)
      handle = null
      pending = false
    }
  }
}
