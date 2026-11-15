import { createPinia, setActivePinia } from 'pinia'
import { useMetricsStore } from '../../stores/metrics/metricsStore'
import { useMigrationHelpers } from '../../composables/useMigrationHelpers'

beforeEach(() => setActivePinia(createPinia()))

it('counts tools from stored messages by name and never records an unnamed one', () => {
  const metrics = useMetricsStore()
  metrics.recordToolUse('s1', '')
  metrics.recordToolUse('s1', undefined as unknown as string)
  const { addMessageWithMetrics } = useMigrationHelpers()
  addMessageWithMetrics('s1', { id: 'm1', role: 'assistant', content: '', timestamp: new Date().toISOString(), toolUses: [{ id: 't1', name: 'CronCreate', input: {} }] } as never)
  expect(metrics.getSessionToolStats('s1')).toEqual({ CronCreate: 1 })
})
