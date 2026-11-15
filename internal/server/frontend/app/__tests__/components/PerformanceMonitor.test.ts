import { mount, flushPromises } from '@vue/test-utils'
import PerformanceMonitor from '../../components/settings/PerformanceMonitor.vue'

const t0 = Date.parse('2026-10-03T10:00:00Z')
const report = (findings: any[]) => ({
  started_at: new Date(t0).toISOString(),
  interval_seconds: 30,
  findings,
  samples: Array.from({ length: 30 }, (_, i) => ({
    time: new Date(t0 + i * 60000).toISOString(), goroutines: 80, heap_mb: 40, sys_mb: 90, rss_mb: 300,
    cpu_percent: 1, open_fds: 30, children: 2, children_rss_mb: 600, db_mb: 2, wal_mb: 0,
  })),
  client: [],
})

function stub(body: any, ok = true) {
  const fetchWithAuth = vi.fn(async () => ({ ok, status: ok ? 200 : 500, json: async () => body }))
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth }))
  return fetchWithAuth
}
afterEach(() => vi.unstubAllGlobals())

describe('Performance monitor panel', () => {
  it('shows tiles with trends and an all-clear', async () => {
    stub(report([]))
    const w = mount(PerformanceMonitor)
    await flushPromises()
    expect(w.find('[data-test="all-clear"]').exists()).toBe(true)
    const rss = w.find('[data-tile="rss"]')
    expect(rss.text()).toContain('300 MB')
    expect(rss.text()).toContain('steady')
    expect(w.find('[data-tile="children"]').text()).toContain('2')
    w.unmount()
  })

  it('lists findings instead of the all-clear', async () => {
    stub(report([{ id: 'wal-size', severity: 'warn', message: 'The database write-ahead log is 250 MB.' }]))
    const w = mount(PerformanceMonitor)
    await flushPromises()
    expect(w.find('[data-finding="wal-size"]').text()).toContain('250 MB')
    expect(w.find('[data-test="all-clear"]').exists()).toBe(false)
    w.unmount()
  })

  it('says so when the data cannot be loaded', async () => {
    stub({}, false)
    const w = mount(PerformanceMonitor)
    await flushPromises()
    expect(w.find('[role="alert"]').text()).toContain('Could not load performance data')
    w.unmount()
  })

  it('inspects running animations on demand', async () => {
    stub(report([]))
    ;(document as any).getAnimations = () => [{ playState: 'running', animationName: 'sidebarGlow', effect: { target: { tagName: 'DIV', id: '', className: 'sidebar-color-glow' } } }]
    const w = mount(PerformanceMonitor)
    await flushPromises()
    expect(w.find('[data-test="animations"]').exists()).toBe(false)
    await w.find('[data-test="inspect"]').trigger('click')
    expect(w.find('[data-test="animations"]').text()).toContain('sidebarGlow × 1')
    expect(w.find('[data-test="animations"]').text()).toContain('div.sidebar-color-glow')
    delete (document as any).getAnimations
    w.unmount()
  })

  it('copies a report', async () => {
    stub(report([]))
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const w = mount(PerformanceMonitor)
    await flushPromises()
    await w.find('[data-test="copy"]').trigger('click')
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('Server memory'))
    w.unmount()
  })
})
