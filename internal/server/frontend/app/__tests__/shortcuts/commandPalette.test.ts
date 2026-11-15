import { ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

const mocks = vi.hoisted(() => ({
  push: vi.fn(async () => {}),
  fetchWithAuth: vi.fn(),
  openOverviewAgent: vi.fn(async () => {}),
  toggleDarkMode: vi.fn()
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: mocks.push }) }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => ({ fetchWithAuth: mocks.fetchWithAuth }) }))
vi.mock('../../composables/useTheme', () => ({ useTheme: () => ({ isDark: ref(false), toggleDarkMode: mocks.toggleDarkMode }) }))
vi.mock('../../utils/agents/openOverviewAgent', () => ({ openOverviewAgent: mocks.openOverviewAgent }))

import CommandPalette from '../../components/CommandPalette.vue'
import {
  installShortcutDispatcher, registerShortcutHandler, __resetShortcuts, __setShortcutEnv
} from '../../composables/useKeyboardShortcuts'
import { useCommandPalette } from '../../composables/useCommandPalette'

const sessions = [
  { id: 'aaaa1111', status: 'idle', project_id: 'p1', updated_at: new Date().toISOString(), selected_avatar: { name: 'Julian' } },
  { id: 'bbbb2222', status: 'processing', project_id: 'p2', updated_at: new Date(Date.now() - 60000).toISOString(), selected_avatar: { name: 'Matilda' } }
]
const projects = [
  { id: 'p1', name: 'Eggify', path: '/code/eggify', color: '#c2185b' },
  { id: 'p2', name: 'Lunar rails', path: '/code/lunar', color: '#1f7a70' }
]

let uninstall: () => void
beforeEach(() => {
  setActivePinia(createPinia())
  __resetShortcuts()
  __setShortcutEnv({ platform: 'mac', shell: 'web' })
  uninstall = installShortcutDispatcher(window)
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth: mocks.fetchWithAuth }))
  mocks.fetchWithAuth.mockImplementation(async (url: string) => ({
    ok: true,
    json: async () => url.startsWith('/api/agent/sessions') ? { sessions } : url.startsWith('/api/projects') ? { projects } : {}
  }))
})
afterEach(() => {
  uninstall()
  useCommandPalette().closePalette()
  __setShortcutEnv({})
  vi.unstubAllGlobals()
  vi.clearAllMocks()
  document.body.innerHTML = ''
})

const cmd = (key: string, extra: KeyboardEventInit = {}, target: EventTarget = document.body) =>
  target.dispatchEvent(new KeyboardEvent('keydown', { key, code: key.length === 1 ? `Key${key.toUpperCase()}` : key, metaKey: true, bubbles: true, cancelable: true, ...extra }))

async function openPalette() {
  const w = mount(CommandPalette, { attachTo: document.body, global: { stubs: { Teleport: true, Transition: false } } })
  const composer = document.createElement('textarea')
  document.body.append(composer)
  composer.focus()
  cmd('k', {}, composer) // ⌘K works while typing
  await flushPromises()
  return w
}

it('opens with ⌘K (also from the composer), lists actions, sessions and projects', async () => {
  registerShortcutHandler('session.new', vi.fn())
  const w = await openPalette()
  expect(w.find('.palette').exists()).toBe(true)
  const groups = w.findAll('.palette-group').map(g => g.text())
  expect(groups).toEqual(['Actions', 'Sessions', 'Projects'])
  expect(w.text()).toContain('New session')
  expect(w.text()).toContain('Julian')
  expect(w.text()).toContain('Lunar rails')
  // The shortcut hint is the binding that applies here (browser: ⌥⌘N)
  const row = w.findAll('.palette-item').find(r => r.text().includes('New session'))!
  expect(row.findAll('kbd').map(k => k.text()).join('')).toBe('⌥⌘N')
  w.unmount()
})

it('only offers actions that can run here', async () => {
  const w = await openPalette()
  // No session open: no "Stop agent", "Search messages"…
  expect(w.text()).not.toContain('Stop agent')
  w.unmount()
})

it('filters fuzzily and runs the chosen session with Enter', async () => {
  const w = await openPalette()
  await w.get('input').setValue('matld')
  const titles = w.findAll('.palette-item-title').map(t => t.text())
  expect(titles[0]).toBe('Matilda')
  await w.get('input').trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(mocks.openOverviewAgent).toHaveBeenCalledWith(expect.objectContaining({ id: 'bbbb2222' }), expect.anything())
  expect(useCommandPalette().isOpen.value).toBe(false)
  w.unmount()
})

it('runs actions through the shortcut registry and switches projects', async () => {
  const newSession = vi.fn()
  registerShortcutHandler('session.new', newSession)
  let w = await openPalette()
  await w.get('input').setValue('new sess')
  await w.get('input').trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(newSession).toHaveBeenCalledWith(expect.objectContaining({ source: 'palette' }))
  w.unmount()

  w = await openPalette()
  await w.get('input').setValue('lunar')
  const items = w.findAll('.palette-item')
  await items.find(i => i.text().includes('Lunar rails') && i.text().includes('/code/lunar'))!.trigger('click')
  await flushPromises()
  expect(mocks.push).toHaveBeenCalledWith({ path: '/agents', query: { project: 'p2' } })
  w.unmount()
})

it('moves with the arrow keys, closes on Escape and toggles with ⌘K', async () => {
  const w = await openPalette()
  const input = w.get('input')
  await input.trigger('keydown', { key: 'ArrowDown' })
  expect(w.findAll('.palette-item')[1]!.classes()).toContain('active')
  // Escape goes through the registry's Escape stack
  input.element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
  await flushPromises()
  expect(useCommandPalette().isOpen.value).toBe(false)
  cmd('k')
  await flushPromises()
  expect(useCommandPalette().isOpen.value).toBe(true)
  cmd('k', {}, w.get('input').element)
  await flushPromises()
  expect(useCommandPalette().isOpen.value).toBe(false)
  w.unmount()
})

it('shows Just recipes and settings pages when searching', async () => {
  const w = await openPalette()
  await w.get('input').setValue('remote')
  expect(w.text()).toContain('Remote access')
  const remote = w.findAll('.palette-item').find(i => i.text().includes('Remote access'))!
  await remote.trigger('click')
  await flushPromises()
  expect(mocks.push).toHaveBeenCalledWith('/settings#access')
  w.unmount()
})
