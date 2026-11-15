<template>
  <Teleport to="body">
    <section
      v-if="show"
      ref="panel"
      class="subagents-view"
      :class="{ 'sv-show-detail': showDetailOnPhone }"
      role="dialog"
      aria-modal="true"
      aria-labelledby="subagents-view-title"
      tabindex="-1"
    >
      <header class="sv-header">
        <div class="sv-heading">
          <h1 id="subagents-view-title">Subagents <span class="sv-count">{{ counts.all }}</span></h1>
          <p class="sv-summary">
            <span class="sv-session">{{ sessionName || 'This session' }}</span>
            <span v-if="counts.running" class="sv-sum-running"><span class="sv-dot" /> {{ counts.running }} running</span>
            <span v-else class="sv-sum-idle">idle</span>
          </p>
        </div>
        <nav v-if="mode === 'activity'" class="sv-filters" aria-label="Filter subagents">
          <button
            v-for="f in SUBAGENT_FILTERS"
            :key="f"
            type="button"
            :class="`sv-filter-${f}`"
            :aria-pressed="filter === f"
            :disabled="f !== 'all' && !counts[f] && filter !== f"
            @click="filter = f"
          >{{ filterLabel(f) }} <span>{{ counts[f] }}</span></button>
        </nav>
        <div class="sv-actions">
          <div class="sv-modes" role="group" aria-label="View">
            <button type="button" :aria-pressed="mode === 'activity'" @click="setMode('activity')">Activity</button>
            <button type="button" :aria-pressed="mode === 'zen'" @click="setMode('zen')">Zen</button>
          </div>
          <div v-if="mode === 'zen'" class="sv-modes" role="group" aria-label="Zen stage">
            <button type="button" :aria-pressed="stage === 'circus'" title="Flying Circus: a paper collage" @click="setStage('circus')">Circus</button>
            <button type="button" :aria-pressed="stage === 'synthwave'" title="Synthwave: neon skyline" @click="setStage('synthwave')">Synthwave</button>
          </div>
          <button type="button" class="sv-close" aria-label="Close subagents view" @click="emit('close')">
            <span class="sv-close-label">Back to chat</span> <kbd>{{ subagentsKey }}</kbd><span class="sv-close-x" aria-hidden="true">×</span>
          </button>
        </div>
      </header>

      <div v-if="loadError" class="sv-error" role="status">{{ loadError }}</div>

      <!-- Zen always has someone on stage: the main agent, with its subagents around it. -->
      <SubagentsZen v-if="mode === 'zen' && (zenAgents.length || lead)" :agents="zenAgents" :lead="lead" :stage="stage" :session-id="sessionId" @select="showAgent" @lead="emit('close')" />

      <div v-else-if="!groups.running.length && !groups.finished.length" class="sv-empty">
        <span class="sv-empty-symbol" aria-hidden="true">◌</span>
        <h2>{{ loading ? 'Loading subagents…' : filter !== 'all' && counts.all ? `No ${filterLabel(filter).toLowerCase()} subagents` : 'No subagents yet' }}</h2>
        <p v-if="!loading">Subagents this session launches show up here with every command they run, live.</p>
        <button v-if="filter !== 'all' && counts.all" type="button" @click="filter = 'all'">Show all {{ counts.all }}</button>
      </div>

      <div v-else class="sv-body">
        <div ref="rail" class="sv-rail" role="listbox" aria-label="Subagents" @keydown="moveSelection">
          <template v-for="group in railGroups" :key="group.key">
            <h2 class="sv-group-title" :id="`sv-group-${group.key}`"><span v-if="group.key === 'running'" class="sv-dot" /> {{ group.title }} <span>{{ group.items.length }}</span></h2>
            <button
              v-for="item in group.items"
              :key="item.id"
              type="button"
              role="option"
              class="sv-row"
              :class="[`sv-row-${item.status}`, { 'sv-row-selected': item.id === selected?.id, 'sv-row-highlight': highlighted === item.id }]"
              :aria-selected="item.id === selected?.id"
              :data-agent-id="item.id"
              :tabindex="item.id === selected?.id ? 0 : -1"
              @click="select(item.id, true)"
            >
              <span class="sv-row-mark" aria-hidden="true" />
              <span class="sv-row-main">
                <span class="sv-row-title">{{ item.title }}</span>
                <span v-if="item.status === 'running'" class="sv-row-line" :class="{ 'sv-row-cmd': item.latest?.detailKind === 'command' && item.now.kind === 'tool' }">
                  <template v-if="item.now.kind === 'tool' && item.latest?.detailKind === 'command'"><span class="sv-prompt">$</span>{{ item.latest.detail }}</template>
                  <template v-else>{{ item.now.kind === 'tool' && item.latest ? item.latest.text : item.now.text }}</template>
                </span>
                <!-- Finished rows stay one line; the selected one says how it ended. -->
                <span v-else-if="item.id === selected?.id && item.summary" class="sv-row-line">{{ item.summary }}</span>
              </span>
              <span class="sv-row-side">
                <span class="sv-row-time" :title="`${item.stepCount} ${item.stepCount === 1 ? 'step' : 'steps'}`">{{ rowElapsed(item) }}</span>
                <SubagentModelChip :label="item.model" :title="item.modelTitle" :requested="item.modelRequested" />
              </span>
            </button>
          </template>
        </div>

        <div ref="detailPane" class="sv-detail" tabindex="-1" aria-live="off">
          <button type="button" class="sv-back" @click="showDetailOnPhone = false">‹ All subagents</button>
          <SubagentDetail v-if="selected" ref="detail" :key="selected.id" :item="selected" :now="now" />
        </div>
      </div>
    </section>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, shallowRef, watch, watchEffect } from 'vue'
