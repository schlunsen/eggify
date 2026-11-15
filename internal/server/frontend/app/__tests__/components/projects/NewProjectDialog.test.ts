import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import NewProjectDialog from '../../../components/projects/NewProjectDialog.vue'

type Handler = (url: string, init?: RequestInit) => { status?: number, body: any }

function json(status: number, body: any): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body, clone() { return this } } as unknown as Response
}

/** Stubs useAuthenticatedFetch with a URL-prefix router; records every call. */
function stubApi(routes: Record<string, Handler>) {
  const calls: { url: string, init?: RequestInit }[] = []
  const fetchWithAuth = vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, init })
    const method = (init?.method || 'GET').toUpperCase()
    const key = Object.keys(routes).find(k => {
      const [m, prefix] = k.split(' ')
      return m === method && url.startsWith(prefix!)
    })
    if (!key) return json(404, { error: `no route for ${method} ${url}` })
    const r = routes[key]!(url, init)
    return json(r.status ?? 200, r.body)
  })
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth }))
  return { calls, fetchWithAuth }
}

const defaults = { home: '/home/me', projects_dir: '/home/me/projects', projects_dir_display: '~/projects', git_available: true }

function inspectFor(map: Record<string, any>): Handler {
  return (url) => {
    const path = decodeURIComponent(url.split('path=')[1] || '')
    return { body: map[path] ?? { path, display_path: path, suggested_name: path.split('/').pop(), exists: false, is_dir: false, can_create: true } }
  }
}

