// Parses one file's unified diff (as `git diff` prints it) into hunks of
// numbered lines, and marks the changed part of edited lines.

export type DiffLineKind = 'add' | 'del' | 'ctx' | 'note'

export interface DiffSegment { text: string, changed: boolean }

export interface DiffLine {
  kind: DiffLineKind
  old: number | null
  new: number | null
  text: string
  /** For an edited line paired with its counterpart: the text split into unchanged / changed parts. */
  segments?: DiffSegment[]
}

export interface DiffHunk {
  header: string
  /** The function/section name git prints after the second @@. */
  section: string
  oldStart: number
  newStart: number
  lines: DiffLine[]
}

const HUNK_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$/

/** Longest lines we compare for in-line highlights; longer ones stay whole. */
const MAX_PAIR_LENGTH = 1000

export function parseUnifiedDiff(diff: string | undefined | null): DiffHunk[] {
  if (!diff) return []
  const hunks: DiffHunk[] = []
  let hunk: DiffHunk | null = null
  let oldNo = 0
  let newNo = 0
  for (const line of diff.split('\n')) {
    const m = HUNK_RE.exec(line)
    if (m) {
      oldNo = Number(m[1])
      newNo = Number(m[3])
      hunk = { header: line, section: (m[5] || '').trim(), oldStart: oldNo, newStart: newNo, lines: [] }
      hunks.push(hunk)
      continue
    }
    // File headers (diff --git, index, ---, +++, mode lines) come before the first hunk.
    if (!hunk) continue
    const first = line[0]
    if (first === '+') hunk.lines.push({ kind: 'add', old: null, new: newNo++, text: line.slice(1) })
    else if (first === '-') hunk.lines.push({ kind: 'del', old: oldNo++, new: null, text: line.slice(1) })
    else if (first === ' ') hunk.lines.push({ kind: 'ctx', old: oldNo++, new: newNo++, text: line.slice(1) })
    else if (first === '\\') hunk.lines.push({ kind: 'note', old: null, new: null, text: line.slice(2) })
    else if (line === '') continue
    else hunk = null // a following file's header, if a caller passed several
  }
  for (const h of hunks) markEdits(h.lines)
  return hunks
}

/**
 * Pairs each run of removed lines with the added lines right after it
 * (line by line) and marks the part between their common prefix and suffix.
 */
function markEdits(lines: DiffLine[]) {
  let i = 0
  while (i < lines.length) {
    if (lines[i].kind !== 'del') { i++; continue }
    const delStart = i
    while (i < lines.length && lines[i].kind === 'del') i++
    const addStart = i
    while (i < lines.length && lines[i].kind === 'add') i++
    const pairs = Math.min(addStart - delStart, i - addStart)
    for (let k = 0; k < pairs; k++) {
      const del = lines[delStart + k]
      const add = lines[addStart + k]
      const seg = editSegments(del.text, add.text)
      if (seg) [del.segments, add.segments] = seg
    }
  }
}

export function editSegments(a: string, b: string): [DiffSegment[], DiffSegment[]] | null {
  if (a === b || a.length > MAX_PAIR_LENGTH || b.length > MAX_PAIR_LENGTH) return null
  let p = 0
  const max = Math.min(a.length, b.length)
  while (p < max && a[p] === b[p]) p++
  let s = 0
  while (s < max - p && a[a.length - 1 - s] === b[b.length - 1 - s]) s++
  // Nothing in common: highlighting the whole line adds nothing.
  if (p === 0 && s === 0) return null
  const split = (t: string): DiffSegment[] => [
    { text: t.slice(0, p), changed: false },
    { text: t.slice(p, t.length - s), changed: true },
    { text: t.slice(t.length - s), changed: false },
  ].filter(x => x.text !== '')
  return [split(a), split(b)]
}

/** Number of lines a parsed diff renders (to collapse big files). */
export function renderedLineCount(hunks: DiffHunk[]): number {
  return hunks.reduce((n, h) => n + h.lines.length + 1, 0)
}
