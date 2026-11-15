<template>
  <div class="homepage">
    <div class="content-wrapper">
      <header class="hd">
        <div class="hd-brand">
          <EggifyLogo :size="48" />
          <div>
            <h1 class="hd-title">{{ greeting }}</h1>
            <p class="hd-sub">{{ subtitle }}</p>
          </div>
        </div>
        <div class="hd-actions">
          <NuxtLink to="/agents?new_session=1" class="btn btn--primary" data-test="new-session">New session</NuxtLink>
          <NuxtLink to="/projects?new=1" class="btn" data-test="new-project">New project</NuxtLink>
        </div>
      </header>

      <!-- First-run checklist -->
      <section v-if="showChecklist" class="panel checklist" aria-labelledby="hp-setup">
        <h2 id="hp-setup" class="panel-title">Get set up</h2>
        <ul class="checklist-items">
          <li v-for="step in checklist" :key="step.id" :class="{ done: step.done }" :data-step="step.id">
            <span class="check" aria-hidden="true">{{ step.done ? '✓' : '' }}</span>
            <span class="step-text">
              <strong>{{ step.title }}</strong>
              <span>{{ step.desc }}</span>
            </span>
            <button v-if="!step.done && step.action" type="button" class="btn btn--small" @click="step.action()">{{ step.cta }}</button>
          </li>
        </ul>
      </section>

      <!-- Needs you / Working -->
      <section v-if="active.length" class="panel" aria-labelledby="hp-active">
        <h2 id="hp-active" class="panel-title">Needs you &amp; working <span class="count">{{ active.length }}</span></h2>
        <ul class="rows">
          <li v-for="a in active" :key="a.id">
            <NuxtLink :to="{ path: '/agents', query: { session: a.id } }" class="row" data-test="active-row">
              <span class="badge" :class="`badge--${a.category}`">{{ a.label }}</span>
              <span class="row-main">
                <span class="row-title">{{ a.title }}</span>
                <span class="row-meta">{{ a.meta }}</span>
              </span>
              <span class="row-time">{{ a.ago }}</span>
            </NuxtLink>
          </li>
        </ul>
      </section>

      <!-- Recent sessions -->
      <section v-if="recent.length" class="panel" aria-labelledby="hp-recent">
        <h2 id="hp-recent" class="panel-title">Recent sessions
          <NuxtLink to="/agents" class="panel-link">All sessions →</NuxtLink>
        </h2>
        <ul class="rows">
          <li v-for="a in recent" :key="a.id">
            <NuxtLink :to="{ path: '/agents', query: { session: a.id } }" class="row" data-test="recent-row">
              <span class="row-main">
                <span class="row-title">{{ a.title }}</span>
                <span class="row-meta">{{ a.meta }}</span>
              </span>
              <span v-for="t in a.tags" :key="t" class="tag">{{ t }}</span>
              <span class="row-time">{{ a.ago }}</span>
            </NuxtLink>
          </li>
        </ul>
      </section>

      <!-- Projects -->
      <section v-if="projectCards.length" class="panel" aria-labelledby="hp-projects">
        <h2 id="hp-projects" class="panel-title">Projects
          <NuxtLink to="/projects" class="panel-link">All projects →</NuxtLink>
        </h2>
        <div class="project-grid">
          <div v-for="p in projectCards" :key="p.id" class="project" data-test="project-card" :class="{ missing: p.missing }">
            <NuxtLink :to="`/projects/${p.id}`" class="project-main">
              <span class="avatar" :style="p.color ? { background: p.color } : undefined">{{ p.initials }}</span>
              <span class="project-text">
                <span class="project-name">{{ p.name }}</span>
                <span class="project-meta">{{ p.meta }}</span>
              </span>
            </NuxtLink>
            <NuxtLink v-if="!p.missing" :to="{ path: '/agents', query: { project: p.id, new_session: '1' } }" class="btn btn--small" :aria-label="`Start a session in ${p.name}`">Start session</NuxtLink>
          </div>
        </div>
      </section>

      <p v-if="statLine" class="statline" data-test="statline">{{ statLine }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import type { Stats } from '~/types/analytics'
import { overviewState, type OverviewCategory } from '~/utils/agents/overviewState'
import { relativeTime, projectInitials, sortProjects, type ProjectListItem } from '~/utils/projects/projectSetup'
import { useAgentRuntimes } from '~/composables/useAgentRuntimes'

interface HealthData { status: string, uptime_seconds: number }
interface ProviderData { providers: Array<{ id: string, name: string, is_configured: boolean }> }
interface DBStatsData { stats: { db_size_human: string } }
interface HomeSession {
  id: string
  status: string
  updated_at?: string
  message_count?: number
  num_turns?: number
  pending_permissions?: number
  pending_questions?: number
  subagents_running?: number
  model_name?: string
  git_branch?: string
  context_summary?: string
  project_id?: string
  options?: { agent_name?: string, working_directory?: string }
}

const router = useRouter()
const { fetchWithAuth } = useAuthenticatedFetch()
const runtimes = useAgentRuntimes()

const stats = ref<Stats | null>(null)
const health = ref<HealthData | null>(null)
const providers = ref<ProviderData | null>(null)
const dbStats = ref<DBStatsData | null>(null)
const sessions = ref<HomeSession[]>([])
const projects = ref<Array<ProjectListItem & Record<string, any>>>([])
const digests = ref<Record<string, { summary?: string, tags?: string[] }>>({})
const loaded = ref(false)

const MAX_ACTIVE = 8
const MAX_RECENT = 6
const MAX_PROJECTS = 6

const greeting = computed(() => {
  const h = new Date().getHours()
  return h < 5 ? 'Late night' : h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening'
})

const projectName = (id?: string) => projects.value.find(p => p.id === id)?.name || ''

function describe(s: HomeSession) {
  const project = projectName(s.project_id) || s.options?.working_directory?.split('/').filter(Boolean).pop() || ''
  const digest = digests.value[s.id]
  const title = digest?.summary || s.context_summary || s.options?.agent_name || project || 'Session'
  const meta = [project, s.git_branch, s.model_name].filter(Boolean).join(' · ')
  return { id: s.id, title, meta, ago: relativeTime(s.updated_at), tags: (digest?.tags || []).slice(0, 2) }
}

const byRecency = (a: HomeSession, b: HomeSession) => Date.parse(b.updated_at || '') - Date.parse(a.updated_at || '')

const active = computed(() => {
  const rank: Record<OverviewCategory, number> = { attention: 0, working: 1, recent: 2 }
  return sessions.value
    .map(s => ({ s, state: overviewState(s as any) }))
    .filter(x => x.state.category !== 'recent')
    .sort((a, b) => rank[a.state.category] - rank[b.state.category] || byRecency(a.s, b.s))
    .slice(0, MAX_ACTIVE)
    .map(x => ({ ...describe(x.s), category: x.state.category, label: x.state.label }))
})

const recent = computed(() => {
  const activeIds = new Set(active.value.map(a => a.id))
  return sessions.value
    .filter(s => !activeIds.has(s.id) && ((s.message_count || 0) > 0))
    .sort(byRecency)
    .slice(0, MAX_RECENT)
    .map(describe)
})

const projectCards = computed(() => sortProjects(projects.value, 'recent').slice(0, MAX_PROJECTS).map((p) => {
  const bits: string[] = []
  if (p.running_count) bits.push(`${p.running_count} running`)
  else if (p.last_activity) bits.push(relativeTime(p.last_activity))
  else bits.push('No sessions yet')
  if (p.git?.branch) bits.push(p.git.branch + (p.git.dirty ? '*' : ''))
  if (p.missing) bits.splice(0, bits.length, 'Folder missing')
  return { id: p.id, name: p.name, color: p.color, missing: !!p.missing, initials: projectInitials(p.name), meta: bits.join(' · ') }
}))

const configuredProviders = computed(() => (providers.value?.providers || []).filter(p => p.is_configured).length)

const checklist = computed(() => [
  {
    id: 'agent',
    title: 'Connect an agent',
    desc: 'Sign in to Claude Code or Codex, or add a provider key.',
    done: runtimes.anyReady.value || configuredProviders.value > 0,
    cta: 'Set up agents',
    action: () => runtimes.openWizard('manual'),
  },
  {
    id: 'project',
    title: 'Add a project',
    desc: 'Open a folder you work in, or clone a repository.',
    done: projects.value.length > 0,
    cta: 'Add project',
    action: () => router.push('/projects?new=1'),
  },
  {
    id: 'session',
    title: 'Start your first session',
    desc: 'Ask an agent to work on your project.',
    done: sessions.value.some(s => (s.message_count || 0) > 0),
    cta: 'New session',
    action: () => router.push('/agents?new_session=1'),
  },
])
const showChecklist = computed(() => loaded.value && checklist.value.some(s => !s.done))

const subtitle = computed(() => {
  if (!loaded.value) return 'Manage your agents and projects'
  const attention = active.value.filter(a => a.category === 'attention').length
  const working = active.value.filter(a => a.category === 'working').length
  if (attention) return `${attention} ${attention === 1 ? 'agent needs' : 'agents need'} you`
  if (working) return `${working} ${working === 1 ? 'agent is' : 'agents are'} working`
  return 'Nothing waiting on you'
})

function formatNumber(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K'
  return String(n)
}
function formatUptime(secs: number): string {
  if (secs < 60) return `${secs}s`
  const m = Math.floor(secs / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

const statLine = computed(() => {
  const parts: string[] = []
  const total = stats.value?.totalConversations
  if (total) parts.push(`${total} ${total === 1 ? 'session' : 'sessions'}`)
  if (stats.value?.totalTokens) parts.push(`${formatNumber(stats.value.totalTokens)} tokens`)
  if (configuredProviders.value) parts.push(`${configuredProviders.value} ${configuredProviders.value === 1 ? 'provider' : 'providers'}`)
  if (health.value?.uptime_seconds) parts.push(`up ${formatUptime(health.value.uptime_seconds)}`)
  if (dbStats.value?.stats?.db_size_human) parts.push(`database ${dbStats.value.stats.db_size_human}`)
  return parts.join(' · ')
})

async function getJson<T>(url: string, init?: RequestInit): Promise<T | null> {
  try {
    const res = await fetchWithAuth(url, init)
    return res.ok ? await res.json() : null
  } catch {
    return null
  }
}

async function loadData() {
  const [st, he, pr, db, ses, proj] = await Promise.all([
    getJson<Stats>('/api/stats'),
    getJson<HealthData>('/api/health'),
    getJson<ProviderData>('/api/providers'),
    getJson<DBStatsData>('/api/db/stats'),
    getJson<{ sessions?: HomeSession[] }>('/api/agent/sessions?status=all'),
    getJson<{ projects?: any[] }>('/api/projects/overview?git=0'),
    runtimes.load().catch(() => null),
  ])
  stats.value = st
  health.value = he
  providers.value = pr
  dbStats.value = db
  sessions.value = ses?.sessions || []
  projects.value = proj?.projects || []
  loaded.value = true
  loadDigests()
}

/** Cached summaries and tags only; never queues generation from the home page. */
async function loadDigests() {
  const shown = [...active.value, ...recent.value].map(a => sessions.value.find(s => s.id === a.id)).filter(Boolean) as HomeSession[]
  if (!shown.length) return
  const body = await getJson<{ digests?: Record<string, any> }>('/api/agent/overview/digests', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ sessions: shown.map(s => ({ id: s.id, version: `${s.message_count || 0}:${s.status}` })), generate: false }),
  })
  if (body?.digests) digests.value = body.digests
}

