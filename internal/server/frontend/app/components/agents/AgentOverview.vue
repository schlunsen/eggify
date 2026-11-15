<template>
  <section ref="panel" class="agent-overview" role="dialog" aria-modal="true" aria-labelledby="overview-title" tabindex="-1">
    <header class="overview-header">
      <div>
        <div class="overview-eyebrow"><span class="live-dot" /> ALL PROJECTS · LIVE</div>
        <h1 id="overview-title">Agent overview <span>{{ counts.all }}</span></h1>
        <p>{{ projectCount }} {{ projectCount === 1 ? 'project' : 'projects' }} <span class="header-divider">/</span> {{ counts.working }} working<template v-if="counts.justIdle"> · {{ counts.justIdle }} just went idle</template> · {{ counts.attention }} need attention · {{ counts.unreviewed }} to review</p>
      </div>
      <div class="overview-header-actions">
      <div class="view-toggle detail-toggle" role="group" aria-label="Overview detail">
        <button :aria-pressed="viewMode === 'simple'" @click="viewMode = 'simple'">Simple</button>
        <button :aria-pressed="viewMode === 'advanced'" @click="viewMode = 'advanced'">Advanced</button>
      </div>
      <button class="overview-close" @click="$emit('close')" aria-label="Close agent overview">
        Back to editor <kbd>{{ overviewKey }}</kbd><span aria-hidden="true">×</span>
      </button>
      </div>
    </header>
    <nav class="overview-filters" aria-label="Filter agents">
      <button v-for="tab in tabs" :key="tab.value" :aria-pressed="filter === tab.value" @click="filter = tab.value">
        {{ tab.label }} <span>{{ counts[tab.value] }}<template v-if="tab.value === 'working' && counts.justIdle"> +{{ counts.justIdle }} idle</template></span>
      </button>
      <div v-if="viewMode === 'advanced'" class="view-toggle" role="group" aria-label="Layout">
        <button :aria-pressed="layout === 'columns'" @click="layout = 'columns'">Columns</button>
        <button :aria-pressed="layout === 'list'" @click="layout = 'list'">List</button>
      </div>
      <button v-if="viewMode === 'advanced'" class="summaries-toggle" :aria-pressed="summariesOn" title="AI summary and tags per session (a small Haiku call when a session has moved on)" @click="summariesOn = !summariesOn">✦ Summaries</button>
      <span v-if="viewMode === 'simple'" class="key-hint" aria-hidden="true"><kbd>←</kbd><kbd>→</kbd> project · <kbd>↑</kbd><kbd>↓</kbd> session · <kbd>↵</kbd> open</span>
      <span v-else class="key-hint" aria-hidden="true"><kbd>←</kbd><kbd>→</kbd> select · <kbd>↵</kbd> open</span>
      <label class="recent-window">Recent
        <select v-model.number="recentHours" aria-label="Recent activity window"><option :value="24">Last 24 hours</option><option :value="168">Last 7 days</option></select>
      </label>
    </nav>
    <div v-if="summariesOn && topTags.length" class="tag-bar" role="group" aria-label="Filter by tag">
      <button v-for="t in topTags.slice(0, 16)" :key="t.tag" class="tag-chip" :aria-pressed="tagFilter === t.tag" @click="toggleTag(t.tag)">#{{ t.tag }} <span>{{ t.count }}</span></button>
      <button v-if="tagFilter" class="tag-clear" @click="tagFilter = ''">Clear #{{ tagFilter }} ×</button>
    </div>
    <div v-if="navigationError || error" class="overview-error" role="status">{{ navigationError || error }}</div>
    <button v-if="filter === 'working' && counts.attention" class="attention-hint" @click="filter = 'attention'">
      {{ counts.attention }} {{ counts.attention === 1 ? 'agent needs' : 'agents need' }} your attention
    </button>
    <div v-if="loading && !agents.length" class="overview-empty">Loading agents…</div>
    <div v-else-if="!agents.length && !error" class="overview-empty">
      <span class="empty-symbol" aria-hidden="true">◌</span>
      <h2>{{ filter === 'working' ? 'No agents are running' : filter === 'all' ? 'All quiet for now' : 'No agents in this view' }}</h2>
      <p>Running agents and pending requests stay visible. Finished turns appear here for the selected time window.</p>
      <button v-if="tagFilter" @click="tagFilter = ''">Clear #{{ tagFilter }}</button>
      <button v-else-if="filter !== 'all' && counts.all" @click="filter = 'all'">Show all {{ counts.all }} agents</button>
      <button @click="$emit('close')">Return to editor</button>
    </div>
    <div v-else-if="viewMode === 'simple'" class="simple-board">
      <div class="simple-grid" aria-label="Agents by project">
        <section v-for="column in simpleColumns" :key="column.key" class="simple-column" :style="{ '--agent-color': column.color }" :aria-label="`${column.name}, ${column.agents.length} ${column.agents.length === 1 ? 'agent' : 'agents'}`">
          <h3 class="simple-column-title"><span>{{ column.name }}</span><span>{{ column.agents.length }}</span></h3>
          <div role="list" class="simple-stack">
            <article v-for="agent in column.agents" :key="agent.id" role="listitem" class="simple-card" :class="{ selected: agent.id === selectedId }" :data-agent-id="agent.id" :aria-current="agent.id === selectedId ? 'true' : undefined" :style="{ '--agent-color': projects[agent.project_id || '']?.color || agent.selected_avatar?.color || '#9d8bff' }">
              <button class="column-header simple-main" @focus="selectedId = agent.id" :aria-label="`Open agent ${agentName(agent)}`" @click="emit('select', agent)">
                <h2><span class="simple-name">{{ agentName(agent) }}</span><span v-if="agent.id === activeSessionId" class="simple-current">Current</span></h2>
                <span class="running-label" :class="`status-${stateOf(agent).category}`"><span class="live-dot" />{{ isJustIdle(agent) ? 'Just finished' : stateOf(agent).label }}</span>
                <p class="simple-summary">{{ simpleSummary(agent) }}</p>
                <span v-if="stateOf(agent).category === 'attention'" class="simple-attention">{{ agent.pending_questions ? 'Answer a question →' : agent.pending_permissions ? 'Review approval request →' : 'Check this agent →' }}</span>
                <span v-else class="simple-open">Open conversation →</span>
              </button>
              <div v-if="summariesOn && digests[agent.id]?.tags?.length" class="simple-tags">
                <button v-for="tag in digests[agent.id]!.tags" :key="tag" class="tag-chip" :aria-pressed="tagFilter === tag" @click="toggleTag(tag)">#{{ tag }}</button>
              </div>
              <footer v-if="needsReview(agent)" class="simple-footer"><span>Ready to review</span><button @click="markReviewed(agent)">Mark reviewed ✓</button></footer>
            </article>
          </div>
        </section>
      </div>
      <button v-if="hiddenRecentCount && (filter === 'all' || filter === 'recent')" class="simple-more" @click="recentLimit += 12">Show more recent agents ({{ hiddenRecentCount }})</button>
    </div>
    <div v-else-if="layout === 'list'" class="agent-list" role="list" aria-label="Agents">
      <article v-for="agent in agents" :key="agent.id" role="listitem" class="agent-row" :class="[`state-${stateOf(agent).category}`, { 'just-idle': isJustIdle(agent), selected: agent.id === selectedId }]" :data-agent-id="agent.id" :aria-current="agent.id === selectedId ? 'true' : undefined" :style="{ '--agent-color': projects[agent.project_id || '']?.color || agent.selected_avatar?.color || '#9d8bff' }">
        <button class="column-header row-main" @focus="selectedId = agent.id" :aria-label="`Open agent ${agent.selected_avatar?.name || agent.id.slice(0, 8)}`" @click="emit('select', agent)">
          <span class="row-who">
            <strong>{{ agentName(agent) }}</strong>
            <small>{{ projects[agent.project_id || '']?.name || projectName(agent) }}<template v-if="agent.git_branch || agent.options?.workspace?.branch"> · {{ agent.options?.workspace?.branch || agent.git_branch }}</template></small>
          </span>
          <span class="row-summary" :class="{ pending: summariesOn && !digests[agent.id]?.summary && digests[agent.id]?.pending }">{{ rowSummary(agent) }}</span>
        </button>
        <div v-if="summariesOn && digests[agent.id]?.tags?.length" class="row-tags">
          <button v-for="tag in digests[agent.id]!.tags" :key="tag" class="tag-chip small" :aria-pressed="tagFilter === tag" @click="toggleTag(tag)">#{{ tag }}</button>
        </div>
        <div class="row-meta">
          <span class="running-label" :class="`status-${stateOf(agent).category}`"><span class="live-dot" /> <template v-if="isJustIdle(agent)">Went idle · <LiveText :interval="15000" :text="() => relativeTime(agent.updated_at).toLowerCase()" /></template><template v-else>{{ stateOf(agent).label }}</template></span>
          <span v-if="contextFor(agent)" class="row-context" :title="`Context ${Math.round(contextFor(agent).percentage)}%`"><span :style="{ width: `${Math.min(100, contextFor(agent).percentage)}%` }" /></span>
        </div>
      </article>
      <button v-if="hiddenRecentCount && (filter === 'all' || filter === 'recent')" class="show-more" @click="recentLimit += 12">Show more recent agents ({{ hiddenRecentCount }})</button>
    </div>
    <div v-else class="agent-columns" aria-label="Agent activity">
      <article v-for="agent in agents" :key="agent.id" class="agent-column" :class="[`state-${stateOf(agent).category}`, { 'just-idle': isJustIdle(agent), selected: agent.id === selectedId }]" :data-agent-id="agent.id" :aria-current="agent.id === selectedId ? 'true' : undefined" :style="{ '--agent-color': projects[agent.project_id || '']?.color || agent.selected_avatar?.color || '#9d8bff' }">
        <button class="column-header" @focus="selectedId = agent.id" :aria-label="`Open agent ${agent.selected_avatar?.name || agent.id.slice(0, 8)}`" @click="emit('select', agent)">
          <div class="column-project">{{ projects[agent.project_id || '']?.name || projectName(agent) }}</div>
          <div class="column-title"><h2>{{ agent.selected_avatar?.name || agent.options?.agent_name || `Agent ${agent.id.slice(0, 6)}` }}</h2><span class="running-label" :class="`status-${stateOf(agent).category}`"><span class="live-dot" /> <template v-if="isJustIdle(agent)">Went idle · <LiveText :interval="15000" :text="() => relativeTime(agent.updated_at).toLowerCase()" /></template><template v-else>{{ stateOf(agent).label }}</template></span></div>
          <div class="column-model">{{ agent.model_name || agent.options?.model || 'Agent' }} <span><span v-if="agent.view_mode === 'zen'" class="view-mode" title="Opens in Zen view, as it was last viewed">ZEN</span>{{ agent.id.slice(0, 8) }}</span></div>
          <div v-if="stateOf(agent).category === 'working' && latestAction(agent)" class="column-now" :title="latestAction(agent)"><span aria-hidden="true">▸</span> {{ latestAction(agent) }}</div>
        </button>
        <div v-if="summariesOn && (digests[agent.id]?.summary || digests[agent.id]?.pending)" class="column-digest">
          <p v-if="digests[agent.id]?.summary" class="digest-summary">{{ digests[agent.id]!.summary }}</p>
          <p v-else class="digest-summary pending">Summarizing…</p>
          <div v-if="digests[agent.id]?.tags?.length" class="digest-tags">
            <button v-for="tag in digests[agent.id]!.tags" :key="tag" class="tag-chip small" :aria-pressed="tagFilter === tag" @click="toggleTag(tag)">#{{ tag }}</button>
          </div>
        </div>
        <div v-if="stateOf(agent).category === 'attention'" class="attention-notice">
          {{ agent.pending_questions ? 'Open this agent to answer its question.' : agent.pending_permissions ? 'Open this agent to review its approval request.' : agent.error_message || 'The last run failed. Open the conversation to investigate.' }}
        </div>
        <div v-if="stateOf(agent).category === 'recent'" class="recent-notice">
          <span><LiveText :interval="15000" :text="() => relativeTime(agent.updated_at)" /> <span v-if="agent.status === 'idle' && agent.num_turns">· Last turn finished</span></span>
          <strong v-if="needsReview(agent)">Ready to review</strong><span v-else>Reviewed ✓</span>
        </div>
        <div class="column-context">
          <div class="context-heading"><span>Context</span><strong>{{ contextFor(agent) ? `${Math.round(contextFor(agent).percentage)}%` : 'Not reported' }}</strong></div>
          <div class="context-track" role="meter" aria-label="Context usage" :aria-valuenow="contextFor(agent)?.percentage" aria-valuemin="0" aria-valuemax="100"><span :style="{ width: `${Math.min(100, contextFor(agent)?.percentage || 0)}%` }" /></div>
          <div class="context-numbers"><span>{{ contextFor(agent) ? `${tokens(contextFor(agent).total_tokens)} / ${tokens(contextFor(agent).context_window)} tokens` : 'Waiting for a measured context reading' }}</span><span v-if="agent.cost_usd">${{ agent.cost_usd.toFixed(3) }}</span></div>
        </div>
        <div class="column-git">
          <div class="git-branch"><span aria-hidden="true">⑂</span><strong>{{ details[agent.id]?.git?.branch || agent.options?.workspace?.branch || agent.git_branch || 'No branch reported' }}</strong><span v-if="worktree(agent)" class="wt-label" :title="worktree(agent)">WT</span></div>
          <div v-if="worktree(agent)" class="worktree-location" :title="worktree(agent)">{{ worktree(agent).split('/').filter(Boolean).pop() }}</div>
          <details v-if="details[agent.id]?.git && changedFileCount(details[agent.id].git)" class="git-changes">
            <summary>{{ changedFileCount(details[agent.id].git) }} changed files <span>{{ details[agent.id].git.staged?.length || 0 }} staged</span></summary>
            <ul><li v-for="file in changedFiles(details[agent.id].git)" :key="file" :title="file">{{ file }}</li></ul>
          </details>
          <div v-else class="git-clean">{{ details[agent.id]?.gitError || (details[agent.id]?.git ? 'Working tree clean' : 'Loading Git status…') }}<span v-if="details[agent.id]?.git?.ahead"> · {{ details[agent.id].git.ahead }} ahead</span><span v-if="details[agent.id]?.git?.behind"> · {{ details[agent.id].git.behind }} behind</span></div>
        </div>
        <section v-if="agent.subagents_total" class="column-subagents" :aria-label="`Subagents of agent ${agent.id.slice(0, 8)}`">
          <div class="subagents-heading"><span>SUBAGENTS</span><span><strong v-if="agent.subagents_running">{{ agent.subagents_running }} running · </strong>{{ agent.subagents_total }} total</span></div>
          <ul v-if="subagentRows(details[agent.id]?.subagents || []).rows.length" class="subagent-list">
            <li v-for="sub in subagentRows(details[agent.id]?.subagents || []).rows" :key="sub.agent_id" :class="[`sub-${sub.status}`, { nested: sub.parent_agent_id }]">
              <details>
                <summary :title="sub.description">
                  <span v-if="circusStage" class="sub-fig" aria-hidden="true"><CircusFigure :id="sub.agent_id" :status="sub.status" :size="18" /></span><span v-else class="sub-dot" aria-hidden="true" /><span class="sub-desc">{{ sub.description || 'Subagent' }}</span>
                  <span class="sub-meta"><span class="visually-hidden">{{ sub.status }}, </span>{{ sub.background ? 'bg · ' : '' }}<LiveText :text="now => subagentDuration(sub, now)" /></span>
                  <span v-if="sub.status === 'running' && subagentNow(sub)" class="sub-now">{{ subagentNow(sub) }}</span>
                </summary>
                <div class="sub-type">{{ sub.subagent_type || 'agent' }}</div>
                <pre>{{ subagentTail(sub) }}</pre>
              </details>
            </li>
          </ul>
          <div v-else class="sub-loading">Loading subagents…</div>
          <div v-if="subagentRows(details[agent.id]?.subagents || []).hidden" class="sub-more">+{{ subagentRows(details[agent.id]?.subagents || []).hidden }} earlier</div>
        </section>
        <div class="activity-heading">{{ stateOf(agent).category === 'recent' ? 'RECENT ACTIVITY' : 'ACTIVITY' }} <span v-if="details[agent.id]?.logError">Update paused</span><span v-else>{{ stateOf(agent).category === 'working' ? 'LIVE' : 'LATEST' }}</span></div>
        <div :ref="el => setLogElement(agent.id, el)" class="activity-log" @scroll="trackScroll(agent.id, $event)" role="log" aria-live="off" :aria-label="`Activity for agent ${agent.id.slice(0, 8)}`">
          <div v-if="!details[agent.id]?.logs.length" class="log-empty">{{ details[agent.id]?.logError || 'Waiting for the next activity…' }}</div>
          <div v-for="entry in details[agent.id]?.logs || []" :key="entry.id" class="log-entry" :class="{ 'tool-entry': !['User', 'Agent', 'System'].includes(entry.kind) }">
            <div class="log-meta"><span>{{ entry.kind }}</span><time>{{ clockTime(entry.time) }}</time></div>
            <pre>{{ entry.text }}</pre>
          </div>
        </div>
        <footer class="column-footer"><span>{{ agent.message_count || 0 }} messages</span><button v-if="paused[agent.id]" @click="jumpToLatest(agent.id)">↓ Follow latest</button><button v-if="needsReview(agent)" @click="markReviewed(agent)">Mark reviewed ✓</button><span v-else-if="!paused[agent.id]">Following latest ↓</span></footer>
      </article>
      <button v-if="hiddenRecentCount && (filter === 'all' || filter === 'recent')" class="show-more" @click="recentLimit += 12">Show more recent agents ({{ hiddenRecentCount }})</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useAgentOverview, type OverviewAgent } from '../../composables/agents/useAgentOverview'
