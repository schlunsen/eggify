import { parseToolLine, subagentLatestAction } from './subagentOutput'

export interface OverviewLogEntry {
  id: string
  time: string
  kind: string
  text: string
}

function textContent(value: unknown): string {
  if (typeof value === 'string') {
    try {
      const parsed = JSON.parse(value)
      if (Array.isArray(parsed)) return textContent(parsed)
    } catch { /* Plain message text. */ }
    return value
  }
  if (Array.isArray(value)) return value.map(block => {
    if (block?.type === 'text') return block.text || ''
    if (block?.type === 'tool_result') return textContent(block.content)
    return ''
  }).filter(Boolean).join('\n')
  return ''
}

export function overviewLog(messages: any[]): OverviewLogEntry[] {
  const entries: OverviewLogEntry[] = []
  for (const message of messages) {
    const time = message.created_at || message.timestamp || ''
    const id = message.id || `${time}-${entries.length}`
    const text = textContent(message.content)
    if (text.trim()) entries.push({ id, time, kind: message.role === 'user' ? 'User' : message.role === 'system' ? 'System' : 'Agent', text: text.slice(0, 3000) })
    for (const tool of message.tool_uses || message.toolUses || []) {
      const input = tool.input || {}
      const detail = input.command || input.cmd || input.file_path || input.path || input.query || input.description || ''
      entries.push({ id: `${id}-${tool.id || entries.length}`, time, kind: tool.name || 'Tool', text: String(detail).slice(0, 1500) || 'Running tool' })
    }
  }
  return entries.slice(-60)
}

export function changedFileCount(status: any): number {
  if (!status) return 0
  return new Set([...(status.staged || []), ...(status.modified || []), ...(status.untracked || []), ...(status.deleted || [])]).size
}

export interface OverviewSubagent {
  agent_id: string
  subagent_type?: string
  description?: string
  status: 'running' | 'completed' | 'failed' | 'waiting' | string
  created_at: string
  updated_at?: string
  completed_at?: string
  error_message?: string
  background?: boolean
  parent_agent_id?: string
  last_output?: string
  recent_output?: string[]
}

/** Running subagents first (oldest first), then the most recently finished, up to `limit` in total. */
export function subagentRows(subagents: OverviewSubagent[], limit = 4): { rows: OverviewSubagent[]; hidden: number } {
  const running = subagents.filter(s => s.status === 'running')
    .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))
  const finished = subagents.filter(s => s.status !== 'running')
    .sort((a, b) => Date.parse(b.completed_at || b.updated_at || b.created_at) - Date.parse(a.completed_at || a.updated_at || a.created_at))
  const rows = [...running, ...finished.slice(0, Math.max(0, limit - running.length))]
  return { rows, hidden: subagents.length - rows.length }
}

function subagentLines(subagent: OverviewSubagent): string[] {
  return subagent.recent_output?.length ? subagent.recent_output : [subagent.last_output || '']
}

/** The last `count` readable output lines, with the server's output markers resolved. */
export function subagentTail(subagent: OverviewSubagent, count = 12): string {
  const lines = subagentLines(subagent)
    .filter(line => line && line !== 'RESULT_START' && line !== 'RESULT_END')
    .map(line => {
      const tool = parseToolLine(line)
      return tool ? `→ ${tool.name}${tool.detail ? ` ${tool.detail}` : ''}` : line
    })
  if (subagent.status === 'failed' && subagent.error_message) lines.push(`✕ ${subagent.error_message}`)
  return lines.slice(-count).join('\n') || 'No output yet'
}

/** What a running subagent is doing right now, e.g. "Bash · go test ./...". */
export function subagentNow(subagent: OverviewSubagent): string {
  return subagentLatestAction(subagentLines(subagent))
}

/** How long a subagent has run, or ran: "45s", "3m", "1h 5m". */
export function subagentDuration(subagent: OverviewSubagent, now = Date.now()): string {
  const start = Date.parse(subagent.created_at)
  const end = subagent.status === 'running' ? now : Date.parse(subagent.completed_at || subagent.updated_at || '')
  const seconds = Math.max(0, Math.round(((Number.isFinite(end) ? end : now) - start) / 1000))
  if (!Number.isFinite(seconds)) return ''
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return minutes < 60 ? `${minutes}m` : `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}
