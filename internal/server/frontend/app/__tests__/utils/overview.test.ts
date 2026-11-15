import { overviewLog, changedFileCount, subagentRows, subagentTail, subagentDuration, subagentNow } from '../../utils/agents/overview'

it('renders saved text and tool calls without showing image payloads or raw JSON blocks', () => {
  const entries = overviewLog([
    { id: 'a', role: 'assistant', created_at: '2026-09-16T10:00:00Z', content: 'Checking tests', tool_uses: [{ id: 'tool', name: 'Bash', input: { command: 'go test ./...' } }] },
    { id: 'b', role: 'user', content: JSON.stringify([{ type: 'image', source: { data: 'image-payload' } }, { type: 'text', text: 'Use this design' }]) }
  ])
  expect(entries.map(entry => entry.text)).toEqual(['Checking tests', 'go test ./...', 'Use this design'])
  expect(entries[1].kind).toBe('Bash')
})

it('bounds the live log and counts staged-and-modified files once', () => {
  expect(overviewLog(Array.from({ length: 80 }, (_, index) => ({ id: String(index), content: 'message', role: 'assistant' })))).toHaveLength(60)
  expect(changedFileCount({ staged: ['file.go'], modified: ['file.go', 'other.go'], untracked: ['new.go'] })).toBe(3)
})

describe('subagent helpers', () => {
  const base = { created_at: '2026-09-25T10:00:00Z' }
  const subs = [
    { ...base, agent_id: 'done-old', status: 'completed', completed_at: '2026-09-25T10:01:00Z' },
    { ...base, agent_id: 'run-2', status: 'running', created_at: '2026-09-25T10:03:00Z' },
    { ...base, agent_id: 'failed', status: 'failed', completed_at: '2026-09-25T10:05:00Z' },
    { ...base, agent_id: 'run-1', status: 'running' },
    { ...base, agent_id: 'done-new', status: 'completed', completed_at: '2026-09-25T10:06:00Z' }
  ]

  it('lists running subagents first, then the latest finished ones', () => {
    const { rows, hidden } = subagentRows(subs, 4)
    expect(rows.map(s => s.agent_id)).toEqual(['run-1', 'run-2', 'done-new', 'failed'])
    expect(hidden).toBe(1)
  })

  it('shows every running subagent even past the limit', () => {
    const running = Array.from({ length: 6 }, (_, i) => ({ ...base, agent_id: `r${i}`, status: 'running' }))
    expect(subagentRows(running, 4)).toEqual({ rows: running, hidden: 0 })
  })

  it('resolves output markers and appends the failure reason', () => {
    const tail = subagentTail({ ...base, agent_id: 'x', status: 'failed', error_message: 'Agent killed',
      recent_output: ['TOOL:Bash', 'RESULT_START', 'hello', 'RESULT_END'] })
    expect(tail).toBe('→ Bash\nhello\n✕ Agent killed')
    expect(subagentTail({ ...base, agent_id: 'y', status: 'running' })).toBe('No output yet')
  })

  it('shows what each tool call acted on and what a running subagent is doing now', () => {
    const sub = { ...base, agent_id: 'z', status: 'running', recent_output: ['TOOL:Bash: go test ./...', 'RESULT_START', 'ok', 'RESULT_END', 'TOOL:Edit: /app/a.ts'] }
    expect(subagentTail(sub)).toBe('→ Bash go test ./...\nok\n→ Edit /app/a.ts')
    expect(subagentNow(sub)).toBe('Edit · /app/a.ts')
  })

  it('measures running time up to now and finished time up to completion', () => {
    const now = Date.parse('2026-09-25T10:02:05Z')
    expect(subagentDuration({ ...base, agent_id: 'r', status: 'running' }, now)).toBe('2m')
    expect(subagentDuration({ ...base, agent_id: 'd', status: 'completed', completed_at: '2026-09-25T10:00:42Z' }, now)).toBe('42s')
    expect(subagentDuration({ ...base, agent_id: 'h', status: 'completed', completed_at: '2026-09-25T11:05:00Z' }, now)).toBe('1h 5m')
  })
})
