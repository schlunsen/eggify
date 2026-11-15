<template>
  <Teleport to="body">
    <section
      v-if="show"
      ref="panel"
      class="all-agents"
      :class="{ 'aa-show-detail': showDetailOnPhone }"
      role="dialog"
      aria-modal="true"
      aria-labelledby="all-agents-title"
      tabindex="-1"
      @keydown="onKeydown"
    >
      <header class="aa-header">
        <div class="aa-heading">
          <h1 id="all-agents-title">All agents</h1>
          <p class="aa-stats">
            <span class="aa-stat" :class="{ 'aa-stat-live': stats.sessionsWorking }"><strong>{{ stats.sessionsWorking }}</strong> {{ stats.sessionsWorking === 1 ? 'session' : 'sessions' }} working</span>
            <span class="aa-stat" :class="{ 'aa-stat-live': stats.subagentsRunning }"><strong>{{ stats.subagentsRunning }}</strong> {{ stats.subagentsRunning === 1 ? 'subagent' : 'subagents' }} running</span>
            <span v-if="stats.loopsActive" class="aa-stat aa-stat-loop"><strong>{{ stats.loopsActive }}</strong> {{ stats.loopsActive === 1 ? 'loop' : 'loops' }} scheduled</span>
            <span class="aa-stat"><strong>{{ stats.finishedLastHour }}</strong> finished in the last hour</span>
            <span class="aa-stat" :class="{ 'aa-stat-bad': stats.failedLastHour }"><strong>{{ stats.failedLastHour }}</strong> failed in the last hour</span>
          </p>
        </div>
        <nav class="aa-filters" aria-label="Filter agents">
          <button v-for="f in ALL_AGENTS_FILTERS" :key="f" type="button" :class="`aa-filter-${f}`" :aria-pressed="filter === f" @click="setFilter(f)">{{ FILTER_LABELS[f] }}</button>
        </nav>
        <input
          ref="search"
          v-model="query"
          class="aa-search"
          type="search"
          placeholder="Filter by session, tag, worker, subagent or loop"
          aria-label="Filter by session name, tag, worker role or model, subagent description or type, or loop prompt"
          autocomplete="off"
          spellcheck="false"
        >
        <span class="aa-loading" aria-hidden="true"><EggLoader v-if="loading && loaded" compact /></span>
        <button type="button" class="aa-close" aria-label="Close all agents" @click="emit('close')">
          <span class="aa-close-label">Close</span> <kbd>{{ toggleKey }}</kbd><span class="aa-close-x" aria-hidden="true">×</span>
        </button>
      </header>

      <div v-if="error || navigationError" class="aa-error" role="status">{{ navigationError || error }}</div>

      <div v-if="!loaded" class="aa-empty">
        <EggLoader compact />
        <p>Loading agents…</p>
      </div>
      <div v-else-if="!selectable.length" class="aa-empty">
        <span class="aa-empty-symbol" aria-hidden="true">◌</span>
        <h2>{{ emptyTitle }}</h2>
        <p v-if="filter === 'running' && !query">Sessions that are working, their subagents and their loops show up here live.</p>
        <button v-if="query" type="button" @click="query = ''">Clear the filter</button>
        <button v-else-if="filter !== 'all'" type="button" @click="setFilter('all')">Show all agents</button>
      </div>

      <div v-else class="aa-body">
        <div ref="tree" class="aa-tree" role="tree" aria-label="Sessions, loops, workers and subagents">
          <template v-for="row in rows" :key="row.key">
            <h2 v-if="row.kind === 'group'" class="aa-group" :class="`aa-group-${row.group}`" role="presentation">
              <span v-if="row.group !== 'recent'" class="aa-group-dot" aria-hidden="true" />{{ row.title }} <span class="aa-group-count">{{ row.count }}</span>
            </h2>

            <button
              v-else-if="row.kind === 'session'"
              type="button"
              role="treeitem"
              class="aa-row aa-session"
              :class="[`aa-session-${row.node.group}`, { 'aa-selected': row.key === selectedKey }]"
              :aria-level="1"
              :aria-expanded="row.hasChildren ? row.expanded : undefined"
              :aria-selected="row.key === selectedKey"
              :tabindex="row.key === selectedKey ? 0 : -1"
              :data-row-key="row.key"
              :style="{ '--aa-avatar': avatarOf(row.node.session).color }"
              @click="select(row.key, true)"
              @dblclick="openRow(row)"
            >
              <span class="aa-twist" :class="{ 'aa-twist-open': row.expanded, 'aa-twist-none': !row.hasChildren }" aria-hidden="true" @click.stop="row.hasChildren && toggleSession(row.node.session.id)" />
              <span class="aa-avatar" aria-hidden="true">
                <img v-if="!brokenAvatars[row.node.session.id]" :src="avatarOf(row.node.session).image" alt="" @error="brokenAvatars[row.node.session.id] = true">
                <span v-else>{{ row.node.name.charAt(0) || '?' }}</span>
              </span>
              <span class="aa-main">
                <span class="aa-title-line">
                  <span class="aa-name">{{ row.node.name }}</span>
                  <span v-for="tag in row.node.tags" :key="tag" class="aa-tag" :style="tagChipStyle(tag)">{{ tag }}</span>
                </span>
                <span class="aa-sub-line">
                  <span class="aa-status" :class="`aa-status-${row.node.group}`">{{ sessionStatusLabel(row.node) }}</span>
                  <template v-if="row.node.counts.total"><span class="aa-sep" aria-hidden="true">·</span><span>{{ subagentCountLabel(row.node) }}</span></template>
                </span>
              </span>
              <span class="aa-time">{{ activityAgo(Date.parse(row.node.session.updated_at || ''), now) }}</span>
            </button>

            <button
              v-else-if="row.kind === 'subagent'"
              type="button"
              role="treeitem"
              class="aa-row aa-agent"
              :class="[`aa-agent-${row.node.status}`, { 'aa-selected': row.key === selectedKey, 'aa-agent-last': row.last }]"
              :aria-level="row.depth + 1"
              :aria-selected="row.key === selectedKey"
              :tabindex="row.key === selectedKey ? 0 : -1"
              :data-row-key="row.key"
              :style="{ '--aa-depth': row.depth }"
              @click="select(row.key, true)"
              @dblclick="openRow(row)"
            >
              <span class="aa-dot" :title="STATUS_LABELS[row.node.status]"><span class="visually-hidden">{{ STATUS_LABELS[row.node.status] }}</span></span>
              <span class="aa-main">
                <span class="aa-title-line">
                  <span class="aa-name">{{ row.node.agent.description || row.node.agent.subagent_type || 'Subagent' }}</span>
                  <span class="aa-type">{{ row.node.agent.subagent_type || 'agent' }}</span>
                  <SubagentModelChip :label="modelLabel(row.node.agent.model)" :title="modelTitle(row.node.agent.model, row.node.agent.model_source)" :requested="row.node.agent.model_source === 'requested'" />
                </span>
                <span v-if="row.node.status === 'running'" class="aa-now" :class="{ 'aa-now-cmd': nowOf(row.node.agent).command }">
                  <span v-if="nowOf(row.node.agent).command" class="aa-prompt" aria-hidden="true">$</span>{{ nowOf(row.node.agent).text }}
                </span>
              </span>
              <span class="aa-time">{{ elapsedOf(row.node.agent) }}</span>
            </button>

            <button
              v-else-if="row.kind === 'loop'"
              type="button"
              role="treeitem"
              class="aa-row aa-loop"
              :class="[`aa-loop-${row.loop.phase}`, { 'aa-selected': row.key === selectedKey, 'aa-agent-last': row.last }]"
              :aria-level="2"
              :aria-selected="row.key === selectedKey"
              :tabindex="row.key === selectedKey ? 0 : -1"
              :data-row-key="row.key"
              @click="select(row.key, true)"
              @dblclick="openRow(row)"
            >
              <span class="aa-loop-icon" aria-hidden="true">↻</span>
              <span class="aa-main">
                <span class="aa-title-line">
                  <span class="aa-name">{{ row.loop.title }}</span>
                  <span class="aa-type">{{ row.loop.schedule }}</span>
                </span>
                <span class="aa-now">{{ LOOP_KIND_LABELS[row.loop.kind] }} · {{ loopWhen(row.loop, now) }}<template v-if="row.loop.kind !== 'eggify' && row.loop.phase === 'waiting' && row.loop.nextAt"> · {{ clockTime(row.loop.nextAt) }}</template></span>
              </span>
              <span class="aa-time">{{ loopWhenShort(row.loop, now) }}</span>
            </button>

            <button
              v-else-if="row.kind === 'worker'"
              type="button"
              role="treeitem"
              class="aa-row aa-loop aa-worker"
              :class="[`aa-worker-${row.worker.view.status}`, { 'aa-selected': row.key === selectedKey, 'aa-agent-last': row.last }]"
              :aria-level="2"
              :aria-selected="row.key === selectedKey"
              :tabindex="row.key === selectedKey ? 0 : -1"
              :data-row-key="row.key"
              @click="select(row.key, true)"
              @dblclick="openRow(row)"
            >
              <span class="aa-worker-dot" :title="WORKER_STATUS_LABELS[row.worker.view.status]"><span class="visually-hidden">{{ WORKER_STATUS_LABELS[row.worker.view.status] }}</span></span>
              <span class="aa-main">
                <span class="aa-title-line">
                  <span class="aa-name">{{ WORKER_ROLE_LABELS[row.worker.view.role] }} worker</span>
                  <SubagentModelChip :label="row.worker.view.modelLabel" :title="row.worker.view.model ? `Runs on ${row.worker.view.model}` : ''" />
                  <span v-if="row.worker.view.verdict" class="aa-type aa-worker-verdict" :class="`aa-verdict-${row.worker.view.verdict}`">{{ VERDICT_LABELS[row.worker.view.verdict] }}</span>
                </span>
                <span class="aa-now">{{ workerLine(row.worker) }}</span>
              </span>
              <span class="aa-time">{{ activityAgo(Date.parse(row.worker.session.updated_at || ''), now) }}</span>
            </button>

            <button
              v-else
              type="button"
              role="treeitem"
              class="aa-row aa-more"
              :class="{ 'aa-selected': row.key === selectedKey }"
              :aria-level="2"
              :aria-expanded="row.expanded"
              :aria-selected="row.key === selectedKey"
              :tabindex="row.key === selectedKey ? 0 : -1"
              :data-row-key="row.key"
              @click="select(row.key); toggleFinished(row.sessionId)"
            >
              <span class="aa-twist" :class="{ 'aa-twist-open': row.expanded }" aria-hidden="true" />
              <span class="aa-main">{{ row.count }} finished</span>
            </button>
          </template>
        </div>

        <div ref="detailPane" class="aa-detail" tabindex="-1" aria-live="off">
          <button type="button" class="aa-back" @click="showDetailOnPhone = false">‹ All agents</button>

          <template v-if="selectedRow?.kind === 'subagent' && detailItem">
            <div class="aa-detail-bar">
              <span class="aa-detail-session">{{ sessionNameOf(selectedRow.sessionId) }}</span>
              <button type="button" class="aa-open" @click="openRow(selectedRow)">Open in session <span aria-hidden="true">→</span></button>
            </div>
            <SubagentDetail :key="detailItem.id" :item="detailItem" :now="now" />
          </template>

          <section v-else-if="selectedRow?.kind === 'loop'" class="aa-loop-detail" :aria-label="selectedRow.loop.title">
            <div class="aa-detail-bar">
              <span class="aa-detail-session">{{ sessionNodeOf(selectedRow.sessionId)?.name }}</span>
              <button type="button" class="aa-open" @click="openRow(selectedRow)">Open session <span aria-hidden="true">→</span></button>
            </div>
            <header class="aa-ld-head">
              <span class="aa-loop-icon aa-loop-icon-big" aria-hidden="true">↻</span>
              <div>
                <p class="aa-ld-kind">{{ LOOP_KIND_LABELS[selectedRow.loop.kind] }}</p>
                <h2>{{ selectedRow.loop.schedule }}</h2>
                <span class="aa-ld-when" :class="`aa-loop-when-${selectedRow.loop.phase}`">{{ loopWhen(selectedRow.loop, now) }}</span>
              </div>
            </header>
            <dl class="aa-facts">
              <template v-if="selectedRow.loop.cron"><dt>Cron</dt><dd><code>{{ selectedRow.loop.cron }}</code><span class="aa-muted">{{ selectedRow.loop.recurring ? 'repeats' : 'runs once' }}</span></dd></template>
              <template v-if="selectedRow.loop.nextAt && selectedRow.loop.kind !== 'eggify'"><dt>Next run</dt><dd>{{ dateTime(selectedRow.loop.nextAt) }}<span v-if="selectedRow.loop.kind === 'cron'" class="aa-muted">the CLI may start it a few minutes later</span></dd></template>
              <template v-if="selectedRow.loop.lastRunAt"><dt>Last run</dt><dd>{{ dateTime(selectedRow.loop.lastRunAt) }} <span class="aa-muted">{{ activityAgo(selectedRow.loop.lastRunAt, now) }}</span></dd></template>
              <template v-if="selectedRow.loop.createdAt"><dt>Scheduled</dt><dd>{{ dateTime(selectedRow.loop.createdAt) }}</dd></template>
              <template v-if="selectedRow.loop.expiresAt"><dt>Expires</dt><dd>{{ dateTime(selectedRow.loop.expiresAt) }}</dd></template>
              <template v-if="selectedRow.loop.verifyCommand"><dt>Check</dt><dd><code>{{ selectedRow.loop.verifyCommand }}</code><span v-if="selectedRow.loop.verifyPassed !== undefined" :class="selectedRow.loop.verifyPassed ? 'aa-count-running' : 'aa-count-failed'">{{ selectedRow.loop.verifyPassed ? 'last check passed' : 'last check failed' }}</span></dd></template>
            </dl>
            <h3 v-if="selectedRow.loop.prompt" class="aa-ld-label">{{ selectedRow.loop.kind === 'eggify' ? 'Goal' : 'Prompt' }}</h3>
            <pre v-if="selectedRow.loop.prompt" class="aa-ld-prompt">{{ selectedRow.loop.prompt }}</pre>
            <h3 v-if="selectedRow.loop.verifyOutput" class="aa-ld-label">Last check output</h3>
            <pre v-if="selectedRow.loop.verifyOutput" class="aa-ld-prompt aa-ld-output">{{ selectedRow.loop.verifyOutput }}</pre>
            <p class="aa-ld-note aa-muted">{{ loopNote(selectedRow.loop) }}</p>
          </section>

          <section v-else-if="selectedRow?.kind === 'worker'" class="aa-loop-detail aa-worker-detail" :aria-label="`${WORKER_ROLE_LABELS[selectedRow.worker.view.role]} worker`">
            <div class="aa-detail-bar">
              <span class="aa-detail-session">{{ sessionNameOf(selectedRow.sessionId) }}</span>
              <button type="button" class="aa-open" @click="openRow(selectedRow)">Open session <span aria-hidden="true">→</span></button>
            </div>
            <header class="aa-ld-head">
              <div>
                <p class="aa-ld-kind">Worker session</p>
                <h2>{{ WORKER_ROLE_LABELS[selectedRow.worker.view.role] }} worker</h2>
                <span class="aa-ld-when" :class="{ 'aa-loop-when-running': selectedRow.worker.view.status === 'working' }">{{ WORKER_STATUS_LABELS[selectedRow.worker.view.status] }}</span>
              </div>
            </header>
            <dl class="aa-facts">
              <dt>Started by</dt><dd>{{ sessionNameOf(selectedRow.sessionId) }}</dd>
              <dt>Model</dt><dd>{{ selectedRow.worker.view.model || 'Unknown' }}</dd>
              <template v-if="selectedRow.worker.view.branch"><dt>Branch</dt><dd><code>{{ selectedRow.worker.view.branch }}</code><span v-if="workerOf(selectedRow.worker.session)?.base_branch" class="aa-muted">from {{ workerOf(selectedRow.worker.session)!.base_branch }}</span></dd></template>
              <template v-if="selectedRow.worker.view.role === 'review'"><dt>Access</dt><dd>Read-only</dd></template>
              <template v-if="selectedRow.worker.view.verdict"><dt>Review</dt><dd :class="`aa-verdict-${selectedRow.worker.view.verdict}`">{{ VERDICT_LABELS[selectedRow.worker.view.verdict] }}<span v-if="workerOf(selectedRow.worker.session)?.review_rounds" class="aa-muted">round {{ workerOf(selectedRow.worker.session)!.review_rounds }} of {{ workerOf(selectedRow.worker.session)!.max_review_rounds || DEFAULT_MAX_REVIEW_ROUNDS }}</span></dd></template>
              <template v-if="selectedRow.worker.subagentsRunning"><dt>Subagents</dt><dd class="aa-count-running">{{ selectedRow.worker.subagentsRunning }} running</dd></template>
            </dl>
            <h3 v-if="workerOf(selectedRow.worker.session)?.task" class="aa-ld-label">Task</h3>
            <pre v-if="workerOf(selectedRow.worker.session)?.task" class="aa-ld-prompt">{{ workerOf(selectedRow.worker.session)!.task }}</pre>
            <p class="aa-ld-note aa-muted">It runs sandboxed without permission prompts and reports back to the session that started it after each turn.</p>
          </section>

          <section v-else-if="selectedSession" class="aa-session-detail" :aria-label="selectedSession.name">
            <header class="aa-sd-head">
              <span class="aa-avatar aa-avatar-big" aria-hidden="true" :style="{ '--aa-avatar': avatarOf(selectedSession.session).color }">
                <img v-if="!brokenAvatars[selectedSession.session.id]" :src="avatarOf(selectedSession.session).image" alt="" @error="brokenAvatars[selectedSession.session.id] = true">
                <span v-else>{{ selectedSession.name.charAt(0) || '?' }}</span>
              </span>
              <div>
                <h2>{{ selectedSession.name }}</h2>
                <span class="aa-status" :class="`aa-status-${selectedSession.group}`">{{ sessionStatusLabel(selectedSession) }}</span>
              </div>
              <button type="button" class="aa-open aa-open-primary" @click="openSession(selectedSession.session)">Open session <span aria-hidden="true">→</span></button>
            </header>
            <p v-if="digestOf(selectedSession.session.id)?.summary" class="aa-digest">{{ digestOf(selectedSession.session.id)!.summary }}</p>
            <p v-else class="aa-digest aa-muted">No summary yet.</p>
            <dl class="aa-facts">
              <dt>Model</dt><dd>{{ selectedSession.session.model_name || selectedSession.session.options?.model || 'Unknown' }}</dd>
              <dt>Project</dt><dd>{{ projectName(selectedSession.session) }}</dd>
              <template v-if="branchOf(selectedSession.session)"><dt>Branch</dt><dd><code>{{ branchOf(selectedSession.session) }}</code></dd></template>
              <template v-if="selectedSession.tags.length"><dt>Tags</dt><dd><span v-for="tag in selectedSession.tags" :key="tag" class="aa-tag" :style="tagChipStyle(tag)">{{ tag }}</span></dd></template>
              <template v-if="digestOf(selectedSession.session.id)?.tags?.length"><dt>Topics</dt><dd><span v-for="tag in digestOf(selectedSession.session.id)!.tags" :key="tag" class="aa-tag aa-tag-ai">{{ tag }}</span></dd></template>
              <dt>Subagents</dt>
              <dd v-if="selectedSession.counts.total" class="aa-counts">
                <span v-if="selectedSession.counts.running" class="aa-count-running">{{ selectedSession.counts.running }} running</span>
                <span v-if="selectedSession.counts.done">{{ selectedSession.counts.done }} done</span>
                <span v-if="selectedSession.counts.failed" class="aa-count-failed">{{ selectedSession.counts.failed }} failed</span>
                <span v-if="selectedSession.counts.interrupted">{{ selectedSession.counts.interrupted }} interrupted</span>
              </dd>
              <dd v-else class="aa-muted">None in the last 24 hours</dd>
              <template v-if="selectedSession.loops.length">
                <dt>Loops</dt>
                <dd class="aa-loop-list"><span v-for="loop in selectedSession.loops" :key="loop.id">↻ {{ loop.schedule }} · {{ loopWhen(loop, now) }}</span></dd>
              </template>
            </dl>
          </section>
        </div>
      </div>
    </section>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, shallowRef, watch } from 'vue'
