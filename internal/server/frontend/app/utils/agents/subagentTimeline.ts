/**
 * A subagent's activity as the Subagents view's detail pane shows it: every
 * tool call with the command or file it acted on (cleaned up for reading) and
 * the first lines of its result, plus the subagent's own notes in between.
 *
 * Built from the kept output lines (see subagentOutput.ts).
 */
import { subagentFeed } from './subagentOutput'
import { describeToolAction } from './subagentActivity'
import { cleanCommand, tidyPaths } from './commandText'

export { cleanCommand, splitPath, tidyPaths, type CleanCommand } from './commandText'

// ---------------------------------------------------------------------------
// Timeline
// ---------------------------------------------------------------------------

export type TimelineDetailKind = 'command' | 'file' | 'text' | 'none'

export interface TimelineTool {
  kind: 'tool'
  /** 1-based position among the tool calls. */
  n: number
  tool: string
  icon: string
  /** Short label: "Bash", "Read", "Search" … */
  label: string
  /** Plain words, e.g. "Reading SettingsView.swift" (for the list and Zen). */
  text: string
  detailKind: TimelineDetailKind
  /** The cleaned command / path / pattern. */
  detail: string
  cwd: string
  result: string[]
  /** The result looks like a failure (non-zero exit, error). */
  failed: boolean
  /** When the call was made (unix ms); absent when unknown. */
  at?: number
  /** When its result came back (unix ms), and how long that took; absent when unknown. */
  endedAt?: number
  durationMs?: number
}

export interface TimelineNote {
  kind: 'note'
  text: string
  /** When the note's first line was written (unix ms); absent when unknown. */
  at?: number
}

export type TimelineEntry = TimelineTool | TimelineNote

const SHORT_LABELS: Record<string, string> = {
  Bash: 'Shell', BashOutput: 'Shell', KillShell: 'Shell', KillBash: 'Shell',
  Read: 'Read', NotebookRead: 'Read', Edit: 'Edit', MultiEdit: 'Edit', NotebookEdit: 'Edit',
  Write: 'Write', FileChange: 'Change', Grep: 'Search', Glob: 'Find', LS: 'List',
  WebFetch: 'Fetch', WebSearch: 'Web', Task: 'Agent', Agent: 'Agent', TodoWrite: 'To-dos', Skill: 'Skill'
}

const FAILED_RE = /\[exited with code [1-9]\d*\]|exit (?:code|status) [1-9]\d*|^error[:[ ]|^fatal:|command not found|no such file or directory|<tool_use_error>/i

function toolLabel(tool: string): string {
  if (SHORT_LABELS[tool]) return SHORT_LABELS[tool]!
  if (tool.startsWith('mcp__')) {
    const name = tool.split('__').pop() || tool
    return SHORT_LABELS[name] || name.replace(/^n0_/, '')
  }
  return tool || 'Tool'
}

function detailKindFor(tool: string, detail: string): TimelineDetailKind {
  const name = tool.startsWith('mcp__') ? tool.split('__').pop() || tool : tool
  if (name === 'Bash') return 'command'
  if (['Read', 'NotebookRead', 'Edit', 'MultiEdit', 'NotebookEdit', 'Write', 'FileChange', 'LS'].includes(name)) return 'file'
  return detail.trim() ? 'text' : 'none'
}

/** A known time from a times array parallel to the lines, else undefined. */
function timeAt(times: readonly number[] | undefined, index: number): number | undefined {
  const t = index >= 0 ? times?.[index] : undefined
  return typeof t === 'number' && Number.isFinite(t) && t > 0 ? t : undefined
}

/**
 * Tool calls (with their results) and notes, oldest first. A done agent's
 * closing text is its report, shown on its own, so it is left out
 * (`dropTrailingNote`). `times` (unix ms per line, parallel to `lines`, 0 =
 * unknown) dates each entry by its first line, and a result's end by its
 * RESULT_END line.
 */
export function subagentTimeline(lines: string[], dropTrailingNote = false, times?: readonly number[]): TimelineEntry[] {
  const entries: TimelineEntry[] = []
  let n = 0
  // Only a times array that lines up with the lines is used.
  const t = times && times.length === lines.length ? times : undefined
  for (const entry of subagentFeed(lines, Infinity)) {
    if (entry.kind === 'tool') {
      const action = describeToolAction(entry.name, entry.detail)
      const detailKind = detailKindFor(entry.name, entry.detail)
      let detail = entry.detail.trim()
      let cwd = ''
      if (detailKind === 'command') ({ command: detail, cwd } = cleanCommand(detail))
      else detail = tidyPaths(detail)
      const text = detailKind === 'command' && detail ? `${action.verb} ${detail}` : action.text
      const tool: TimelineTool = { kind: 'tool', n: ++n, tool: entry.name, icon: action.icon, label: toolLabel(entry.name), text, detailKind, detail, cwd, result: [], failed: false }
      const at = timeAt(t, entry.start)
      if (at !== undefined) tool.at = at
      entries.push(tool)
    } else if (entry.kind === 'result') {
      const last = entries[entries.length - 1]
      if (last?.kind === 'tool' && !last.result.length) {
        last.result = entry.lines
        last.failed = entry.lines.some(l => FAILED_RE.test(l.trim()))
        const endedAt = timeAt(t, entry.end)
        if (endedAt !== undefined) {
          last.endedAt = endedAt
          if (last.at !== undefined && endedAt >= last.at) last.durationMs = endedAt - last.at
        }
      }
    } else {
      const note: TimelineNote = { kind: 'note', text: entry.text.trim() }
      const at = timeAt(t, entry.start)
      if (at !== undefined) note.at = at
      entries.push(note)
    }
  }
  if (dropTrailingNote && entries[entries.length - 1]?.kind === 'note') entries.pop()
  return entries
}

/** The newest tool call, or null. */
export function latestTool(entries: TimelineEntry[]): TimelineTool | null {
  for (let i = entries.length - 1; i >= 0; i--) if (entries[i]!.kind === 'tool') return entries[i] as TimelineTool
  return null
}

/** Markdown reduced to one plain line, for list rows. */
export function plainLine(markdown: string): string {
  return markdown
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/(\*\*|__|`|~~)/g, '')
    .replace(/^\s*(?:[-*+]|\d+[.)]|#{1,6})\s+/gm, '')
    .replace(/\s+/g, ' ')
    .trim()
}
