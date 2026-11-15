import { mount } from '@vue/test-utils'
import SubagentDetail from '../../components/agents/SubagentDetail.vue'
import type { BackgroundAgent } from '../../composables/agents/useBackgroundAgents'
import { buildSubagentItem } from '../../utils/agents/subagentItem'

const base = new Date(2026, 9, 11, 14, 3, 27).getTime() // local time
const agent = (status: BackgroundAgent['status']): BackgroundAgent => ({
  agent_id: 'detail-times', parent_session_id: 's', subagent_type: 'general-purpose', description: 'Fix the build', status,
  progress: 0, last_output: '', output_lines: 0, created_at: new Date(base - 60_000).toISOString(), updated_at: new Date(base + 10_000).toISOString()
})
const lines = [
  'TOOL:Edit: /repo/a.go',
  'RESULT_START', 'ok', 'RESULT_END',
  'Checking the tests now.',
  'TOOL:Bash: go test ./...'
]
const times = [base, base + 2300, base + 2300, base + 2300, base + 3000, base + 4000]

it('shows when each step happened and how long it took', async () => {
  const item = buildSubagentItem(agent('running'), lines, '', times)
  const wrapper = mount(SubagentDetail, { props: { item, now: base + 9000 } })
  // Key steps: the edit (2.3 s); the running command is under All steps.
  const edit = wrapper.findAll('.sd-step-tool').find(s => s.text() === 'Edit')!.element.closest('.sd-step')!
  expect(edit.querySelector('.sd-step-when time')?.textContent).toBe('14:03:27')
  expect(edit.querySelector('.sd-step-took')?.textContent).toBe('2.3 s')
  expect(edit.querySelector('.sd-step-when')?.getAttribute('title')).toMatch(/2026.*ago · took 2\.3 s$/)

  // The Now box says since when the current step runs.
  expect(wrapper.get('.sd-since').text()).toBe('since 14:03:31')

  await wrapper.findAll('.sd-chip').find(b => b.text() === 'All steps')!.trigger('click')
  const steps = wrapper.findAll('.sd-step')
  const bash = steps.find(s => s.find('.sd-tool-shell').exists())!
  expect(bash.get('.sd-step-when time').text()).toBe('14:03:31')
  expect(bash.get('.sd-step-took').text()).toBe('5.0 s') // still running: so far
  expect(wrapper.get('.sd-note-time').text()).toBe('14:03:30')
})

it('shows no times when none are known', async () => {
  const item = buildSubagentItem(agent('running'), lines, '')
  const wrapper = mount(SubagentDetail, { props: { item, now: base + 9000 } })
  await wrapper.findAll('.sd-chip').find(b => b.text() === 'All steps')!.trigger('click')
  expect(wrapper.find('.sd-step-when').exists()).toBe(false)
  expect(wrapper.find('.sd-note-time').exists()).toBe(false)
  expect(wrapper.find('.sd-since').exists()).toBe(false)
})