import type { BackgroundAgent } from '~/composables/agents/useBackgroundAgents'
import { useAllAgents } from '~/composables/agents/useAllAgents'
import { useSessionDigests } from '~/composables/agents/useSessionDigests'
import { useCharacterAvatar } from '~/composables/useCharacterAvatar'
import { getAvatarImageUrl, type Avatar } from '~/composables/useAvatarThemes'
import { useClosable, useShortcutLabels } from '~/composables/useKeyboardShortcuts'
import {
  ALL_AGENTS_FILTERS, allAgentsStats, buildAllAgentsTree, flattenAllAgents, selectableRows,
  type AllAgentsFilter, type AllAgentsRow, type AllAgentsSession, type SessionNode, type WorkerNode
} from '~/utils/agents/allAgentsTree'
import { DEFAULT_MAX_REVIEW_ROUNDS, VERDICT_LABELS, WORKER_ROLE_LABELS, WORKER_STATUS_LABELS, workerOf } from '~/utils/agents/team'
import { workersWorkingLabel } from '~/utils/agents/sessionGroups'
import { activityAgo, formatDuration, STATUS_LABELS, subagentNowAction } from '~/utils/agents/subagentActivity'
import { LOOP_KIND_LABELS, loopWhen, loopWhenShort, type SessionLoop } from '~/utils/agents/sessionLoops'
import { buildSubagentItem, type SubagentItem } from '~/utils/agents/subagentItem'
import { modelLabel, modelTitle } from '~/utils/agents/modelLabel'
import SubagentModelChip from '~/components/agents/SubagentModelChip.vue'
import { latestTool, subagentTimeline } from '~/utils/agents/subagentTimeline'
import { sessionTagList, sessionTagsVersion } from '~/utils/sessionTags'
import { tagChipStyle } from '~/utils/tagColor'
import SubagentDetail from '~/components/agents/SubagentDetail.vue'
import EggLoader from '~/components/ui/EggLoader.vue'

