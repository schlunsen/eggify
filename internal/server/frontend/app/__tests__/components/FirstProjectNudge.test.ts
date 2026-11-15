import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import FirstProjectNudge from '../../components/agents/FirstProjectNudge.vue'
import { useFirstProjectNudge } from '../../composables/useFirstProjectNudge'
import { PROJECT_START_OPTIONS } from '../../utils/projects/projectSetup'

describe('FirstProjectNudge', () => {
  it('renders the three starting points with the dialog titles', () => {
    const w = mount(FirstProjectNudge)
    const buttons = w.findAll('[data-mode]')
    expect(buttons.map(b => b.attributes('data-mode'))).toEqual(['existing', 'clone', 'empty'])
    PROJECT_START_OPTIONS.forEach((o, i) => {
      expect(buttons[i]!.text()).toContain(o.title)
      expect(buttons[i]!.text()).toContain(o.desc)
    })
  })

  it('emits choose with the mode and dismiss for Not now', async () => {
    const w = mount(FirstProjectNudge)
    await w.find('[data-mode="clone"]').trigger('click')
    await w.find('[data-test="fpn-dismiss"]').trigger('click')
    expect(w.emitted('choose')).toEqual([['clone']])
    expect(w.emitted('dismiss')).toHaveLength(1)
  })
})

describe('useFirstProjectNudge', () => {
  let projects: any[] | null
  const push = vi.fn()

  beforeEach(() => {
    projects = []
    sessionsLoaded.value = true
    sessionCount.value = 0
    push.mockClear()
    localStorage.clear()
    vi.stubGlobal('useRouter', () => ({ push }))
    vi.stubGlobal('useAuthenticatedFetch', () => ({
      fetchWithAuth: vi.fn(async () => projects === null
        ? { ok: false, status: 500, json: async () => ({}) }
        : { ok: true, status: 200, json: async () => ({ projects }) }),
    }))
  })
  afterEach(() => { vi.unstubAllGlobals(); localStorage.clear() })

  const sessionsLoaded = ref(true)
  const sessionCount = ref(0)
  function host() {
    let api!: ReturnType<typeof useFirstProjectNudge>
    const w = mount({ template: '<div/>', setup() { api = useFirstProjectNudge({ loaded: sessionsLoaded, count: sessionCount }); return {} } })
    return { api, w }
  }

  it('is hidden while loading, shows once an empty list arrives', async () => {
    const { api } = host()
    expect(api.show.value).toBe(false)
    await flushPromises()
    expect(api.show.value).toBe(true)
  })

  it('stays hidden when projects exist or the request fails', async () => {
    projects = [{ id: 'a' }]
    const a = host()
    await flushPromises()
    expect(a.api.show.value).toBe(false)
    projects = null
    const b = host()
    await flushPromises()
    expect(b.api.show.value).toBe(false)
  })

  it('disappears when a project appears after the page regains focus', async () => {
    const { api } = host()
    await flushPromises()
    expect(api.show.value).toBe(true)
    projects = [{ id: 'new' }]
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(api.show.value).toBe(false)
  })

  it('waits for the session list and hides for users with sessions', async () => {
    sessionsLoaded.value = false
    const { api } = host()
    await flushPromises()
    expect(api.show.value).toBe(false)
    sessionsLoaded.value = true
    expect(api.show.value).toBe(true)
    sessionCount.value = 1
    expect(api.show.value).toBe(false)
  })

  it('keeps the last good answer when a re-check fails', async () => {
    const { api } = host()
    await flushPromises()
    projects = null
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(api.show.value).toBe(true)
  })

  it('focus and visibilitychange together make one request', async () => {
    const fetchWithAuth = vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ projects: [] }) }))
    vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth }))
    host()
    await flushPromises()
    fetchWithAuth.mockClear()
    window.dispatchEvent(new Event('focus'))
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(fetchWithAuth).toHaveBeenCalledTimes(1)
  })

  it('Not now hides it and is remembered', async () => {
    const { api } = host()
    await flushPromises()
    api.dismiss()
    expect(api.show.value).toBe(false)
    expect(localStorage.getItem('eggify-first-project-nudge-dismissed')).toBe('1')
    expect(host().api.show.value).toBe(false)
    await flushPromises()
  })

  it('dismissing survives a throwing localStorage', async () => {
    const { api } = host()
    await flushPromises()
    vi.stubGlobal('localStorage', { getItem: () => { throw new Error('x') }, setItem: () => { throw new Error('x') } })
    expect(() => api.dismiss()).not.toThrow()
    expect(api.show.value).toBe(false)
  })

  it('choose goes to /projects?new=<mode>', () => {
    const { api } = host()
    api.choose('empty')
    expect(push).toHaveBeenCalledWith({ path: '/projects', query: { new: 'empty' } })
  })
})