import { changedFileCount, subagentDuration, subagentNow, subagentRows, subagentTail } from '../../utils/agents/overview'
import CircusFigure from './CircusFigure.vue'
import { loadZenStage } from '../../utils/agents/zenStage'

// Subagents wear the same Flying Circus character as on the zen stage (unless you chose synthwave).
const circusStage = loadZenStage() === 'circus'
import type { OverviewFilter } from '../../utils/agents/overviewState'
import LiveText from '../ui/LiveText.vue'
import { isModalOpen, useShortcutLabels } from '../../composables/useKeyboardShortcuts'
const props = defineProps<{ navigationError?: string; activeSessionId?: string | null }>()
const emit = defineEmits<{ close: []; select: [agent: OverviewAgent] }>()
const { agents, projects, details, logsVersion, stateOf, loading, error, projectCount, contextFor, filter, recentHours, recentLimit, counts, needsReview, markReviewed, hiddenRecentCount, isJustIdle, layout, summariesOn, tagFilter, digests, topTags } = useAgentOverview()
const VIEW_MODE_KEY = 'eggify:overview-detail'
const viewMode = ref<'simple' | 'advanced'>('simple')
try { if (localStorage.getItem(VIEW_MODE_KEY) === 'advanced') viewMode.value = 'advanced' } catch { /* Storage may be unavailable. */ }
watch(viewMode, value => {
  try { localStorage.setItem(VIEW_MODE_KEY, value) } catch { /* Storage may be unavailable. */ }
})
const tabs: { value: OverviewFilter; label: string }[] = [
  { value: 'working', label: 'Working' }, { value: 'attention', label: 'Needs attention' },
  { value: 'recent', label: 'Recent' }, { value: 'all', label: 'All' }
]
function relativeTime(value?: string) {
  const minutes = Math.max(0, Math.floor((Date.now() - Date.parse(value || '')) / 60000))
  if (!Number.isFinite(minutes)) return 'Recently active'
  return minutes < 1 ? 'Just now' : minutes < 60 ? `${minutes}m ago` : minutes < 1440 ? `${Math.floor(minutes / 60)}h ago` : `${Math.floor(minutes / 1440)}d ago`
}
const panel = ref<HTMLElement | null>(null)
const logs = new Map<string, HTMLElement>()
const paused = ref<Record<string, boolean>>({})
// Running subagents' durations and "5m ago" labels tick inside <LiveText>,
// so the clock doesn't re-render every card.
const tokens = (n: number) => n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n)
const clockTime = (value: string) => value && !Number.isNaN(new Date(value).getTime()) ? new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : ''
const projectName = (agent: OverviewAgent) => agent.options?.working_directory?.split('/').filter(Boolean).pop() || 'No project'
const worktree = (agent: OverviewAgent) => details.value[agent.id]?.git?.worktree_path ?? agent.options?.workspace?.worktree_path ?? ''
const overviewKey = useShortcutLabels().label('overview.toggle')
const agentName = (agent: OverviewAgent) => agent.selected_avatar?.name || agent.options?.agent_name || `Agent ${agent.id.slice(0, 6)}`
// Simple view: one column per project (in the order its first agent appears),
// its agents stacked. ←/→ move between projects, ↑/↓ within one.
// While the overview is open the board keeps its layout: agents and projects stay
// where they were first shown (new ones join at the end), so status changes from
// polling don't slide cards out from under the cursor. Reopening re-sorts.
const seenOrder = new Map<string, number>()
const placeOf = (id: string) => { if (!seenOrder.has(id)) seenOrder.set(id, seenOrder.size); return seenOrder.get(id)! }
const simpleColumns = computed(() => {
  const columns = new Map<string, { key: string; name: string; color: string; agents: OverviewAgent[] }>()
  for (const agent of agents.value) placeOf(agent.id)
  for (const agent of [...agents.value].sort((a, b) => placeOf(a.id) - placeOf(b.id))) {
    const key = agent.project_id || `dir:${projectName(agent)}`
    let column = columns.get(key)
    if (!column) {
      const project = projects.value[agent.project_id || '']
      column = { key, name: project?.name || projectName(agent), color: project?.color || agent.selected_avatar?.color || '#9d8bff', agents: [] }
      columns.set(key, column)
    }
    column.agents.push(agent)
  }
  const list = [...columns.values()]
  for (const column of list) placeOf(`project:${column.key}`)
  return list.sort((a, b) => placeOf(`project:${a.key}`) - placeOf(`project:${b.key}`))
})
// Agents in on-screen order, which is what the keyboard cursor walks.
const orderedAgents = computed(() => viewMode.value === 'simple' ? simpleColumns.value.flatMap(column => column.agents) : agents.value)
function toggleTag(tag: string) { tagFilter.value = tagFilter.value === tag ? '' : tag }
// A list row's line: the AI summary, or what the agent did last.
function rowSummary(agent: OverviewAgent) {
  const digest = summariesOn.value ? digests.value[agent.id] : undefined
  if (digest?.summary) return digest.summary
  if (digest?.pending) return 'Summarizing…'
  return latestAction(agent) || 'No activity yet'
}
function simpleSummary(agent: OverviewAgent) {
  const digest = summariesOn.value ? digests.value[agent.id] : undefined
  if (digest?.summary) return digest.summary
  const message = details.value[agent.id]?.logs.filter(entry => entry.kind === 'Agent' || entry.kind === 'User').at(-1)
  if (message) return message.text.replace(/\s+/g, ' ').trim()
  if (digest?.pending) return 'Summarizing…'
  return stateOf(agent).category === 'working' ? 'Working on your request…' : 'Open the conversation for details.'
}
// The newest activity entry as one line: what a working agent is doing now.
function latestAction(agent: OverviewAgent) {
  const entry = details.value[agent.id]?.logs.at(-1)
  if (!entry) return ''
  const text = entry.text.replace(/\s+/g, ' ').trim()
  return entry.kind === 'Agent' ? text : `${entry.kind} · ${text}`
}
const changedFiles = (status: any) => [...new Set<string>([...(status.staged || []), ...(status.modified || []), ...(status.untracked || []), ...(status.deleted || [])])]
function setLogElement(id: string, el: any) { if (el) logs.set(id, el); else { logs.delete(id); delete paused.value[id] } }
function trackScroll(id: string, event: Event) { const el = event.target as HTMLElement; paused.value[id] = el.scrollHeight - el.scrollTop - el.clientHeight > 50 }
function jumpToLatest(id: string) { const el = logs.get(id); if (el) { el.scrollTop = el.scrollHeight; paused.value[id] = false } }
watch(logsVersion, async () => { await nextTick(); for (const id of logs.keys()) if (!paused.value[id]) jumpToLatest(id) })
// Keyboard cursor: arrow keys move between agents, Enter opens the one selected.
// It starts on the agent you came from, and follows focus (click or Tab) too.
const selectedId = ref('')
// Where the selection was, so it stays close by if that agent leaves the view.
let selectedIndex = 0
watch(orderedAgents, list => {
  const index = list.findIndex(agent => agent.id === selectedId.value)
  if (index >= 0) { selectedIndex = index; return }
  if (!list.length) { selectedId.value = ''; return }
  const fallback = selectedId.value ? list[Math.min(selectedIndex, list.length - 1)] : list.find(agent => agent.id === props.activeSessionId)
  selectedId.value = (fallback ?? list[0]!).id
}, { immediate: true })