import { useBackgroundAgents } from '~/composables/agents/useBackgroundAgents'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import { createFrameBatcher, formatDuration, groupSubagents, SUBAGENT_FILTERS, STATUS_LABELS, type SubagentFilter } from '~/utils/agents/subagentActivity'
import { buildSubagentItem, itemInputsChanged, type SubagentItem } from '~/utils/agents/subagentItem'
import SubagentDetail from '~/components/agents/SubagentDetail.vue'
import SubagentModelChip from '~/components/agents/SubagentModelChip.vue'
import SubagentsZen, { type ZenLead, type ZenStage, type ZenSubagent } from '~/components/agents/SubagentsZen.vue'
import { loadZenStage, saveZenStage } from '~/utils/agents/zenStage'
import { isModalOpen, useClosable, useShortcutLabels } from '~/composables/useKeyboardShortcuts'

// focusAgentId: open on this subagent (from the All agents overlay, ⇧⌘S).
const props = defineProps<{ show: boolean; sessionId: string | null; sessionName?: string; initialMode?: 'activity' | 'zen'; lead?: ZenLead | null; focusAgentId?: string }>()
const emit = defineEmits<{ close: [] }>()
const subagentsKey = useShortcutLabels().label('subagents.toggle')
useClosable(() => props.show, () => emit('close'))

const { getAgentsForSession, getAgentOutput, agentOutputWithTimes, processWebSocketMessage } = useBackgroundAgents()
const { fetchWithAuth } = useAuthenticatedFetch()

const filter = ref<SubagentFilter>('all')
// Activity: the list of subagents beside the selected one's commands and
// report. Zen: them as characters on a stage. Opens in the session's own view
// mode (Live → Activity).
type Mode = 'activity' | 'zen'
const initialMode = (): Mode => props.initialMode === 'zen' ? 'zen' : 'activity'
const mode = ref<Mode>(initialMode())
function setMode(next: Mode) {
  mode.value = next
}
// Which zen stage to draw: the Flying Circus collage unless you picked synthwave.
const stage = ref<ZenStage>(loadZenStage())
function setStage(next: ZenStage) {
  stage.value = next
  saveZenStage(next)
}
const highlighted = ref('')
const loading = ref(false)
const loadError = ref('')
const panel = ref<HTMLElement | null>(null)
const rail = ref<HTMLElement | null>(null)
const detailPane = ref<HTMLElement | null>(null)
const detail = ref<InstanceType<typeof SubagentDetail> | null>(null)
// The subagent the user picked; '' follows the default (first running, else newest finished).
const chosenId = ref('')
// Phones show the list or the detail, not both.
const showDetailOnPhone = ref(false)
// Ticks once a second so running durations and "last activity" stay current.
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
let previousFocus: Element | null = null

