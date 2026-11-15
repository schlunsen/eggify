/**
 * Turns an assistant reply (markdown) into short, speakable chunks for read-aloud.
 *
 * Kokoro synthesises one chunk at a time and playback starts with the first one,
 * so the first chunk is kept short (a long first sentence is cut at a comma,
 * semicolon, colon or dash, never mid-word). Later chunks grow up to ~280
 * characters, which keeps the number of inferences low while the voice stays
 * ahead of playback.
 *
 * Markdown is read the way a person would read it aloud: code blocks become
 * "Code omitted.", tables "Table omitted.", links their text, bare URLs their
 * domain, numbered list items "One. …", bullets just their text, and
 * emphasis/heading/quote markers disappear.
 */

export interface SpeechChunkOptions {
  /** Longest first chunk (characters). */
  firstMax?: number
  /** Shortest first chunk when a long first sentence is cut. */
  firstMin?: number
  /** Longest second chunk: a step between the short first and the long rest. */
  secondMax?: number
  /** Longest later chunk. */
  maxLen?: number
  /** Stop after this many characters (at a chunk boundary). */
  maxTotal?: number
}

const DEFAULTS: Required<SpeechChunkOptions> = {
  firstMax: 120,
  firstMin: 40,
  secondMax: 180,
  maxLen: 280,
  maxTotal: 6000,
}

const NUMBER_WORDS = [
  'Zero', 'One', 'Two', 'Three', 'Four', 'Five', 'Six', 'Seven', 'Eight', 'Nine', 'Ten',
  'Eleven', 'Twelve', 'Thirteen', 'Fourteen', 'Fifteen', 'Sixteen', 'Seventeen', 'Eighteen', 'Nineteen', 'Twenty',
]

export const CODE_OMITTED = 'Code omitted.'
export const TABLE_OMITTED = 'Table omitted.'

// Words ending in "." that do not end a sentence.
const ABBREVIATIONS = /^(e\.g|i\.e|etc|vs|mr|mrs|ms|dr|st|no|approx|cf|fig|incl|min|max|sec)\.$/i

function urlWords(url: string): string {
  try {
    const host = new URL(url).hostname.replace(/^www\./, '')
    return host || 'a link'
  } catch {
    return 'a link'
  }
}

/** Inline markdown → plain words. */
export function cleanInline(text: string): string {
  return text
    // images and links keep their text
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    // autolinks and bare URLs → the domain (trailing punctuation stays outside)
    .replace(/<(https?:\/\/[^>\s]+)>/g, (_, url: string) => urlWords(url))
    .replace(/\bhttps?:\/\/[^\s<>()\]]*[^\s<>()\].,;:!?'"]/g, (url) => urlWords(url))
    // inline code keeps its content
    .replace(/`+([^`]*)`+/g, '$1')
    // HTML tags
    .replace(/<\/?[a-zA-Z][^>]*>/g, ' ')
    // emphasis / strikethrough markers
    .replace(/\*\*|__|~~/g, '')
    .replace(/\*/g, '')
    // snake_case reads as words; stray underscores go
    .replace(/(\w)_(?=\w)/g, '$1 ')
    .replace(/_/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

function endSentence(text: string): string {
  const t = text.replace(/[:;,]\s*$/, '').trim()
  if (!t) return ''
  return /[.!?…]["')\]]*$/.test(t) ? t : `${t}.`
}

/** Splits markdown into speakable units: paragraphs, headings and list items. */
export function speakableUnits(markdown: string): string[] {
  const units: string[] = []
  const push = (raw: string) => {
    const u = endSentence(cleanInline(raw))
    if (u && /[\p{L}\p{N}]/u.test(u)) units.push(u)
  }
  let para: string[] = []
  const flush = () => { if (para.length) push(para.join(' ')); para = [] }
  const pushMarker = (marker: string) => { flush(); if (units[units.length - 1] !== marker) units.push(marker) }

  let fence: string | null = null
  for (const line of markdown.replace(/\r\n?/g, '\n').split('\n')) {
    const fenceMatch = /^\s*(```|~~~)/.exec(line)
    if (fence) {
      if (fenceMatch && fenceMatch[1] === fence) fence = null
      continue
    }
    if (fenceMatch) { fence = fenceMatch[1]!; pushMarker(CODE_OMITTED); continue }
    if (!line.trim()) { flush(); continue }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) { flush(); continue } // horizontal rule
    if (/^\s*\|.*\|\s*$/.test(line)) { pushMarker(TABLE_OMITTED); continue }

    const heading = /^\s*#{1,6}\s+(.*)$/.exec(line)
    if (heading) { flush(); push(heading[1]!.replace(/\s#+\s*$/, '')); continue }

    const numbered = /^\s*(\d+)[.)]\s+(.*)$/.exec(line)
    if (numbered) {
      flush()
      const n = Number(numbered[1])
      const word = NUMBER_WORDS[n] ?? String(n)
      push(`${word}. ${numbered[2]}`)
      continue
    }
    const bullet = /^\s*[-*+•]\s+(?:\[[ xX]\]\s+)?(.*)$/.exec(line)
    if (bullet) { flush(); push(bullet[1]!); continue }

    para.push(line.replace(/^\s*(>\s?)+/, '').trim())
  }
  flush()
  return units
}

