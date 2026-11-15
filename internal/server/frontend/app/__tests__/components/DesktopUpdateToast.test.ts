import { flushPromises, mount } from '@vue/test-utils'
import { vi } from 'vitest'

const bridge = vi.hoisted(() => ({
  shell: 'wails' as string,
  state: null as any,
  install: vi.fn(async () => true),
  open: vi.fn(async () => {}),
  openSettings: vi.fn(async () => true),
}))

vi.mock('~/utils/desktop', async (orig) => {
  const real: any = await orig()
  return {
    ...real,
    desktopShell: () => bridge.shell,
    desktopUpdateState: async () => bridge.state,
    desktopInstallUpdate: bridge.install,
    desktopOpenExternal: bridge.open,
    desktopOpenAppManagementSettings: bridge.openSettings,
    onDesktopUpdate: async () => () => {},
    onDesktopUpdateShow: async () => () => {},
  }
})

const { fetchWithAuth } = vi.hoisted(() => ({ fetchWithAuth: vi.fn(async () => new Response(JSON.stringify({ sessions: [], working_now: 0 }))) }))
vi.mock('~/composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => ({ fetchWithAuth }) }))

import DesktopUpdateToast from '~/components/DesktopUpdateToast.vue'

const available = (over: Record<string, unknown> = {}) => ({
  current_version: '2.1.0', latest_version: '2.1.1', update_available: true, can_install: true, phase: 'idle', download_url: 'https://updates.example.com/download/Eggify.dmg', ...over,
})

async function render() {
  const wrapper = mount(DesktopUpdateToast, { attachTo: document.body })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  bridge.shell = 'wails'
  bridge.install.mockClear()
  bridge.open.mockClear()
  bridge.openSettings.mockClear()
  document.body.innerHTML = ''
})

it('opens a dialog for a new version and updates in one click', async () => {
  bridge.state = available()
  const wrapper = await render()
  const dialog = document.querySelector('.update-dialog')!
  expect(dialog.textContent).toContain('Eggify 2.1.1 is ready')
  expect(dialog.textContent).toContain('You have 2.1.0')
  const button = [...dialog.querySelectorAll('button')].find(b => b.textContent?.includes('Update & restart'))!
  button.click()
  await flushPromises()
  expect(bridge.install).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})

it('folds into a pill on "Later" and reopens from it', async () => {
  bridge.state = available()
  const wrapper = await render()
  ;[...document.querySelectorAll('.update-dialog button')].find(b => b.textContent === 'Later')!.dispatchEvent(new Event('click'))
  await flushPromises()
  expect(document.querySelector('.update-dialog')).toBeNull()
  const pill = document.querySelector<HTMLButtonElement>('.update-pill')!
  expect(pill.textContent).toContain('Eggify 2.1.1 is ready')
  pill.click()
  await flushPromises()
  expect(document.querySelector('.update-dialog')).not.toBeNull()
  wrapper.unmount()
})

it('offers the installer when the app cannot replace itself', async () => {
  bridge.state = available({ can_install: false })
  const wrapper = await render()
  const dialog = document.querySelector('.update-dialog')!
  expect(dialog.textContent).toContain('running from the disk image')
  ;[...dialog.querySelectorAll('button')].find(b => b.textContent?.includes('Download 2.1.1'))!.click()
  expect(bridge.open).toHaveBeenCalledWith('https://updates.example.com/download/Eggify.dmg')
  wrapper.unmount()
})

it('shows progress while updating and stays open', async () => {
  bridge.state = available({ phase: 'downloading', percent: 42 })
  const wrapper = await render()
  expect(document.querySelector('.update-dialog')!.textContent).toContain('Downloading Eggify 2.1.1… 42%')
  wrapper.unmount()
})

it('renders nothing outside the desktop app', async () => {
  bridge.shell = 'web'
  bridge.state = available()
  const wrapper = await render()
  expect(document.querySelector('.update-dialog')).toBeNull()
  expect(document.querySelector('.update-pill')).toBeNull()
  wrapper.unmount()
})

it('says how many working sessions continue after the restart', async () => {
  fetchWithAuth.mockImplementationOnce(async () => new Response(JSON.stringify({ sessions: [], working_now: 2, auto_resume: true })))
  bridge.state = available()
  const wrapper = await render()
  expect(document.querySelector('.update-dialog')!.textContent).toContain('Updating restarts Eggify. 2 working sessions will continue after the restart.')
  expect(fetchWithAuth).toHaveBeenCalledWith('/api/agent/resume-after-restart')
  wrapper.unmount()
})

it('says nothing about sessions when none are working', async () => {
  bridge.state = available()
  const wrapper = await render()
  expect(document.querySelector('.update-working')).toBeNull()
  wrapper.unmount()
})

it('explains an App Management block and offers settings, retry and the release DMG', async () => {
  bridge.state = available({ phase: 'error', error: 'blocked', error_code: 'app_management', download_url: 'https://updates.example.com/download/Eggify-2.1.1.dmg' })
  const wrapper = await render()
  const dialog = document.querySelector('.update-dialog')!
  expect(dialog.textContent).toContain('macOS blocked the update')
  expect(dialog.textContent).toContain('App Management')
  const button = (label: string) => [...dialog.querySelectorAll('button')].find(b => b.textContent?.includes(label))!
  expect(document.activeElement).toBe(button('Open App Management settings'))
  button('Open App Management settings').click()
  await flushPromises()
  expect(bridge.openSettings).toHaveBeenCalledTimes(1)
  button('Download the installer').click()
  expect(bridge.open).toHaveBeenCalledWith('https://updates.example.com/download/Eggify-2.1.1.dmg')
  button('Try again').click()
  await flushPromises()
  expect(bridge.install).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})

it('keeps the plain error for other install failures', async () => {
  bridge.state = available({ phase: 'error', error: 'install: disk full' })
  const wrapper = await render()
  const dialog = document.querySelector('.update-dialog')!
  expect(dialog.textContent).toContain("The update didn't go through")
  expect(dialog.textContent).toContain('disk full')
  expect(dialog.textContent).not.toContain('App Management')
  wrapper.unmount()
})
