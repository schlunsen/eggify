import { flushPromises, mount } from '@vue/test-utils'
import { vi } from 'vitest'

const bridge = vi.hoisted(() => ({
  shell: 'wails' as string,
  machines: null as any,
  switchTo: vi.fn(async () => true),
  add: vi.fn(async () => true),
  emit: null as null | ((m: any) => void),
  usage: null as null | Record<string, any>,
}))

vi.mock('~/utils/desktop', async (orig) => {
  const real: any = await orig()
  return {
    ...real,
    desktopShell: () => bridge.shell,
    desktopMachines: async () => (bridge.shell === 'wails' ? bridge.machines : null),
    desktopSwitchMachine: bridge.switchTo,
    desktopAddMachineFromClipboard: bridge.add,
    desktopMachineUsage: async () => bridge.usage,
    onDesktopMachines: async (cb: (m: any) => void) => { bridge.emit = cb; return () => { bridge.emit = null } },
  }
})

import MachineChip from '~/components/MachineChip.vue'
import { __setShortcutEnv, closeTopmost } from '~/composables/useKeyboardShortcuts'

const here = { id: '', name: 'This Mac', hue: 252, here: true }
const studio = { id: 'abc', name: 'Studio', hue: 120, here: false }

async function render() {
  const wrapper = mount(MachineChip, { attachTo: document.body })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  bridge.shell = 'wails'
  bridge.usage = null
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, json: async () => ({}) })))
  bridge.switchTo.mockClear()
  bridge.add.mockClear()
  document.body.innerHTML = ''
  __setShortcutEnv({ platform: 'mac', shell: 'wails' })
})

it('shows nothing with only this Mac', async () => {
  bridge.machines = { current: '', machines: [here] }
  const wrapper = await render()
  expect(wrapper.find('.machine-chip').exists()).toBe(false)
  wrapper.unmount()
})

it('shows nothing outside the desktop app', async () => {
  bridge.shell = 'web'
  bridge.machines = { current: '', machines: [here, studio] }
  const wrapper = await render()
  expect(wrapper.find('.machine-chip').exists()).toBe(false)
  wrapper.unmount()
})

it('names the machine shown, with its colour and the switch keys', async () => {
  bridge.machines = { current: 'abc', machines: [here, studio] }
  const wrapper = await render()
  const button = wrapper.find('.machine-chip-button')
  expect(button.text()).toContain('Studio')
  expect(button.attributes('style')).toContain('--machine-hue: 120')
  expect(button.attributes('title')).toContain('⌥⌘←')
  expect(button.attributes('title')).toContain('⌥⌘→')
  wrapper.unmount()
})

it('says This Mac on the local one, and follows the machines event', async () => {
  bridge.machines = { current: '', machines: [here, studio] }
  const wrapper = await render()
  expect(wrapper.find('.machine-name').text()).toBe('This Mac')
  bridge.emit!({ current: '', machines: [here] })
  await flushPromises()
  expect(wrapper.find('.machine-chip').exists()).toBe(false)
  wrapper.unmount()
})

it('switches from the menu, with the current machine checked', async () => {
  bridge.machines = { current: '', machines: [here, studio] }
  const wrapper = await render()
  await wrapper.find('.machine-chip-button').trigger('click')
  await flushPromises()
  const items = wrapper.findAll('[role="menuitemradio"]')
  expect(items).toHaveLength(2)
  expect(items[0]!.attributes('aria-checked')).toBe('true')
  expect(items[1]!.attributes('aria-checked')).toBe('false')
  expect(document.activeElement).toBe(items[0]!.element)
  // Arrow keys move through the menu.
  await items[0]!.trigger('keydown', { key: 'ArrowDown' })
  expect(document.activeElement).toBe(items[1]!.element)
  await items[1]!.trigger('click')
  await flushPromises()
  expect(bridge.switchTo).toHaveBeenCalledWith('abc')
  expect(wrapper.find('.machine-menu').exists()).toBe(false)
  wrapper.unmount()
})

it('adds a Mac from the clipboard and closes with Escape', async () => {
  bridge.machines = { current: 'abc', machines: [here, studio] }
  const wrapper = await render()
  await wrapper.find('.machine-chip-button').trigger('click')
  await flushPromises()
  // Picking the machine already shown does nothing.
  await wrapper.findAll('[role="menuitemradio"]')[1]!.trigger('click')
  expect(bridge.switchTo).not.toHaveBeenCalled()

  await wrapper.find('.machine-chip-button').trigger('click')
  await flushPromises()
  expect(closeTopmost()).toBe(true) // Escape closes the newest open thing
  await flushPromises()
  expect(wrapper.find('.machine-menu').exists()).toBe(false)
  expect(document.activeElement).toBe(wrapper.find('.machine-chip-button').element)

  await wrapper.find('.machine-chip-button').trigger('click')
  await flushPromises()
  await wrapper.find('[role="menuitem"]').trigger('click')
  expect(bridge.add).toHaveBeenCalledTimes(1)
  wrapper.unmount()
})