/** Splits one unit into sentences. */
export function splitSentences(unit: string): string[] {
  const out: string[] = []
  let start = 0
  const re = /[.!?…]+["')\]]*(?=\s+\S)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(unit))) {
    const end = m.index + m[0].length
    const lastWord = /(\S+)$/.exec(unit.slice(start, end))?.[1] ?? ''
    if (ABBREVIATIONS.test(lastWord)) continue
    const next = unit.slice(end).trimStart()[0] ?? ''
    // "e.g. this" / "3.5 apples": a lowercase letter after a dot is not a new sentence
    if (m[0].startsWith('.') && /[a-z]/.test(next)) continue
    out.push(unit.slice(start, end).trim())
    start = end
  }
  const rest = unit.slice(start).trim()
  if (rest) out.push(rest)
  return out
}

/**
 * Cuts `text` into a head of at most `max` characters and the rest: at the last
 * clause break (`;` `:` `—` `–`, then `,`) at or after `min`, else at the last
 * space. Only a single "word" longer than `max` (a hash, a path) is cut inside.
 */
export function cutText(text: string, max: number, min: number): [string, string] {
  if (text.length <= max) return [text, '']
  const window = text.slice(0, max + 1)
  let at = -1
  for (const re of [/[;:—–]\s/g, /,\s/g]) {
    let m: RegExpExecArray | null
    while ((m = re.exec(window))) {
      const end = m.index + 1
      if (end >= min && end <= max) at = end
    }
    if (at > 0) break
  }
  if (at < 0) {
    const space = window.lastIndexOf(' ')
    if (space > 0) at = space
  }
  if (at <= 0) {
    // No space at all: the next space ends the word, or cut a giant token
    const nextSpace = text.indexOf(' ', max)
    at = nextSpace > 0 && nextSpace < max * 2 ? nextSpace : max
  }
  return [text.slice(0, at).trim(), text.slice(at).trim()]
}

/** Markdown reply → ordered speech chunks (first one short). */
export function chunkForSpeech(markdown: string, options: SpeechChunkOptions = {}): string[] {
  const o = { ...DEFAULTS, ...options }
  const units = speakableUnits(markdown)
  const chunks: string[] = []
  let total = 0
  const add = (c: string) => {
    if (!c || total >= o.maxTotal) return
    chunks.push(c)
    total += c.length
  }
  const limit = () => (chunks.length === 0 ? o.firstMax : chunks.length === 1 ? o.secondMax : o.maxLen)
  const minFor = () => (chunks.length === 0 ? o.firstMin : Math.floor(limit() / 2))

  let cur = ''
  // Appends a piece (a whole unit, or one sentence of a long unit) to the chunk
  // being built; a piece that does not fit starts the next chunk.
  const pack = (piece: string) => {
    let s = piece
    if (cur && cur.length + 1 + s.length > limit()) { add(cur); cur = '' }
    while (!cur && s.length > limit()) {
      const [head, tail] = cutText(s, limit(), minFor())
      add(head)
      s = tail
    }
    if (!s) return
    cur = cur ? `${cur} ${s}` : s
  }

  for (const unit of units) {
    if (total >= o.maxTotal) break
    const sentences = splitSentences(unit)
    if (chunks.length === 0 && !cur) {
      // The first chunk is the first sentence (or its first clause) alone
      const [first, ...rest] = sentences
      pack(first!)
      if (cur) { add(cur); cur = '' }
      for (const s of rest) pack(s)
      continue
    }
    // Keep a paragraph or list item together when it fits in one chunk
    if (unit.length <= limit()) pack(unit)
    else for (const s of sentences) pack(s)
  }
  add(cur)
  return chunks
}
