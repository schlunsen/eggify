// @vitest-environment jsdom
// (jsdom: DOMPurify's sanitizing is what the safe-markdown result test checks)
import {
  activityAgo, createFrameBatcher, describeToolAction, finalReport, groupSubagents, isInterruptionMessage, reportGist,
  subagentDisplayStatus, subagentNowAction, subagentProblem, subagentSteps
} from '../../utils/agents/subagentActivity'
import { buildSubagentItem, itemInputsChanged } from '../../utils/agents/subagentItem'
import type { BackgroundAgent } from '../../composables/agents/useBackgroundAgents'

describe('tool actions in plain words', () => {
  it.each([
    ['Read', '/Users/me/app/components/ZenMode.vue', 'Reading ZenMode.vue', '▤'],
    ['Edit', '/app/zen/sceneCircus.ts', 'Editing sceneCircus.ts', '✎'],
    ['MultiEdit', 'src/a.go', 'Editing a.go', '✎'],
    ['Write', '/tmp/out/', 'Writing out', '✎'],
    ['Bash', 'go test   ./internal/...', 'Running go test ./internal/...', '>_'],
    ['Grep', 'useZenMusic', 'Searching for useZenMusic', '⌕'],
    ['Glob', '**/*.vue', 'Finding files **/*.vue', '⌕'],
    ['WebFetch', 'https://docs.example.com/guide/intro?x=1', 'Fetching docs.example.com/guide/intro', '◍'],
    ['WebSearch', 'nuxt 4 layers', 'Searching the web for nuxt 4 layers', '◍'],
    ['TodoWrite', '3 todos', 'Updating the to-do list', '☑'],
    ['mcp__eggify-tools__Read', '/a/b/c.ts', 'Reading c.ts', '▤'],
    ['mcp__n0__n0_tasks_list', 'board 4', 'Calling n0_tasks_list (n0) board 4', '⚙'],
    ['Frobnicate', 'x', 'Using Frobnicate x', '⚙'],
    ['Frobnicate', '', 'Using Frobnicate', '⚙']
  ])('%s %s → %s', (tool, detail, text, icon) => {
    const action = describeToolAction(tool, detail)
    expect(action.text).toBe(text)
    expect(action.icon).toBe(icon)
  })

  it('shortens long commands and keeps the full detail for a tooltip', () => {
    const command = `npm run build -- ${'x'.repeat(200)}`
    const action = describeToolAction('Bash', command)
    expect(action.text.length).toBeLessThanOrEqual('Running '.length + 140)
    expect(action.text.endsWith('…')).toBe(true)
    expect(action.full).toBe(command)
  })
})

describe('now and steps', () => {
  it('reads the latest tool call, skipping its result', () => {
    expect(subagentNowAction(['TOOL:Read: /a/b.ts', 'TOOL:Bash: go vet', 'RESULT_START', 'ok', 'RESULT_END']))
      .toMatchObject({ kind: 'tool', text: 'Running go vet', full: 'go vet' })
  })
  it('says Thinking… when the newest line is the agent’s own text', () => {
    expect(subagentNowAction(['TOOL:Read: /a/b.ts', 'Now I understand the bug'])).toMatchObject({ kind: 'thinking', text: 'Thinking…', full: 'Now I understand the bug' })
  })
  it('says Starting… without output', () => {
    expect(subagentNowAction([]).text).toBe('Starting…')
  })
  it('keeps the last few tool calls, oldest first', () => {
    const lines = ['TOOL:Read: /1.ts', 'text', 'TOOL:Read: /2.ts', 'RESULT_START', 'x', 'RESULT_END', 'TOOL:Edit: /3.ts', 'TOOL:Bash: make']
    expect(subagentSteps(lines, 3).map(s => s.text)).toEqual(['Reading 2.ts', 'Editing 3.ts', 'Running make'])
  })
})

describe('status', () => {
  it('tells interrupted agents from failed ones', () => {
    expect(subagentDisplayStatus({ status: 'running' })).toBe('running')
    expect(subagentDisplayStatus({ status: 'waiting' })).toBe('running')
    expect(subagentDisplayStatus({ status: 'completed' })).toBe('done')
    expect(subagentDisplayStatus({ status: 'failed', error_message: 'Agent failed: boom' })).toBe('failed')
    expect(subagentDisplayStatus({ status: 'failed', error_message: 'App restarted while agent was running' })).toBe('interrupted')
    expect(subagentDisplayStatus({ status: 'failed', error_message: 'whatever', interrupted: true })).toBe('interrupted')
  })
  it('matches the server’s interruption messages only at the start', () => {
    for (const msg of ['Agent stopped', 'Agent killed: x', 'Sub-agent explorer interrupted', '[Request interrupted by user]', 'Session ended']) {
      expect(isInterruptionMessage(msg)).toBe(true)
    }
    for (const msg of ['', 'Agent failed: exit 1', 'go test stopped with failures', 'Error: build was interrupted']) {
      expect(isInterruptionMessage(msg)).toBe(false)
    }
  })
  it('explains problems in plain words', () => {
    expect(subagentProblem({ status: 'failed', error_message: 'App restarted while agent was running' })).toMatch(/^Eggify restarted/)
    expect(subagentProblem({ status: 'failed', error_message: 'Agent stopped' })).toBe('It was stopped before it finished.')
    expect(subagentProblem({ status: 'failed', error_message: 'Agent failed: exit status 2\nstack…' })).toBe('exit status 2')
    expect(subagentProblem({ status: 'failed' })).toBe('It failed without an error message.')
  })
})

