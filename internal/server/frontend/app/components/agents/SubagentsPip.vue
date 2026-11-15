<template>
  <div v-if="visible" ref="root" class="subagents-pip" :class="{ collapsed }" role="region" aria-label="Subagents">
    <!-- Closed: a small pill that brings the card back -->
    <button
      v-if="collapsed"
      type="button"
      class="pip-pill"
      :title="`Show subagents (${summary})`"
      aria-label="Show subagents"
      @click="setCollapsed(false)"
    >
      <span class="pip-dot" :class="{ idle: !selection.running }" aria-hidden="true" />
      <span class="pip-pill-text">{{ pillText }}</span>
      <Icon name="mdi:chevron-down" size="14" aria-hidden="true" />
    </button>

    <template v-else>
      <div class="pip-header">
        <Icon name="mdi:robot-outline" size="15" class="pip-header-icon" aria-hidden="true" />
        <span class="pip-title">{{ hasWorkers ? 'Agents' : 'Subagents' }}</span>
        <span class="pip-summary" :class="{ running: selection.running > 0, failed: selection.failed > 0 }">{{ summary }}</span>
        <button type="button" class="pip-btn" :title="openViewTitle" aria-label="Open the subagents view" @click="$emit('open-view')">
          <Icon name="mdi:arrow-expand" size="14" />
        </button>
        <button type="button" class="pip-btn" title="Close (reopen from the pill)" aria-label="Close the subagents card" @click="setCollapsed(true)">
          <Icon name="mdi:close" size="14" />
        </button>
      </div>

      <ul class="pip-list">
        <li v-for="row in selection.rows" :key="row.id">
          <button
            type="button"
            class="pip-row"
            :class="row.status"
            :data-agent-id="row.id"
            :title="row.status === 'running' ? row.now.full : row.summary || row.title"
            @click="modalId = row.id"
          >
            <CircusFigure :id="row.id" :status="row.status" :talking="row.status === 'running'" :size="22" class="pip-figure" />
            <span class="pip-body">
              <span class="pip-name">{{ row.title }}</span>
              <span class="pip-now">
                <template v-if="row.status === 'running'">
                  <span class="pip-now-icon" aria-hidden="true">{{ row.now.icon }}</span>{{ quiet(row) || row.now.text }}
                </template>
                <template v-else>
                  <span class="pip-status" :class="row.status">{{ STATUS_LABELS[row.status] }}</span>
                  <span v-if="row.summary"> · {{ row.summary }}</span>
                </template>
              </span>
            </span>
            <span class="pip-side">
              <span class="pip-time">{{ elapsed(row) }}</span>
              <SubagentModelChip :label="row.model" :title="row.modelTitle" :requested="row.modelRequested" />
            </span>
          </button>
        </li>
      </ul>
      <button v-if="selection.hidden" type="button" class="pip-more" @click="$emit('open-view')">+{{ selection.hidden }} more</button>
    </template>
  </div>

  <!-- A row opens just that subagent, zooming out of the row; ‹ › step through the rest. -->
  <SubagentZoomModal
    v-model:agent-id="modalId"
    :items="modalOrder"
    :now="now"
    :origin-for="rowElement"
    @open-view="(id) => $emit('open-agent', id)"
    @open-session="(id) => $emit('open-session', id)"
  />
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, shallowRef, watch, watchEffect } from 'vue'
import CircusFigure from '~/components/agents/CircusFigure.vue'
import SubagentZoomModal from '~/components/agents/SubagentZoomModal.vue'
import SubagentModelChip from '~/components/agents/SubagentModelChip.vue'
import { useBackgroundAgents } from '~/composables/agents/useBackgroundAgents'
import { useWorkerActivity } from '~/composables/agents/useWorkerActivity'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import { useShortcutLabels } from '~/composables/useKeyboardShortcuts'
import { activityAgo, createFrameBatcher, formatDuration, STALE_AFTER_MS, STATUS_LABELS } from '~/utils/agents/subagentActivity'
import { buildSubagentItem, itemInputsChanged, type SubagentItem } from '~/utils/agents/subagentItem'
import { loadPipCollapsed, pipNavOrder, pipSelection, pipSummary, savePipCollapsed } from '~/utils/agents/subagentsPip'
import type { TeamSession } from '~/utils/agents/team'
import { createWorkerAgentCache } from '~/utils/agents/workerAgent'

const props = defineProps<{
  sessionId: string
  /** False while something else covers the chat (Zen, the Subagents view). */
  show: boolean
  /**
   * The worker sessions this session started. A worker is another session,
   * so its activity comes from worker_activity events (useWorkerActivity);
   * it is shown like a subagent, and its modal opens its session.
   */
  workers?: readonly TeamSession[]
}>()

defineEmits<{
  /** Open the full Subagents view on this agent (the modal's "Open in Subagents view"). */
  'open-agent': [agentId: string]
  'open-view': []
  /** Open a worker's session. */
  'open-session': [sessionId: string]
}>()