const props = defineProps<{ show: boolean; navigationError?: string }>()
const emit = defineEmits<{
  close: []
  /** Open a session; with agentId, its Subagents view with that subagent. */
  open: [payload: { session: AllAgentsSession; agentId?: string }]
}>()

const FILTER_LABELS: Record<AllAgentsFilter, string> = { running: 'Running', all: 'All', failed: 'Failed' }
const FILTER_KEY = 'eggify-all-agents-filter'
const toggleKey = useShortcutLabels().label('allagents.toggle')
useClosable(() => props.show, () => emit('close'), { modal: true })

const { sessions, subagents, projects, loading, loaded, error, start, stop, loadSessionSubagents } = useAllAgents()
const { loadCached: loadCachedDigest, digests } = useSessionDigests()

function readFilter(): AllAgentsFilter {
  try {
    const saved = localStorage.getItem(FILTER_KEY)
    if (saved === 'all' || saved === 'failed' || saved === 'running') return saved
  } catch { /* storage unavailable */ }
  return 'running'
}
const filter = ref<AllAgentsFilter>('running')
function setFilter(next: AllAgentsFilter) {
  filter.value = next
  try { localStorage.setItem(FILTER_KEY, next) } catch { /* storage unavailable */ }
}
const query = ref('')
const now = ref(Date.now())
const collapsed = ref(new Set<string>())
const expandedFinished = ref(new Set<string>())
const brokenAvatars = ref<Record<string, boolean>>({})
const showDetailOnPhone = ref(false)
const panel = ref<HTMLElement | null>(null)
const tree = ref<HTMLElement | null>(null)
const detailPane = ref<HTMLElement | null>(null)
const search = ref<HTMLInputElement | null>(null)