function moveSelection(to: number) {
  const list = orderedAgents.value
  if (!list.length) return
  const index = Math.min(list.length - 1, Math.max(0, to))
  selectedIndex = index
  selectedId.value = list[index]!.id
  nextTick(() => {
    const header = panel.value?.querySelector<HTMLElement>(`[data-agent-id="${selectedId.value}"] .column-header`)
    header?.focus()
    header?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
  })
}

// Index (in orderedAgents) an arrow key leads to on the Simple board: up/down stay in
// the project's column, left/right jump to the neighbouring column at the same height.
function gridTarget(key: string, current: number) {
  if (current < 0) return 0
  const columns = simpleColumns.value
  let col = 0, row = current, start = 0
  while (col < columns.length - 1 && row >= columns[col]!.agents.length) { row -= columns[col]!.agents.length; start += columns[col]!.agents.length; col++ }
  const size = columns[col]!.agents.length
  if (key === 'ArrowUp') return start + Math.max(0, row - 1)
  if (key === 'ArrowDown') return start + Math.min(size - 1, row + 1)
  const next = Math.min(columns.length - 1, Math.max(0, col + (key === 'ArrowRight' ? 1 : -1)))
  if (next === col) return current
  const nextStart = columns.slice(0, next).reduce((sum, column) => sum + column.agents.length, 0)
  return nextStart + Math.min(row, columns[next]!.agents.length - 1)
}

