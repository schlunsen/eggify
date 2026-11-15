import { buildClientSample, countRunningAnimations, listRunningAnimations, measureFps } from '../../utils/perfReporter'
import { buildTiles, formatTrend, formatValue, reportText, slopePerHour, sparkPath, type PerfReport } from '../../utils/perfSummary'

describe('perfSummary', () => {
  const t0 = Date.parse('2026-10-03T10:00:00Z')
  const pts = (n: number, stepMin: number, f: (i: number) => number) =>
    Array.from({ length: n }, (_, i) => ({ t: t0 + i * stepMin * 60000, v: f(i) }))

  it('fits a trend and ignores a single spike', () => {
    expect(slopePerHour(pts(20, 1, i => 100 + i * 2))!).toBeCloseTo(120, 0)
    const spike = slopePerHour(pts(40, 1, i => (i === 20 ? 900 : 300)))!
    expect(Math.abs(spike)).toBeLessThan(40)
  })

  it('does not call a trend from a short window', () => {
    expect(slopePerHour(pts(5, 1, i => i * 100))).toBeNull()
    expect(slopePerHour(pts(2, 30, i => i))).toBeNull()
  })

  it('formats values and trends', () => {
    expect(formatValue(1234.5, 'MB')).toBe('1,235 MB')
    expect(formatValue(12.34, '%')).toBe('12.3 %')
    expect(formatTrend(null, 'MB')).toBe('collecting…')
    expect(formatTrend(0.01, 'MB')).toBe('steady')
    expect(formatTrend(60, 'MB')).toBe('+60 MB/h')
    expect(formatTrend(-3.2, '')).toBe('−3.2/h')
  })

  it('draws a sparkline only for two or more points', () => {
    expect(sparkPath([1], 100, 20)).toBe('')
    expect(sparkPath([1, 2, 3], 100, 20).startsWith('M0.0')).toBe(true)
  })

  const report = (): PerfReport => ({
    started_at: '2026-10-03T10:00:00Z',
    interval_seconds: 30,
    findings: [{ id: 'animations', severity: 'warn', message: '60 animations are running at once.' }],
    samples: Array.from({ length: 30 }, (_, i) => ({
      time: new Date(t0 + i * 60000).toISOString(), goroutines: 80, heap_mb: 40, sys_mb: 90, rss_mb: 200 + i * 2,
      cpu_percent: 0, open_fds: 30, children: 0, children_rss_mb: 0, db_mb: 2, wal_mb: 0,
    })),
    client: [
      { time: new Date(t0).toISOString(), dom_nodes: 5000, animations: 60, fps: 40, messages: 10, sessions: 3, js_heap_mb: 0, visible: true },
      { time: new Date(t0 + 60000).toISOString(), dom_nodes: 99999, animations: 1, fps: 1, messages: 10, sessions: 3, js_heap_mb: 0, visible: false },
    ],
  })

  it('hides readings that are always zero and ignores hidden-page samples', () => {
    const { server, page } = buildTiles(report())
    expect(server.map(t => t.id)).toEqual(['rss', 'goroutines', 'fds'])
    expect(page.find(t => t.id === 'dom')!.current).toBe(5000)
    expect(page.find(t => t.id === 'js-heap')).toBeUndefined()
    expect(server[0]!.perHour).toBeCloseTo(120, 0)
  })

  it('writes a pasteable report', () => {
    const text = reportText(report())
    expect(text).toContain('Server memory: 258 MB (+120 MB/h)')
    expect(text).toContain('- 60 animations are running at once.')
    expect(reportText(null)).toBe('No performance data yet.')
  })
})

describe('perfReporter', () => {
  it('counts only running animations', () => {
    const doc = { getAnimations: () => [{ playState: 'running' }, { playState: 'paused' }, { playState: 'finished' }] } as any
    expect(countRunningAnimations(doc)).toBe(1)
    expect(countRunningAnimations({ getAnimations: () => { throw new Error('x') } } as any)).toBe(0)
  })

  it('groups running animations by name, busiest first', () => {
    const el = (cls: string) => ({ tagName: 'DIV', id: '', className: cls })
    const anim = (name: string, cls: string, playState = 'running') => ({ playState, animationName: name, effect: { target: el(cls) } })
    const doc = { getAnimations: () => [anim('spin', 'a b c'), anim('pulse', 'x'), anim('spin', 'y'), anim('done', 'z', 'finished')] } as any
    expect(listRunningAnimations(doc)).toEqual([
      { name: 'spin', count: 2, example: 'div.a.b' },
      { name: 'pulse', count: 1, example: 'div.x' },
    ])
  })

  it('builds a sample from the document', () => {
    document.body.innerHTML = '<div><span></span></div>'
    const s = buildClientSample({ document, path: '/agents', messages: 7, sessions: 2, fps: 59.94, heapBytes: 52428800 })
    expect(s.dom_nodes).toBeGreaterThanOrEqual(2)
    expect(s).toMatchObject({ page: '/agents', messages: 7, sessions: 2, fps: 59.9, js_heap_mb: 50 })
  })

  it('measures frames per second over the window', async () => {
    let t = 0
    const raf = (cb: (t: number) => void) => { t += 16.667; setTimeout(() => cb(t), 0); return 1 }
    const fps = await measureFps(raf, 1000)
    expect(fps).toBeGreaterThan(55)
    expect(fps).toBeLessThan(65)
  })
})
