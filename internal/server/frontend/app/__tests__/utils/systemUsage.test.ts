import { formatBytes, parseSystemUsage, parseUsageMap, usageLevel, usageShort } from '~/utils/systemUsage'

const GIB = 1024 ** 3

describe('systemUsage', () => {
  it('reads the server JSON and refuses unusable readings', () => {
    const u = parseSystemUsage({ available: true, cpu_percent: 24.4, mem_used_bytes: 18.4 * GIB, mem_total_bytes: 24 * GIB, cores: 10 })
    expect(u && usageShort(u)).toBe('CPU 24% · 18.4 GB')
    expect(parseSystemUsage({ available: false, cpu_percent: 1, mem_used_bytes: 1, mem_total_bytes: 2 })).toBeNull()
    expect(parseSystemUsage({ available: true, cpu_percent: 'x', mem_used_bytes: 1, mem_total_bytes: 2 })).toBeNull()
    expect(parseSystemUsage({ available: true, cpu_percent: 1, mem_used_bytes: 1, mem_total_bytes: 0 })).toBeNull()
    expect(parseUsageMap({ '': { available: true, cpu_percent: 5, mem_used_bytes: GIB, mem_total_bytes: 2 * GIB }, bad: null })).toHaveProperty([''])
    expect(Object.keys(parseUsageMap({ bad: { available: false } }))).toEqual([])
  })

  it('formats memory like macOS and keeps normal memory use calm', () => {
    expect(formatBytes(512 * 1024 * 1024)).toBe('512 MB')
    expect(formatBytes(1.5 * GIB)).toBe('1.5 GB')
    const base = { memTotalBytes: 100, cores: 8, eggifyCpuPercent: 0, eggifyRssBytes: 0, eggifyProcesses: 0 }
    expect(usageLevel({ ...base, cpuPercent: 20, memUsedBytes: 80 })).toBe('calm')
    expect(usageLevel({ ...base, cpuPercent: 75, memUsedBytes: 10 })).toBe('warn')
    expect(usageLevel({ ...base, cpuPercent: 10, memUsedBytes: 91 })).toBe('warn')
    expect(usageLevel({ ...base, cpuPercent: 92, memUsedBytes: 10 })).toBe('high')
    expect(usageLevel({ ...base, cpuPercent: 10, memUsedBytes: 96 })).toBe('high')
  })
})
