/**
 * Small fuzzy matcher for the command palette.
 *
 * Every word of the query must appear in the text as a subsequence. Matches at
 * word starts, consecutive characters and plain substrings/prefixes score
 * higher. Returns null when the text does not match.
 */
export function fuzzyScore(query: string, text: string): number | null {
  const q = query.trim().toLowerCase()
  if (!q) return 0
  const t = text.toLowerCase()
  let total = 0
  for (const word of q.split(/\s+/)) {
    const s = wordScore(word, t)
    if (s === null) return null
    total += s
  }
  return total
}

function isBoundary(t: string, i: number): boolean {
  if (i === 0) return true
  const prev = t[i - 1]!
  return !/[a-z0-9]/.test(prev)
}

function wordScore(word: string, t: string): number | null {
  let score = 0
  const sub = t.indexOf(word)
  if (sub >= 0) {
    score += 20 + word.length * 2
    if (sub === 0) score += 15
    else if (isBoundary(t, sub)) score += 10
  }
  // Subsequence pass (also scores the substring case, so word starts count)
  let ti = 0
  let last = -2
  let matched = 0
  for (const ch of word) {
    let found = -1
    // Prefer the next occurrence at a word boundary within reach
    for (let j = ti; j < t.length; j++) {
      if (t[j] === ch) {
        if (found < 0) found = j
        if (isBoundary(t, j) && j !== last + 1) { found = j; break }
        if (j === last + 1) { found = j; break }
      }
    }
    if (found < 0) return null
    matched++
    score += 1
    if (found === last + 1) score += 3
    if (isBoundary(t, found)) score += 4
    score -= Math.min(found - ti, 8) * 0.1
    last = found
    ti = found + 1
  }
  return matched === word.length ? score : null
}

/** Every word of the query appears in the text as a substring. */
export function containsWords(query: string, text: string): boolean {
  const t = text.toLowerCase()
  return query.trim().toLowerCase().split(/\s+/).every(w => !w || t.includes(w))
}