describe('final report', () => {
  it('takes the first meaningful paragraph, skipping headings and labels', () => {
    expect(reportGist('# Report\n\n**Summary:**\n\nFixed the **race** in `hub.go`. Tests pass.\n\nMore details here.'))
      .toBe('Fixed the **race** in `hub.go`. Tests pass.')
    expect(reportGist('## Findings\n- one\n- two\n- three\n- four')).toBe('- one\n- two\n- three')
    expect(reportGist('```\ncode\n```\nAfter code.')).toBe('After code.')
  })
  it('cuts long paragraphs at a sentence end', () => {
    const report = `${'First sentence is here. '.repeat(10)}${'word '.repeat(80)}`
    const gist = reportGist(report, 120)
    expect(gist.length).toBeLessThanOrEqual(120)
    expect(gist.endsWith('.')).toBe(true)
  })
  it('prefers the completion output, falling back to the trailing text', () => {
    expect(finalReport({ status: 'completed', last_output: 'All done. Summary follows.' }, ['TOOL:Read: /a', 'short'])).toBe('All done. Summary follows.')
    expect(finalReport({ status: 'completed', last_output: 'TOOL:Read: /a' }, ['TOOL:Read: /a', 'The report', 'line two'])).toBe('The report\nline two')
  })
})

describe('grouping and filters', () => {
  const at = (s: number) => new Date(Date.UTC(2026, 0, 1, 0, 0, s)).toISOString()
  const agents = [
    { id: 'r2', status: 'running', created_at: at(20) },
    { id: 'd1', status: 'completed', created_at: at(1), completed_at: at(30) },
    { id: 'r1', status: 'running', created_at: at(10) },
    { id: 'i1', status: 'failed', error_message: 'App restarted while agent was running', created_at: at(2), updated_at: at(40) },
    { id: 'f1', status: 'failed', error_message: 'boom', created_at: at(3), updated_at: at(35) }
  ]
  it('puts running agents first (oldest first) and finished ones newest first', () => {
    const groups = groupSubagents(agents)
    expect(groups.running.map(a => a.id)).toEqual(['r1', 'r2'])
    expect(groups.finished.map(a => a.id)).toEqual(['i1', 'f1', 'd1'])
    expect(groups.counts).toEqual({ all: 5, running: 2, done: 1, failed: 1, interrupted: 1 })
  })
  it('filters by status but keeps the counts', () => {
    const groups = groupSubagents(agents, 'interrupted')
    expect(groups.running).toEqual([])
    expect(groups.finished.map(a => a.id)).toEqual(['i1'])
    expect(groups.counts.all).toBe(5)
  })
})

describe('time', () => {
  it('describes how long ago something happened', () => {
    expect(activityAgo(1000, 3000)).toBe('just now')
    expect(activityAgo(0, 42000)).toBe('42s ago')
    expect(activityAgo(0, 185000)).toBe('3m ago')
  })
})

describe('frame batcher', () => {
  it('runs once per frame however many updates arrive', () => {
    const frames: Array<() => void> = []
    const run = vi.fn()
    const batcher = createFrameBatcher(run, cb => frames.push(cb), () => {})
    for (let i = 0; i < 50; i++) batcher.request()
    expect(run).not.toHaveBeenCalled()
    expect(frames).toHaveLength(1)
    frames.shift()!()
    expect(run).toHaveBeenCalledTimes(1)
    batcher.request()
    expect(frames).toHaveLength(1)
  })
  it('flushes now, and cancel drops a pending run', () => {
    const run = vi.fn()
    const frames: Array<() => void> = []
    const batcher = createFrameBatcher(run, cb => frames.push(cb), () => {})
    batcher.request()
    batcher.flush()
    expect(run).toHaveBeenCalledTimes(1)
    frames.shift()!() // the stale frame does nothing
    expect(run).toHaveBeenCalledTimes(1)
    batcher.request()
    batcher.cancel()
    frames.shift()!()
    expect(run).toHaveBeenCalledTimes(1)
  })
})

describe('subagent items', () => {
  const agent = (over: Partial<BackgroundAgent>): BackgroundAgent => ({
    agent_id: 'a', parent_session_id: 's', subagent_type: 'general-purpose', description: 'Task', status: 'completed', progress: 1,
    last_output: '', output_lines: 0, created_at: new Date(0).toISOString(), updated_at: new Date(1000).toISOString(), ...over
  })

  it('renders the result as safe markdown', () => {
    const item = buildSubagentItem(agent({
      last_output: 'Done <img src=x onerror=alert(1)> [click](javascript:alert(1)) <script>alert(2)</script> **ok**\n\nMore.'
    }), [], '')
    expect(item.resultHtml).toContain('<strong>ok</strong>')
    expect(item.resultHtml).not.toMatch(/onerror|javascript:|<script/i)
    expect(item.hasMoreReport).toBe(true)
    expect(item.reportHtml).toContain('More.')
  })

  it('keeps an item when nothing it depends on changed', () => {
    const a = agent({ status: 'running' })
    const lines = ['TOOL:Read: /a.ts']
    const item = buildSubagentItem(a, lines, '')
    expect(item.now.text).toBe('Reading a.ts')
    expect(itemInputsChanged(item, a, lines, '')).toBe(false)
    expect(itemInputsChanged(item, a, [...lines, 'TOOL:Bash: ls'], '')).toBe(true)
    expect(itemInputsChanged(item, { ...a, status: 'completed' } as BackgroundAgent, lines, '')).toBe(true)
  })
})