// ── Sessions: avatar, name, tags (as the sidebar shows them) ────────────────
function avatarOf(session: AllAgentsSession) {
  const chosen = session.selected_avatar
  if (chosen?.id) return { name: chosen.name || '', image: getAvatarImageUrl(chosen as Avatar), color: chosen.color || '#95A5A6' }
  const character = useCharacterAvatar(session.id)
  return { name: character.name, image: character.avatar, color: character.color }
}
const nameOf = (session: AllAgentsSession) => avatarOf(session).name || session.options?.agent_name || `Session ${session.id.slice(0, 8)}`
function tagsOf(session: AllAgentsSession) {
  const local = sessionTagList(session.id)
  return local.length ? local : session.tags || []
}

// ── The tree ────────────────────────────────────────────────────────────────
const groups = computed(() => {
  void sessionTagsVersion.value
  return buildAllAgentsTree({ sessions: sessions.value, subagents: subagents.value, filter: filter.value, query: query.value, now: now.value, nameOf, tagsOf })
})
const stats = computed(() => allAgentsStats(sessions.value, subagents.value, now.value))
const rows = computed(() => flattenAllAgents(groups.value, { collapsed: collapsed.value, expandedFinished: expandedFinished.value }))
const selectable = computed(() => selectableRows(rows.value))
const sessionNodes = computed(() => new Map(groups.value.flatMap(g => g.sessions).map(n => [n.session.id, n])))
const sessionNodeOf = (id: string) => sessionNodes.value.get(id)
// A worker is not a session row; its subagent rows still belong to its session.
const sessionOf = (id: string) => sessionNodeOf(id)?.session ?? sessions.value.find(s => s.id === id)
function sessionNameOf(id: string) {
  const node = sessionNodeOf(id)
  if (node) return node.name
  const session = sessionOf(id)
  if (!session) return ''
  const worker = workerOf(session)
  return worker ? `${WORKER_ROLE_LABELS[worker.mode === 'review' ? 'review' : 'build']} worker` : nameOf(session)
}
const emptyTitle = computed(() => query.value ? 'Nothing matches this filter'
  : filter.value === 'running' ? 'No agents are running' : filter.value === 'failed' ? 'No failed subagents' : 'All quiet for now')