const { getAgentsForSession, getAgentOutput, agentOutputWithTimes, hasFullList, processWebSocketMessage } = useBackgroundAgents()
const { fetchWithAuth } = useAuthenticatedFetch()
const { getWorkerLines, getWorkerTimes, loadWorkerActivity } = useWorkerActivity()
const workerAgents = createWorkerAgentCache()
const openViewTitle = useShortcutLabels().title('Open the subagents view', 'subagents.toggle')

const collapsed = ref(loadPipCollapsed())
function setCollapsed(value: boolean) {
  collapsed.value = value
  savePipCollapsed(value)
}

const now = ref(Date.now())

// Rebuilt at most once per frame, only for agents whose inputs changed (as in the Subagents view).
const items = shallowRef<SubagentItem[]>([])
const itemCache = new Map<string, SubagentItem>()
function rebuild() {
  if (!props.sessionId) { items.value = []; itemCache.clear(); workerAgents.clear(); return }
  const seen = new Set<string>()
  const list = getAgentsForSession(props.sessionId)
  const titles = new Map(list.map(a => [a.agent_id, a.description || a.subagent_type || 'another subagent']))
  const next = list.map(agent => {
    const { lines, times } = agentOutputWithTimes(agent)
    const parent = agent.parent_agent_id ? titles.get(agent.parent_agent_id) || 'another subagent' : ''
    const old = itemCache.get(agent.agent_id)
    const item = old && !itemInputsChanged(old, agent, lines, parent, times) ? old : buildSubagentItem(agent, lines, parent, times)
    itemCache.set(agent.agent_id, item)
    seen.add(agent.agent_id)
    return item
  })
  const workerIds = new Set<string>()
  for (const session of props.workers || []) {
    const lines = getWorkerLines(session.id)
    const times = getWorkerTimes(session.id)
    const agent = workerAgents.get(session, lines)
    if (!agent) continue
    workerIds.add(session.id)
    const old = itemCache.get(session.id)
    const item = old && !itemInputsChanged(old, agent, lines, '', times)
      ? old
      : { ...buildSubagentItem(agent, lines, '', times), sessionLink: session.id }
    itemCache.set(session.id, item)
    seen.add(session.id)
    next.push(item)
  }
  workerAgents.keep(workerIds)
  items.value = next
  for (const id of itemCache.keys()) if (!seen.has(id)) itemCache.delete(id)
}
const batcher = createFrameBatcher(rebuild)
watchEffect(() => {
  if (!props.sessionId) return
  for (const agent of getAgentsForSession(props.sessionId)) {
    void agent.status; void agent.updated_at; void agent.completed_at; void agent.error_message
    void agent.last_output; void agent.description; void agent.interrupted; void agent.recent_output
    getAgentOutput(agent.agent_id)
  }
  for (const session of props.workers || []) {
    void session.status; void session.updated_at; void session.subagents_running; void session.limit_wait
    getWorkerLines(session.id)
  }
  batcher.request()
})

const selection = computed(() => pipSelection(items.value, now.value))
const summary = computed(() => pipSummary(selection.value))
const pillText = computed(() => selection.value.running
  ? `${selection.value.running} ${hasWorkers.value ? 'agent' : 'subagent'}${selection.value.running === 1 ? '' : 's'} working`
  : summary.value)
const hasWorkers = computed(() => items.value.some(i => i.sessionLink))
const visible = computed(() => props.show && !!props.sessionId && selection.value.rows.length > 0)

// The subagent open in the modal, and the order ‹ › step through: the card's
// order, then every other subagent of the session.
const modalId = ref<string | null>(null)
const modalOrder = computed(() => modalId.value ? pipNavOrder(items.value, now.value) : [])
const root = ref<HTMLElement | null>(null)
function rowElement(agentId: string): HTMLElement | null {
  return [...(root.value?.querySelectorAll<HTMLElement>('.pip-row') || [])].find(el => el.dataset.agentId === agentId) || null
}
// Something covers the chat (Zen, the Subagents view) or the session changed: close it.
watch([() => props.show, () => props.sessionId], () => { modalId.value = null })

// The clock ticks only while the card is on screen.
let clock: ReturnType<typeof setInterval> | undefined
const hasAgents = computed(() => items.value.length > 0)
watch([() => props.show, hasAgents], ([show, any]) => {
  if (show && any && !clock) clock = setInterval(() => { now.value = Date.now() }, 1000)
  else if ((!show || !any) && clock) { clearInterval(clock); clock = undefined }
}, { immediate: true })

// A session opened before any of its subagent events arrived: load its list once.
const loaded = new Set<string>()
watch(() => props.sessionId, async (sessionId) => {
  itemCache.clear()
  rebuild()
  if (!sessionId || hasFullList(sessionId) || loaded.has(sessionId)) return
  loaded.add(sessionId)
  try {
    const response = await fetchWithAuth(`/api/agent/sessions/${sessionId}/subagents`)
    if (!response.ok) return
    const result = await response.json()
    processWebSocketMessage({ type: 'background_agents_list', session_id: sessionId, agents: result.subagents || [] } as any)
  } catch { /* live events still arrive */ }
}, { immediate: true })