const filterLabel = (f: SubagentFilter) => f === 'all' ? 'All' : STATUS_LABELS[f]

// Items are rebuilt at most once per animation frame, however many output
// events arrive, and only for agents whose inputs changed; unchanged items keep
// their identity, so their rows are not re-rendered.
const items = shallowRef<SubagentItem[]>([])
const itemCache = new Map<string, SubagentItem>()
function rebuild() {
  if (!props.sessionId) {
    items.value = []
    itemCache.clear()
    return
  }
  const list = getAgentsForSession(props.sessionId)
  const titles = new Map(list.map(a => [a.agent_id, a.description || a.subagent_type || 'another subagent']))
  const seen = new Set<string>()
  items.value = list.map(agent => {
    const { lines, times } = agentOutputWithTimes(agent)
    const parent = agent.parent_agent_id ? titles.get(agent.parent_agent_id) || 'another subagent' : ''
    const old = itemCache.get(agent.agent_id)
    const item = old && !itemInputsChanged(old, agent, lines, parent, times) ? old : buildSubagentItem(agent, lines, parent, times)
    itemCache.set(agent.agent_id, item)
    seen.add(agent.agent_id)
    return item
  })
  for (const id of itemCache.keys()) if (!seen.has(id)) itemCache.delete(id)
}
const batcher = createFrameBatcher(rebuild)
// Track what items depend on; changes only schedule a rebuild.
watchEffect(() => {
  if (!props.show || !props.sessionId) return
  for (const agent of getAgentsForSession(props.sessionId)) {
    void agent.status; void agent.updated_at; void agent.completed_at; void agent.error_message
    void agent.last_output; void agent.description; void agent.interrupted; void agent.recent_output
    getAgentOutput(agent.agent_id)
  }
  batcher.request()
})

const groups = computed(() => groupSubagents(items.value, filter.value))
const counts = computed(() => groups.value.counts)

const zenAgents = computed<ZenSubagent[]>(() => [...groups.value.running, ...groups.value.finished].map(item => ({
  id: item.id,
  name: item.title,
  status: item.agent.status,
  lines: item.lines,
  error: item.error_message,
  parentId: item.agent.parent_agent_id
})))

const railGroups = computed(() => [
  { key: 'running', title: 'Running', items: groups.value.running },
  { key: 'finished', title: 'Finished', items: groups.value.finished }
].filter(g => g.items.length))
const visible = computed(() => [...groups.value.running, ...groups.value.finished])
const selected = computed<SubagentItem | null>(() =>
  visible.value.find(i => i.id === chosenId.value) || visible.value[0] || null)

function rowElapsed(item: SubagentItem): string {
  if (!Number.isFinite(item.startedAt)) return ''
  const end = item.status === 'running' ? now.value : Number.isFinite(item.endedAt) ? item.endedAt : now.value
  return formatDuration(end - item.startedAt)
}

function rowFor(id: string): HTMLElement | undefined {
  return [...(rail.value?.querySelectorAll<HTMLElement>('.sv-row') || [])].find(el => el.dataset.agentId === id)
}

function select(id: string, fromClick = false) {
  if (chosenId.value !== id) detailPane.value?.scrollTo?.({ top: 0 })
  chosenId.value = id
  if (fromClick) showDetailOnPhone.value = true
}

