/**
 * Parsing for the output lines the server keeps per subagent
 * (BackgroundAgent.recent_output and live background_agent_output events):
 *
 *   TOOL:<name>               a tool call (older servers)
 *   TOOL:<name>: <detail>     a tool call and what it acts on (command, file, ...)
 *   RESULT_START … RESULT_END a tool result (first lines only)
 *   anything else             the subagent's own text
 */

export interface SubagentToolCall {
  name: string
  detail: string
}

/**
 * `start` is the index of the entry's first line in the input; a result's
 * `end` is the index of its RESULT_END line (-1 while it is still open). They
 * let callers look up when an entry happened (a parallel times array).
 */
export type SubagentFeedEntry =
  | { kind: 'tool'; name: string; detail: string; start: number }
  | { kind: 'result'; lines: string[]; isError: boolean; start: number; end: number }
  | { kind: 'text'; text: string; start: number }

/** Parses a "TOOL:" line; returns null for any other line. */
export function parseToolLine(line: string): SubagentToolCall | null {
  if (!line.startsWith('TOOL:')) return null
  const rest = line.slice(5)
  const split = rest.indexOf(': ')
  return split > 0
    ? { name: rest.slice(0, split), detail: rest.slice(split + 2) }
    : { name: rest, detail: '' }
}

/** Groups output lines into tool calls, results and runs of text, newest last. */
export function subagentFeed(lines: string[], limit = 80): SubagentFeedEntry[] {
  const entries: SubagentFeedEntry[] = []
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? ''
    const tool = parseToolLine(line)
    if (tool) {
      entries.push({ kind: 'tool', ...tool, start: i })
      continue
    }
    if (line === 'RESULT_START') {
      const start = i
      const result: string[] = []
      while (++i < lines.length && lines[i] !== 'RESULT_END') result.push(lines[i] ?? '')
      if (result.length) entries.push({ kind: 'result', lines: result, isError: false, start, end: i < lines.length ? i : -1 })
      continue
    }
    if (line === 'RESULT_END' || !line.trim()) continue
    const last = entries[entries.length - 1]
    if (last?.kind === 'text') last.text += '\n' + line
    else entries.push({ kind: 'text', text: line, start: i })
  }
  return entries.slice(-limit)
}

/** One line saying what a subagent did most recently: its last tool call or text. */
export function subagentLatestAction(lines: string[]): string {
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i] ?? ''
    const tool = parseToolLine(line)
    if (tool) return tool.detail ? `${tool.name} · ${tool.detail}` : tool.name
    if (line === 'RESULT_END' || line === 'RESULT_START' || !line.trim()) continue
    // Skip result lines: walk back to the tool call that produced them.
    if (lines.lastIndexOf('RESULT_START', i) > lines.lastIndexOf('RESULT_END', i)) continue
    return line.trim()
  }
  return ''
}
