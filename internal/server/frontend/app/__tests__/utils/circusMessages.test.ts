import { describe, it, expect } from 'vitest'
import {
  sanitizeTileText, wrapMessage, shortenSummary, summaryPhrase, poolForContext, pickJoke, pickLetterMessage,
  newActivity, noteActivity, MAX_MESSAGE_CHARS, RECENT_WINDOW, LINE_CHARS, MAX_MESSAGE_LINES, LONG_IDLE_MS,
  type PickState,
} from '~/composables/agents/zen/circusMessages'
import { JOKES, TOPIC_PHRASES, ACTIVITY_PHRASES } from '~/composables/agents/zen/circusMessagePools'

/** Deterministic rng cycling through values. */
const seq = (...v: number[]) => { let i = 0; return () => v[i++ % v.length]! }
const fresh = (): PickState => ({ recent: [], lastKind: 'word' })
const NOW = 1_000_000_000

describe('sanitizeTileText', () => {
  it('uppercases and keeps letters, digits and basic punctuation only', () => {
    expect(sanitizeTileText('hello <b>world</b> & $a=sk-ant!')).toBe('HELLO B WORLD B A SK-ANT!')
    expect(sanitizeTileText('Café  déjà\n vu')).toBe('CAFE DEJA VU')
  })
  it('limits length at a word edge', () => {
    const t = sanitizeTileText('one two three four five six seven eight nine')
    expect(t.length).toBeLessThanOrEqual(MAX_MESSAGE_CHARS)
    expect(t).toBe('ONE TWO THREE FOUR FIVE SIX')
    expect(sanitizeTileText('x'.repeat(60)).length).toBe(MAX_MESSAGE_CHARS)
  })
})

describe('wrapMessage', () => {
  it('wraps to at most two short lines', () => {
    const lines = wrapMessage('SORRY ABOUT THE BUG, OLD BEAN')
    expect(lines.length).toBeLessThanOrEqual(MAX_MESSAGE_LINES)
    for (const l of lines) expect(l.length).toBeLessThanOrEqual(LINE_CHARS)
    expect(wrapMessage('TEA BREAK?')).toEqual(['TEA BREAK?'])
  })
})

describe('pools', () => {
  it('every line is tile-safe and within the length limit', () => {
    const all = [...Object.values(JOKES).flat(), ...TOPIC_PHRASES.map(t => t.phrase), ...Object.values(ACTIVITY_PHRASES)]
    for (const line of all) {
      expect(line.length, line).toBeLessThanOrEqual(MAX_MESSAGE_CHARS)
      expect(sanitizeTileText(line), line).toBe(line.toUpperCase())
      expect(wrapMessage(sanitizeTileText(line)).join(' '), line).toBe(sanitizeTileText(line))
    }
  })
  it('has about sixty distinct jokes', () => {
    const all = Object.values(JOKES).flat()
    expect(new Set(all).size).toBeGreaterThanOrEqual(55)
  })
})

describe('context pools', () => {
  it('picks oops after an error, done when idle, build after Bash, idle after a long pause', () => {
    const a = newActivity(NOW - 1000)
    expect(poolForContext(a, 'processing', NOW)).toBe('general')
    expect(poolForContext(a, 'idle', NOW)).toBe('done')
    noteActivity(a, { category: 'bash', isError: false }, NOW - 5000)
    expect(poolForContext(a, 'processing', NOW)).toBe('build')
    noteActivity(a, { category: 'error', isError: true }, NOW - 2000)
    expect(poolForContext(a, 'processing', NOW)).toBe('oops')
    const old = newActivity(NOW - LONG_IDLE_MS - 5000)
    expect(poolForContext(old, 'processing', NOW)).toBe('idle')
  })
  it('draws from the context pool, else the general pool', () => {
    expect(JOKES.oops.map(j => sanitizeTileText(j))).toContain(pickJoke('oops', [], seq(0.1, 0)))
    const general = pickJoke('oops', [], seq(0.99, 0))
    expect(JOKES.general.map(j => sanitizeTileText(j))).toContain(general)
  })
})

