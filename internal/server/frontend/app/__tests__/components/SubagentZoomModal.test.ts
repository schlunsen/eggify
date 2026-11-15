import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import SubagentsPip from '../../components/agents/SubagentsPip.vue'
import { useBackgroundAgents } from '../../composables/agents/useBackgroundAgents'
import { __resetShortcuts, __setShortcutEnv, installShortcutDispatcher } from '../../composables/useKeyboardShortcuts'

const auth = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => auth }))

const session = 'zoom-session'
const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString()
const agent = (id: string, description: string, msAgo: number, done: boolean, lines: string[] = []) => ({
  agent_id: id, parent_session_id: session, subagent_type: 'general-purpose', description, progress: done ? 1 : 0,
  status: done ? 'completed' : 'running', last_output: lines.at(-1) || '', output_lines: lines.length,
  created_at: iso(msAgo + 60_000), updated_at: iso(msAgo), completed_at: done ? iso(msAgo) : undefined, recent_output: lines
})
// PiP order: running oldest first, then recently finished; the one that ended
// 10 minutes ago is not in the card but can still be stepped to.
const subagents = [
  agent('d-old', 'Old report', 10 * 60_000, true, ['Old findings.']),
  agent('r2', 'Second runner', 1_000, false, ['TOOL:Bash: make']),
  agent('d1', 'Fresh report', 5_000, true, ['Fresh findings.']),
  agent('r1', 'First runner', 2_000, false, ['TOOL:Read: /tmp/a.go'])
]
subagents[3]!.created_at = iso(5 * 60_000) // r1 started first

let uninstall: () => void
let wrapper: VueWrapper | null = null
beforeEach(() => {
  try { localStorage.clear() } catch { /* ignore */ }
  // No element.animate unless a test adds one: animations finish at once.
  ;(HTMLElement.prototype as any).animate = undefined
  __resetShortcuts()
  __setShortcutEnv({ platform: 'mac', shell: 'wails' })
  uninstall = installShortcutDispatcher(window)
  useBackgroundAgents().clearSessionAgents(session)
  useBackgroundAgents().resetListTracking()
  auth.fetchWithAuth.mockImplementation(async () => ({ ok: true, json: async () => ({ subagents: structuredClone(subagents) }) }) as Response)
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  uninstall()
  __setShortcutEnv({})
  vi.restoreAllMocks()
  delete (HTMLElement.prototype as any).animate
})

const nextFrame = () => new Promise(resolve => requestAnimationFrame(() => resolve(null)))
async function mountPip() {
  // Real <Transition>s: their hooks run the zoom and slide animations.
  wrapper = mount(SubagentsPip, { props: { sessionId: session, show: true }, attachTo: document.body, global: { stubs: { transition: false } } })
  await flushPromises()
  await nextFrame()
  return wrapper
}
const modal = () => document.body.querySelector<HTMLElement>('.szm')
const title = () => document.body.querySelector('.szm-title')?.textContent
const position = () => document.body.querySelector('.szm-pos')?.textContent
const press = async (init: KeyboardEventInit, target: EventTarget = document.activeElement || document.body) => {
  target.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }))
  await flushPromises()
}
const click = async (selector: string) => {
  document.body.querySelector<HTMLElement>(selector)!.click()
  await flushPromises()
}
async function openRow(name: string) {
  const w = await mountPip()
  const row = w.findAll('.pip-row').find(r => r.get('.pip-name').text() === name)!
  await row.trigger('click')
  await flushPromises()
  return { w, row: row.element as HTMLElement }
}

it('opens a modal for the clicked subagent, not the full view', async () => {
  const { w } = await openRow('Fresh report')
  expect(modal()).not.toBeNull()
  expect(title()).toBe('Fresh report')
  expect(modal()!.querySelector('.sd')?.getAttribute('data-agent-id')).toBe('d1')
  expect(modal()!.querySelector('.sd-head')).toBeNull() // the modal's header shows title and status
  expect(document.body.querySelector('.szm-pill')?.textContent).toContain('Done')
  expect(w.emitted('open-agent')).toBeUndefined()
})

it('closes with ×, with Escape and by clicking outside, and gives focus back to the row', async () => {
  const { row } = await openRow('First runner')
  expect(modal()!.contains(document.activeElement)).toBe(true)
  await click('[data-testid="szm-close"]')
  expect(modal()).toBeNull()
  expect(document.activeElement).toBe(row)

  row.click(); await flushPromises()
  expect(modal()).not.toBeNull()
  await press({ key: 'Escape' })
  expect(modal()).toBeNull()

  row.click(); await flushPromises()
  await click('[data-testid="szm-backdrop"]')
  expect(modal()).toBeNull()
})

it('"Open in Subagents view" closes it and asks for the full view on that agent', async () => {
  const { w } = await openRow('Second runner')
  await click('[data-testid="szm-open-view"]')
  expect(modal()).toBeNull()
  expect(w.emitted('open-agent')).toEqual([['r2']])
})

it('steps through every subagent in the card’s order with ‹ ›, and stops at the ends', async () => {
  await openRow('First runner')
  expect(position()).toBe('1 of 4')
  const prev = () => document.body.querySelector<HTMLButtonElement>('[data-testid="szm-prev"]')!
  const next = () => document.body.querySelector<HTMLButtonElement>('[data-testid="szm-next"]')!
  expect(prev().disabled).toBe(true)

  const seen = [title()]
  for (let i = 0; i < 3; i++) { await click('[data-testid="szm-next"]'); seen.push(title()) }
  // Running oldest first, then the fresh report, then the one no longer in the card.
  expect(seen).toEqual(['First runner', 'Second runner', 'Fresh report', 'Old report'])
  expect(position()).toBe('4 of 4')
  expect(next().disabled).toBe(true)
  await click('[data-testid="szm-next"]')
  expect(title()).toBe('Old report')

  await click('[data-testid="szm-prev"]')
  expect(title()).toBe('Fresh report')
})

