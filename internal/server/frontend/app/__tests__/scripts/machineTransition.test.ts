import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Exercise the injected script's navigation lifecycle without a native window.
const src = readFileSync(resolve(process.cwd(), '../../../cmd/eggify-desktop/machine_transition.js'), 'utf8')
const ID = '__eggify_machine_switch'
const overlay = () => document.getElementById(ID) as HTMLElement | null
const part = (p: string) => overlay()!.querySelector(`[data-part="${p}"]`) as HTMLElement
const run = (opts: Record<string, unknown>) => (0, eval)('(' + src + ')(' + JSON.stringify({ name: 'Studio', hue: 210, seq: 1, start: Date.now(), ...opts }) + ');')
const wait = (ms: number) => vi.advanceTimersByTimeAsync(ms)
let posts: string[], reduce: boolean
let anims: { el: Element, frames: any[], opt: any, animation: any }[]
const originalAnimate = Element.prototype.animate
const originalGetAnimations = Element.prototype.getAnimations
const revealed = () => posts.some(p => p.startsWith('eggify-machine:reveal '))

beforeEach(() => {
  vi.useFakeTimers()
  document.body.innerHTML = '<div id="__nuxt"><p>dashboard</p></div>'
  overlay()?.remove()
  history.replaceState(null, '', '/agents')
  posts = []; anims = []; reduce = false
  vi.stubGlobal('webkit', { messageHandlers: { external: { postMessage: (m: string) => posts.push(m) } } })
  vi.stubGlobal('matchMedia', () => ({ matches: reduce }))
  vi.stubGlobal('requestAnimationFrame', (cb: () => void) => setTimeout(cb, 16))
  vi.spyOn(document, 'readyState', 'get').mockReturnValue('complete')
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => { throw new Error('No canvas required') })
  Element.prototype.animate = vi.fn(function (this: Element, frames: any, opt: any) {
    const animation: any = { onfinish: null, cancel: () => clearTimeout(timer) }
    const timer = setTimeout(() => animation.onfinish?.(), opt.duration || 0)
    anims.push({ el: this, frames, opt, animation })
    return animation
  })
  Element.prototype.getAnimations = vi.fn(function (this: Element) {
    return anims.filter(a => this === a.el || this.contains(a.el)).map(a => a.animation)
  })
})
afterEach(() => {
  vi.clearAllTimers(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals()
  Element.prototype.animate = originalAnimate
  Element.prototype.getAnimations = originalGetAnimations
  overlay()?.remove()
})

describe('machine transition fade', () => {
  it('identifies remote and local instances and remembers the page left', () => {
    history.replaceState(null, '', '/agents?s=1')
    run({ phase: 'out', seq: 4 })
    expect(part('cap').textContent).toBe('Remote instance')
    expect(part('name').textContent).toBe('Studio')
    expect(posts).toContain('eggify-machine:left 4 ' + encodeURIComponent('/agents?s=1'))
    run({ phase: 'out', here: true, name: 'This Mac', seq: 5 })
    expect(part('cap').textContent).toBe('Local instance')
    expect(document.querySelectorAll('#' + ID)).toHaveLength(1)
    expect(HTMLCanvasElement.prototype.getContext).not.toHaveBeenCalled()
  })
  it('animates only opacity, including delayed loading feedback', async () => {
    run({ phase: 'out', start: Date.now() - 6000 })
    await wait(20)
    run({ phase: 'in' })
    await wait(1000)
    expect(anims.length).toBeGreaterThan(2)
    for (const a of anims) for (const f of a.frames) expect(Object.keys(f)).toEqual(['opacity'])
    expect(anims.some(a => a.el === document.body)).toBe(false)
    expect(HTMLCanvasElement.prototype.getContext).not.toHaveBeenCalled()
    expect(overlay()).toBeNull()
  })
  it('reveals a warm dashboard after two frames without an artificial hold', async () => {
    run({ phase: 'in' })
    expect(overlay()!.style.opacity).toBe('1')
    await wait(16)
    expect(revealed()).toBe(false)
    await wait(16)
    expect(revealed()).toBe(true)
    await wait(200)
    expect(overlay()).toBeNull()
  })
  it('waits beyond 1.5 seconds for a cold dashboard, then fades it in', async () => {
    document.getElementById('__nuxt')!.innerHTML = ''
    run({ phase: 'in' })
    await wait(2000)
    expect(revealed()).toBe(false)
    document.getElementById('__nuxt')!.innerHTML = '<main>Ready</main>'
    await wait(300)
    expect(revealed()).toBe(true)
    expect(overlay()).toBeNull()
  })
  it('eventually exposes a failed mount rather than trapping the user', async () => {
    document.getElementById('__nuxt')!.innerHTML = ''
    run({ phase: 'in' })
    await wait(10500)
    expect(posts).toContain('eggify-machine:ready 1 -1')
    expect(overlay()).toBeNull()
  })
  it.each([
    { path: '/__desktop/machine-bootstrap?t=x', hold: false },
    { path: '/agents', hold: true }
  ])('keeps bootstrap or superseded navigation covered: %o', async ({ path, hold }) => {
    history.replaceState(null, '', path)
    run({ phase: 'in', hold })
    await wait(11000)
    expect(revealed()).toBe(false)
    expect(overlay()).not.toBeNull()
  })
  it('offers a return to the previous instance after a slow connection', async () => {
    run({ phase: 'out', seq: 7, from: 'This Mac' })
    await wait(200)
    expect(anims.some(a => (a.el as HTMLElement).dataset.part === 'loading')).toBe(false)
    await wait(4800)
    expect(part('slow').style.display).toBe('flex')
    expect(part('stay').textContent).toBe('Stay on This Mac')
    expect(overlay()!.hasAttribute('aria-hidden')).toBe(false)
    part('stay').click()
    expect(posts).toContain('eggify-machine:cancel 7 0')
  })
  it.each([16, 50, 150])('an interrupted reveal cannot remove a newer cover at %d ms', async (ms) => {
    run({ phase: 'in' })
    await wait(ms)
    run({ phase: 'out', seq: 2, name: 'Other Mac' })
    await wait(1200)
    expect(overlay()).not.toBeNull()
    expect(part('name').textContent).toBe('Other Mac')
    if (ms === 16) expect(revealed()).toBe(false)
  })
  it('uses shorter fades for reduced motion', async () => {
    reduce = true
    run({ phase: 'out' })
    expect(anims[0].opt.duration).toBe(80)
    run({ phase: 'in' })
    await wait(32)
    expect(anims.at(-1)!.opt.duration).toBe(100)
    await wait(100)
    expect(overlay()).toBeNull()
  })
  it('cleans up when animation completion and painted frames never arrive', async () => {
    vi.stubGlobal('requestAnimationFrame', () => 0)
    run({ phase: 'in' })
    await wait(120)
    expect(revealed()).toBe(true)
    anims.at(-1)!.animation.onfinish = null
    await wait(800)
    expect(overlay()).toBeNull()
  })
})
