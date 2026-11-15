import { parseToolLine, subagentFeed, subagentLatestAction } from '../../utils/agents/subagentOutput'

describe('subagent output lines', () => {
  it('reads tool calls with and without what they act on', () => {
    expect(parseToolLine('TOOL:Bash: go test ./... -run X')).toEqual({ name: 'Bash', detail: 'go test ./... -run X' })
    expect(parseToolLine('TOOL:Read')).toEqual({ name: 'Read', detail: '' })
    expect(parseToolLine('TOOL:mcp__eggify-tools__Read: /a.go')).toEqual({ name: 'mcp__eggify-tools__Read', detail: '/a.go' })
    expect(parseToolLine('plain text')).toBeNull()
  })

  it('groups lines into tool calls, results and runs of text', () => {
    const feed = subagentFeed(['Looking at the tests', 'first', 'TOOL:Bash: go test', 'RESULT_START', 'ok  pkg', 'PASS', 'RESULT_END', 'All green'])
    expect(feed).toMatchObject([
      { kind: 'text', text: 'Looking at the tests\nfirst' },
      { kind: 'tool', name: 'Bash', detail: 'go test' },
      { kind: 'result', lines: ['ok  pkg', 'PASS'], isError: false },
      { kind: 'text', text: 'All green' }
    ])
    expect(subagentFeed(Array.from({ length: 10 }, (_, i) => `TOOL:T${i}`), 3).map(e => (e as any).name)).toEqual(['T7', 'T8', 'T9'])
  })

  it('says what the subagent did last, skipping tool output', () => {
    expect(subagentLatestAction(['TOOL:Grep: TODO', 'RESULT_START', 'a.go:1 TODO', 'RESULT_END'])).toBe('Grep · TODO')
    expect(subagentLatestAction(['TOOL:Bash: ls', 'RESULT_START', 'x', 'RESULT_END', 'Found three files'])).toBe('Found three files')
    expect(subagentLatestAction(['TOOL:Read', 'RESULT_START', 'still streaming'])).toBe('Read')
    expect(subagentLatestAction([])).toBe('')
  })
})