// The workers' kept lines (a reload, or a worker that spoke before the page opened).
watch([() => props.sessionId, () => (props.workers || []).length > 0], ([sessionId, any]) => {
  if (sessionId && any) void loadWorkerActivity(sessionId, fetchWithAuth)
}, { immediate: true })

onUnmounted(() => {
  if (clock) clearInterval(clock)
  batcher.cancel()
})

function elapsed(row: SubagentItem): string {
  if (!Number.isFinite(row.startedAt)) return ''
  if (row.status === 'running') return formatDuration(now.value - row.startedAt)
  return activityAgo(row.endedAt, now.value)
}

function quiet(row: SubagentItem): string {
  const idle = now.value - row.lastActivityAt
  return Number.isFinite(idle) && idle > STALE_AFTER_MS ? `Quiet for ${formatDuration(idle)}` : ''
}
</script>

<style scoped>
.subagents-pip {
  position: absolute;
  top: 0.75rem;
  right: 1rem;
  z-index: 12;
  width: min(300px, calc(100% - 2rem));
  color: var(--text-primary);
  background: var(--card-bg, var(--bg-secondary));
  border: 1px solid var(--border-color);
  border-radius: 12px;
  box-shadow: 0 10px 28px var(--shadow-color, rgba(0, 0, 0, 0.3));
  overflow: hidden;
  animation: pip-in 0.2s ease-out;
}
.subagents-pip.collapsed {
  width: auto;
  border-radius: 999px;
}
@keyframes pip-in {
  from { opacity: 0; translate: 0 -6px; }
  to { opacity: 1; translate: 0 0; }
}

.pip-pill {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px 5px 12px;
  font: inherit;
  font-size: 0.75rem;
  color: var(--text-primary);
  background: transparent;
  border: 0;
  cursor: pointer;
}
.pip-pill:hover { background: var(--overlay-bg-hover); }
.pip-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--status-processing, var(--accent-purple));
  animation: pip-pulse 1.6s ease-in-out infinite;
}
.pip-dot.idle { background: var(--text-muted); animation: none; }
@keyframes pip-pulse {
  50% { opacity: 0.35; }
}

.pip-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 6px 6px 10px;
  border-bottom: 1px solid var(--border-color);
  font-size: 0.78rem;
}
.pip-header-icon { color: var(--text-secondary); flex-shrink: 0; }
.pip-title { font-weight: 600; flex-shrink: 0; }
.pip-summary {
  flex: 1;
  min-width: 0;
  text-align: right;
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.pip-summary.running { color: var(--status-processing, var(--accent-purple)); }
.pip-summary.failed { color: var(--status-error, #ef4444); }
.pip-btn {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  color: var(--text-secondary);
  background: transparent;
  border: 0;
  border-radius: 6px;
  cursor: pointer;
}
.pip-btn:hover { color: var(--text-primary); background: var(--overlay-bg-hover); }

.pip-list {
  list-style: none;
  margin: 0;
  padding: 4px;
  max-height: min(280px, 40vh);
  overflow-y: auto;
}
.pip-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 5px 6px;
  font: inherit;
  text-align: left;
  color: inherit;
  background: transparent;
  border: 0;
  border-radius: 8px;
  cursor: pointer;
}
.pip-row:hover { background: var(--overlay-bg-hover); }
.pip-row:not(.running) { opacity: 0.75; }
.pip-figure { flex-shrink: 0; }
.pip-body {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}
.pip-now {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
/* The agent's title wraps to two lines so its task stays readable. */
.pip-name {
  font-size: 0.78rem;
  font-weight: 500;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}
.pip-now { font-size: 0.7rem; color: var(--text-secondary); }
.pip-now-icon { margin-right: 4px; }
.pip-status.done { color: var(--status-success, #22c55e); }
.pip-status.failed { color: var(--status-error, #ef4444); }
.pip-status.interrupted { color: var(--status-warning, #f59e0b); }
.pip-side {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 2px;
}
.pip-time {
  flex-shrink: 0;
  font-size: 0.68rem;
  color: var(--text-muted, var(--text-secondary));
  font-variant-numeric: tabular-nums;
}
.pip-more {
  display: block;
  width: 100%;
  padding: 5px;
  font: inherit;
  font-size: 0.72rem;
  color: var(--text-secondary);
  background: transparent;
  border: 0;
  border-top: 1px solid var(--border-color);
  cursor: pointer;
}
.pip-more:hover { color: var(--text-primary); }
.pip-row:focus-visible, .pip-btn:focus-visible, .pip-pill:focus-visible, .pip-more:focus-visible {
  outline: 2px solid var(--accent-purple);
  outline-offset: -2px;
}

@media (prefers-reduced-motion: reduce) {
  .subagents-pip, .pip-dot { animation: none; }
}
@media (max-width: 640px) {
  .subagents-pip:not(.collapsed) { width: calc(100% - 1.5rem); right: 0.75rem; }
}
</style>
