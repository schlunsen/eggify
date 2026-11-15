import { describe, expect, it } from 'vitest'
import { stripInlineMarkdown } from '~/utils/plainText'
import { leadLinesFrom, speechFor } from '~/utils/agents/subagentCharacters'
import { plateSummary } from '~/utils/zenPlate'

describe('stripInlineMarkdown', () => {
  it('removes bold, italic, strike and code markers', () => {
    expect(stripInlineMarkdown('Three subagents are working: the **schema**, the **store** and the __api__'))
      .toBe('Three subagents are working: the schema, the store and the api')
    expect(stripInlineMarkdown('an *emphasised* and _slanted_ word')).toBe('an emphasised and slanted word')
    expect(stripInlineMarkdown('run `go test ./...` then ~~skip~~ done')).toBe('run go test ./... then skip done')
  })
  it('keeps the text of links and images', () => {
    expect(stripInlineMarkdown('see [the docs](https://x.dev/a_b) and ![logo](a.png)')).toBe('see the docs and logo')
  })
  it('removes heading, quote and list markers at the start of a line', () => {
    expect(stripInlineMarkdown('## Plan')).toBe('Plan')
    expect(stripInlineMarkdown('- one\n* two\n1. three\n> four')).toBe('one\ntwo\nthree\nfour')
    expect(stripInlineMarkdown('```ts\ncode\n```')).toBe('\ncode\n')
  })
  it('leaves plain text, math and identifiers alone', () => {
    expect(stripInlineMarkdown('2 * 3 * 4 = 24')).toBe('2 * 3 * 4 = 24')
    expect(stripInlineMarkdown('read snake_case_name and my_var_x')).toBe('read snake_case_name and my_var_x')
    expect(stripInlineMarkdown('ls *.go')).toBe('ls *.go')
    expect(stripInlineMarkdown('')).toBe('')
  })
  it('drops a bold marker whose partner has not arrived', () => {
    expect(stripInlineMarkdown('Working on the **sch')).toBe('Working on the sch')
  })
})

describe('plain-text bubbles', () => {
  it('speechFor strips markers from the agent\'s own words but not from tool details', () => {
    const said = speechFor(['Three subagents are working: the **schema**, the **store** and the `api`.'], 'running')
    expect(said.text).toBe('Three subagents are working: the schema, the store and the api.')
    expect(speechFor(['TOOL:Bash: ls *.go'], 'running').text).toBe('Bash ls *.go')
    expect(speechFor([], 'failed', 'could not **write**').text).toBe('Ouch: could not write')
  })
  it('the main agent\'s reply reaches its bubble without markers', () => {
    const lines = leadLinesFrom([{ role: 'assistant', content: '## Status\n- the **schema** is done' }])
    expect(speechFor(lines, 'running').text).toBe('Status the schema is done')
  })
  it('the name plate summary is plain too', () => {
    expect(plateSummary('Fixing the **login** page')).toBe('Fixing the login page')
  })
})