function toggleSession(id: string) {
  const next = new Set(collapsed.value)
  if (next.has(id)) next.delete(id); else next.add(id)
  collapsed.value = next
}
function toggleFinished(sessionId: string) {
  const next = new Set(expandedFinished.value)
  if (next.has(sessionId)) next.delete(sessionId); else next.add(sessionId)
  expandedFinished.value = next
}

// ── Rows: labels ────────────────────────────────────────────────────────────
function sessionStatusLabel(node: SessionNode) {
  const s = node.session
  if (node.group === 'needs') return s.pending_questions ? 'Question waiting' : 'Approval needed'
  if (node.group === 'working') {
    if (s.status === 'processing' || s.status === 'active') return 'Working'
    const workersWorking = node.workers.filter(w => w.view.status === 'working').length
    if (workersWorking) return `Idle · ${workersWorkingLabel(workersWorking)}`
    // The count line next to it says how many subagents run.
    if (node.counts.total) return 'Idle'
    const n = s.subagents_running || 0
    if (n) return `Idle · ${n} ${n === 1 ? 'subagent' : 'subagents'} working`
    const loop = node.loops[0]
    return loop ? `Idle · loop ${loopWhen(loop, now.value)}` : 'Idle'
  }
  return s.status === 'ended' ? 'Stopped' : s.status === 'error' ? 'Failed' : 'Idle'
}
function subagentCountLabel(node: SessionNode) {
  const { running, total } = node.counts
  return running ? `${running} of ${total} subagents running` : `${total} ${total === 1 ? 'subagent' : 'subagents'}`
}
/** "Working · branch worker/x · 2 subagents running" */
function workerLine(worker: WorkerNode) {
  const parts = [WORKER_STATUS_LABELS[worker.view.status]]
  if (worker.view.role === 'build' && worker.view.branch) parts.push(`branch ${worker.view.branch}`)
  if (worker.view.role === 'review') parts.push('read-only')
  if (worker.subagentsRunning) parts.push(`${worker.subagentsRunning} ${worker.subagentsRunning === 1 ? 'subagent' : 'subagents'} running`)
  return parts.join(' · ')
}
function elapsedOf(agent: BackgroundAgent) {
  const started = Date.parse(agent.created_at)
  if (!Number.isFinite(started)) return ''
  const running = agent.status === 'running' || agent.status === 'waiting'
  const end = running ? now.value : Date.parse(agent.completed_at || agent.updated_at || '') || now.value
  return formatDuration(end - started)
}
// The "Now" line of a running subagent: its latest command, or what it does.
const nowCache = new WeakMap<BackgroundAgent, { text: string; command: boolean }>()
function nowOf(agent: BackgroundAgent) {
  let cached = nowCache.get(agent)
  if (cached) return cached
  const lines = agent.recent_output?.length ? agent.recent_output : agent.last_output ? [agent.last_output] : []
  const action = subagentNowAction(lines)
  const latest = action.kind === 'tool' ? latestTool(subagentTimeline(lines, false)) : null
  cached = latest?.detailKind === 'command' ? { text: latest.detail, command: true } : { text: latest ? latest.text : action.text, command: false }
  nowCache.set(agent, cached)
  return cached
}
const clockTime = (t: number) => new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
const dateTime = (t: number) => new Date(t).toLocaleString([], { weekday: 'short', hour: '2-digit', minute: '2-digit', month: 'short', day: 'numeric' })
function loopNote(loop: SessionLoop) {
  if (loop.kind === 'eggify') return 'Eggify sends the next iteration until the check passes or the iterations run out. Stop it from the session.'
  const what = loop.kind === 'cron' ? 'This job' : 'This /loop'
  return `${what} runs inside the session's Claude Code and ends when the session's CLI closes (ending the session, switching model, or restarting Eggify). To stop it sooner, ask the agent in the session.`
}
const projectName = (s: AllAgentsSession) => (s.project_id && projects.value[s.project_id]?.name) ||
  s.options?.working_directory?.split('/').filter(Boolean).pop() || 'No project'
const branchOf = (s: AllAgentsSession) => s.options?.workspace?.branch || s.git_branch || ''
const digestOf = (id: string) => digests.value[id]

// ── Selection and keyboard ──────────────────────────────────────────────────
const selectedKey = ref('')
let selectedIndex = 0
watch(selectable, list => {
  const index = list.findIndex(r => r.key === selectedKey.value)
  if (index >= 0) { selectedIndex = index; return }
  if (!list.length) { selectedKey.value = ''; return }
  selectedKey.value = list[Math.min(selectedIndex, list.length - 1)]!.key
}, { immediate: true })
const selectedRow = computed(() => selectable.value.find(r => r.key === selectedKey.value) ?? null)
const selectedSession = computed<SessionNode | null>(() => {
  const row = selectedRow.value
  if (!row) return null
  if (row.kind === 'session') return row.node
  if (row.kind === 'more') return sessionNodeOf(row.sessionId) ?? null
  return null
})

function rowElement(key: string) {
  return [...(tree.value?.querySelectorAll<HTMLElement>('[data-row-key]') || [])].find(el => el.dataset.rowKey === key)
}
function select(key: string, fromClick = false) {
  if (selectedKey.value !== key) detailPane.value?.scrollTo?.({ top: 0 })
  selectedKey.value = key
  const index = selectable.value.findIndex(r => r.key === key)
  if (index >= 0) selectedIndex = index
  if (fromClick) showDetailOnPhone.value = true
}
function moveTo(key: string | undefined, focus = true) {
  if (!key) return
  select(key)
  nextTick(() => {
    const el = rowElement(key)
    if (focus && document.activeElement !== search.value) el?.focus()
    el?.scrollIntoView?.({ block: 'nearest' })
  })
}

function openSession(session: AllAgentsSession, agentId?: string) {
  emit('open', agentId ? { session, agentId } : { session })
}
function openRow(row: AllAgentsRow | null) {
  if (!row || row.kind === 'group') return
  if (row.kind === 'more') { toggleFinished(row.sessionId); return }
  if (row.kind === 'session') { openSession(row.node.session); return }
  // A worker row opens the worker's own session.
  if (row.kind === 'worker') { openSession(row.worker.session); return }
  const session = sessionOf(row.sessionId)
  if (row.kind === 'loop') { if (session) openSession(session); return }
  if (session) openSession(session, row.node.agent.agent_id)
}