// Keys that belong to the control that has focus (the time-window select, text
// fields, open log panes) are left alone.
function ownsArrowKeys(target: EventTarget | null) {
  const el = target as HTMLElement | null
  return !!el && (el.tagName === 'SELECT' || el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)
}

function handleKey(event: KeyboardEvent) {
  // Escape is handled by the shortcut registry's Escape stack (layouts/default.vue)
  if (event.defaultPrevented || event.isComposing || isModalOpen()) return
  if (!event.metaKey && !event.ctrlKey && !event.altKey && !ownsArrowKeys(event.target)) {
    const current = orderedAgents.value.findIndex(agent => agent.id === selectedId.value)
    if (viewMode.value === 'simple' && event.key.startsWith('Arrow')) {
      event.preventDefault()
      moveSelection(gridTarget(event.key, current))
      return
    }
    const step: Record<string, number> = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }
    if (event.key in step) {
      event.preventDefault()
      moveSelection(current < 0 ? 0 : current + step[event.key]!)
      return
    }
    if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault()
      moveSelection(event.key === 'Home' ? 0 : orderedAgents.value.length - 1)
      return
    }
    // Enter on a focused button or summary already activates it; otherwise it
    // opens the selected agent.
    const onControl = (event.target as HTMLElement | null)?.closest?.('button, summary, a, select')
    if (event.key === 'Enter' && !onControl) {
      const agent = agents.value.find(a => a.id === selectedId.value)
      if (agent) { event.preventDefault(); emit('select', agent) }
      return
    }
  }
  if (event.key === 'Tab') {
    const elements = [...(panel.value?.querySelectorAll<HTMLElement>('button, select, summary, [href], [tabindex="0"]') || [])]
    const first = elements[0], last = elements[elements.length - 1]
    if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.value)) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
  }
}
onMounted(() => { panel.value?.focus(); window.addEventListener('keydown', handleKey, true) })
onUnmounted(() => { window.removeEventListener('keydown', handleKey, true) })
</script>

