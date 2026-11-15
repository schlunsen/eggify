import { characterFor, leadLinesFrom, speechFor, toolGlyph, ZEN_COLORS } from '../../utils/agents/subagentCharacters'

describe('subagent characters', () => {
  it('gives each subagent a stable look from its id', () => {
    const a = characterFor('toolu_01ABC')
    expect(characterFor('toolu_01ABC')).toEqual(a)
    expect(ZEN_COLORS).toContain(a.color)
    expect(['bean', 'boombox', 'cassette', 'acid', 'alien', 'cat']).toContain(a.body)
    expect(a.accent).not.toBe(a.color)
    expect(a.pace).toBeGreaterThanOrEqual(3)
    expect(a.pace).toBeLessThanOrEqual(7)
    // Many ids should not all look alike.
    const looks = new Set(Array.from({ length: 40 }, (_, i) => JSON.stringify(characterFor(`agent-${i}`))))
    expect(looks.size).toBeGreaterThan(20)
  })

  it('picks a glyph for what a tool does', () => {
    expect(toolGlyph('Bash')).toBe('>_')
    expect(toolGlyph('Edit')).toBe('✎')
    expect(toolGlyph('mcp__eggify-tools__Read')).toBe('▤')
    expect(toolGlyph('Grep')).toBe('⌕')
    expect(toolGlyph('SomethingNew')).toBe('⚙')
  })

  it('says the latest action, and how it ended', () => {
    const lines = ['Looking around', 'TOOL:Bash: go test ./...', 'RESULT_START', 'FAIL pkg', 'RESULT_END']
    expect(speechFor(lines, 'running')).toEqual({ glyph: '>_', text: 'Bash go test ./...' })
    expect(speechFor([...lines, 'Two tests fail, fixing'], 'running')).toEqual({ glyph: '💬', text: 'Two tests fail, fixing' })
    expect(speechFor([], 'running').text).toBe('Getting started…')
    expect(speechFor(lines, 'completed')).toEqual({ glyph: '✓', text: 'Done!' })
    expect(speechFor(lines, 'failed', 'Agent killed')).toEqual({ glyph: '✕', text: 'Ouch: Agent killed' })
    expect(speechFor([`TOOL:Read: ${'x'.repeat(200)}`], 'running', undefined, 20).text).toHaveLength(20)
  })
})

it('reads the main agent’s messages as subagent-style lines', () => {
  const lines = leadLinesFrom([
    { role: 'user', content: 'go' },
    { role: 'assistant', content: [{ type: 'text', text: 'On it' }], toolUses: [{ name: 'Bash', input: { command: 'go test\n./...' } }] },
    { role: 'assistant', content: 'Read', isToolResult: true, toolUses: [{ toolName: 'Read', input: { file_path: '/a.ts' } }] }
  ])
  expect(lines).toEqual(['On it', 'TOOL:Bash: go test ./...', 'TOOL:Read: /a.ts'])
  expect(speechFor(lines, 'running').text).toBe('Read /a.ts')
})