function onKeydown(event: KeyboardEvent) {
  if (event.defaultPrevented || event.isComposing || event.metaKey || event.ctrlKey || event.altKey) return
  const inSearch = event.target === search.value
  if (event.key === 'Tab') { trapTab(event); return }
  const list = selectable.value
  if (!list.length) return
  const index = list.findIndex(r => r.key === selectedKey.value)
  const row = index >= 0 ? list[index]! : null
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault(); moveTo(list[Math.min(list.length - 1, index + 1)]?.key, !inSearch); return
    case 'ArrowUp':
      event.preventDefault(); moveTo(list[Math.max(0, index - 1)]?.key, !inSearch); return
    case 'Home':
    case 'End':
      if (inSearch) return
      event.preventDefault(); moveTo((event.key === 'Home' ? list[0] : list[list.length - 1])?.key); return
    case 'ArrowRight':
      if (inSearch || !row) return
      event.preventDefault()
      if (row.kind === 'session' && row.hasChildren && !row.expanded) toggleSession(row.node.session.id)
      else if (row.kind === 'more' && !row.expanded) toggleFinished(row.sessionId)
      else if ((row.kind === 'session' && row.expanded) || (row.kind === 'more' && row.expanded)) moveTo(list[index + 1]?.key)
      return
    case 'ArrowLeft':
      if (inSearch || !row) return
      event.preventDefault()
      if (row.kind === 'session' && row.expanded) toggleSession(row.node.session.id)
      else if (row.kind === 'more' && row.expanded) toggleFinished(row.sessionId)
      else if (row.kind === 'subagent' || row.kind === 'more' || row.kind === 'loop' || row.kind === 'worker') moveTo(row.parentKey)
      return
    case 'Enter':
      if (!row) return
      event.preventDefault(); openRow(row); return
  }
}
function trapTab(event: KeyboardEvent) {
  const elements = [...(panel.value?.querySelectorAll<HTMLElement>('button:not([disabled]), input, [href], [tabindex="0"]') || [])]
    .filter(el => el.offsetParent !== null || el === document.activeElement)
  const first = elements[0], last = elements[elements.length - 1]
  if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.value)) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
}

// ── Detail: the selected subagent with its full output ──────────────────────
const sessionDetailAgents = shallowRef<{ sessionId: string; agents: BackgroundAgent[] } | null>(null)
let detailRequest = 0
async function loadDetail(sessionId: string) {
  const ticket = ++detailRequest
  const agents = await loadSessionSubagents(sessionId)
  if (ticket !== detailRequest || !agents) return
  sessionDetailAgents.value = { sessionId, agents }
}
const detailItem = computed<SubagentItem | null>(() => {
  const row = selectedRow.value
  if (row?.kind !== 'subagent') return null
  const listed = row.node.agent
  const full = sessionDetailAgents.value?.sessionId === row.sessionId
    ? sessionDetailAgents.value.agents.find(a => a.agent_id === listed.agent_id)
    : undefined
  // The listing is newer for status; the session's own list has the full output.
  const agent = full && full.updated_at >= listed.updated_at
    ? full
    : full?.recent_output?.length
      ? { ...listed, recent_output: full.recent_output, recent_output_at: full.recent_output_at }
      : listed
  const lines = agent.recent_output?.length ? agent.recent_output : agent.last_output ? [agent.last_output] : []
  const times = agent.recent_output?.length && agent.recent_output_at?.length === lines.length ? agent.recent_output_at : []
  const parent = agent.parent_agent_id ? subagents.value.find(a => a.agent_id === agent.parent_agent_id) : undefined
  return buildSubagentItem(agent, lines, parent ? parent.description || parent.subagent_type || 'another subagent' : '', times)
})
// Reload the full output when the selection moves, and on each poll while it runs.
watch([selectedKey, subagents], ([key], [oldKey]) => {
  const row = selectedRow.value
  if (row?.kind !== 'subagent') return
  if (key !== oldKey || row.node.status === 'running') void loadDetail(row.sessionId)
})
watch(selectedSession, node => { if (node) void loadCachedDigest({ id: node.session.id, status: node.session.status, message_count: node.session.message_count }) })

// ── Open / close ────────────────────────────────────────────────────────────
let clock: ReturnType<typeof setInterval> | undefined
let previousFocus: Element | null = null
function open() {
  filter.value = readFilter()
  query.value = ''
  showDetailOnPhone.value = false
  previousFocus = document.activeElement
  now.value = Date.now()
  clock = setInterval(() => { now.value = Date.now() }, 1000)
  start()
  nextTick(() => panel.value?.focus())
}
function close() {
  stop()
  if (clock) clearInterval(clock)
  clock = undefined
  if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus()
}
watch(() => props.show, (show, was) => { if (show) open(); else if (was) close() }, { immediate: true })
// When the list first arrives, put focus on the selected row so the arrows work at once.
watch(loaded, value => { if (value && props.show) nextTick(() => { if (document.activeElement === panel.value) rowElement(selectedKey.value)?.focus() }) })
</script>

<style scoped>
.all-agents {
  position: fixed; inset: 0; z-index: 10040; display: flex; flex-direction: column;
  padding: 14px 20px 0; background: var(--bg-primary); color: var(--text-primary); outline: none; overflow: hidden; font-size: 14px;
}
.aa-header { display: flex; align-items: center; gap: 10px 16px; flex-wrap: wrap; padding-bottom: 12px; border-bottom: 1px solid var(--border-color); }
.aa-heading { display: flex; align-items: baseline; gap: 16px; min-width: 0; flex-wrap: wrap; }
h1 { margin: 0; font-size: 20px; font-weight: 700; letter-spacing: -.01em; white-space: nowrap; }
.aa-stats { display: flex; flex-wrap: wrap; gap: 4px 14px; margin: 0; font-size: 12.5px; color: var(--text-secondary); }
.aa-stat strong { font-variant-numeric: tabular-nums; color: var(--text-primary); margin-right: 3px; }
.aa-stat-live strong { color: var(--status-success); }
.aa-stat-bad strong { color: var(--status-error); }
.aa-filters { display: flex; gap: 6px; }
.aa-filters button { border-radius: 999px; padding: 3px 12px; font-size: 12.5px; }
.aa-search { flex: 1 1 200px; max-width: 320px; min-width: 0; padding: 6px 10px; border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-secondary); color: var(--text-primary); font: inherit; font-size: 13px; }
.aa-search:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 1px; }
.aa-loading { display: inline-flex; width: 22px; justify-content: center; }
.aa-close { display: flex; align-items: center; gap: 8px; margin-left: auto; }
.aa-close-x { font-size: 18px; line-height: 1; color: var(--text-secondary); }
kbd { color: var(--text-secondary); font: 11px var(--font-mono, monospace); }
button { color: inherit; cursor: pointer; border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-secondary); padding: 6px 11px; font: inherit; font-size: 13px; }
button:hover:not([disabled]) { border-color: var(--accent-purple); }
button:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }
button[aria-pressed="true"] { color: var(--accent-purple); font-weight: 650; border-color: var(--accent-purple); background: color-mix(in srgb, var(--accent-purple) 12%, transparent); }
.aa-error { padding: 8px 0 0; color: var(--status-warning); font-size: 13px; }