let timer: ReturnType<typeof setInterval> | null = null
async function refreshSessions() {
  if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return
  const ses = await getJson<{ sessions?: HomeSession[] }>('/api/agent/sessions?status=all')
  if (ses?.sessions) sessions.value = ses.sessions
}

onMounted(() => {
  loadData()
  timer = setInterval(refreshSessions, 5000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
.homepage { width: 100%; height: 100%; background: var(--bg-primary); overflow-y: auto; overflow-x: hidden; }
.content-wrapper { width: 100%; max-width: 860px; margin: 0 auto; padding: 2rem 1.5rem 2rem; display: flex; flex-direction: column; gap: 1.5rem; }

.hd { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; }
.hd-brand { display: flex; align-items: center; gap: 1rem; min-width: 0; }
.hd-title { font-size: 1.6rem; font-weight: 700; color: var(--text-primary); margin: 0; letter-spacing: -0.02em; }
.hd-sub { margin: 0.15rem 0 0; color: var(--text-secondary); font-size: 0.95rem; }
.hd-actions { display: flex; gap: 0.6rem; }

.btn { display: inline-flex; align-items: center; justify-content: center; padding: 0.55rem 1rem; border-radius: 8px; border: 1px solid var(--border-color); background: var(--card-bg); color: var(--text-primary); font-size: 0.9rem; font-weight: 600; text-decoration: none; cursor: pointer; transition: all 0.15s ease; white-space: nowrap; }
.btn:hover { border-color: var(--accent-purple); background: var(--card-hover); }
.btn--primary { background: var(--accent-purple); border-color: var(--accent-purple); color: #fff; }
.btn--primary:hover { filter: brightness(1.08); background: var(--accent-purple); }
.btn--small { padding: 0.3rem 0.7rem; font-size: 0.8rem; }

.panel { background: var(--card-bg); border: 1px solid var(--border-color); border-radius: 12px; padding: 1rem 1.1rem; }
.panel-title { display: flex; align-items: center; gap: 0.5rem; font-size: 0.8rem; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-secondary); margin: 0 0 0.6rem; }
.panel-link { margin-left: auto; font-size: 0.8rem; text-transform: none; letter-spacing: 0; font-weight: 500; color: var(--text-secondary); text-decoration: none; }
.panel-link:hover { color: var(--accent-purple); }
.count { background: var(--accent-purple); color: #fff; border-radius: 999px; padding: 0 0.5rem; font-size: 0.72rem; line-height: 1.4; }

.rows { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; }
.row { display: flex; align-items: center; gap: 0.75rem; padding: 0.55rem 0.4rem; border-radius: 8px; text-decoration: none; color: inherit; }
.row:hover { background: var(--card-hover); }
.row-main { display: flex; flex-direction: column; min-width: 0; flex: 1; }
.row-title { color: var(--text-primary); font-weight: 600; font-size: 0.92rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row-meta { color: var(--text-secondary); font-size: 0.78rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row-time { color: var(--text-secondary); font-size: 0.78rem; white-space: nowrap; }
.badge { font-size: 0.72rem; font-weight: 700; padding: 0.15rem 0.5rem; border-radius: 999px; border: 1px solid var(--border-color); white-space: nowrap; color: var(--text-secondary); }
.badge--attention { color: #b45309; border-color: #b45309; }
.badge--working { color: var(--accent-purple); border-color: var(--accent-purple); }
.tag { font-size: 0.7rem; color: var(--text-secondary); border: 1px solid var(--border-color); border-radius: 6px; padding: 0.05rem 0.4rem; white-space: nowrap; }

.project-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(250px, 1fr)); gap: 0.6rem; }
.project { display: flex; align-items: center; gap: 0.5rem; padding: 0.55rem 0.6rem; border: 1px solid var(--border-color); border-radius: 10px; }
.project:hover { border-color: var(--accent-purple); }
.project.missing { opacity: 0.6; }
.project-main { display: flex; align-items: center; gap: 0.6rem; min-width: 0; flex: 1; text-decoration: none; color: inherit; }
.avatar { width: 32px; height: 32px; border-radius: 8px; background: var(--accent-purple); color: #fff; display: grid; place-items: center; font-size: 0.75rem; font-weight: 700; flex-shrink: 0; }
.project-text { display: flex; flex-direction: column; min-width: 0; }
.project-name { color: var(--text-primary); font-weight: 600; font-size: 0.9rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.project-meta { color: var(--text-secondary); font-size: 0.75rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.checklist-items { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.5rem; }
.checklist-items li { display: flex; align-items: center; gap: 0.75rem; }
.check { width: 20px; height: 20px; border-radius: 50%; border: 2px solid var(--border-color); display: grid; place-items: center; font-size: 0.7rem; flex-shrink: 0; color: #fff; }
.done .check { background: var(--accent-purple); border-color: var(--accent-purple); }
.step-text { display: flex; flex-direction: column; flex: 1; min-width: 0; font-size: 0.8rem; color: var(--text-secondary); }
.step-text strong { color: var(--text-primary); font-size: 0.92rem; }
.done .step-text strong { text-decoration: line-through; opacity: 0.6; }

.statline { text-align: center; color: var(--text-secondary); font-size: 0.78rem; margin: 0.5rem 0 0; }

@media (max-width: 600px) {
  .content-wrapper { padding: 1rem; gap: 1rem; }
  .hd-actions { width: 100%; }
  .hd-actions .btn { flex: 1; }
  .tag { display: none; }
  .project-grid { grid-template-columns: 1fr; }
}
</style>
