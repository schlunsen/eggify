import { describe, it, expect } from 'vitest'
import {
  chunkForSpeech, speakableUnits, splitSentences, cutText, cleanInline, CODE_OMITTED, TABLE_OMITTED,
} from '../../utils/speechChunks'

// Every chunk boundary must fall between words of the cleaned text.
function expectNoMidWordCuts(chunks: string[]) {
  const words = new Set(chunks.join(' ').split(/\s+/))
  for (const c of chunks) {
    const first = c.split(/\s+/)[0]!
    const last = c.split(/\s+/).at(-1)!
    expect(words.has(first)).toBe(true)
    expect(words.has(last)).toBe(true)
  }
  // Rejoining the chunks gives back the same word sequence
  return chunks.join(' ').split(/\s+/)
}

const LONG_FIRST =
  'I looked into why the read-aloud button spins for so long before anything plays, and the short version ' +
  'is that the whole pipeline waits on synthesis in a way that scales with the length of the reply. Then more.'

describe('chunkForSpeech: first chunk', () => {
  it('keeps the first chunk short by cutting a long first sentence at a clause break', () => {
    const chunks = chunkForSpeech(LONG_FIRST)
    expect(chunks[0]).toBe('I looked into why the read-aloud button spins for so long before anything plays,')
    expect(chunks[0]!.length).toBeLessThanOrEqual(120)
    expect(chunks[1]!.startsWith('and the short version')).toBe(true)
  })

  it('a short first sentence is the first chunk on its own', () => {
    const chunks = chunkForSpeech('Done. I fixed the bug in the parser and added a test for it. Everything passes now.')
    expect(chunks[0]).toBe('Done.')
    expect(chunks[1]).toBe('I fixed the bug in the parser and added a test for it. Everything passes now.')
  })

  it('cuts at a space when there is no clause break, never inside a word', () => {
    const sentence = Array.from({ length: 40 }, (_, i) => `word${i}`).join(' ') + '.'
    const chunks = chunkForSpeech(sentence)
    expect(chunks[0]!.length).toBeLessThanOrEqual(120)
    expect(chunks.join(' ')).toBe(sentence)
    expectNoMidWordCuts(chunks)
  })

  it('later chunks stay under the limit and nothing is lost', () => {
    const text = Array.from({ length: 30 }, (_, i) => `Sentence number ${i} talks about something, briefly.`).join(' ')
    const chunks = chunkForSpeech(text)
    expect(chunks[0]!.length).toBeLessThanOrEqual(120)
    expect(chunks[1]!.length).toBeLessThanOrEqual(180)
    for (const c of chunks.slice(2)) expect(c.length).toBeLessThanOrEqual(280)
    expect(chunks.join(' ')).toBe(text)
    expectNoMidWordCuts(chunks)
  })

  it('stops at maxTotal on a chunk boundary', () => {
    const text = Array.from({ length: 200 }, () => 'This sentence is here to make the reply very long.').join(' ')
    const chunks = chunkForSpeech(text, { maxTotal: 1000 })
    const total = chunks.reduce((n, c) => n + c.length, 0)
    expect(total).toBeGreaterThanOrEqual(1000)
    expect(total).toBeLessThan(1000 + 281)
    expect(chunks.every(c => c.endsWith('.'))).toBe(true)
  })
})

describe('speakableUnits: markdown', () => {
  it('splits paragraphs, headings and list items, reading numbers as words', () => {
    const md = '## Summary\n\nHere is what I changed:\n\n1. **Chunking.** Split the text\n2. Streaming of audio\n\n- a bullet\n- [x] done item\n\nLast paragraph\nwraps here.'
    expect(speakableUnits(md)).toEqual([
      'Summary.',
      'Here is what I changed.',
      'One. Chunking. Split the text.',
      'Two. Streaming of audio.',
      'a bullet.',
      'done item.',
      'Last paragraph wraps here.',
    ])
  })

  it('skips code blocks and tables with a short note', () => {
    const md = 'Run this:\n\n```bash\nnpm run generate\nnpm test\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nThat is all.'
    expect(speakableUnits(md)).toEqual(['Run this.', CODE_OMITTED, TABLE_OMITTED, 'That is all.'])
  })

  it('never reads code inside an unterminated fence', () => {
    expect(speakableUnits('Look:\n```\nconst x = 1')).toEqual(['Look.', CODE_OMITTED])
  })

  it('reads URLs as their domain and links as their text', () => {
    expect(cleanInline('See https://www.github.com/schlunsen/eggify/pull/12. And [the docs](https://x.dev/a).'))
      .toBe('See github.com. And the docs.')
    expect(cleanInline('Open <https://example.org/path?q=1> now')).toBe('Open example.org now')
  })

  it('drops emphasis, inline code ticks and snake_case underscores', () => {
    expect(cleanInline('**Bold** and *italic* and `use_zen_tts()` and ~~old~~')).toBe('Bold and italic and use zen tts() and old')
  })

  it('drops headings markers, quotes and rules', () => {
    expect(speakableUnits('# Title\n> quoted text\n\n---\n\nEnd')).toEqual(['Title.', 'quoted text.', 'End.'])
  })
})

describe('splitSentences / cutText', () => {
  it('does not split on abbreviations or decimals', () => {
    expect(splitSentences('Use e.g. this one. It costs 3.5 dollars! Really?')).toEqual([
      'Use e.g. this one.', 'It costs 3.5 dollars!', 'Really?',
    ])
  })

  it('prefers a clause break at or after min', () => {
    const [head, tail] = cutText('Short, then a much longer clause follows here; and the rest of it goes on and on.', 60, 20)
    expect(head).toBe('Short, then a much longer clause follows here;')
    expect(tail).toBe('and the rest of it goes on and on.')
  })

  it('only cuts inside a single word longer than the limit', () => {
    const [head, tail] = cutText('x'.repeat(50), 20, 10)
    expect(head.length).toBe(20)
    expect(tail.length).toBe(30)
  })
})
