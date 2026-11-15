import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { discoverySummary, normaliseDiscovery } from '../../utils/skillDiscovery'

function json(status: number, body: any): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body, clone() { return this } } as unknown as Response
}

// One fetchWithAuth for the page and usePendingReview (both import the
// composable from this module).
const fetchWithAuth = vi.fn()
vi.mock('../../composables/useAuthenticatedFetch', () => ({
  useAuthenticatedFetch: () => ({ fetchWithAuth })
}))

const pendingFile = {
  skill: {
    id: 's-careful', name: 'careful', description: 'Guardrails', scope: 'personal', body: 'Be careful',
    created_by: 'file:/Users/me/.claude/skills/careful/SKILL.md', status: 'pending', created_at: '2026-10-01T10:00:00Z'
  },
  created_by_session_id: '',
  agent_authored: false
}

let discoverResponse: Response
let pendingAfterDiscover = false
let discoverCalls = 0

function routeApi() {
  fetchWithAuth.mockImplementation(async (url: string) => {
    if (url === '/api/skills/discover') {
      discoverCalls++
      pendingAfterDiscover = true
      return discoverResponse
    }
    if (url.startsWith('/api/review/pending?kind=skill')) {
      return json(200, { skills: pendingAfterDiscover ? [pendingFile] : [] })
    }
    if (url.startsWith('/api/review/pending/counts')) return json(200, { memories: 0, skills: 0, total: 0, memories_by_project: {} })
    if (url.startsWith('/api/skills/')) return json(200, { skills: [], templates: [], categories: [] })
    if (url.startsWith('/api/packs/')) return json(200, { packs: [] })
    return json(404, {})
  })
}

let wrapper: VueWrapper | null = null
async function mountPage() {
  const SkillsPage = (await import('../../pages/skills.vue')).default
  wrapper = mount(SkillsPage, { global: { stubs: { Icon: true, EggLoader: true } } })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  fetchWithAuth.mockReset()
  pendingAfterDiscover = false
  discoverCalls = 0
  discoverResponse = json(200, {
    discovered: 3, synced: 2, pending: 1, unchanged: 1, skipped: 1,
    skipped_items: [{ path: '/Users/me/.claude/skills/synced/u/n0-app/SKILL.md', name: 'n0-app', reason: 'name "n0-app" is already used by /Users/me/.claude/skills/n0-app/SKILL.md' }],
    errors: [{ path: '/Users/me/.claude/skills/broken/SKILL.md', message: 'failed to parse YAML frontmatter: did not find expected node content' }],
    skills: []
  })
  routeApi()
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
})

describe('Skills page discovery', () => {
  it('discovers once on open and reloads the review queue afterwards', async () => {
    const w = await mountPage()
    expect(discoverCalls).toBe(1)
    // The pending file skill found by discovery is in the review queue.
    expect(w.findAll('[data-test="rq-item"]')).toHaveLength(1)
    expect(w.text()).toContain('From ~/.claude/skills/careful/SKILL.md')

    const banner = w.find('[data-test="discovery-banner"]')
    expect(banner.exists()).toBe(true)
    expect(w.find('[data-test="discovery-summary"]').text()).toBe(
      'Found 3 skill files · 1 new or changed, waiting for your review · 1 unchanged · 1 skipped · 1 error'
    )
    expect(w.find('[data-test="discovery-errors"]').text()).toContain('~/.claude/skills/broken/SKILL.md')
    expect(w.find('[data-test="discovery-skipped"]').text()).toContain('already used by')
  })

  it('reloads the review queue after a manual discovery too', async () => {
    const w = await mountPage()
    pendingAfterDiscover = false
    await flushPromises()
    const before = fetchWithAuth.mock.calls.filter(([u]) => String(u).startsWith('/api/review/pending?kind=skill')).length
    await w.find('[data-test="discover-btn"]').trigger('click')
    await flushPromises()
    expect(discoverCalls).toBe(2)
    const after = fetchWithAuth.mock.calls.filter(([u]) => String(u).startsWith('/api/review/pending?kind=skill')).length
    expect(after).toBe(before + 1)
    // The banner stays until dismissed.
    await w.find('[data-test="discovery-dismiss"]').trigger('click')
    expect(w.find('[data-test="discovery-banner"]').exists()).toBe(false)
  })

  it('shows a failed discovery', async () => {
    discoverResponse = json(500, { error: 'Failed to discover skills: boom' })
    const w = await mountPage()
    expect(w.find('[data-test="discovery-error"]').text()).toContain('Failed to discover skills: boom')
  })

  it('stays quiet when an automatic run finds nothing new', async () => {
    discoverResponse = json(200, { discovered: 2, synced: 2, pending: 0, unchanged: 2, skipped: 0, skipped_items: [], errors: [], skills: [] })
    const w = await mountPage()
    expect(w.find('[data-test="discovery-banner"]').exists()).toBe(false)
  })

  it('approves every skill of a folder through the single approve call', async () => {
    const second = { ...pendingFile, skill: { ...pendingFile.skill, id: 's-ship', name: 'ship', created_by: 'file:/Users/me/.claude/skills/ship/SKILL.md' } }
    fetchWithAuth.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.startsWith('/api/review/pending?kind=skill')) return json(200, { skills: [pendingFile, second] })
      if (url.startsWith('/api/review/skills/') && init?.method === 'POST') return json(200, { status: 'approved' })
      if (url === '/api/skills/discover') return discoverResponse
      if (url.startsWith('/api/review/pending/counts')) return json(200, { memories: 0, skills: 0, total: 0, memories_by_project: {} })
      return json(200, { skills: [], packs: [], templates: [], categories: [] })
    })
    const w = await mountPage()
    await w.find('[data-test="rq-approve-folder"]').trigger('click')
    await w.find('[data-test="rq-approve-folder-confirm"]').trigger('click')
    await flushPromises()
    const approves = fetchWithAuth.mock.calls.map(([u]) => String(u)).filter(u => u.startsWith('/api/review/skills/'))
    expect(approves).toEqual(['/api/review/skills/s-careful/approve', '/api/review/skills/s-ship/approve'])
  })
})

describe('discovery result helpers', () => {
  it('fills in what an older server leaves out', () => {
    const r = normaliseDiscovery({ discovered: 1, synced: 1, pending: 0, skipped: 0, errors: ['x: bad'] })
    expect(r.unchanged).toBe(0)
    expect(r.skipped_items).toEqual([])
    expect(r.errors).toEqual([{ path: '', message: 'x: bad' }])
    expect(discoverySummary(normaliseDiscovery({ discovered: 1 }))).toBe('Found 1 skill file')
  })
})
