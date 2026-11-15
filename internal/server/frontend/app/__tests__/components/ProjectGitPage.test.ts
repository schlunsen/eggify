import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import GitPage from '../../pages/projects/[id]/git.vue'

function json(status: number, body: any): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as unknown as Response
}

const wt = (over: any) => ({ head: 'abc1234', is_main: false, locked: false, prunable: false, missing: false, ahead: 0, behind: 0, base_ahead: 0, base_behind: 0, dirty: { staged: 0, unstaged: 0, untracked: 0, conflicted: 0 }, ...over })
const overview = {
  root: '/p', current_branch: 'main', default_branch: 'main', remote_web_url: 'https://github.com/o/r',
  worktrees: [
    wt({ path: '/p', branch: 'main', is_main: true }),
    wt({ path: '/p/.worktrees/cart', branch: 'cart', base_ahead: 2, dirty: { staged: 0, unstaged: 1, untracked: 0, conflicted: 0 } }),
  ],
  branches: [
    { name: 'main', head: 'a', subject: 'init', author: 'Ada', at: 1, ahead: 0, behind: 0, base_ahead: 0, base_behind: 0, merged: false, current: true, is_default: true },
    { name: 'cart', head: 'b', subject: 'add cart', author: 'Ada', at: 2, ahead: 0, behind: 0, base_ahead: 2, base_behind: 0, merged: false, current: false, is_default: false, worktree: '/p/.worktrees/cart' },
    { name: 'old', head: 'c', subject: 'old work', author: 'Ada', at: 0, ahead: 0, behind: 0, base_ahead: 0, base_behind: 3, merged: true, current: false, is_default: false },
  ],
  recent_commits: [],
}
const uncommitted = { scope: 'uncommitted', stats: { files_changed: 1, additions: 1, deletions: 0 }, files: [{ path: 'cart.ts', status: 'modified', additions: 1, deletions: 0, diff: '@@ -1 +1,2 @@\n a\n+b' }] }
const branch = { scope: 'branch', base: 'main', ref: 'cart', stats: { files_changed: 1, additions: 3, deletions: 0 }, files: [{ path: 'cart.go', status: 'added', additions: 3, deletions: 0, diff: '@@ -0,0 +1,3 @@\n+a\n+b\n+c' }], commits: [{ sha: 'b'.repeat(40), short: 'bbbbbbb', subject: 'add cart', author: 'Ada', at: 2 }] }

let wrapper: VueWrapper | null = null
async function mountPage(url: string, repo = true) {
  const fetchWithAuth = vi.fn(async (u: string) => {
    if (u === '/api/projects/p1') return json(200, { project: { id: 'p1', name: 'shop', path: '/p' } })
    if (u.startsWith('/api/projects/p1/git/overview')) return json(200, repo ? { is_repo: true, overview, sessions_by_worktree: { '/p/.worktrees/cart': [{ id: 's1', name: 'Build cart', status: 'processing', updated_at: '' }] } } : { is_repo: false })
    if (u.includes('scope=branch')) return json(200, branch)
    if (u.includes('scope=uncommitted')) return json(200, uncommitted)
    return json(404, {})
  })
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth }))
  vi.stubGlobal('useHead', () => {})
  const Stub = { template: '<div />' }
  const router: Router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/projects/:id/git', component: GitPage }, { path: '/:any(.*)', component: Stub },
  ] })
  await router.push(url)
  await router.isReady()
  wrapper = mount(GitPage, { attachTo: document.body, global: { plugins: [router], stubs: { NuxtLink: { props: ['to'], template: '<a :href="String(to)"><slot /></a>' } } } })
  await flushPromises()
  return { w: wrapper, router, fetchWithAuth }
}

afterEach(() => { wrapper?.unmount(); wrapper = null; vi.unstubAllGlobals() })

describe('Project Git page', () => {
  it('lists worktrees with their sessions and opens on uncommitted work', async () => {
    const { w } = await mountPage('/projects/p1/git')
    expect(w.findAll('.wt')).toHaveLength(2)
    expect(w.find('.wt.selected .wt-branch').text()).toBe('cart')
    expect(w.text()).toContain('Build cart')
    expect(w.find('.wt.selected a[href*="cwd=%2Fp%2F.worktrees%2Fcart"]').exists()).toBe(true)
    expect(w.findAll('.gdf')).toHaveLength(1)
    expect(w.find('.gdf-name').text()).toBe('cart.ts')
  })

  it('shows a branch\'s commits and changes against main, and keeps the choice in the URL', async () => {
    const { w, router } = await mountPage('/projects/p1/git')
    // Active hides merged branches.
    expect(w.findAll('.br-name').map(n => n.text())).toEqual(['main', 'cart'])
    await w.findAll('.br')[1]!.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({ branch: 'cart', scope: 'branch', wt: '/p/.worktrees/cart' })
    expect(w.find('.gcp-commits').text()).toContain('add cart')
    expect(w.find('.gdf-name').text()).toBe('cart.go')
  })

  it('reopens the selection from the URL', async () => {
    const { w } = await mountPage('/projects/p1/git?branch=cart&scope=branch')
    expect(w.find('.gcp-tabs button.on').text()).toContain('vs main')
  })

  it('says so when the folder is not a repository', async () => {
    const { w } = await mountPage('/projects/p1/git', false)
    expect(w.text()).toContain('Not a Git repository')
  })
})
