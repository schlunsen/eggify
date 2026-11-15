/**
 * Message source for the Circus "cut-out letters" gag. Each time the gag
 * fires, `pickLetterMessage` chooses between the old behaviour (a word from
 * the reply), a short SUMMARY of the session, or a JOKE from the pools in
 * `circusMessagePools.ts`. Pure logic (no DOM) so it can be unit tested.
 *
 * Nothing but curated text, the session's cached digest and fixed phrases
 * reaches the tiles; the last user message is only matched against keywords,
 * never shown.
 */
import { JOKES, TOPIC_PHRASES, ACTIVITY_PHRASES, type JokePool } from './circusMessagePools'
import type { ToolCategory } from './types'

/** Longest message spelled across the tiles. */
export const MAX_MESSAGE_CHARS = 28
/** Characters per tile line; a message wraps to at most MAX_MESSAGE_LINES lines. */
export const LINE_CHARS = 18
export const MAX_MESSAGE_LINES = 2
/** A joke or summary is never repeated within this many picks. */
export const RECENT_WINDOW = 15
/** Chance that a firing is a joke or summary instead of a word from the reply. */
export const SPECIAL_CHANCE = 0.4
/** Of those, the share that is a summary (the rest are jokes). */
export const SUMMARY_SHARE = 0.4
/** A matching context pool is used this often (else the general pool). */
export const CONTEXT_POOL_CHANCE = 0.7

export const ERROR_WINDOW_MS = 60_000
export const BUILD_WINDOW_MS = 60_000
export const LONG_IDLE_MS = 120_000

export type MessageKind = 'word' | 'summary' | 'joke'
export interface LetterMessage { kind: MessageKind; text: string }

/**
 * UI-safe text for the tiles: ASCII letters, digits and basic punctuation,
 * uppercase, single spaces, at most `max` characters (cut at a word edge).
 */
