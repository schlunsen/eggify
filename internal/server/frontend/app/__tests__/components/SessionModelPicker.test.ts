import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import SessionModelPicker from '../../components/agents/SessionModelPicker.vue'
import { rankModelChoices, modelKey, loadModelPins, MODEL_PINS_KEY } from '../../utils/agents/modelShortcuts'
const providers = [
  { id: 'claude', name: 'Claude', icon: '', models: ['sonnet', 'opus'] },
  { id: 'codex', name: 'Codex', icon: '', models: ['astra', 'sol'] },
]
const sessions = [
  { options: { provider: 'codex', model: 'astra' } },
  { provider: 'codex', model_name: 'astra' },
  { provider: 'claude', model_name: 'opus' },
]
const current = { provider: 'claude', model: 'sonnet' }
beforeEach(() => localStorage.clear())
afterEach(() => { document.body.innerHTML = '' })
function picker(extra = {}) {
  const wrapper = mount(SessionModelPicker, {
    attachTo: document.body,
    props: { providers, sessions, ...current, ...extra, onSelect: (c: any) => wrapper.setProps({ provider: c.provider, model: c.model }) },
  })
  return wrapper
}
it('ranks actual usage, keeps provider/model pairs distinct, and honours pins', () => {
  expect(rankModelChoices(providers, sessions, [], current).map(c => c.model)).toEqual(['astra', 'opus', 'sonnet', 'sol'])
  expect(rankModelChoices(providers, sessions, [modelKey({ provider: 'codex', model: 'sol' })], current)[0].model).toBe('sol')
  expect(rankModelChoices([...providers, { id: 'other', name: 'Other', icon: '', models: ['astra'] }], sessions, [], current)[0].provider).toBe('codex')
})
it('selects across providers with arrows and number keys, then launches once with Enter', async () => {
  const wrapper = picker()
  await (wrapper.vm as any).focus()
  expect(document.activeElement?.textContent).toContain('sonnet')
  const group = wrapper.get('[role="radiogroup"]')
  await group.trigger('keydown', { key: '1' })
  expect(wrapper.props('provider')).toBe('codex')
  expect(wrapper.props('model')).toBe('astra')
  expect(document.activeElement?.textContent).toContain('astra')
  await group.trigger('keydown', { key: 'ArrowDown' })
  expect(wrapper.props('model')).toBe('opus')
  expect(wrapper.findAll('[role="radio"]').map(r => r.text())).toEqual(expect.arrayContaining([expect.stringContaining('1astra'), expect.stringContaining('2opus')]))
  await group.trigger('keydown', { key: 'Enter' })
  await group.trigger('keydown', { key: 'Enter', repeat: true })
  expect(wrapper.emitted('launch')).toHaveLength(1)
  wrapper.unmount()
})
it('searches, pins favourites persistently and selects a model outside the top three', async () => {
  const wrapper = picker()
  await wrapper.get('.browse-button').trigger('click')
  await wrapper.get('input').setValue('sol')
  await wrapper.get('.pin-button').trigger('click')
  expect(loadModelPins()).toEqual([modelKey({ provider: 'codex', model: 'sol' })])
  await wrapper.get('.result-select').trigger('click')
  expect(wrapper.props('model')).toBe('sol')
  expect(wrapper.find('.model-browser').exists()).toBe(false)
  wrapper.unmount()
})
it('ignores malformed storage and unavailable models', () => {
  localStorage.setItem(MODEL_PINS_KEY, '{}')
  expect(loadModelPins()).toEqual([])
  expect(rankModelChoices(providers, sessions, ['removed-model'], current)).toHaveLength(4)
})
it('does not select or launch while disabled', async () => {
  const wrapper = picker({ disabled: true })
  await wrapper.get('[role="radiogroup"]').trigger('keydown', { key: '2' })
  await wrapper.get('[role="radiogroup"]').trigger('keydown', { key: 'Enter' })
  expect(wrapper.emitted('select')).toBeUndefined()
  expect(wrapper.emitted('launch')).toBeUndefined()
  wrapper.unmount()
})
it('pins up to five favourites and shows them as quick picks 1–5', async () => {
  const many = [{ id: 'claude', name: 'Claude', icon: '', models: ['m1', 'm2', 'm3', 'm4', 'm5', 'm6', 'm7'] }]
  const wrapper = picker({ providers: many, sessions: [], provider: 'claude', model: 'm7' })
  await wrapper.get('.browse-button').trigger('click')
  const pinButtons = () => wrapper.findAll('.pin-button')
  for (let i = 0; i < 5; i++) await pinButtons().find(b => b.text() === '☆')!.trigger('click')
  expect(loadModelPins()).toHaveLength(5)
  expect(wrapper.text()).toContain('5/5 favourites pinned')
  expect(pinButtons().filter(b => b.text() === '☆').every(b => b.attributes('disabled') !== undefined)).toBe(true)
  expect(wrapper.findAll('[role="radio"]')).toHaveLength(5)
  await wrapper.get('[role="radiogroup"]').trigger('keydown', { key: '5' })
  expect(wrapper.props('model')).toBe(wrapper.findAll('[role="radio"]')[4].find('strong').text())
  wrapper.unmount()
})