// Clicking a character in Zen opens that subagent with its output showing.
async function showAgent(agentId: string) {
  filter.value = 'all'
  setMode('activity')
  highlighted.value = agentId
  batcher.flush()
  select(agentId, true)
  await nextTick()
  const row = rowFor(agentId)
  row?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' })
  row?.focus?.({ preventScroll: true })
  detail.value?.reveal()
  setTimeout(() => { if (highlighted.value === agentId) highlighted.value = '' }, 2400)
}

// ↑/↓ (Home/End) move the selection in the list; Enter jumps into the detail.
function moveSelection(event: KeyboardEvent) {
  const list = visible.value
  const index = list.findIndex(i => i.id === selected.value?.id)
  let next = -1
  if (event.key === 'ArrowDown' || event.key === 'ArrowRight' || event.key === 'j') next = Math.min(list.length - 1, index + 1)
  else if (event.key === 'ArrowUp' || event.key === 'ArrowLeft' || event.key === 'k') next = Math.max(0, index - 1)
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = list.length - 1
  else if (event.key === 'Enter' && (event.target as HTMLElement)?.classList?.contains('sv-row')) {
    event.preventDefault()
    showDetailOnPhone.value = true
    detailPane.value?.focus()
    return
  }
  if (next < 0 || !list[next]) return
  event.preventDefault()
  select(list[next]!.id)
  nextTick(() => {
    const row = rowFor(list[next]!.id)
    row?.focus()
    row?.scrollIntoView?.({ block: 'nearest' })
  })
}

// The live WebSocket only carries events from now on; load what already ran
// (with the output the server kept) each time the view opens.
async function load() {
  if (!props.sessionId) return
  const sessionId = props.sessionId
  loading.value = true
  loadError.value = ''
  try {
    const response = await fetchWithAuth(`/api/agent/sessions/${sessionId}/subagents`)
    if (!response.ok) throw new Error(`HTTP ${response.status}`)
    const result = await response.json()
    if (props.show && props.sessionId === sessionId) {
      processWebSocketMessage({ type: 'background_agents_list', session_id: sessionId, agents: result.subagents || [] } as any)
    }
  } catch {
    loadError.value = 'Could not load earlier subagents. Live updates still appear.'
  } finally {
    loading.value = false
    batcher.request()
    batcher.flush()
  }
  if (props.show && props.sessionId === sessionId && props.focusAgentId && items.value.some(i => i.id === props.focusAgentId)) {
    void showAgent(props.focusAgentId)
  }
}

function handleKey(event: KeyboardEvent) {
  // Escape: the shortcut registry's Escape stack (useClosable below)
  if (isModalOpen()) return
  if (event.key === 'Tab') {
    const elements = [...(panel.value?.querySelectorAll<HTMLElement>('button:not([disabled]), [href], [tabindex="0"]') || [])]
    const first = elements[0], last = elements[elements.length - 1]
    if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.value)) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
  }
}
function open() {
  mode.value = initialMode()
  chosenId.value = ''
  showDetailOnPhone.value = false
  previousFocus = document.activeElement
  window.addEventListener('keydown', handleKey, true)
  clock = setInterval(() => { now.value = Date.now() }, 1000)
  rebuild()
  nextTick(() => panel.value?.focus())
  void load()
}
function close() {
  window.removeEventListener('keydown', handleKey, true)
  if (clock) clearInterval(clock)
  clock = undefined
  batcher.cancel()
  if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus()
}
watch(() => props.show, (show, was) => { if (show) open(); else if (was) close() }, { immediate: true })
watch(() => props.focusAgentId, id => { if (props.show && id && items.value.some(i => i.id === id)) void showAgent(id) })
watch(() => props.sessionId, () => { if (props.show) { itemCache.clear(); chosenId.value = ''; rebuild(); void load() } })
onUnmounted(() => { if (props.show) close() })
</script>

<style scoped>
.subagents-view {
  position: fixed; inset: 0; z-index: 10050; display: flex; flex-direction: column;
  padding: 14px 20px 0; background: var(--bg-primary); color: var(--text-primary); outline: none; overflow: hidden; font-size: 14px;
}