let wrapper: VueWrapper | null = null
async function mountDialog(props: Record<string, any> = {}) {
  wrapper = mount(NewProjectDialog, { props: { open: true, ...props }, attachTo: document.body })
  await flushPromises()
  return wrapper
}
async function settle() {
  await vi.advanceTimersByTimeAsync(400)
  await flushPromises()
}
async function type(w: VueWrapper, selector: string, value: string) {
  const input = w.get(selector)
  await input.setValue(value)
  await input.trigger('input')
  await settle()
}

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('NewProjectDialog', () => {
  it('offers three starting points and focuses the first', async () => {
    stubApi({ 'GET /api/projects/setup/defaults': () => ({ body: defaults }) })
    const w = await mountDialog()
    const options = w.findAll('.np-option')
    expect(options.map(o => o.attributes('data-mode'))).toEqual(['existing', 'clone', 'empty'])
    expect(document.activeElement).toBe(options[0]!.element)
    await options[0]!.trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement).toBe(options[1]!.element)
    expect(w.text()).toContain('~/projects')
  })

  it('adds an existing folder: inspects it, derives the name, Enter submits', async () => {
    const api = stubApi({
      'GET /api/projects/setup/defaults': () => ({ body: defaults }),
      'GET /api/projects/setup/complete': () => ({ body: { suggestions: [] } }),
      'GET /api/projects/setup/inspect': inspectFor({
        '~/code/site': { path: '/home/me/code/site', display_path: '~/code/site', suggested_name: 'site', exists: true, is_dir: true, writable: true, git: { is_repo: true, branch: 'main', dirty: true, remote: 'https://github.com/me/site.git' } },
      }),
      'POST /api/projects/setup': () => ({ status: 201, body: { project: { id: 'p1', name: 'site', path: '/home/me/code/site', color: '#ef4444' } } }),
    })
    const w = await mountDialog()
    await w.get('[data-mode="existing"]').trigger('click')
    await flushPromises()
    await type(w, 'input[role="combobox"]', '~/code/site')
    expect(w.text()).toContain('Git repository on main')
    expect(w.text()).toContain('uncommitted changes')
    const name = w.get<HTMLInputElement>('input[placeholder="site"], input[id^="np-name"]')
    expect((name.element as HTMLInputElement).value).toBe('site')

    await w.get('form').trigger('submit')
    await flushPromises()
    const post = api.calls.find(c => c.init?.method === 'POST')!
    expect(JSON.parse(post.init!.body as string)).toMatchObject({ mode: 'existing', path: '~/code/site', name: 'site' })
    expect(w.emitted('created')?.[0]?.[0]).toMatchObject({ id: 'p1' })
    expect(w.text()).toContain('Project ready')
    await w.get('.np-done .np-btn--primary').trigger('click')
    expect(w.emitted('start-session')?.[0]?.[0]).toMatchObject({ id: 'p1' })
  })

  it('blocks a missing folder and offers to open an existing project', async () => {
    const api = stubApi({
      'GET /api/projects/setup/defaults': () => ({ body: defaults }),
      'GET /api/projects/setup/complete': () => ({ body: { suggestions: [] } }),
      'GET /api/projects/setup/inspect': inspectFor({
        '~/known': { path: '/home/me/known', display_path: '~/known', suggested_name: 'known', exists: true, is_dir: true, existing_project: { id: 'k', name: 'Known', path: '/home/me/known' } },
      }),
    })
    const w = await mountDialog({ initialMode: 'existing' })
    await type(w, 'input[role="combobox"]', '~/nope')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain('No folder at this path')
    expect(api.calls.some(c => c.init?.method === 'POST')).toBe(false)

    await type(w, 'input[role="combobox"]', '~/known')
    expect(w.text()).toContain('Already a project: Known')
    await w.get('.np-status .np-link').trigger('click')
    expect(w.emitted('open-existing')?.[0]?.[0]).toMatchObject({ id: 'k' })
    // Enter on an already-registered folder opens it instead of failing.
    expect(w.get('button[type="submit"]').text()).toBe('Open project')
    await w.get('form').trigger('submit')
    expect(w.emitted('open-existing')).toHaveLength(2)
    expect(api.calls.some(c => c.init?.method === 'POST')).toBe(false)
  })

  it('clones a repository with progress', async () => {
    let polls = 0
    const api = stubApi({
      'GET /api/projects/setup/defaults': () => ({ body: defaults }),
      'GET /api/projects/setup/inspect': inspectFor({}),
      'POST /api/projects/setup': () => ({ status: 202, body: { job: { id: 'j1', status: 'running', stage: 'Starting', percent: 0, output: [] } } }),
      'GET /api/projects/setup/jobs/j1': () => {
        polls++
        return polls < 2
          ? { body: { job: { id: 'j1', status: 'running', stage: 'Receiving objects', percent: 40, output: [] } } }
          : { body: { job: { id: 'j1', status: 'done', stage: 'Done', percent: 100, output: [], project: { id: 'c1', name: 'Hello-World', path: '/home/me/projects/Hello-World' } } } }
      },
    })
    const w = await mountDialog({ initialMode: 'clone' })
    await type(w, 'input[placeholder^="https://github.com/owner/repo"]', 'octocat/Hello-World')
    expect((w.get('input[aria-label="Folder name"]').element as HTMLInputElement).value).toBe('Hello-World')
    expect(w.text()).toContain('Will create ~/projects/Hello-World')

    await w.get('form').trigger('submit')
    await flushPromises()
    expect(JSON.parse(api.calls.find(c => c.init?.method === 'POST')!.init!.body as string))
      .toMatchObject({ mode: 'clone', source: 'octocat/Hello-World', parent: '~/projects', folder: 'Hello-World', name: 'Hello-World' })
    expect(w.text()).toContain('Receiving objects')
    // Esc does not close the dialog mid-clone.
    await w.get('.np-dialog').trigger('keydown', { key: 'Escape' })
    expect(w.emitted('close')).toBeUndefined()
    await settle()
    await settle()
    expect(w.text()).toContain('Project ready')
    expect(w.emitted('created')?.[0]?.[0]).toMatchObject({ id: 'c1' })
  })

  it('shows clone errors inline', async () => {
    stubApi({
      'GET /api/projects/setup/defaults': () => ({ body: defaults }),
      'GET /api/projects/setup/inspect': inspectFor({}),
      'POST /api/projects/setup': () => ({ status: 202, body: { job: { id: 'j2', status: 'running', stage: 'Starting', percent: 0, output: [] } } }),
      'GET /api/projects/setup/jobs/j2': () => ({ body: { job: { id: 'j2', status: 'error', stage: 'Starting', percent: 0, output: [], error: "fatal: repository 'x' not found" } } }),
    })
    const w = await mountDialog({ initialMode: 'clone' })
    await type(w, 'input[placeholder^="https://github.com/owner/repo"]', 'a/missing')
    await w.get('form').trigger('submit')
    await settle()
    expect(w.text()).toContain('Clone failed')
    expect(w.text()).toContain('not found')
    expect(w.find('.np-done').exists()).toBe(false)
  })

  it('rejects non-HTTPS clone URLs before calling the server', async () => {
    const api = stubApi({
      'GET /api/projects/setup/defaults': () => ({ body: defaults }),
      'GET /api/projects/setup/inspect': inspectFor({}),
    })
    const w = await mountDialog({ initialMode: 'clone' })
    await type(w, 'input[placeholder^="https://github.com/owner/repo"]', 'http://example.com/a/b')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain('Only HTTPS URLs can be cloned')
    expect(api.calls.some(c => c.init?.method === 'POST')).toBe(false)
  })

  it('creates an empty project with a derived folder and git init', async () => {
    const api = stubApi({
      'GET /api/projects/setup/defaults': () => ({ body: defaults }),
      'GET /api/projects/setup/inspect': inspectFor({
        '~/projects/taken': { path: '/home/me/projects/taken', display_path: '~/projects/taken', suggested_name: 'taken', exists: true, is_dir: true },
      }),
      'POST /api/projects/setup': () => ({ status: 201, body: { project: { id: 'e1', name: 'My App', path: '/home/me/projects/my-app' } } }),
    })
    const w = await mountDialog({ initialMode: 'empty' })
    await type(w, 'input[placeholder="My new project"]', 'Taken')
    expect(w.text()).toContain('~/projects/taken already exists')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.calls.some(c => c.init?.method === 'POST')).toBe(false)

    await type(w, 'input[placeholder="My new project"]', 'My App')
    expect((w.get('input[aria-label="Folder name"]').element as HTMLInputElement).value).toBe('my-app')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(JSON.parse(api.calls.find(c => c.init?.method === 'POST')!.init!.body as string))
      .toMatchObject({ mode: 'empty', name: 'My App', folder: 'my-app', parent: '~/projects', git_init: true })
    expect(w.text()).toContain('Project ready')
  })

  it('closes on Escape and explains sign-in refusals', async () => {
    stubApi({
      'GET /api/projects/setup/defaults': () => ({ status: 403, body: { code: 'user_required', error: 'x requires a signed-in user' } }),
      'GET /api/projects/setup/inspect': inspectFor({}),
      'POST /api/projects/setup': () => ({ status: 403, body: { code: 'user_required', error: 'x requires a signed-in user' } }),
    })
    const w = await mountDialog({ initialMode: 'empty' })
    await type(w, 'input[placeholder="My new project"]', 'Thing')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain('Sign in to Eggify')
    await w.get('.np-dialog').trigger('keydown', { key: 'Escape' })
    expect(w.emitted('close')).toHaveLength(1)
  })
})