<style scoped>
.simple-board { flex: 1; min-height: 0; display: flex; flex-direction: column; gap: 12px; }
.simple-grid { flex: 1; min-height: 0; overflow: auto; display: flex; align-items: flex-start; gap: 20px; padding: 3px 3px 16px; }
.simple-column { flex: 0 0 min(360px, 100%); min-width: 0; display: flex; flex-direction: column; gap: 12px; }
.simple-column-title { display: flex; justify-content: space-between; gap: 8px; margin: 0; padding: 0 4px; font: 11px 'JetBrains Mono', monospace; letter-spacing: .08em; text-transform: uppercase; color: var(--agent-color); }
.simple-column-title span:first-child { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.simple-column-title span:last-child { color: var(--text-secondary, #8b8c9a); }
.simple-current { margin-left: 8px; vertical-align: middle; font: 9px 'JetBrains Mono', monospace; letter-spacing: .08em; text-transform: uppercase; padding: 2px 6px; border-radius: 999px; color: var(--text-secondary, #8b8c9a); border: 1px solid var(--border-color, #2b2c37); }
.simple-stack { display: flex; flex-direction: column; gap: 16px; }
.simple-card { min-width: 0; display: flex; flex-direction: column; border: 1px solid var(--border-color, #2b2c37); border-top: 2px solid var(--agent-color); border-radius: 12px; background: var(--bg-secondary, #181920); overflow: hidden; transition: transform .12s ease, box-shadow .12s ease; }
.simple-card.selected { border-color: var(--agent-color); box-shadow: 0 0 0 1px var(--agent-color), 0 8px 24px rgba(0, 0, 0, .25); transform: translateY(-2px); }
.simple-card .column-header:focus-visible { outline: none; }
@media (prefers-reduced-motion: reduce) { .simple-card { transition: none; } .simple-card.selected { transform: none; } }
.column-header.simple-main { display: flex; flex: 1; flex-direction: column; align-items: flex-start; padding: 22px 24px; min-height: 180px; scroll-margin: 32px 16px 16px; }
.simple-main h2 { margin: 0 0 10px; font-size: 20px; font-weight: 550; overflow-wrap: anywhere; }
.simple-main .running-label { font-size: 13px; }
.simple-summary { font-size: 15px; line-height: 1.6; margin: 14px 0 18px; color: var(--text-primary, #dadbe5); display: -webkit-box; -webkit-line-clamp: 5; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; }
.simple-open, .simple-attention { margin-top: auto; font-size: 13px; color: var(--text-primary, #eeeef4); }
.simple-attention { color: var(--status-warning, #e8bc82); }
.simple-tags { display: flex; flex-wrap: wrap; gap: 6px; margin-top: -10px; padding: 0 24px 18px; }
.simple-footer { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 12px 22px; border-top: 1px solid var(--border-color, #2b2c37); font-size: 12px; color: var(--accent-purple, #b9aaff); }
.simple-footer button { font-size: 12px; padding: 5px 8px; }
.simple-more { align-self: center; font-size: 12px; }
@media (max-width: 640px) { .overview-header { flex-wrap: wrap; } .overview-header-actions { flex-wrap: wrap; } }

.agent-overview { position: absolute; inset: 0; z-index: 20; display: flex; flex-direction: column; padding: 28px 30px 20px; background: var(--bg-primary, #111218); color: var(--text-primary, #eeeef4); outline: none; overflow: hidden; }
.overview-filters { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 18px; }
.overview-filters button { font-size: 11px; padding: 7px 11px; }.overview-filters button span { opacity: .65; margin-left: 5px; }
.overview-filters button[aria-pressed="true"] { border-color: #9d8bff; background: #9d8bff18; }
.attention-hint { display: block; width: 100%; text-align: left; font-size: 12px; margin-bottom: 14px; padding: 9px 12px; border-color: #ffb45e66; background: #ffb45e14; color: var(--status-warning, #ffc98a); }
.view-toggle { display: flex; margin-left: 10px; }
.view-toggle button { border-radius: 0; font-size: 11px; padding: 7px 10px; } .view-toggle button:first-child { border-radius: 7px 0 0 7px; } .view-toggle button:last-child { border-radius: 0 7px 7px 0; border-left: 0; }
.view-toggle button[aria-pressed="true"], .summaries-toggle[aria-pressed="true"] { border-color: #9d8bff; background: #9d8bff18; }
.summaries-toggle { font-size: 11px; padding: 7px 10px; } .summaries-toggle[aria-pressed="false"] { opacity: .6; }
/* Tags: a bar of the tags in view, and chips on each agent. Clicking one filters. */
.tag-bar { display: flex; flex-wrap: wrap; gap: 6px; margin: -6px 0 16px; }
.tag-chip { font: 10px 'JetBrains Mono', monospace; padding: 3px 8px; border-radius: 999px; color: var(--accent-purple, #c9bdff); background: rgba(var(--accent-purple-rgb, 157, 139, 255), .08); border-color: rgba(var(--accent-purple-rgb, 157, 139, 255), .25); }
.tag-chip span { opacity: .6; margin-left: 3px; } .tag-chip[aria-pressed="true"] { background: var(--accent-purple, #9d8bff); color: var(--bg-primary, #140a24); border-color: var(--accent-purple, #9d8bff); }
.tag-chip.small { font-size: 9px; padding: 2px 7px; }
.tag-clear { font-size: 10px; padding: 3px 8px; border-radius: 999px; }
.column-digest { padding: 0 18px 12px; }
.digest-summary { margin: 0; font-size: 12px; line-height: 1.5; color: var(--text-primary, #dadbe5); }
.digest-summary.pending, .row-summary.pending { color: var(--text-secondary, #989aaa); font-style: italic; animation: digest-wait 1.6s ease-in-out infinite; }
@keyframes digest-wait { 50% { opacity: .45; } }
.digest-tags, .row-tags { display: flex; flex-wrap: wrap; gap: 5px; margin-top: 8px; }
/* List layout: one dense row per agent. */
.agent-list { flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 6px; padding: 2px 2px 8px; }
.agent-row { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; align-items: center; gap: 14px; padding-right: 16px; background: var(--bg-secondary, #181920); border: 1px solid var(--border-color, #2b2c37); border-left: 3px solid var(--agent-color); border-radius: 9px; transition: box-shadow .18s ease, border-color .18s ease; }
.agent-row.just-idle { border-left-style: dashed; }
.agent-row.selected { border-color: var(--agent-color); box-shadow: 0 0 0 1px var(--agent-color), 0 8px 26px -14px var(--agent-color); }
.agent-row.selected .row-main:focus-visible { outline: none; }
.row-main { display: grid; grid-template-columns: minmax(150px, 220px) minmax(0, 1fr); align-items: center; gap: 16px; padding: 11px 14px; border-radius: 9px 0 0 9px; }
.row-who { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.row-who strong { font-size: 13px; font-weight: 550; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.row-who small { font: 9px 'JetBrains Mono', monospace; color: var(--agent-color); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.row-summary { font-size: 12px; color: var(--text-primary, #dadbe5); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.row-tags { margin: 0; flex-wrap: nowrap; }
.row-meta { display: flex; align-items: center; gap: 12px; }
.row-context { width: 48px; height: 4px; border-radius: 4px; background: var(--bg-primary, #111218); overflow: hidden; }
.row-context span { display: block; height: 100%; background: var(--agent-color); }
.key-hint { margin-left: auto; display: flex; align-items: center; gap: 4px; font-size: 10px; color: var(--text-secondary, #989aaa); }
.key-hint kbd { font: 10px 'JetBrains Mono', monospace; padding: 1px 5px; border: 1px solid var(--border-color, #32333e); border-radius: 4px; }
.key-hint + .recent-window { margin-left: 16px; }
/* The keyboard cursor: the selected agent glows in its own colour. */
.agent-column { transition: box-shadow .18s ease, border-color .18s ease, transform .18s ease; }
.agent-column.selected { border-color: var(--agent-color); box-shadow: 0 0 0 1px var(--agent-color), 0 14px 38px -14px var(--agent-color); transform: translateY(-2px); }
.agent-column.selected .column-header { background: color-mix(in srgb, var(--agent-color) 9%, transparent); }
.agent-column.selected .column-header:focus-visible { outline: none; }
.recent-window { margin-left: auto; display: flex; gap: 8px; align-items: center; font-size: 11px; color: var(--text-secondary); }
.recent-window select { color: var(--text-primary); background: var(--bg-secondary); border: 1px solid var(--border-color); padding: 6px; border-radius: 6px; }
.attention-notice { padding: 10px 18px; font-size: 11px; line-height: 1.5; color: var(--status-warning, #e8bc82); background: #e8bc8210; max-height: 80px; overflow: auto; }
.recent-notice { padding: 10px 18px; display: flex; justify-content: space-between; gap: 8px; font-size: 10px; color: var(--text-secondary); }.recent-notice strong { color: var(--accent-purple, #b9aaff); font-weight: 500; }
.running-label.status-attention { color: var(--status-warning, #e8bc82); }.status-attention .live-dot { background: #e8bc82; box-shadow: none; }
.running-label.status-recent { color: var(--accent-purple, #b9aaff); }
/* Went idle moments ago: shown in Working, set apart from agents still at it. */
.agent-column.just-idle { border-top-style: dashed; }
.agent-column.just-idle .column-header { opacity: .85; }.status-recent .live-dot { background: #b9aaff; box-shadow: none; }
.show-more { align-self: center; flex: 0 0 180px; font-size: 12px; }
.overview-header { display: flex; align-items: center; justify-content: space-between; gap: 20px; margin-bottom: 25px; }
.overview-eyebrow { display: flex; align-items: center; gap: 8px; font: 10px 'JetBrains Mono', monospace; letter-spacing: .16em; color: var(--text-secondary, #989aaa); }
.live-dot { width: 6px; height: 6px; display: inline-block; border-radius: 50%; background: #69cda4; box-shadow: 0 0 8px #69cda444; flex-shrink: 0; }
h1 { font-size: 27px; letter-spacing: -.04em; margin: 9px 0 7px; font-weight: 550; } h1 span { font-size: 16px; color: var(--text-secondary, #989aaa); margin-left: 8px; }
.overview-header p { font-size: 12px; color: var(--text-secondary, #989aaa); margin: 0; }.header-divider { padding: 0 8px; opacity: .4; }
button { color: inherit; cursor: pointer; border: 1px solid var(--border-color, #32333e); border-radius: 7px; background: var(--bg-secondary, #1b1c24); padding: 9px 12px; font: inherit; }button:hover { border-color: #9d8bff; }button:focus-visible, summary:focus-visible { outline: 2px solid #9d8bff; outline-offset: 3px; }
.overview-header-actions { display: flex; align-items: center; gap: 8px; }
.overview-close { display: flex; align-items: center; gap: 14px; font-size: 12px; }.overview-close > span { font-size: 22px; color: var(--text-secondary); }kbd { color: var(--text-secondary, #999); font-size: 11px; }
.agent-columns { display: flex; align-items: stretch; gap: 16px; min-height: 0; flex: 1; overflow-x: auto; padding-bottom: 8px; scroll-snap-type: x proximity; }
.agent-column { width: 355px; min-width: 310px; flex: 1 0 310px; max-width: 480px; display: flex; flex-direction: column; min-height: 0; background: var(--bg-secondary, #181920); border: 1px solid var(--border-color, #2b2c37); border-top: 2px solid var(--agent-color); border-radius: 10px; overflow: hidden; scroll-snap-align: start; }
.column-header { padding: 18px 18px 13px; text-align: left; border: 0; border-radius: 0; background: transparent; width: 100%; }.column-header:hover { background: var(--bg-primary); }.column-header:focus-visible { outline-offset: -3px; }.column-project { color: var(--agent-color); text-transform: uppercase; font-size: 10px; letter-spacing: .1em; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }.column-title { display: flex; gap: 8px; align-items: center; justify-content: space-between; margin: 10px 0; }.column-title h2 { font-size: 16px; margin: 0; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; font-weight: 550; }.running-label { display: flex; align-items: center; gap: 5px; color: var(--status-success, #69cda4); font-size: 10px; flex-shrink: 0; }.column-model { display: flex; justify-content: space-between; gap: 10px; font: 10px 'JetBrains Mono', monospace; color: var(--text-secondary, #989aaa); overflow-wrap: anywhere; }.column-model > span { opacity: .6; }.view-mode { font-size: 8px; letter-spacing: .1em; padding: 1px 4px; margin-right: 6px; border: 1px solid currentColor; border-radius: 3px; }.column-now { margin-top: 9px; font: 10px 'JetBrains Mono', monospace; color: var(--text-primary, #dadbe5); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }.column-now span { color: var(--status-success, #69cda4); }
.column-context { padding: 12px 18px; border-top: 1px solid var(--border-color, #2b2c37); }.context-heading,.context-numbers { display: flex; align-items: center; justify-content: space-between; gap: 8px; font-size: 10px; color: var(--text-secondary, #989aaa); }.context-heading strong { color: var(--text-primary); font-weight: 500; }.context-track { height: 4px; background: var(--bg-primary, #111218); border-radius: 4px; overflow: hidden; margin: 9px 0; }.context-track span { display: block; height: 100%; background: var(--agent-color); transition: width .4s ease; }.context-numbers { font: 9px 'JetBrains Mono', monospace; }
.column-git { padding: 12px 18px; border-top: 1px solid var(--border-color, #2b2c37); }.git-branch { display: flex; align-items: center; gap: 8px; font-size: 12px; }.git-branch strong { font-weight: 500; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }.wt-label { font-size: 8px; padding: 2px 4px; border: 1px solid var(--agent-color); border-radius: 3px; color: var(--agent-color); }.worktree-location { color: var(--text-secondary); font: 9px monospace; margin-top: 5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.git-clean,.git-changes { font-size: 10px; color: var(--text-secondary, #989aaa); margin-top: 8px; }.git-changes summary { cursor: pointer; color: var(--accent-yellow, #d5b879); }.git-changes summary span { float: right; color: var(--text-secondary); }.git-changes ul { list-style: none; padding: 0; max-height: 100px; overflow: auto; font-family: monospace; }.git-changes li { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 3px 0; }
.activity-heading { display: flex; justify-content: space-between; padding: 13px 18px 10px; border-top: 1px solid var(--border-color, #2b2c37); color: var(--text-secondary, #989aaa); font-size: 9px; letter-spacing: .12em; }.activity-heading span { color: var(--status-success, #69cda4); font-size: 8px; }.activity-log { flex: 1; min-height: 100px; overflow-y: auto; overscroll-behavior: contain; padding: 0 18px 12px; }.log-entry { padding: 12px 0; border-bottom: 1px solid var(--border-color, #292a33); }.log-meta { display: flex; justify-content: space-between; align-items: center; gap: 10px; font: 9px 'JetBrains Mono', monospace; color: var(--text-secondary, #989aaa); }.tool-entry .log-meta > span { color: var(--agent-color); }.log-meta time { font-size: 8px; opacity: .65; }.log-entry pre { font: 11px/1.7 'JetBrains Mono', monospace; white-space: pre-wrap; overflow-wrap: anywhere; margin: 7px 0 0; color: var(--text-primary, #dadbe5); }.column-footer { display: flex; align-items: center; justify-content: space-between; min-height: 35px; padding: 6px 18px; font-size: 9px; color: var(--text-secondary, #989aaa); border-top: 1px solid var(--border-color, #2b2c37); }.column-footer button { font-size: 10px; padding: 4px 8px; }
.column-subagents { padding: 11px 18px 12px; border-top: 1px solid var(--border-color, #2b2c37); }.subagents-heading { display: flex; justify-content: space-between; color: var(--text-secondary, #989aaa); font-size: 9px; letter-spacing: .12em; }.subagents-heading strong { color: var(--status-success, #69cda4); font-weight: 500; }.subagent-list { list-style: none; margin: 8px 0 0; padding: 0; max-height: 190px; overflow-y: auto; overscroll-behavior: contain; }.subagent-list li { border-radius: 6px; }.subagent-list li.nested { margin-left: 14px; }.subagent-list summary { display: flex; flex-wrap: wrap; align-items: center; gap: 7px; padding: 5px 4px; cursor: pointer; list-style: none; font-size: 11px; border-radius: 6px; }.subagent-list summary::-webkit-details-marker { display: none; }.subagent-list summary:hover { background: var(--bg-primary, #111218); }.sub-fig { width: 18px; height: 24px; flex-shrink: 0; margin: -6px 0 -4px; }.sub-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; background: #9d8bff; }.sub-running .sub-dot { background: #69cda4; box-shadow: 0 0 8px #69cda466; animation: sub-pulse 1.6s ease-in-out infinite; }.sub-failed .sub-dot { background: #e8837f; }.sub-desc { flex: 1; min-width: 0; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }.sub-completed .sub-desc { color: var(--text-secondary, #989aaa); }.sub-now { flex-basis: 100%; padding-left: 25px; font: 9px 'JetBrains Mono', monospace; color: var(--text-secondary, #989aaa); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }.sub-meta { flex-shrink: 0; font: 9px 'JetBrains Mono', monospace; color: var(--text-secondary, #989aaa); }.sub-type { font: 9px 'JetBrains Mono', monospace; color: var(--agent-color); padding: 2px 4px 0 17px; }.subagent-list pre { font: 10px/1.6 'JetBrains Mono', monospace; white-space: pre-wrap; overflow-wrap: anywhere; margin: 4px 0 6px 17px; padding: 7px 9px; max-height: 150px; overflow-y: auto; background: var(--bg-primary, #111218); border-radius: 6px; color: var(--text-primary, #dadbe5); }.sub-more,.sub-loading { font-size: 10px; color: var(--text-secondary, #989aaa); padding: 6px 4px 0; }.visually-hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
@keyframes sub-pulse { 50% { opacity: .45; } }
.overview-empty { display: flex; flex: 1; flex-direction: column; align-items: center; justify-content: center; text-align: center; gap: 14px; color: var(--text-secondary, #989aaa); font-size: 13px; }.overview-empty h2 { color: var(--text-primary); font-weight: 500; margin: 0; }.overview-empty p { margin: 0; max-width: 330px; line-height: 1.7; }.empty-symbol { font-size: 64px; color: #9d8bff; }.overview-error { padding: 10px; color: var(--accent-yellow, #d5b879); font-size: 12px; }.log-empty { font-size: 11px; color: var(--text-secondary); padding-top: 15px; }
@media (max-width: 640px) { .agent-row { grid-template-columns: 1fr; padding: 0 0 10px; } .row-main { grid-template-columns: 1fr; gap: 6px; } .row-summary { white-space: normal; } .row-tags, .row-meta { padding: 0 14px; flex-wrap: wrap; } .key-hint { display: none; } .agent-overview { padding: 18px 12px 8px; }.overview-header { gap: 10px; align-items: flex-start; }h1 { font-size: 23px; }.overview-close { font-size: 0; gap: 7px; }.overview-header p { font-size: 10px; }.agent-column { flex-basis: 85vw; min-width: 280px; }.agent-columns { gap: 10px; } }
@media (prefers-reduced-motion: reduce) { .digest-summary.pending, .row-summary.pending { animation: none; } .agent-column, .agent-row { transition: none; } .agent-column.selected { transform: none; } .context-track span { transition: none; }.sub-running .sub-dot { animation: none; } }
</style>