describe('summary', () => {
  it('shortens a digest to 2-4 words within the limit', () => {
    expect(shortenSummary('Fixing the Escape key handling in the session list.')).toBe('FIXING THE ESCAPE KEY')
    expect(shortenSummary('Refactoring')).toBeNull()
    expect(shortenSummary(undefined)).toBeNull()
  })
  it('falls back locally without a digest', () => {
    const a = newActivity(NOW)
    expect(summaryPhrase({ lastUserMessage: 'please fix the failing tests' }, a, 'processing', NOW)).toBe('Fixing the tests')
    expect(summaryPhrase({}, a, 'processing', NOW)).toBe('Tea break?')
    noteActivity(a, { category: 'read', isError: false }, NOW)
    expect(summaryPhrase({}, a, 'processing', NOW + 1000)).toBe('Reading the code')
    noteActivity(a, { category: 'bash', isError: false }, NOW)
    expect(summaryPhrase({}, a, 'processing', NOW + 1000)).toBe('Running a command')
  })
  it('prefers the digest when present', () => {
    const a = newActivity(NOW)
    expect(summaryPhrase({ summary: 'Wiring up the new settings page' }, a, 'processing', NOW, () => 0)).toBe('WIRING UP THE NEW')
  })
  it('never shows the raw user message', () => {
    const a = newActivity(NOW)
    const t = summaryPhrase({ lastUserMessage: 'my password is hunter2 zzz' }, a, 'processing', NOW)
    expect(t.toUpperCase()).not.toContain('HUNTER2')
  })
})

describe('pickLetterMessage', () => {
  const input = { ctx: {}, activity: newActivity(NOW), status: 'processing', now: NOW }

  it('keeps the old word behaviour most of the time', () => {
    expect(pickLetterMessage(input, seq(0.9), fresh()).kind).toBe('word')
  })
  it('rotates into a joke or summary roughly one in three, never twice in a row', () => {
    const st = fresh()
    const kinds = [
      pickLetterMessage(input, seq(0.1, 0.9, 0.1), st).kind, // special -> joke
      pickLetterMessage(input, seq(0.1), st).kind, // right after: forced word
      pickLetterMessage(input, seq(0.1, 0.1), st).kind, // summary
    ]
    expect(kinds).toEqual(['joke', 'word', 'summary'])
  })
  it('long run frequency is between 20% and 45%', () => {
    let s = 7
    const rng = () => { s = (s * 1664525 + 1013904223) % 4294967296; return s / 4294967296 }
    const st = fresh()
    let specials = 0
    for (let i = 0; i < 2000; i++) if (pickLetterMessage(input, rng, st).kind !== 'word') specials++
    expect(specials / 2000).toBeGreaterThan(0.2)
    expect(specials / 2000).toBeLessThan(0.45)
  })
  it('never repeats a joke within the recent window and respects length limits', () => {
    let s = 3
    const rng = () => { s = (s * 1664525 + 1013904223) % 4294967296; return s / 4294967296 }
    const st = fresh()
    const seen: string[] = []
    for (let i = 0; i < 3000; i++) {
      const m = pickLetterMessage(input, rng, st)
      if (m.kind === 'word') continue
      expect(m.text.length).toBeLessThanOrEqual(MAX_MESSAGE_CHARS)
      expect(seen.slice(-RECENT_WINDOW)).not.toContain(m.text)
      seen.push(m.text)
    }
    expect(seen.length).toBeGreaterThan(100)
    expect(st.recent.length).toBeLessThanOrEqual(RECENT_WINDOW)
  })
  it('uses the oops pool after an error', () => {
    const a = newActivity(NOW)
    noteActivity(a, { category: 'error', isError: true }, NOW - 1000)
    const m = pickLetterMessage({ ctx: {}, activity: a, status: 'processing', now: NOW }, seq(0.1, 0.9, 0.1, 0), fresh())
    expect(m.kind).toBe('joke')
    expect(JOKES.oops.map(j => sanitizeTileText(j))).toContain(m.text)
  })
})
