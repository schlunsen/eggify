import { flushPromises, mount, shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { ref } from 'vue'
import SessionSkillsPanel from '../../components/agents/SessionSkillsPanel.vue'
import MetricsSidebar from '../../components/agents/MetricsSidebar.vue'
import { refreshSessionSkills, resetSessionSkills } from '../../composables/agents/useSessionSkills'
import { USER_REQUIRED_EVENT } from '../../utils/userRequired'

const auth = vi.hoisted(() => ({ fetchWithAuth: vi.fn() }))
vi.mock('../../composables/useAuthenticatedFetch', () => ({ useAuthenticatedFetch: () => auth }))
vi.mock('../../composables/useN0Connections', () => ({
  useN0Connections: () => ({ connections: ref([]), loaded: ref(true) }),
  n0ConnectionLabel: () => ''
}))
vi.mock('../../composables/useN0Bindings', () => ({
  useN0Bindings: () => ({ getBinding: () => null, refresh: vi.fn() })
}))

const session = 'skills-session'
const url = `/api/agent/sessions/${session}/skills`
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

const skillRows = () => [
  { id: 'a', name: 'commit', description: 'Write a commit message', scope: 'personal', user_invocable: true, reach: 'everywhere', added: false },
  { id: 'b', name: 'deploy', scope: 'project', user_invocable: false, reach: 'folder', added: false },
  { id: 'c', name: 'review', scope: 'project', user_invocable: true, reach: 'added', added: true },
  { id: 'd', name: 'release', description: 'Ship the desktop app', scope: 'project', user_invocable: true, reach: '', added: false },
]

let body: Record<string, any>
let putResponse: (sent: any) => Response

beforeEach(() => {
  resetSessionSkills()
  setActivePinia(createPinia())
  body = { session_id: session, runtime: 'claude-sdk', skills: skillRows(), restart_pending: false, subagents_running: 0 }
  putResponse = (sent) => {
    const ids: string[] = sent.skill_ids
    const skills = skillRows().map(s => ({ ...s, added: ids.includes(s.id), reach: ids.includes(s.id) && s.reach !== 'everywhere' && s.reach !== 'folder' ? 'added' : (s.reach === 'added' ? '' : s.reach) }))
    return json({ ...body, skills, restart_pending: true, subagents_running: 2 })
  }
  auth.fetchWithAuth.mockReset()
  auth.fetchWithAuth.mockImplementation(async (u: string, init?: RequestInit) => {
    if (u === url && init?.method === 'PUT') return putResponse(JSON.parse(String(init.body)))
    if (u === url) return json(structuredClone(body))
    return json({})
  })
})

async function mountPanel() {
  const wrapper = mount(SessionSkillsPanel, {
    props: { sessionId: session, sessionName: 'My session', open: true },
    global: { stubs: { Teleport: true, NuxtLink: { template: '<a class="nuxt-link"><slot /></a>' } } },
    attachTo: document.body,
  })
  await flushPromises()
  return wrapper
}

const puts = () => auth.fetchWithAuth.mock.calls.filter(([, init]) => init?.method === 'PUT')
const row = (wrapper: Awaited<ReturnType<typeof mountPanel>>, name: string) => wrapper.get(`[data-skill="${name}"]`)

describe('SessionSkillsPanel', () => {
  it('lists the session\'s skills with why it gets them, and other projects\' skills apart', async () => {
    const wrapper = await mountPanel()
    const active = wrapper.get('[data-testid="skills-active"]')
    expect(active.text()).toContain('In this session')
    expect(active.findAll('[data-skill]').map(r => r.attributes('data-skill'))).toEqual(['commit', 'deploy', 'review'])
    expect(row(wrapper, 'commit').text()).toContain('Everywhere')
    expect(row(wrapper, 'deploy').text()).toContain('This project')
    expect(row(wrapper, 'review').text()).toContain('Added')
    // Use only for skills a user can invoke; Remove only for added ones
    expect(row(wrapper, 'deploy').find('[data-action="use"]').exists()).toBe(false)
    expect(row(wrapper, 'commit').find('[data-action="remove"]').exists()).toBe(false)
    expect(row(wrapper, 'review').find('[data-action="remove"]').exists()).toBe(true)
    const other = wrapper.get('[data-testid="skills-other"]')
    expect(other.text()).toContain("Other projects' skills")
    expect(other.find('[data-skill="release"] [data-action="add"]').exists()).toBe(true)
    // Few skills: no search field, no note
    expect(wrapper.find('input[type="search"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="skills-note"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('Use emits the slash text and closes', async () => {
    const wrapper = await mountPanel()
    await row(wrapper, 'commit').get('[data-action="use"]').trigger('click')
    expect(wrapper.emitted('use')).toEqual([['/commit ']])
    expect(wrapper.emitted('update:open')?.at(-1)).toEqual([false])
    wrapper.unmount()
  })

  it('adding sends the full added list and shows the restart note', async () => {
    const wrapper = await mountPanel()
    await row(wrapper, 'release').get('[data-action="add"]').trigger('click')
    await flushPromises()
    expect(puts()).toHaveLength(1)
    expect(JSON.parse(String(puts()[0]![1].body))).toEqual({ skill_ids: ['c', 'd'] })
    expect(wrapper.get('[data-testid="skills-active"]').text()).toContain('release')
    expect(wrapper.get('[data-testid="skills-note"]').text())
      .toBe('Takes effect with your next message. The agent restarts then, which stops 2 running subagents.')
    wrapper.unmount()
  })

  it('removing sends the list without that skill', async () => {
    const wrapper = await mountPanel()
    await row(wrapper, 'review').get('[data-action="remove"]').trigger('click')
    await flushPromises()
    expect(JSON.parse(String(puts()[0]![1].body))).toEqual({ skill_ids: [] })
    expect(wrapper.get('[data-testid="skills-other"]').text()).toContain('review')
    wrapper.unmount()
  })

  it('a refusal for the API key shows the sign-in toast and keeps the list', async () => {
    putResponse = () => json({ error: "Changing a session's skills requires a signed-in user", code: 'user_required' }, 403)
    const announced = vi.fn()
    window.addEventListener(USER_REQUIRED_EVENT, announced)
    const wrapper = await mountPanel()
    await row(wrapper, 'release').get('[data-action="add"]').trigger('click')
    await flushPromises()
    expect(announced).toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('signed-in user')
    expect(wrapper.get('[data-testid="skills-other"]').text()).toContain('release')
    window.removeEventListener(USER_REQUIRED_EVENT, announced)
    wrapper.unmount()
  })

  it('explains how Codex sessions get skills', async () => {
    body.runtime = 'codex'
    const wrapper = await mountPanel()
    expect(wrapper.get('[data-testid="skills-note"]').text()).toContain('through its skill tools')
    wrapper.unmount()
  })

  it('searches when there are many skills', async () => {
    body.skills = Array.from({ length: 10 }, (_, i) => ({ id: `s${i}`, name: `skill-${i}`, scope: 'personal', user_invocable: true, reach: 'everywhere', added: false }))
    body.skills[3].description = 'Find the needle'
    const wrapper = await mountPanel()
    await wrapper.get('input[type="search"]').setValue('needle')
    expect(wrapper.findAll('[data-skill]').map(r => r.attributes('data-skill'))).toEqual(['skill-3'])
    wrapper.unmount()
  })

  it('links to the Skills page when there are no approved skills', async () => {
    body.skills = []
    const wrapper = await mountPanel()
    expect(wrapper.get('[data-testid="skills-empty"]').text()).toContain('No approved skills yet.')
    expect(wrapper.find('.nuxt-link').exists()).toBe(true)
    wrapper.unmount()
  })

  it('reloads when the server says the session\'s skills changed', async () => {
    const wrapper = await mountPanel()
    body.skills = skillRows().map(s => s.id === 'd' ? { ...s, reach: 'added', added: true } : s)
    refreshSessionSkills(session)
    refreshSessionSkills('another-session') // never asked for: nothing loads
    await flushPromises()
    expect(wrapper.get('[data-testid="skills-active"]').text()).toContain('release')
    expect(auth.fetchWithAuth.mock.calls.filter(([u]) => String(u).includes('another-session'))).toHaveLength(0)
    wrapper.unmount()
  })
})

describe('MetricsSidebar Skills row', () => {
  it('shows how many skills are active and asks to open the panel', async () => {
    const wrapper = shallowMount(MetricsSidebar, {
      props: { show: true, session: { id: session, status: 'idle', options: { provider: 'claude', model: 'claude-opus-5-5' } }, connected: true, messageCount: 1, toolExecutions: {}, permissionStats: {} },
      global: { provide: { agentWs: { send: vi.fn() } }, stubs: { Teleport: true, NuxtLink: true, Icon: true } }
    })
    await flushPromises()
    const skillsRow = wrapper.get('[data-testid="skills-row"]')
    expect(skillsRow.text()).toContain('3 active')
    await skillsRow.get('button').trigger('click')
    expect(wrapper.emitted('open-skills')).toHaveLength(1)
  })
})