.sv-header { display: flex; align-items: center; gap: 10px 18px; flex-wrap: wrap; padding-bottom: 12px; border-bottom: 1px solid var(--border-color); }
.sv-heading { display: flex; align-items: baseline; gap: 14px; min-width: 0; }
h1 { margin: 0; font-size: 20px; font-weight: 700; letter-spacing: -.01em; white-space: nowrap; }
.sv-count { font-size: 15px; color: var(--text-secondary); margin-left: 4px; font-weight: 500; }
.sv-summary { display: flex; align-items: baseline; gap: 10px; margin: 0; font-size: 13px; color: var(--text-secondary); min-width: 0; }
.sv-session { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 22ch; }
.sv-sum-running { display: inline-flex; align-items: center; gap: 6px; color: var(--status-success); font-weight: 650; white-space: nowrap; }
.sv-dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; background: var(--status-success); animation: sv-pulse 1.6s ease-in-out infinite; }
.sv-filters { display: flex; flex-wrap: wrap; gap: 6px; }
.sv-filters button { border-radius: 999px; padding: 3px 11px; font-size: 12.5px; }
.sv-filters button span { margin-left: 3px; font-variant-numeric: tabular-nums; opacity: .7; }
.sv-actions { display: flex; align-items: center; gap: 10px; margin-left: auto; }
.sv-modes { display: flex; padding: 2px; border: 1px solid var(--border-color); border-radius: 9px; background: var(--bg-secondary); }
.sv-modes button { border: none; background: transparent; padding: 4px 11px; border-radius: 7px; }
.sv-modes button[aria-pressed="true"] { background: var(--bg-primary); box-shadow: 0 1px 2px rgb(0 0 0 / .25); }
button { color: inherit; cursor: pointer; border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-secondary); padding: 6px 11px; font: inherit; font-size: 13px; }
button:hover:not([disabled]) { border-color: var(--accent-purple); }
.sv-modes button:hover:not([disabled]) { color: var(--accent-purple); }
button:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }
button[aria-pressed="true"] { color: var(--accent-purple); font-weight: 650; }
.sv-filters button[aria-pressed="true"] { border-color: var(--accent-purple); background: color-mix(in srgb, var(--accent-purple) 12%, transparent); }
button[disabled] { opacity: .4; cursor: default; }
.sv-close { display: flex; align-items: center; gap: 8px; }
.sv-close-x { font-size: 18px; line-height: 1; color: var(--text-secondary); }
kbd { color: var(--text-secondary); font: 11px var(--font-mono, monospace); }
.sv-error { padding: 8px 0 0; color: var(--status-warning); font-size: 13px; }

/* Two panes: the list of subagents and the selected one. */
.sv-body { flex: 1; min-height: 0; display: grid; grid-template-columns: minmax(300px, 380px) minmax(0, 1fr); }
.sv-rail { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 10px 12px 16px 0; border-right: 1px solid var(--border-color); display: flex; flex-direction: column; gap: 2px; }
.sv-group-title { display: flex; align-items: center; gap: 7px; margin: 10px 8px 6px; font-size: 11px; font-weight: 750; letter-spacing: .09em; text-transform: uppercase; color: var(--text-secondary); }
.sv-group-title:first-child { margin-top: 2px; }
.sv-group-title > span:last-child { font-weight: 500; letter-spacing: 0; }

