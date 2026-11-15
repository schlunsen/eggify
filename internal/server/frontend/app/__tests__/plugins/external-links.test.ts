// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const desktop = vi.hoisted(() => ({
  isDesktop: vi.fn(() => true),
  desktopOpenExternal: vi.fn(async () => true),
}))
vi.mock('~/utils/desktop', () => desktop)

let removeHandler: (() => void) | undefined

beforeEach(async () => {
  vi.clearAllMocks()
  desktop.isDesktop.mockReturnValue(true)
  vi.stubGlobal('defineNuxtPlugin', (setup: () => void) => setup)
  vi.resetModules()
})

afterEach(() => {
  removeHandler?.()
  removeHandler = undefined
  document.body.innerHTML = ''
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function installPlugin() {
  const spy = vi.spyOn(document, 'addEventListener')
  const { default: setup } = await import('../../plugins/external-links.client')
  setup()
  const registration = spy.mock.calls.find(([type]) => type === 'click')
  if (registration) {
    const [type, handler, options] = registration
    removeHandler = () => document.removeEventListener(type, handler, options)
  }
}

function clickLink(href: string) {
  const link = document.createElement('a')
  link.href = href
  link.target = '_blank'
  const child = document.createElement('span')
  link.append(child)
  document.body.append(link)
  const event = new MouseEvent('click', { bubbles: true, cancelable: true })
  let prevented = false
  // Observe the plugin's decision, then suppress real navigation in the DOM test.
  link.addEventListener('click', () => { prevented = event.defaultPrevented; event.preventDefault() })
  child.dispatchEvent(event)
  return { defaultPrevented: prevented }
}

it.each([
  'http://localhost:8090',
  'http://localhost:8091/_readiness',
  'http://127.0.0.1:8090',
  'http://[::1]:8090',
  'https://example.com/docs',
])('opens %s through the desktop browser bridge', async (href) => {
  await installPlugin()
  expect(clickLink(href).defaultPrevented).toBe(true)
  expect(desktop.desktopOpenExternal).toHaveBeenCalledExactlyOnceWith(href)
})

it.each(['/projects', '#content', `${window.location.origin}/projects`])(
  'keeps app navigation %s in the webview', async (href) => {
    await installPlugin()
    expect(clickLink(href).defaultPrevented).toBe(false)
    expect(desktop.desktopOpenExternal).not.toHaveBeenCalled()
  },
)

it('leaves browser links to the browser outside the desktop app', async () => {
  desktop.isDesktop.mockReturnValue(false)
  await installPlugin()
  expect(clickLink('http://localhost:8090').defaultPrevented).toBe(false)
  expect(desktop.desktopOpenExternal).not.toHaveBeenCalled()
})
