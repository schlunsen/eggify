/**
 * CPU and memory of a machine (GET /api/system/usage, or the desktop app's
 * MachineUsage binding for every paired Mac), shown next to the machine chip
 * like a menu-bar meter: "CPU 24% · 18.4 GB". Pure, unit-tested.
 */
export interface SystemUsage {
  cpuPercent: number
  memUsedBytes: number
  memTotalBytes: number
  cores: number
  eggifyCpuPercent: number
  eggifyRssBytes: number
  eggifyProcesses: number
}

export type UsageLevel = 'calm' | 'warn' | 'high'

const num = (v: unknown): number | null => (typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : null)

/** Reads the server's JSON; null when it is missing, malformed or not available. */
export function parseSystemUsage(v: unknown): SystemUsage | null {
  if (!v || typeof v !== 'object') return null
  const o = v as Record<string, unknown>
  if (o.available !== true) return null
  const cpu = num(o.cpu_percent)
  const used = num(o.mem_used_bytes)
  const total = num(o.mem_total_bytes)
  if (cpu === null || used === null || !total) return null
  return {
    cpuPercent: Math.min(cpu, 100),
    memUsedBytes: Math.min(used, total),
    memTotalBytes: total,
    cores: num(o.cores) ?? 0,
    eggifyCpuPercent: Math.min(num(o.eggify_cpu_percent) ?? 0, 100),
    eggifyRssBytes: num(o.eggify_rss_bytes) ?? 0,
    eggifyProcesses: num(o.eggify_processes) ?? 0,
  }
}

/** Every machine's usage from the desktop binding, keyed by machine id ("" = the app's own Mac). */
export function parseUsageMap(v: unknown): Record<string, SystemUsage> {
  const out: Record<string, SystemUsage> = {}
  if (!v || typeof v !== 'object') return out
  for (const [id, raw] of Object.entries(v as Record<string, unknown>)) {
    const u = parseSystemUsage(raw)
    if (u) out[id] = u
  }
  return out
}

const GIB = 1024 ** 3
const MIB = 1024 ** 2

/** Memory as macOS shows it (binary units, labelled GB/MB). */
export function formatBytes(bytes: number): string {
  if (bytes >= GIB) return `${(bytes / GIB).toFixed(1)} GB`
  return `${Math.round(bytes / MIB)} MB`
}

/** The meter's text: "CPU 24% · 18.4 GB". */
export function usageShort(u: SystemUsage): string {
  return `CPU ${Math.round(u.cpuPercent)}% · ${formatBytes(u.memUsedBytes)}`
}

/** The tooltip: the machine, then Eggify's share. */
export function usageDetail(u: SystemUsage, name = 'This machine'): string {
  const cores = u.cores ? ` of ${u.cores} cores` : ''
  const lines = [
    `${name}: CPU ${Math.round(u.cpuPercent)}%${cores} · memory ${formatBytes(u.memUsedBytes)} of ${formatBytes(u.memTotalBytes)}`,
  ]
  if (u.eggifyProcesses > 0) {
    const procs = u.eggifyProcesses === 1 ? '1 process' : `${u.eggifyProcesses} processes`
    lines.push(`Eggify and its agents: CPU ${Math.round(u.eggifyCpuPercent)}% · ${formatBytes(u.eggifyRssBytes)} (${procs})`)
  }
  return lines.join('\n')
}

/**
 * CPU: warn from 70 %, high from 90 %. Memory: warn from 90 %, high from 95 %
 * (macOS keeps memory well used on purpose; 75-80 % is normal).
 */
export function usageLevel(u: SystemUsage): UsageLevel {
  const mem = u.memTotalBytes ? (100 * u.memUsedBytes) / u.memTotalBytes : 0
  if (u.cpuPercent >= 90 || mem >= 95) return 'high'
  if (u.cpuPercent >= 70 || mem >= 90) return 'warn'
  return 'calm'
}
