import { mount } from '@vue/test-utils'
import ChatHeader from '../../components/agents/ChatHeader.vue'

it('opens the session model picker and displays the current model', async () => {
  const wrapper = mount(ChatHeader, {
    props: { activeTab: 'chat', viewMode: 'live', currentModel: 'test-model' },
    global: { stubs: { ViewModeToggle: true } }
  })
  const button = wrapper.get('button[aria-label="Change session model"]')
  expect(button.text()).toContain('test-model')
  await button.trigger('click')
  expect(wrapper.emitted('change-model')).toHaveLength(1)
  await wrapper.setProps({ modelSwitchDisabledReason: 'Wait for the current turn' })
  expect(button.attributes('disabled')).toBeDefined()
  expect(button.attributes('title')).toBe('Wait for the current turn')
  await button.trigger('click')
  expect(wrapper.emitted('change-model')).toHaveLength(1)
  wrapper.unmount()
})

it('offers both chat widths and hides the control outside live chat', async () => {
  const wrapper = mount(ChatHeader, {
    props: { activeTab: 'chat', viewMode: 'live', chatWidth: 'full' },
    global: { stubs: { ViewModeToggle: true } }
  })
  const select = wrapper.get('select[aria-label="Chat width"]')
  await select.setValue('reading')
  expect(wrapper.emitted('update:chat-width')).toEqual([['reading']])
  await select.setValue('full')
  expect(wrapper.emitted('update:chat-width')?.[1]).toEqual(['full'])
  await wrapper.setProps({ viewMode: 'zen' })
  expect(wrapper.find('select[aria-label="Chat width"]').exists()).toBe(false)
  await wrapper.setProps({ viewMode: 'live', activeTab: 'terminal' })
  expect(wrapper.find('select[aria-label="Chat width"]').exists()).toBe(false)
  wrapper.unmount()
})