.aa-empty { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; text-align: center; color: var(--text-secondary); }
.aa-empty h2 { margin: 0; color: var(--text-primary); font-weight: 550; }
.aa-empty p { margin: 0; max-width: 380px; line-height: 1.6; }
.aa-empty-symbol { font-size: 60px; color: var(--accent-purple); }

/* Two panes: the tree and the selected session or subagent. */
.aa-body { flex: 1; min-height: 0; display: grid; grid-template-columns: minmax(320px, 460px) minmax(0, 1fr); }
.aa-tree { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 8px 12px 20px 0; border-right: 1px solid var(--border-color); display: flex; flex-direction: column; gap: 1px; }
.aa-group { display: flex; align-items: center; gap: 7px; margin: 14px 8px 6px; font-size: 11px; font-weight: 750; letter-spacing: .09em; text-transform: uppercase; color: var(--text-secondary); }
.aa-group:first-child { margin-top: 4px; }
.aa-group-count { font-weight: 500; letter-spacing: 0; }
.aa-group-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--status-success); }
.aa-group-needs .aa-group-dot { background: var(--status-warning); }

.aa-row {
  --aa-accent: var(--text-secondary);
  position: relative; display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: start; gap: 9px;
  width: 100%; padding: 7px 10px; border: 1px solid transparent; border-radius: 10px; background: transparent; text-align: left;
}
.aa-row:hover:not([disabled]) { border-color: transparent; background: color-mix(in srgb, var(--text-primary) 5%, transparent); }
.aa-row:focus-visible { outline-offset: -2px; }
.aa-selected, .aa-selected:hover:not([disabled]) { background: color-mix(in srgb, var(--accent-purple) 13%, transparent); border-color: color-mix(in srgb, var(--accent-purple) 45%, transparent); }
.aa-session { grid-template-columns: 12px 30px minmax(0, 1fr) auto; align-items: center; margin-top: 4px; }
.aa-twist { width: 12px; height: 12px; display: inline-flex; align-items: center; justify-content: center; color: var(--text-secondary); font-size: 10px; }
.aa-twist::before { content: '▸'; transition: transform .12s ease; }
.aa-twist-open::before { transform: rotate(90deg); }
.aa-twist-none::before { content: ''; }
.aa-avatar { width: 30px; height: 30px; border-radius: 50%; overflow: hidden; display: inline-flex; align-items: center; justify-content: center; background: var(--aa-avatar); box-shadow: 0 0 0 2px color-mix(in srgb, var(--aa-avatar) 55%, transparent); font-weight: 700; color: var(--bg-primary); }
.aa-avatar img { width: 100%; height: 100%; object-fit: cover; }
.aa-avatar-big { width: 48px; height: 48px; flex: none; }
.aa-main { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.aa-title-line { display: flex; align-items: baseline; gap: 6px; min-width: 0; flex-wrap: wrap; }
.aa-name { font-weight: 600; line-height: 1.3; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 100%; }
/* The user's tags: the sidebar's chips (utils/tagColor.ts), same colour per tag. */
.aa-tag {
  --tag-base: hsl(var(--tag-hue, 260) 75% 50%);
  max-width: 11em; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  font-size: 0.72rem; font-weight: 600; line-height: 1.3; padding: 1px 8px; border-radius: 999px;
  color: color-mix(in oklab, var(--tag-base) 45%, var(--text-primary));
  background: color-mix(in srgb, var(--tag-base) 15%, var(--card-bg));
  border: 1px solid color-mix(in srgb, var(--tag-base) 50%, var(--card-bg));
}
/* AI topics stay quiet so they don't compete with the user's tags. */
.aa-tag-ai { color: var(--text-secondary); background: transparent; border-color: var(--border-color); font-weight: 500; }
.aa-sub-line { display: flex; gap: 6px; font-size: 12px; color: var(--text-secondary); }
.aa-status-needs { color: var(--status-warning); font-weight: 600; }
.aa-status-working { color: var(--status-success); font-weight: 600; }
.aa-sep { opacity: .6; }
.aa-time { font: 600 12px var(--font-mono, monospace); font-variant-numeric: tabular-nums; color: var(--text-secondary); white-space: nowrap; padding-top: 2px; }

/* Subagents hang from their session (and from each other) on a connector line. */
.aa-agent { padding-left: calc(30px + var(--aa-depth, 1) * 20px); grid-template-columns: 12px minmax(0, 1fr) auto; }
.aa-agent::before { content: ''; position: absolute; left: calc(14px + var(--aa-depth, 1) * 20px); top: -2px; bottom: -2px; border-left: 1px solid var(--border-color); }
.aa-agent.aa-agent-last::before { bottom: 50%; }
.aa-agent::after { content: ''; position: absolute; left: calc(14px + var(--aa-depth, 1) * 20px); top: 50%; width: 10px; border-top: 1px solid var(--border-color); }
.aa-agent-running { --aa-accent: var(--status-success); }
.aa-agent-done { --aa-accent: var(--accent-cyan, var(--accent-purple)); }
.aa-agent-failed { --aa-accent: var(--status-error); }
.aa-agent-interrupted { --aa-accent: var(--status-warning); }
.aa-dot { margin-top: 5px; width: 9px; height: 9px; border-radius: 50%; background: var(--aa-accent); }
.aa-agent-running .aa-dot { animation: aa-pulse 1.4s ease-in-out infinite; }
.aa-agent-done .aa-dot { background: transparent; border: 2px solid var(--aa-accent); }
.aa-agent-interrupted .aa-dot { border-radius: 2px; }
.aa-agent .aa-name { font-weight: 550; font-size: 13.5px; }
.aa-type { font-size: 11.5px; color: var(--text-secondary); white-space: nowrap; }
.aa-now { font-size: 12px; color: var(--text-secondary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.aa-now-cmd { font: 11.5px/1.45 var(--font-mono, ui-monospace, monospace); color: var(--text-primary); }
.aa-prompt { color: var(--status-success); font-weight: 700; margin-right: 6px; }
.aa-agent-running .aa-time { color: var(--status-success); }
/* Loops hang from their session like subagents, above them. */
.aa-loop { --aa-depth: 1; padding-left: 50px; grid-template-columns: 14px minmax(0, 1fr) auto; }
.aa-loop::before { content: ''; position: absolute; left: 34px; top: -2px; bottom: -2px; border-left: 1px solid var(--border-color); }
.aa-loop.aa-agent-last::before { bottom: 50%; }
.aa-loop::after { content: ''; position: absolute; left: 34px; top: 50%; width: 10px; border-top: 1px solid var(--border-color); }
.aa-loop-icon { margin-top: 1px; font-weight: 700; color: var(--accent-purple); line-height: 1.2; }
.aa-loop-running .aa-loop-icon { color: var(--status-success); animation: aa-spin 2.4s linear infinite; display: inline-block; }
.aa-loop-due .aa-loop-icon { color: var(--status-warning); }
.aa-loop .aa-name { font-weight: 550; font-size: 13.5px; }
.aa-loop-running .aa-time { color: var(--status-success); }
.aa-stat-loop strong { color: var(--accent-purple); }
.aa-loop-detail { max-width: 860px; }
/* A worker session under the session that started it */
.aa-worker { grid-template-columns: 12px minmax(0, 1fr) auto; }
.aa-worker-dot { margin-top: 5px; width: 9px; height: 9px; border-radius: 50%; border: 2px solid var(--text-secondary); opacity: .6; }
.aa-worker-working .aa-worker-dot { border: 0; background: var(--status-success); opacity: 1; animation: aa-pulse 1.4s ease-in-out infinite; }
.aa-worker-waiting .aa-worker-dot { border: 0; background: var(--status-warning); opacity: 1; }
.aa-worker-ended .aa-main { opacity: .65; }
.aa-worker-working .aa-time { color: var(--status-success); }
.aa-verdict-approve { color: var(--status-success); }
.aa-verdict-changes_requested { color: var(--status-warning); }
@media (prefers-reduced-motion: reduce) {
  .aa-worker-working .aa-worker-dot { animation: none; }
}
.aa-ld-head { display: flex; align-items: center; gap: 14px; margin-bottom: 18px; }
.aa-ld-head h2 { margin: 0 0 2px; font-size: 20px; font-weight: 650; }
.aa-ld-kind { margin: 0 0 2px; font-size: 11px; font-weight: 750; letter-spacing: .09em; text-transform: uppercase; color: var(--text-secondary); }
.aa-loop-icon-big { font-size: 30px; width: 48px; height: 48px; display: inline-flex; align-items: center; justify-content: center; border-radius: 50%; background: color-mix(in srgb, var(--accent-purple) 13%, transparent); }
.aa-ld-when { font-size: 13px; font-weight: 600; color: var(--accent-purple); }
.aa-loop-when-running { color: var(--status-success); }
.aa-loop-when-due { color: var(--status-warning); }
.aa-ld-label { margin: 20px 0 6px; font-size: 11px; font-weight: 750; letter-spacing: .09em; text-transform: uppercase; color: var(--text-secondary); }
.aa-ld-prompt { margin: 0; padding: 10px 12px; border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-secondary); white-space: pre-wrap; overflow-wrap: anywhere; font: 12.5px/1.55 var(--font-mono, ui-monospace, monospace); color: var(--text-primary); max-height: 320px; overflow-y: auto; }
.aa-ld-note { margin: 18px 0 0; font-size: 12.5px; line-height: 1.6; }
.aa-loop-list { flex-direction: column; gap: 2px !important; }
.aa-more { padding-left: 50px; grid-template-columns: 12px minmax(0, 1fr); font-size: 12.5px; color: var(--text-secondary); }

.aa-detail { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 18px 4px 28px 24px; outline: none; }
.aa-detail > :deep(.sd) { max-width: 1100px; }
.aa-detail-bar { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 12px; font-size: 12.5px; color: var(--text-secondary); }
.aa-open-primary { margin-left: auto; color: var(--accent-purple); border-color: color-mix(in srgb, var(--accent-purple) 45%, transparent); }
.aa-back { display: none; }
.aa-session-detail { max-width: 760px; }
.aa-sd-head { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; }
.aa-sd-head h2 { margin: 0 0 2px; font-size: 20px; font-weight: 650; }
.aa-digest { margin: 18px 0; font-size: 15px; line-height: 1.6; }
.aa-muted { color: var(--text-secondary); }
.aa-facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: 8px 18px; margin: 0; font-size: 13px; }
.aa-facts dt { color: var(--text-secondary); }
.aa-facts dd { margin: 0; display: flex; flex-wrap: wrap; gap: 6px; overflow-wrap: anywhere; }
.aa-counts span + span::before { content: '·'; margin-right: 6px; color: var(--text-secondary); }
.aa-count-running { color: var(--status-success); }
.aa-count-failed { color: var(--status-error); }
.visually-hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
@keyframes aa-pulse { 50% { opacity: .35; } }
@keyframes aa-spin { to { transform: rotate(360deg); } }

@media (max-width: 760px) {
  .all-agents { padding: 12px 16px 0; }
  .aa-close-label, .aa-close kbd { display: none; }
  .aa-search { max-width: none; order: 4; flex-basis: 100%; }
  .aa-body { grid-template-columns: minmax(0, 1fr); }
  .aa-tree { border-right: none; padding-right: 0; }
  .aa-detail { display: none; padding: 10px 0 24px; }
  .aa-show-detail .aa-tree { display: none; }
  .aa-show-detail .aa-detail { display: block; }
  .aa-back { display: inline-flex; margin-bottom: 12px; border: none; background: none; padding: 4px 0; color: var(--accent-purple); }
}
@media (prefers-reduced-motion: reduce) {
  .aa-agent-running .aa-dot, .aa-loop-running .aa-loop-icon { animation: none; }
  .aa-twist::before { transition: none; }
}
</style>

<style>
html.desktop-mac .all-agents { padding-top: 10px; }
html.desktop-mac .all-agents .aa-header { --wails-draggable: drag; padding-left: 76px; min-height: 40px; }
html.desktop-mac .all-agents .aa-header button, html.desktop-mac .all-agents .aa-header input { --wails-draggable: no-drag; }
</style>