.sv-row {
  --sv-accent: var(--text-tertiary, var(--text-secondary));
  display: grid; grid-template-columns: 14px minmax(0, 1fr) auto; align-items: start; gap: 10px;
  width: 100%; padding: 8px 10px; border: 1px solid transparent; border-radius: 10px; background: transparent; text-align: left;
}
.sv-row:hover:not([disabled]) { border-color: transparent; background: color-mix(in srgb, var(--text-primary) 5%, transparent); }
.sv-row:focus-visible { outline-offset: -2px; }
.sv-row-selected, .sv-row-selected:hover:not([disabled]) { background: color-mix(in srgb, var(--accent-purple) 13%, transparent); border-color: color-mix(in srgb, var(--accent-purple) 45%, transparent); }
.sv-row-highlight { box-shadow: 0 0 0 2px var(--accent-purple); }
.sv-row-running { --sv-accent: var(--status-success); }
.sv-row-done { --sv-accent: var(--accent-cyan, var(--accent-purple)); }
.sv-row-failed { --sv-accent: var(--status-error); }
.sv-row-mark { margin-top: 3px; font-size: 11px; line-height: 1.4; color: var(--sv-accent); text-align: center; }
.sv-row-mark::before { content: '✓'; }
.sv-row-running .sv-row-mark::before { content: ''; display: inline-block; width: 8px; height: 8px; border-radius: 50%; background: currentColor; animation: sv-pulse 1.4s ease-in-out infinite; }
.sv-row-failed .sv-row-mark::before { content: '✕'; }
.sv-row-interrupted .sv-row-mark::before { content: '⏸'; }
.sv-row-main { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.sv-row-title { font-size: 14px; font-weight: 600; line-height: 1.3; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sv-row-line { font-size: 12.5px; color: var(--text-secondary); line-height: 1.4; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; }
.sv-row-cmd { font: 12px/1.45 var(--font-mono, ui-monospace, monospace); color: var(--text-primary); }
.sv-prompt { color: var(--status-success); font-weight: 700; margin-right: 6px; }
.sv-row-failed .sv-row-line { color: color-mix(in srgb, var(--status-error) 75%, var(--text-secondary)); }
.sv-row-side { display: flex; flex-direction: column; align-items: flex-end; gap: 2px; white-space: nowrap; }
.sv-row-time { font: 600 12px var(--font-mono, monospace); font-variant-numeric: tabular-nums; color: var(--text-secondary); }
.sv-row-running .sv-row-time { color: var(--status-success); }

.sv-detail { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 18px 4px 28px 24px; outline: none; }
.sv-detail > .sd { max-width: 1100px; }
.sv-back { display: none; }

.sv-empty { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; text-align: center; color: var(--text-secondary); font-size: 14px; }
.sv-empty h2 { margin: 0; color: var(--text-primary); font-weight: 550; }
.sv-empty p { margin: 0; max-width: 360px; line-height: 1.6; }
.sv-empty-symbol { font-size: 60px; color: var(--accent-purple); }
@keyframes sv-pulse { 50% { opacity: .4; } }

@media (max-width: 1100px) { .sv-filters { order: 3; width: 100%; } }
@media (max-width: 760px) {
  .subagents-view { padding: 12px 16px 0; }
  .sv-close-label, .sv-close kbd { display: none; }
  .sv-session { display: none; }
  .sv-body { grid-template-columns: minmax(0, 1fr); }
  .sv-rail { border-right: none; padding-right: 0; }
  .sv-detail { display: none; padding: 10px 0 24px; }
  .sv-show-detail .sv-rail { display: none; }
  .sv-show-detail .sv-detail { display: block; }
  .sv-back { display: inline-flex; margin-bottom: 12px; border: none; background: none; padding: 4px 0; color: var(--accent-purple); }
}
@media (prefers-reduced-motion: reduce) { .sv-dot, .sv-row-running .sv-row-mark::before { animation: none; } }
</style>

<style>
/* Mac app: the traffic lights float over the top-left corner; the header is the drag region.
   Unscoped on purpose: in scoped CSS `:global(html.x) .y` compiles to `html.x` alone,
   which padded the whole app. */
html.desktop-mac .subagents-view { padding-top: 10px; }
html.desktop-mac .subagents-view .sv-header { --wails-draggable: drag; padding-left: 76px; min-height: 40px; }
html.desktop-mac .subagents-view .sv-header button { --wails-draggable: no-drag; }
</style>
