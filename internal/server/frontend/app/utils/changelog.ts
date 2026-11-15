/**
 * "What's new": the repository's CHANGELOG.md as served by GET /api/changelog
 * (internal/changelog), plus the "last seen version" that drives the badge dot.
 */

export interface ChangelogSection {
  title: string
  /** Markdown, rendered with safeMarkdown. */
  items: string[]
}

export interface ChangelogRelease {
  version: string
  date: string
  name?: string
  /** The [Unreleased] block: version is "Unreleased", no date. */
  unreleased?: boolean
  /** Text between the heading and the first section. */
  note?: string
  sections: ChangelogSection[]
}

export interface ChangelogResponse {
  current: string
  total: number
  releases: ChangelogRelease[]
}

export const CHANGELOG_SEEN_KEY = 'eggify-changelog-seen'
export const CHANGELOG_PAGE_SIZE = 20

/** Splits "rc10" into ["rc", 10] (number -1 when there is none). */
function splitTrailingNumber(s: string): [string, number] {
  const m = /^(.*?)(\d+)$/.exec(s)
  return m ? [m[1]!, parseInt(m[2]!, 10)] : [s, -1]
}

function comparePrerelease(a: string, b: string): number {
  const as = a.split('.')
  const bs = b.split('.')
  for (let i = 0; i < Math.min(as.length, bs.length); i++) {
    const x = as[i]!
    const y = bs[i]!
    const xNum = /^\d+$/.test(x)
    const yNum = /^\d+$/.test(y)
    if (xNum && yNum) {
      if (+x !== +y) return +x < +y ? -1 : 1
    } else if (xNum) return -1
    else if (yNum) return 1
    else {
      const [xt, xn] = splitTrailingNumber(x)
      const [yt, yn] = splitTrailingNumber(y)
      if (xt !== yt) return xt < yt ? -1 : 1
      if (xn !== yn) return xn < yn ? -1 : 1
    }
  }
  return as.length === bs.length ? 0 : as.length < bs.length ? -1 : 1
}

/**
 * Compares dotted numeric versions ("v" allowed). A "-prerelease" sorts before
 * the release and its identifiers compare numerically (rc2 < rc10). -1, 0 or 1.
 */
export function compareVersions(a: string, b: string): number {
  const split = (v: string): [number[], string] => {
    let s = v.trim().replace(/^v/i, '').replace(/\+.*$/, '')
    let pre = ''
    const i = s.indexOf('-')
    if (i >= 0) { pre = s.slice(i + 1); s = s.slice(0, i) }
    return [s.split('.').map(p => parseInt(p, 10) || 0), pre]
  }
  const [an, apre] = split(a)
  const [bn, bpre] = split(b)
  for (let i = 0; i < Math.max(3, an.length, bn.length); i++) {
    const x = an[i] ?? 0
    const y = bn[i] ?? 0
    if (x !== y) return x < y ? -1 : 1
  }
  if (apre === bpre) return 0
  if (!apre) return 1
  if (!bpre) return -1
  return comparePrerelease(apre, bpre)
}

export function readSeenVersion(): string | null {
  try {
    return localStorage.getItem(CHANGELOG_SEEN_KEY)
  } catch {
    return null
  }
}

export function writeSeenVersion(version: string): void {
  try {
    localStorage.setItem(CHANGELOG_SEEN_KEY, version)
  } catch {
    // storage blocked: the dot just shows again next time
  }
}

/**
 * True when the running version is newer than the last one the user saw.
 * The first ever call (nothing stored) stores the version silently and
 * returns false.
 */
export function hasUnseenChanges(current: string): boolean {
  if (!current) return false
  const seen = readSeenVersion()
  if (seen === null) {
    writeSeenVersion(current)
    return false
  }
  return compareVersions(current, seen) > 0
}

/** Marks the running version as seen. */
export function markChangelogSeen(current: string): void {
  if (current) writeSeenVersion(current)
}

/** GET /api/changelog through an authenticated fetch (see useAuthenticatedFetch). */
export async function fetchChangelog(
  fetcher: (url: string) => Promise<Response>,
  opts: { limit?: number, since?: string } = {},
): Promise<ChangelogResponse> {
  const q = new URLSearchParams()
  if (opts.limit) q.set('limit', String(opts.limit))
  if (opts.since) q.set('since', opts.since)
  const res = await fetcher(`/api/changelog${q.size ? `?${q}` : ''}`)
  if (!res.ok) throw new Error(`Changelog request failed (${res.status})`)
  const data = await res.json()
  return { current: data.current ?? '', total: data.total ?? 0, releases: data.releases ?? [] }
}

/** Keeps the releases and items that mention the text (case-insensitive). */
export function filterReleases(releases: ChangelogRelease[], text: string): ChangelogRelease[] {
  const q = text.trim().toLowerCase()
  if (!q) return releases
  const out: ChangelogRelease[] = []
  for (const r of releases) {
    if (r.version.toLowerCase().includes(q) || (r.name ?? '').toLowerCase().includes(q) || (r.note ?? '').toLowerCase().includes(q)) {
      out.push(r)
      continue
    }
    const sections = r.sections
      .map(s => ({ ...s, items: s.items.filter(i => i.toLowerCase().includes(q)) }))
      .filter(s => s.items.length > 0)
    if (sections.length) out.push({ ...r, sections })
  }
  return out
}

/** Tag colour family for a section title; unknown titles get "other". */
export function sectionKind(title: string): string {
  const t = title.toLowerCase()
  for (const k of ['added', 'fixed', 'changed', 'security', 'removed', 'improved', 'performance']) {
    if (t.startsWith(k)) return k
  }
  return 'other'
}