export function sanitizeTileText(input: string, max = MAX_MESSAGE_CHARS): string {
  const folded = String(input ?? '')
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .toUpperCase()
    .replace(/[^A-Z0-9 .,!?'-]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  if (folded.length <= max) return folded
  const cut = folded.slice(0, max + 1)
  const edge = cut.lastIndexOf(' ')
  const text = edge > 0 ? cut.slice(0, edge) : folded.slice(0, max)
  return text.replace(/[ ,.-]+$/, '').trim()
}

/** Greedy word wrap into at most `maxLines` lines of `perLine` characters. */
export function wrapMessage(text: string, perLine = LINE_CHARS, maxLines = MAX_MESSAGE_LINES): string[] {
  const lines: string[] = []
  let cur = ''
  for (let word of text.split(' ').filter(Boolean)) {
    while (word.length > perLine) {
      if (cur) { lines.push(cur); cur = '' }
      lines.push(word.slice(0, perLine))
      word = word.slice(perLine)
    }
    if (!cur) cur = word
    else if (cur.length + 1 + word.length <= perLine) cur += ` ${word}`
    else { lines.push(cur); cur = word }
  }
  if (cur) lines.push(cur)
  return lines.slice(0, maxLines)
}

// ── Session context (fed by ZenMode) ──
export interface SessionMessageContext {
  /** Cached session digest summary (never requested just for the tiles). */
  summary?: string
  tags?: readonly string[]
  /** Only matched against keywords, never shown. */
  lastUserMessage?: string
}

let sessionContext: SessionMessageContext = {}
export function setCircusMessageContext(ctx: SessionMessageContext | null) {
  sessionContext = ctx ? { ...ctx } : {}
}
export function getCircusMessageContext(): SessionMessageContext { return sessionContext }

// ── Event tracking (what has the agent been doing lately) ──
export interface ActivityState {
  lastEventAt: number
  lastToolCategory: ToolCategory | null
  lastToolAt: number
  lastErrorAt: number
}

export function newActivity(now = 0): ActivityState {
  return { lastEventAt: now, lastToolCategory: null, lastToolAt: -Infinity, lastErrorAt: -Infinity }
}

/** Record a session event. Call after the picker has run for this event. */
export function noteActivity(a: ActivityState, e: { category: ToolCategory; isError: boolean }, now: number) {
  a.lastEventAt = now
  if (e.isError || e.category === 'error') a.lastErrorAt = now
  if (e.category !== 'message') { a.lastToolCategory = e.category; a.lastToolAt = now }
}

/** Which joke pool fits the moment. */
export function poolForContext(a: ActivityState, status: string, now: number): JokePool {
  if (now - a.lastErrorAt < ERROR_WINDOW_MS) return 'oops'
  if (status === 'idle' && now - a.lastEventAt < LONG_IDLE_MS) return 'done'
  if (a.lastToolCategory === 'bash' && now - a.lastToolAt < BUILD_WINDOW_MS) return 'build'
  if (a.lastEventAt > 0 && now - a.lastEventAt > LONG_IDLE_MS) return 'idle'
  return 'general'
}

// ── Summary ──
/** First few words of a digest summary that fit the tiles (2-4 words). */
export function shortenSummary(summary: string | undefined): string | null {
  const clean = sanitizeTileText(String(summary || '').split(/[.!?;:]/)[0] || '', 80)
  const words = clean.split(' ').filter(Boolean)
  let out = ''
  let n = 0
  for (const w of words) {
    const next = out ? `${out} ${w}` : w
    if (n >= 4 || next.length > MAX_MESSAGE_CHARS) break
    out = next
    n++
  }
  return n >= 2 ? out : null
}

function topicPhrase(text: string | undefined): string | null {
  const t = String(text || '').toLowerCase()
  if (!t) return null
  for (const { words, phrase } of TOPIC_PHRASES) {
    if (words.some(w => new RegExp(`\\b${w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`).test(t))) return phrase
  }
  return null
}

/**
 * A short phrase about what the session is up to: the cached digest, then a
 * tag, then keywords of the last user message, then the active tool.
 */
export function summaryPhrase(ctx: SessionMessageContext, a: ActivityState, status: string, now: number, rng: () => number = Math.random): string {
  const fromDigest = shortenSummary(ctx.summary)
  const tag = ctx.tags?.length ? sanitizeTileText(ctx.tags[Math.floor(rng() * ctx.tags.length)] || '', 18) : ''
  if (fromDigest && (!tag || rng() < 0.6)) return fromDigest
  if (tag) {
    const t = sanitizeTileText(`All about ${tag}`)
    if (t.length <= MAX_MESSAGE_CHARS && tag.length >= 2) return t
  }
  const fromUser = topicPhrase(ctx.lastUserMessage)
  if (fromUser) return fromUser
  if (status === 'idle' && now - a.lastEventAt > LONG_IDLE_MS) return ACTIVITY_PHRASES.idle
  if (a.lastToolCategory && now - a.lastToolAt < LONG_IDLE_MS) {
    if (now - a.lastErrorAt < ERROR_WINDOW_MS) return ACTIVITY_PHRASES.error
    return ACTIVITY_PHRASES[a.lastToolCategory] || ACTIVITY_PHRASES.other
  }
  return ACTIVITY_PHRASES.idle
}

// ── Rotation state ──
export interface PickState {
  recent: string[]
  lastKind: MessageKind
}

const STORAGE_KEY = 'eggify-circus-recent'
let memoryState: PickState = { recent: [], lastKind: 'word' }

export function loadPickState(): PickState {
  try {
    const raw = typeof sessionStorage !== 'undefined' ? sessionStorage.getItem(STORAGE_KEY) : null
    if (raw) {
      const p = JSON.parse(raw)
      if (Array.isArray(p?.recent)) {
        memoryState = {
          recent: p.recent.filter((x: unknown) => typeof x === 'string').slice(-RECENT_WINDOW),
          lastKind: p.lastKind === 'summary' || p.lastKind === 'joke' ? p.lastKind : 'word',
        }
      }
    }
  } catch { /* storage unavailable: keep the in-memory list */ }
  return memoryState
}

export function savePickState(s: PickState) {
  memoryState = s
  try {
    if (typeof sessionStorage !== 'undefined') sessionStorage.setItem(STORAGE_KEY, JSON.stringify(s))
  } catch { /* ignore */ }
}

function remember(s: PickState, kind: MessageKind, text: string) {
  s.lastKind = kind
  if (kind === 'word') return
  s.recent.push(text)
  if (s.recent.length > RECENT_WINDOW) s.recent.splice(0, s.recent.length - RECENT_WINDOW)
}

function pickFrom(pool: readonly string[], recent: readonly string[], rng: () => number): string | null {
  const fresh = pool.filter(j => !recent.includes(sanitizeTileText(j)))
  if (!fresh.length) return null
  return fresh[Math.floor(rng() * fresh.length) % fresh.length] ?? null
}

/** A joke that fits the moment and is not in the recent window. */
export function pickJoke(pool: JokePool, recent: readonly string[], rng: () => number = Math.random): string {
  const first = pool !== 'general' && rng() < CONTEXT_POOL_CHANCE ? pool : 'general'
  const hit = pickFrom(JOKES[first], recent, rng) ?? pickFrom(JOKES.general, recent, rng)
  if (hit) return sanitizeTileText(hit)
  // Everything recent (cannot happen with the window below the pool size): take any other.
  return sanitizeTileText(JOKES.general[Math.floor(rng() * JOKES.general.length)] || 'Splendid!')
}

export interface PickInput {
  ctx?: SessionMessageContext
  activity: ActivityState
  status: string
  now: number
}

/**
 * Choose what the letters spell. `word` means "keep the old behaviour": the
 * caller spells a word from the reply. Roughly one firing in three is a
 * summary or a joke, never two specials in a row.
 */
export function pickLetterMessage(input: PickInput, rng: () => number = Math.random, state: PickState = loadPickState()): LetterMessage {
  const { activity, status, now } = input
  const ctx = input.ctx ?? sessionContext
  if (state.lastKind !== 'word' || rng() >= SPECIAL_CHANCE) {
    remember(state, 'word', '')
    savePickState(state)
    return { kind: 'word', text: '' }
  }
  if (rng() < SUMMARY_SHARE) {
    const text = sanitizeTileText(summaryPhrase(ctx, activity, status, now, rng))
    if (text && !state.recent.includes(text)) {
      remember(state, 'summary', text)
      savePickState(state)
      return { kind: 'summary', text }
    }
  }
  const text = pickJoke(poolForContext(activity, status, now), state.recent, rng)
  remember(state, 'joke', text)
  savePickState(state)
  return { kind: 'joke', text }
}
