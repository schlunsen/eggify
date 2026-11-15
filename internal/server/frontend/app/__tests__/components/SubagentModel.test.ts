import { mount } from '@vue/test-utils'
import SubagentDetail from '../../components/agents/SubagentDetail.vue'
import type { BackgroundAgent } from '../../composables/agents/useBackgroundAgents'
import { buildSubagentItem, itemInputsChanged } from '../../utils/agents/subagentItem'

const agent = (extra: Partial<BackgroundAgent>): BackgroundAgent => ({
  agent_id: 'model-chip', parent_session_id: 's', subagent_type: 'general-purpose', description: 'Scan', status: 'running',
  progress: 0, last_output: '', output_lines: 0, created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
  ...extra
})

it('shows the model the subagent runs on next to its type', () => {
  const item = buildSubagentItem(agent({ model: 'claude-haiku-4-5-20251001', model_source: 'actual' }), [], '')
  const chip = mount(SubagentDetail, { props: { item, now: Date.now() } }).get('[data-testid="subagent-model"]')
  expect(chip.text()).toBe('Haiku 4.5')
  expect(chip.classes()).not.toContain('requested')
  expect(chip.attributes('title')).toBe('Runs on claude-haiku-4-5-20251001')
})

it('marks a model that is only requested', () => {
  const item = buildSubagentItem(agent({ model: 'sonnet', model_source: 'requested' }), [], '')
  const chip = mount(SubagentDetail, { props: { item, now: Date.now() } }).get('[data-testid="subagent-model"]')
  expect(chip.text()).toBe('Sonnet')
  expect(chip.classes()).toContain('requested')
  expect(chip.attributes('title')).toMatch(/requested/)
})

it('shows nothing without a model, and rebuilds when it arrives', () => {
  const before = agent({})
  const item = buildSubagentItem(before, [], '')
  expect(mount(SubagentDetail, { props: { item, now: Date.now() } }).find('[data-testid="subagent-model"]').exists()).toBe(false)
  before.model = 'claude-opus-5-5'
  before.model_source = 'actual'
  expect(itemInputsChanged(item, before, item.lines, '')).toBe(true)
})