it('switches with ←/→, J/K and Home/End, but not while typing', async () => {
  await openRow('Second runner')
  await press({ key: 'ArrowRight', code: 'ArrowRight' })
  expect(title()).toBe('Fresh report')
  await press({ key: 'ArrowLeft', code: 'ArrowLeft' })
  expect(title()).toBe('Second runner')
  await press({ key: 'j', code: 'KeyJ' })
  expect(title()).toBe('Fresh report')
  await press({ key: 'k', code: 'KeyK' })
  expect(title()).toBe('Second runner')
  await press({ key: 'End', code: 'End' })
  expect(title()).toBe('Old report')
  await press({ key: 'Home', code: 'Home' })
  expect(title()).toBe('First runner')
  await press({ key: 'ArrowLeft', code: 'ArrowLeft' }) // already first
  expect(title()).toBe('First runner')

  const input = document.createElement('input')
  modal()!.appendChild(input)
  input.focus()
  await press({ key: 'ArrowRight', code: 'ArrowRight' }, input)
  await press({ key: 'j', code: 'KeyJ' }, input)
  expect(title()).toBe('First runner')
})

it('zooms out of the row and slides the content in the direction of travel', async () => {
  const calls: Array<{ el: Element; frames: Keyframe[] }> = []
  ;(HTMLElement.prototype as any).animate = function (frames: Keyframe[]) {
    calls.push({ el: this, frames })
    return { cancel() {}, set onfinish(fn: () => void) { fn() }, set oncancel(_: () => void) {} }
  }
  const w = await mountPip()
  const row = w.findAll('.pip-row')[0]!
  vi.spyOn(row.element, 'getBoundingClientRect').mockReturnValue({ left: 900, top: 60, width: 300, height: 40, right: 1200, bottom: 100, x: 900, y: 60, toJSON() {} } as DOMRect)
  await row.trigger('click')
  await flushPromises()
  const zoom = calls.find(c => (c.el as HTMLElement).classList.contains('szm'))!
  expect(String(zoom.frames[0]!.transform)).toMatch(/^translate\(.+\) scale\(.+\)$/)
  expect(zoom.frames.at(-1)!.transform).toBe('none')

  calls.length = 0
  await click('[data-testid="szm-next"]')
  const slideIn = calls.find(c => String(c.frames[0]!.transform).startsWith('translateX(24px)'))
  expect(slideIn, 'next comes in from the right').toBeTruthy()
  calls.length = 0
  await click('[data-testid="szm-prev"]')
  expect(calls.some(c => String(c.frames[0]!.transform).startsWith('translateX(-24px)'))).toBe(true)
})

it('only fades with reduced motion, and switches without sliding', async () => {
  vi.spyOn(window, 'matchMedia').mockImplementation((q: string) => ({ matches: q.includes('reduce'), media: q, addEventListener() {}, removeEventListener() {} }) as any)
  const calls: Array<{ el: Element; frames: Keyframe[] }> = []
  ;(HTMLElement.prototype as any).animate = function (frames: Keyframe[]) {
    calls.push({ el: this, frames })
    return { cancel() {}, set onfinish(fn: () => void) { fn() }, set oncancel(_: () => void) {} }
  }
  const w = await mountPip()
  const row = w.findAll('.pip-row')[0]!
  vi.spyOn(row.element, 'getBoundingClientRect').mockReturnValue({ left: 900, top: 60, width: 300, height: 40, right: 1200, bottom: 100, x: 900, y: 60, toJSON() {} } as DOMRect)
  await row.trigger('click')
  await flushPromises()
  expect(calls.length).toBeGreaterThan(0)
  expect(calls.every(c => c.frames.every(f => f.transform === undefined))).toBe(true)

  calls.length = 0
  await click('[data-testid="szm-next"]')
  expect(title()).toBe('Second runner')
  expect(calls).toEqual([])
})

it('traps Tab inside the modal', async () => {
  await openRow('First runner')
  const buttons = [...modal()!.querySelectorAll<HTMLElement>('button:not([disabled])')]
  buttons.at(-1)!.focus()
  await press({ key: 'Tab' })
  expect(document.activeElement).toBe(buttons[0])
  await press({ key: 'Tab', shiftKey: true })
  expect(document.activeElement).toBe(buttons.at(-1))
})

it('stays open showing the result when the open agent is removed, and has no switching then', async () => {
  await openRow('Fresh report')
  useBackgroundAgents().clearSessionAgents(session)
  await flushPromises()
  await nextFrame()
  expect(modal()).not.toBeNull()
  expect(title()).toBe('Fresh report')
  expect(document.body.querySelector('[data-testid="szm-removed"]')).not.toBeNull()
  expect(document.body.querySelector('.szm-nav')).toBeNull()
})

it('with one subagent shows "1 of 1" and both arrows are disabled', async () => {
  auth.fetchWithAuth.mockImplementation(async () => ({ ok: true, json: async () => ({ subagents: [structuredClone(subagents[2])] }) }) as Response)
  await openRow('Fresh report')
  expect(position()).toBe('1 of 1')
  expect(document.body.querySelector<HTMLButtonElement>('[data-testid="szm-prev"]')!.disabled).toBe(true)
  expect(document.body.querySelector<HTMLButtonElement>('[data-testid="szm-next"]')!.disabled).toBe(true)
  await press({ key: 'ArrowRight', code: 'ArrowRight' })
  expect(title()).toBe('Fresh report')
})
