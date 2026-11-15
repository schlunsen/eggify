/**
 * Sessions waiting for the Claude usage limit to reset (server
 * `agents/usage_limit.go`): a session whose turn was cut off by the limit, or
 * whose background subagents were stopped by it, carries `limit_wait` on
 * session listings and session_updated events until it is continued.
 */

export interface LimitWait {
  why: 'turn' | 'subagents' | string
  since: string
  resume_at: string
  resets_at?: string | null
  limit_type?: string
  attempts?: number
  subagents?: { description?: string, prompt?: string }[]
}

/** The account's latest rate-limit report (`GET /api/agent/usage-limit`). */
export interface UsageLimitInfo {
  status: 'allowed' | 'allowed_warning' | 'rejected' | string
  limit_type?: string
  resets_at?: string | null
  utilization?: number | null
  observed_at?: string
}

export function hasLimitWait(wait: LimitWait | null | undefined): wait is LimitWait {
  return !!wait && Number.isFinite(Date.parse(wait.resume_at))
}

/** "02:01", "02:01 tomorrow" or "Sat 09:00" in the viewer's time. */
export function clockText(at: number, now: number): string {
  const d = new Date(at)
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const day = new Date(now)
  if (d.toDateString() === day.toDateString()) return time
  day.setDate(day.getDate() + 1)
  if (d.toDateString() === day.toDateString()) return `${time} tomorrow`
  return `${d.toLocaleDateString([], { weekday: 'short' })} ${time}`
}

function waitText(ms: number): string {
  const minutes = Math.round(ms / 60000)
  if (minutes < 1) return 'under a minute'
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  if (hours >= 24) return `${Math.floor(hours / 24)} d ${hours % 24} h`
  const rest = minutes % 60
  return rest ? `${hours} h ${rest} min` : `${hours} h`
}

export function limitTypeLabel(type: string | undefined): string {
  switch (type) {
    case 'five_hour': return '5-hour window'
    case 'seven_day':
    case 'seven_day_overage_included': return 'weekly limit'
    case 'seven_day_opus': return 'weekly Opus limit'
    case 'seven_day_sonnet': return 'weekly Sonnet limit'
    case 'overage': return 'extra usage'
    default: return type ? type.replace(/_/g, ' ') : ''
  }
}

/** "Usage limit · resumes 02:01 (in 3 h 12 min)", "Usage limit · resuming now"; '' when none. */
export function limitWaitLabel(wait: LimitWait | null | undefined, now: number): string {
  if (!hasLimitWait(wait)) return ''
  const at = Date.parse(wait.resume_at)
  if (at <= now) return 'Usage limit · resuming now'
  return `Usage limit · resumes ${clockText(at, now)} (in ${waitText(at - now)})`
}

/** Short form for tight rows: "⏸ 02:01", "⏸ now". */
export function limitWaitShortLabel(wait: LimitWait | null | undefined, now: number): string {
  if (!hasLimitWait(wait)) return ''
  const at = Date.parse(wait.resume_at)
  if (at <= now) return '⏸ now'
  return `⏸ ${new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
}

/** What the wait covers, for tooltips and the banner. */
export function limitWaitDetail(wait: LimitWait | null | undefined): string {
  if (!hasLimitWait(wait)) return ''
  const kind = limitTypeLabel(wait.limit_type)
  const parts = [`Claude usage limit reached${kind ? ` (${kind})` : ''}.`]
  const n = wait.subagents?.length || 0
  if (wait.why === 'turn') parts.push('The last turn was cut off.')
  if (n) parts.push(`${n} subagent${n === 1 ? ' was' : 's were'} paused.`)
  if (!wait.resets_at) parts.push('No reset time was given, so it tries again periodically.')
  return parts.join(' ')
}

/** "Session limit: 62% used · resets 02:00" for the usage meter; '' when nothing worth showing. */
export function usageLimitSummary(info: UsageLimitInfo | null | undefined, now: number): string {
  if (!info) return ''
  const resets = Date.parse(info.resets_at || '')
  const when = Number.isFinite(resets) && resets > now ? ` · resets ${clockText(resets, now)}` : ''
  const kind = info.limit_type === 'five_hour' ? 'session limit' : (limitTypeLabel(info.limit_type) || 'usage limit')
  const name = kind.charAt(0).toUpperCase() + kind.slice(1)
  if (info.status === 'rejected') return `${name} reached${when}`
  if (typeof info.utilization === 'number') return `${name}: ${Math.round(info.utilization * 100)}% used${when}`
  return info.status === 'allowed_warning' ? `${name} almost reached${when}` : ''
}

/** Codex's plan usage (GET /api/agent/usage-limit?codex=1 → codex). */
export interface CodexUsageWindow {
  used_percent: number
  window_minutes?: number
  resets_at?: string
}
export interface CodexUsageInfo {
  plan_type?: string
  windows: CodexUsageWindow[]
  limit_reached: boolean
  observed_at: string
}

/** "Weekly limit", "5-hour limit", "2-day limit" from a window's length. */
export function windowLabel(minutes: number | undefined): string {
  if (!minutes) return 'Usage limit'
  if (minutes === 10080) return 'Weekly limit'
  if (minutes % 1440 === 0) return `${minutes / 1440}-day limit`
  if (minutes % 60 === 0) return `${minutes / 60}-hour limit`
  return `${minutes}-minute limit`
}

/** The window closest to its limit (the one that will stop Codex first). */
export function codexTightestWindow(info: CodexUsageInfo | null | undefined): CodexUsageWindow | null {
  if (!info?.windows?.length) return null
  return info.windows.reduce((a, b) => (b.used_percent > a.used_percent ? b : a))
}

/** "Weekly limit: 50% used · resets Fri 04:00" for Codex; '' when unknown. */
export function codexUsageSummary(info: CodexUsageInfo | null | undefined, now: number): string {
  const w = codexTightestWindow(info)
  if (!info || !w) return ''
  const resets = Date.parse(w.resets_at || '')
  const when = Number.isFinite(resets) && resets > now ? ` · resets ${clockText(resets, now)}` : ''
  const name = windowLabel(w.window_minutes)
  if (info.limit_reached || w.used_percent >= 100) return `${name} reached${when}`
  return `${name}: ${Math.round(w.used_percent)}% used${when}`
}

/** Whether a session runs on Codex (provider "codex", or a model name with "codex"). */
export function isCodexRuntime(provider?: string | null, model?: string | null): boolean {
  return (provider || '').trim().toLowerCase() === 'codex' || /codex/i.test(model || '')
}

/** How the usage pill looks: calm below 75 %, warn from 75 %, limited when reached. */
export function usageLevel(percent: number | null | undefined, reached: boolean): 'calm' | 'warn' | 'limited' {
  if (reached) return 'limited'
  return typeof percent === 'number' && percent >= 75 ? 'warn' : 'calm'
}
