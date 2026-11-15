// Result of POST /api/skills/discover (internal/server/handlers/skills_handler.go).
// Discovery stores new or edited SKILL.md files as pending review items; it
// never approves anything.

export interface DiscoveryIssue {
  path: string
  name?: string
  message: string
}

export interface DiscoverySkipped {
  path: string
  name: string
  reason: string
}

export interface DiscoveryResult {
  /** SKILL.md files that parsed. */
  discovered: number
  /** New or changed files that now wait for review. */
  pending: number
  /** Files that match what was synced (or approved) before. */
  unchanged: number
  skipped: number
  skipped_items: DiscoverySkipped[]
  errors: DiscoveryIssue[]
}

const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : 0)

/** Fill in what an older server leaves out. */
export function normaliseDiscovery(data: any): DiscoveryResult {
  const errors: DiscoveryIssue[] = Array.isArray(data?.errors)
    ? data.errors.map((e: any) => (typeof e === 'string' ? { path: '', message: e } : { path: String(e?.path ?? ''), name: e?.name, message: String(e?.message ?? '') }))
    : []
  const skipped_items: DiscoverySkipped[] = Array.isArray(data?.skipped_items)
    ? data.skipped_items.map((s: any) => ({ path: String(s?.path ?? ''), name: String(s?.name ?? ''), reason: String(s?.reason ?? '') }))
    : []
  return {
    discovered: num(data?.discovered),
    pending: num(data?.pending),
    unchanged: num(data?.unchanged),
    skipped: num(data?.skipped) || skipped_items.length,
    skipped_items,
    errors
  }
}

const plural = (n: number, one: string, many = one + 's') => `${n} ${n === 1 ? one : many}`

/** One line describing a discovery run. */
export function discoverySummary(r: DiscoveryResult): string {
  const parts = [`Found ${plural(r.discovered, 'skill file')}`]
  if (r.pending) parts.push(`${r.pending} new or changed, waiting for your review`)
  if (r.unchanged) parts.push(`${r.unchanged} unchanged`)
  if (r.skipped) parts.push(`${r.skipped} skipped`)
  if (r.errors.length) parts.push(`${plural(r.errors.length, 'error')}`)
  return parts.join(' · ')
}
