import { defineComponent, h, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import SessionTagPopover from '../../components/agents/SessionTagPopover.vue'
import { installShortcutDispatcher, useShortcut, __resetShortcuts, __setShortcutEnv } from '../../composables/useKeyboardShortcuts'
import { shortcutLabel } from '../../utils/shortcuts/catalog'
import {
  SESSION_TAGS_STORAGE_KEY, addSessionTags, knownSessionTags, readSessionTags, sessionTagList, sessionTagsVersion,
  splitTags, suggestSessionTags
} from '../../utils/sessionTags'

let uninstall: () => void
beforeEach(() => {
  __resetShortcuts()
  uninstall = installShortcutDispatcher(window)
  localStorage.setItem(SESSION_TAGS_STORAGE_KEY, JSON.stringify({ other1: 'frontend, bug', other2: 'Frontend', other3: 'release' }))
})
afterEach(() => {
  uninstall()
  __setShortcutEnv({})
  localStorage.clear()
  document.body.innerHTML = ''
})

const key = (init: KeyboardEventInit) => new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })

describe('session tag storage', () => {
  it('splits, trims and de-duplicates', () => {
    expect(splitTags(' a, b ,, A,c  d ')).toEqual(['a', 'b', 'c d'])
  })

  it('adds to the sidebar\'s tag value and bumps the shared version', () => {
    const v = sessionTagsVersion.value
    const storage = vi.fn()
    window.addEventListener('storage', storage)
    expect(addSessionTags('s1', ['wip'])).toEqual(['wip'])
    expect(addSessionTags('s1', ['frontend', 'WIP'])).toEqual(['wip', 'frontend'])
    expect(readSessionTags().s1).toBe('wip, frontend')
    expect(sessionTagsVersion.value).toBe(v + 2)
    expect(storage).toHaveBeenCalled() // the Zen name plate listens for this
    window.removeEventListener('storage', storage)
  })

  it('suggests known tags, most used first, prefix matches before substring ones', () => {
    const known = knownSessionTags()
    expect(known).toEqual(['frontend', 'bug', 'release'])
    expect(suggestSessionTags('', known, ['bug'])).toEqual(['frontend', 'release'])
    expect(suggestSessionTags('r', known)).toEqual(['release', 'frontend'])
    expect(suggestSessionTags('FRO', known)).toEqual(['frontend'])
    expect(suggestSessionTags('zzz', known)).toEqual([])
  })
})

describe('Add tag shortcut (⌘S)', () => {
  // A stand-in for the agents page: the shortcut opens the popover for the open session.
  function mountPage(sessionId: string | null = 's1') {
    const open = ref(false)
    const active = ref<string | null>(sessionId)
    const w = mount(defineComponent({
      setup() {
        useShortcut('session.tag', () => { open.value = true }, { when: () => !!active.value })
        return () => h('div', [
          h('textarea', { id: 'composer' }),
          h(SessionTagPopover, { open: open.value, 'onUpdate:open': (v: boolean) => { open.value = v }, sessionId: active.value, sessionName: 'My session' })
        ])
      }
    }), { attachTo: document.body })
    return { w, open }
  }

  it('is ⌘S in the desktop app and ⌥⌘S / Ctrl+Alt+S in a browser tab (⌘S saves the page there)', () => {
    expect(shortcutLabel('session.tag', 'mac', 'wails')).toBe('⌘S')
    expect(shortcutLabel('session.tag', 'mac', 'web')).toBe('⌥⌘S')
    expect(shortcutLabel('session.tag', 'other', 'web')).toBe('Ctrl+Alt+S')
    expect(shortcutLabel('session.tag', 'other', 'tauri')).toBe('Ctrl+S')
  })

  it('opens from the composer, adds tags with Enter and commas, autocompletes, and Escape closes', async () => {
    __setShortcutEnv({ platform: 'mac', shell: 'web' })
    const { w, open } = mountPage()
    const composer = document.getElementById('composer') as HTMLTextAreaElement
    composer.focus()
    const ev = key({ key: 'ß', code: 'KeyS', metaKey: true, altKey: true })
    composer.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true) // never the browser's Save dialog
    expect(open.value).toBe(true)
    await nextTick(); await nextTick()

    const input = document.querySelector('.tag-pop__input') as HTMLInputElement
    expect(input).toBeTruthy()
    expect(document.activeElement).toBe(input)
    // Autocomplete from tags on other sessions
    const options = () => [...document.querySelectorAll('.tag-pop__suggestion')].map(e => e.textContent?.trim())
    expect(options()).toEqual(['frontend', 'bug', 'release'])

    input.value = 'fro'
    input.dispatchEvent(new Event('input'))
    await nextTick()
    expect(options()).toEqual(['frontend'])
    input.dispatchEvent(key({ key: 'ArrowDown' }))
    input.dispatchEvent(key({ key: 'Enter' }))
    await nextTick()
    expect(sessionTagList('s1')).toEqual(['frontend'])

    input.value = 'urgent, later'
    input.dispatchEvent(new Event('input'))
    await nextTick()
    expect(sessionTagList('s1')).toEqual(['frontend', 'urgent']) // comma added the first one
    input.dispatchEvent(key({ key: 'Enter' }))
    await nextTick()
    expect(sessionTagList('s1')).toEqual(['frontend', 'urgent', 'later'])
    expect([...document.querySelectorAll('.tag-pop__chip')].length).toBe(3)

    // Escape closes it (Escape stack) and focus goes back to the composer
    window.dispatchEvent(key({ key: 'Escape' }))
    expect(open.value).toBe(false)
    await nextTick(); await nextTick()
    expect(document.activeElement).toBe(composer)
    w.unmount()
  })

  it('removes a tag with its × and does nothing without an open session', async () => {
    addSessionTags('s1', ['a', 'b'])
    __setShortcutEnv({ platform: 'mac', shell: 'web' })
    const { w, open } = mountPage()
    window.dispatchEvent(key({ key: 'ß', code: 'KeyS', metaKey: true, altKey: true }))
    await nextTick(); await nextTick()
    ;(document.querySelector('.tag-pop__chip-x') as HTMLButtonElement).click()
    expect(sessionTagList('s1')).toEqual(['b'])
    open.value = false
    w.unmount()

    const page = mountPage(null)
    const ev = key({ key: 'ß', code: 'KeyS', metaKey: true, altKey: true })
    window.dispatchEvent(ev)
    expect(page.open.value).toBe(false)
    expect(ev.defaultPrevented).toBe(false)
    page.w.unmount()
  })

  it('is left to the native menu item in the desktop app', () => {
    __setShortcutEnv({ platform: 'mac', shell: 'wails' })
    const { w, open } = mountPage()
    const ev = key({ key: 's', code: 'KeyS', metaKey: true })
    window.dispatchEvent(ev)
    expect(open.value).toBe(false)
    w.unmount()
  })
})