describe('fitting on the middle of the bar', () => {
  // A 1000 px bar: logo side, chip on the middle, right controls.
  const rects: Record<string, { left: number; right: number; width: number }> = {}
  const rect = (left: number, width: number) => ({ left, right: left + width, width })
  let spy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    rects.bar = rect(0, 1000)
    rects.left = rect(20, 200)
    rects.right = rect(700, 280)
    rects.chip = rect(440, 120)
    spy = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const key = this.classList.contains('navbar-container') ? 'bar'
        : this.classList.contains('nav-left') ? 'left'
          : this.classList.contains('nav-right') ? 'right'
            : this.classList.contains('machine-chip-button') ? 'chip' : ''
      const r = rects[key] ?? rect(0, 0)
      return { ...r, top: 0, bottom: 30, height: 30, x: r.left, y: 0, toJSON() {} } as DOMRect
    })
  })
  afterEach(() => spy.mockRestore())

  async function renderInBar() {
    document.body.innerHTML = '<div class="navbar-container"><div class="nav-left"></div><div class="nav-center"><div id="slot"></div></div><div class="nav-right"></div></div>'
    const wrapper = mount(MachineChip, { attachTo: document.getElementById('slot')! })
    await flushPromises()
    return wrapper
  }

  it('shows the name when it fits, the dot when the controls come close, then nothing', async () => {
    bridge.machines = { current: 'abc', machines: [here, studio] }
    const wrapper = await renderInBar()
    const chip = () => wrapper.find('.machine-chip')
    expect(chip().attributes('data-mode')).toBe('full')

    // Controls start 70 px right of the middle: room for 116 px, not the 120 px chip.
    rects.right = rect(570, 410)
    ;(wrapper.vm as any).fit()
    await flushPromises()
    expect(chip().attributes('data-mode')).toBe('dot')

    rects.chip = rect(484, 32)
    rects.right = rect(510, 470) // room 16 px
    ;(wrapper.vm as any).fit()
    await flushPromises()
    expect(chip().attributes('data-mode')).toBe('hidden')
    expect(chip().attributes('aria-hidden')).toBe('true')

    // Room again: back to the full name (its width was remembered).
    rects.right = rect(700, 280)
    ;(wrapper.vm as any).fit()
    await flushPromises()
    expect(chip().attributes('data-mode')).toBe('full')
    wrapper.unmount()
  })
})

const GIB = 1024 ** 3
const usage = (cpu: number, usedGiB: number) => ({
  cpuPercent: cpu, memUsedBytes: usedGiB * GIB, memTotalBytes: 32 * GIB, cores: 10,
  eggifyCpuPercent: 3, eggifyRssBytes: 900 * 1024 * 1024, eggifyProcesses: 12,
})

it('shows the shown machine\'s CPU and memory next to its name, and every Mac\'s in the menu', async () => {
  bridge.machines = { current: 'abc', machines: [here, studio, { id: 'mini', name: 'Mini', hue: 30, here: false }] }
  bridge.usage = { '': usage(24, 18.4), abc: usage(95, 12) }
  const wrapper = await render()
  const meter = wrapper.get('.machine-chip-button [data-testid="machine-meter"]')
  expect(meter.text()).toBe('CPU 95% · 12.0 GB')
  expect(meter.classes()).toContain('usage-high')
  expect(meter.attributes('title')).toContain('Studio: CPU 95% of 10 cores · memory 12.0 GB of 32.0 GB')
  expect(meter.attributes('title')).toContain('Eggify and its agents: CPU 3% · 900 MB (12 processes)')

  await wrapper.get('.machine-chip-button').trigger('click')
  const rows = wrapper.findAll('.machine-item[role="menuitemradio"]')
  expect(rows[0].text()).toContain('This Mac')
  expect(rows[0].text()).toContain('CPU 24% · 18.4 GB')
  expect(rows[1].text()).toContain('CPU 95% · 12.0 GB')
  expect(rows[2].find('.machine-item-meter-none').exists()).toBe(true) // Mini did not answer
  wrapper.unmount()
})

it('shows the meter alone with only this Mac', async () => {
  bridge.machines = { current: '', machines: [here] }
  bridge.usage = { '': usage(12, 9) }
  const wrapper = await render()
  expect(wrapper.find('.machine-chip-button').exists()).toBe(false)
  expect(wrapper.get('.machine-meter-only').text()).toBe('CPU 12% · 9.0 GB')
  wrapper.unmount()
})

it('falls back to the page\'s own server on the web', async () => {
  bridge.shell = 'web'
  bridge.machines = null
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: true,
    json: async () => ({ available: true, cpu_percent: 40, mem_used_bytes: 4 * GIB, mem_total_bytes: 8 * GIB, cores: 4 }),
  })))
  const wrapper = await render()
  expect(wrapper.get('.machine-meter-only').text()).toBe('CPU 40% · 4.0 GB')
  wrapper.unmount()
})
