import { flushPromises, mount } from '@vue/test-utils'
import ModelSwitchContextButton from '../../components/agents/ModelSwitchContextButton.vue'

const { fetchWithAuth } = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => ({ fetchWithAuth }) }))

beforeEach(() => {
  fetchWithAuth.mockReset()
  HTMLDialogElement.prototype.showModal = vi.fn()
  HTMLDialogElement.prototype.close = vi.fn()
})

const createWrapper = () => mount(ModelSwitchContextButton, {
  props: { sessionId: 'session-1', messageId: 'switch-2' },
  global: { stubs: { Teleport: true } }
})

it('loads the selected switch on demand and renders the exact context as text', async () => {
  const context = '<conversation_history>\n[user] <script>alert(1)</script>\n</conversation_history>\n'
  fetchWithAuth.mockResolvedValue({ ok: true, json: async () => ({ status: 'sent', context, format: 'text', history_mode: 'replayed' }) })
  const wrapper = createWrapper()
  expect(fetchWithAuth).not.toHaveBeenCalled()
  await wrapper.get('.context-button').trigger('click')
  await flushPromises()
  expect(fetchWithAuth.mock.calls[0][0]).toBe('/api/agent/sessions/session-1/model-switches/switch-2/context')
  expect(wrapper.get('pre').element.textContent).toBe(context)
  expect(wrapper.find('script').exists()).toBe(false)
  wrapper.unmount()
})

it('refreshes a pending handoff and does not invent a transcript', async () => {
  fetchWithAuth.mockResolvedValueOnce({ ok: true, json: async () => ({ status: 'pending' }) })
  fetchWithAuth.mockResolvedValueOnce({ ok: true, json: async () => ({ status: 'sent', context: 'actual payload', format: 'text' }) })
  const wrapper = createWrapper()
  await wrapper.get('.context-button').trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain('Waiting for your next message')
  expect(wrapper.find('pre').exists()).toBe(false)
  await wrapper.findAll('button').find(b => b.text() === 'Refresh')!.trigger('click')
  await flushPromises()
  expect(wrapper.get('pre').text()).toBe('actual payload')
  wrapper.unmount()
})

it('shows the provisional transcript before the next message and refreshes to the sent payload', async () => {
  fetchWithAuth.mockResolvedValueOnce({ ok: true, json: async () => ({ status: 'preview', context: 'will be carried over', format: 'text', history_mode: 'replayed' }) })
  fetchWithAuth.mockResolvedValueOnce({ ok: true, json: async () => ({ status: 'sent', context: 'actual payload', format: 'text', history_mode: 'replayed' }) })
  const wrapper = createWrapper()
  await wrapper.get('.context-button').trigger('click')
  await flushPromises()
  expect(wrapper.get('pre').text()).toBe('will be carried over')
  expect(wrapper.text()).toContain('will be carried over with your next message')
  await wrapper.findAll('button').find(b => b.text() === 'Refresh')!.trigger('click')
  await flushPromises()
  expect(wrapper.get('pre').text()).toBe('actual payload')
  expect(wrapper.findAll('button').find(b => b.text() === 'Refresh')).toBeUndefined()
  wrapper.unmount()
})

it('shows the resumed conversation without calling it a sent payload', async () => {
  fetchWithAuth.mockResolvedValue({ ok: true, json: async () => ({ status: 'resumed', context: 'earlier conversation', format: 'text', history_mode: 'resumed' }) })
  const wrapper = createWrapper()
  await wrapper.get('.context-button').trigger('click')
  await flushPromises()
  expect(wrapper.get('pre').text()).toBe('earlier conversation')
  expect(wrapper.text()).toContain('Local history approximation')
  expect(wrapper.text()).toContain('not the actual provider context')
  expect(wrapper.text()).not.toContain('No separate handoff transcript was sent')
  wrapper.unmount()
})

it.each([
  ['resumed', 'No separate handoff transcript was sent'],
  ['unavailable', 'not recorded for this older switch'],
  ['superseded', 'Another switch happened'],
  ['empty', 'no earlier conversation']
])('explains the %s state', async (status, text) => {
  fetchWithAuth.mockResolvedValue({ ok: true, json: async () => ({ status }) })
  const wrapper = createWrapper()
  await wrapper.get('.context-button').trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain(text)
  expect(wrapper.find('pre').exists()).toBe(false)
  wrapper.unmount()
})

it('shows a retry action on request failure', async () => {
  fetchWithAuth.mockResolvedValue({ ok: false })
  const wrapper = createWrapper()
  await wrapper.get('.context-button').trigger('click')
  await flushPromises()
  expect(wrapper.get('[role="alert"]').text()).toContain('Could not load')
  expect(wrapper.findAll('button').some(b => b.text() === 'Refresh')).toBe(true)
  wrapper.unmount()
})
