/** Turns the /api/perf report into tiles for Settings → Performance. */
export interface PerfSample {
  time: string
  goroutines: number
  heap_mb: number
  sys_mb: number
  rss_mb: number
  cpu_percent: number
  open_fds: number
  children: number
  children_rss_mb: number
  db_mb: number
  wal_mb: number
  extra?: Record<string, number>
}
export interface PerfClientSample {
  time: string
  dom_nodes: number
  animations: number
  fps: number
  messages: number
  sessions: number
  js_heap_mb: number
  long_tasks?: number
  max_stall_ms?: number
  visible: boolean
  page?: string
}
export interface PerfFinding { id: string, severity: string, message: string }
export interface PerfReport {
  started_at: string
  interval_seconds: number
  samples: PerfSample[]
  client: PerfClientSample[]
  findings: PerfFinding[]
}

export interface Tile {
  id: string
  label: string
  unit: string
  values: number[]
  current: number
  /** Change per hour over the shown window, or null when the window is too short. */
  perHour: number | null
  /** Reading that is always zero on this platform/browser is hidden. */
  available: boolean
}

const MIN_TREND_MS = 10 * 60 * 1000

/** Least-squares slope per hour; a fit so one spike does not read as growth. */
export function slopePerHour(points: { t: number, v: number }[]): number | null {
  if (points.length < 3) return null
  if (points[points.length - 1]!.t - points[0]!.t < MIN_TREND_MS) return null
  const t0 = points[0]!.t
  let sx = 0, sy = 0, sxx = 0, sxy = 0
  for (const p of points) {
    const x = (p.t - t0) / 3_600_000
    sx += x; sy += p.v; sxx += x * x; sxy += x * p.v
  }
  const n = points.length
  const den = n * sxx - sx * sx
  return den === 0 ? null : (n * sxy - sx * sy) / den
}

function tile<T extends { time: string }>(
  rows: T[], id: string, label: string, unit: string, get: (r: T) => number,
): Tile {
  const points = rows.map(r => ({ t: Date.parse(r.time), v: get(r) })).filter(p => Number.isFinite(p.t) && Number.isFinite(p.v))
  const values = points.map(p => p.v)
  return {
    id, label, unit, values,
    current: values.length ? values[values.length - 1]! : 0,
    perHour: slopePerHour(points),
    available: values.some(v => v !== 0),
  }
}

export function buildTiles(report: PerfReport | null): { server: Tile[], page: Tile[] } {
  if (!report) return { server: [], page: [] }
  const s = report.samples
  // Only visible-page samples say anything about rendering.
  const c = report.client.filter(x => x.visible)
  const server = [
    tile(s, 'rss', 'Server memory', 'MB', r => r.rss_mb),
    tile(s, 'goroutines', 'Goroutines', '', r => r.goroutines),
    tile(s, 'fds', 'Open files', '', r => r.open_fds),
    tile(s, 'cpu', 'Server CPU', '%', r => r.cpu_percent),
    tile(s, 'children', 'Agent processes', '', r => r.children),
    tile(s, 'children-rss', 'Agent memory', 'MB', r => r.children_rss_mb),
    tile(s, 'wal', 'Database log (WAL)', 'MB', r => r.wal_mb),
    tile(s, 'ws', 'Connected clients', '', r => r.extra?.ws_clients ?? 0),
  ].filter(t => t.available)
  const page = [
    tile(c, 'dom', 'DOM nodes', '', r => r.dom_nodes),
    tile(c, 'animations', 'Running animations', '', r => r.animations),
    tile(c, 'fps', 'Frame rate', 'fps', r => r.fps),
    tile(c, 'messages', 'Messages in memory', '', r => r.messages),
    tile(c, 'js-heap', 'Page JS memory', 'MB', r => r.js_heap_mb),
    tile(c, 'stall', 'Longest main-thread stall', 'ms', r => r.max_stall_ms ?? 0),
  ].filter(t => t.available)
  return { server, page }
}

export function formatValue(v: number, unit: string): string {
  const n = Math.abs(v) >= 100 ? Math.round(v).toLocaleString('en-US') : (Math.round(v * 10) / 10).toString()
  return unit ? `${n} ${unit}` : n
}

export function formatTrend(perHour: number | null, unit: string): string {
  if (perHour === null) return 'collecting…'
  if (Math.abs(perHour) < 0.05) return 'steady'
  const sign = perHour > 0 ? '+' : '−'
  return `${sign}${formatValue(Math.abs(perHour), unit)}/h`
}

/** A sparkline path in a width × height box; empty for fewer than two points. */
export function sparkPath(values: number[], width: number, height: number): string {
  if (values.length < 2) return ''
  const min = Math.min(...values), max = Math.max(...values)
  const span = max - min || 1
  const pad = 2
  return values.map((v, i) => {
    const x = (i / (values.length - 1)) * width
    const y = height - pad - ((v - min) / span) * (height - pad * 2)
    return `${i ? 'L' : 'M'}${x.toFixed(1)} ${y.toFixed(1)}`
  }).join(' ')
}

/** Plain text a person can paste into a bug report. */
export function reportText(report: PerfReport | null): string {
  if (!report) return 'No performance data yet.'
  const { server, page } = buildTiles(report)
  const lines = [`Eggify performance, monitoring since ${report.started_at}`]
  const line = (t: Tile) => `  ${t.label}: ${formatValue(t.current, t.unit)} (${formatTrend(t.perHour, t.unit)})`
  lines.push('Server:', ...server.map(line), 'Page:', ...page.map(line))
  lines.push(report.findings.length ? 'Findings:' : 'Findings: none', ...report.findings.map(f => `  - ${f.message}`))
  return lines.join('\n')
}
