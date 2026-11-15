<template>
  <aside class="sessions-sidebar" :class="{ 'sessions-sidebar-wide': navCollapsed }">
    <div class="sidebar-header">
      <div class="header-top">
        <h3>Sessions</h3>
        <details ref="actionsMenu" class="session-actions"><summary aria-label="Session actions" title="Session actions">⋯</summary>
        <div class="session-actions-menu">
        <button type="button" class="btn-menu-item" :disabled="sessions.length === 0" data-testid="select-sessions" @click="startSelecting()">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <rect x="3" y="3" width="18" height="18" rx="3"></rect>
            <polyline points="8 12 11 15 16 9"></polyline>
          </svg>
          Select sessions…
        </button>
        <button v-if="earlierIds.length" type="button" class="btn-menu-item" data-testid="select-earlier" @click="startSelecting(earlierIds)">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"></circle>
            <polyline points="12 7 12 12 15 14"></polyline>
          </svg>
          Clean up Earlier ({{ earlierIds.length }})…
        </button>
        <button @click="closeActionsMenu(); $emit('delete-all')" class="btn-delete-all btn-menu-item" :disabled="!connected || sessions.length === 0" title="Delete all sessions and kill all active agents">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"></polyline>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
            <line x1="10" y1="11" x2="10" y2="17"></line>
            <line x1="14" y1="11" x2="14" y2="17"></line>
          </svg>
          Delete All Sessions
        </button>
        </div>
        </details>

      </div>
      <div v-if="selecting" class="select-toolbar" data-testid="select-toolbar">
        <span class="select-count" aria-live="polite">{{ selectedCount ? `${selectedCount} selected` : 'Select sessions' }}</span>
        <button type="button" class="btn-link" @click="selectAllOrNone">{{ allSelected ? 'None' : 'All' }}</button>
        <button type="button" class="btn-link" data-testid="select-cancel" @click="stopSelecting">Done</button>
      </div>
      <div v-else class="session-buttons">
        <button @click="$emit('create-new')" class="btn-new-session" :disabled="!connected || creating">
          <svg v-if="!creating" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="12" y1="5" x2="12" y2="19"></line>
            <line x1="5" y1="12" x2="19" y2="12"></line>
          </svg>
          <div v-if="creating" class="btn-spinner-small"></div>
          <span v-if="!creating">New Session</span>
          <span v-else>Creating...</span>
        </button>

      </div>
    </div>

    <!-- Sessions grouped by what they need from you (utils/agents/sessionGroups.ts).
         ↑/↓/Enter move through the visible rows while the list has focus
         (⌘[ / ⌘], ⌥⌘↑/↓ and ⌘1…⌘9 work from anywhere in the same order). -->
    <div
      ref="listEl"
      class="sessions-list"
      tabindex="0"
      aria-label="Sessions (arrow keys to move, Enter to open)"
      @focus="onListFocus"
      @blur="listFocused = false"
      @keydown="onListKeydown"
    >
      <div v-if="shownGroups.length === 0" class="no-sessions">No sessions yet</div>
      <section
        v-for="group in shownGroups"
        :key="group.id"
        class="session-group"
        :class="`group-${group.id}`"
        :data-group="group.id"
      >
        <button
          v-if="group.collapsible"
          type="button"
          class="group-header collapsible"
          :aria-expanded="!isGroupCollapsed(group, collapsed)"
          tabindex="-1"
          @click="toggleGroup(group.id)"
        >
          <span
            v-if="selecting"
            class="group-select-box"
            :class="groupCheckState(groupSessionIds(group), selected)"
            role="checkbox"
            :aria-checked="ariaChecked(group)"
            :aria-label="`Select all in ${group.label}`"
            data-testid="group-select-box"
            @click.stop="toggleGroupSelection(group)"
          ></span>
          <svg class="group-chevron" :class="{ closed: isGroupCollapsed(group, collapsed) }" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" aria-hidden="true">
            <polyline points="6 9 12 15 18 9" />
          </svg>
          <span class="group-label">{{ group.label }}</span>
          <span class="group-count">{{ group.rows.length }}</span>
        </button>
        <div v-else class="group-header">
          <span
            v-if="selecting"
            class="group-select-box"
            :class="groupCheckState(groupSessionIds(group), selected)"
            role="checkbox"
            :aria-checked="ariaChecked(group)"
            :aria-label="`Select all in ${group.label}`"
            data-testid="group-select-box"
            @click.stop="toggleGroupSelection(group)"
          ></span>
          <span class="group-label">{{ group.label }}</span>
          <span class="group-count">{{ group.rows.length }}</span>
        </div>
        <template v-if="!isGroupCollapsed(group, collapsed)">
          <template v-for="row in group.rows" :key="row.session.id">
          <SessionItem
            :session="row.session"
            :variant="row.full ? 'full' : 'compact'"
            :waiting-for="row.waitingFor"
            :workers-working="row.workersWorking"
            :wide="navCollapsed"
            :now="now"
            :is-active="activeSessionId === row.session.id"
            :is-focused="listFocused && focusedId === row.session.id"
            :context-usage="contextUsageMap.get(row.session.id)"
            :initial-prompt="sessionPromptMap.get(row.session.id)"
            :selectable="selecting"
            :selected="selected.has(row.session.id)"
            @toggle-select="onToggleSelect"
            @select="$emit('select', $event)"
            @end="$emit('end', $event)"
            @delete="$emit('delete', $event)"
            @delete-all-but-this="$emit('delete-all-but-this', $event)"
          />
          <!-- The workers it started: one line each, indented under it -->
          <WorkerSessionRow
            v-for="worker in row.workers"
            :key="worker.session.id"
            :session="worker.session"
            :view="worker.view"
            :is-active="activeSessionId === worker.session.id"
            :is-focused="listFocused && focusedId === worker.session.id"
            :selectable="selecting"
            :selected="selected.has(worker.session.id)"
            @toggle-select="onToggleSelect"
            @select="$emit('select', $event)"
          />
          </template>
        </template>
      </section>
    </div>

    <!-- Select mode: what to do with the picked sessions -->
    <div v-if="selecting" class="bulk-bar" data-testid="bulk-bar">
      <button
        type="button"
        class="btn-bulk"
        :disabled="!connected || selection.endable.length === 0"
        :title="selection.endable.length ? `End ${sessionCountLabel(selection.endable.length)} (they stay in the list)` : 'The selected sessions have already ended'"
        data-testid="bulk-end"
        @click="emitBulk('bulk-end', selection.endable)"
      >End{{ selection.endable.length ? ` ${selection.endable.length}` : '' }}</button>
      <button
        type="button"
        class="btn-bulk danger"
        :disabled="!connected || selection.ids.length === 0"
        data-testid="bulk-delete"
        @click="emitBulk('bulk-delete', selection.ids)"
      >Delete{{ selection.ids.length ? ` ${selection.ids.length}` : '' }}</button>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useSidebar } from '~/composables/useSidebar'
import { useN0Bindings } from '~/composables/useN0Bindings'
import { useSessionDigests } from '~/composables/agents/useSessionDigests'
import SessionItem from './SessionItem.vue'
import WorkerSessionRow from './WorkerSessionRow.vue'
import { useSessionGroups } from '~/composables/agents/useSessionGroups'
import { groupSessionIds, isGroupCollapsed, type SessionGroup } from '~/utils/agents/sessionGroups'
import {
  groupCheckState,
  pruneSelected,
  selectRange,
  sessionCountLabel,
  summarizeSelection,
  toggleGroupSelected,
  toggleSelected,
} from '~/utils/agents/sessionSelection'
import { useClosable } from '~/composables/useKeyboardShortcuts'
import { useMetricsStore } from '~/stores/metrics/metricsStore'
import { useSessionStore } from '~/stores/session/sessionStore'
import { storeToRefs } from 'pinia'

// The main nav is down to its icon rail: give the room to the sessions list.
const { isCollapsed: navCollapsed } = useSidebar()
import { extractTextContent } from '~/types/message'

interface Props {
  sessions: any[]
  activeSessionId: string | null
  connected: boolean
  creating: boolean
  sessionContextUsage?: Map<string, any>
}

const props = withDefaults(defineProps<Props>(), {
})

// Get context usage from Pinia metrics store for cached data
const metricsStore = useMetricsStore()
const sessionStore = useSessionStore()
const { sessionContextUsage: cachedContextUsage } = storeToRefs(metricsStore)

// n0 pills: one shared (debounced) bindings fetch for the whole list.
const { scheduleRefresh: scheduleN0BindingsRefresh } = useN0Bindings()
watch(
  () => (Array.isArray(props.sessions) ? props.sessions.map((s: any) => s.id).join(',') : ''),
  () => scheduleN0BindingsRefresh(),
  { immediate: true }
)

// AI summaries + topic tags: one shared, debounced fetch for the whole list. The
// version (message count + status) changes as a session works, so a digest is
// refreshed once the session has moved on (the server rate limits regeneration).
const { scheduleLoad: scheduleDigestLoad } = useSessionDigests()
watch(
  () => (Array.isArray(props.sessions) ? props.sessions.map((s: any) => `${s.id}:${s.message_count || 0}:${s.status}`).join(',') : ''),
  () => scheduleDigestLoad(Array.isArray(props.sessions) ? props.sessions : []),
  { immediate: true }
)

// Compute initial prompt text for each session from their messages
// The store keeps messages for recently opened sessions only, so the text is
// remembered here (a short string per session) and shown after they're dropped.
const PROMPT_PREVIEW_CHARS = 300
const rememberedPrompts = new Map<string, string>()
const sessionPromptMap = computed(() => {
  const map = new Map<string, string>()
  for (const session of props.sessions) {
    const msgs = sessionStore.messages[session.id]
    if (!msgs || msgs.length === 0) {
      const remembered = rememberedPrompts.get(session.id)
      if (remembered) map.set(session.id, remembered)
      continue
    }
    const firstUserMsg = msgs.find((m: any) => m.role === 'user')
    if (!firstUserMsg) continue
    let content = firstUserMsg.content
    // Handle stringified JSON content arrays (e.g. '[{"type":"text","text":"..."}]')
    if (typeof content === 'string' && content.startsWith('[')) {
      try {
        content = JSON.parse(content)
      } catch {
        // not JSON, use as-is
      }
    }
    const text = typeof content === 'string'
      ? content
      : extractTextContent(content)
    if (text) {
      const preview = text.slice(0, PROMPT_PREVIEW_CHARS)
      rememberedPrompts.set(session.id, preview)
      map.set(session.id, preview)
    }
  }
  return map
})

// Compute context usage map from actual SDK data
const contextUsageMap = computed(() => {
  const map = new Map()

  for (const session of props.sessions) {
    const usage = metricsStore.getSessionContextUsage(session.id)
    if (usage) {
      map.set(session.id, usage)
    }
  }

  return map
})

const emit = defineEmits<{
  'create-new': []
  'select': [sessionId: string]
  'end': [sessionId: string]
  'delete': [sessionId: string]
  'delete-all': []
  'delete-all-but-this': [sessionId: string]
  /** Select mode: end / delete these sessions in one go (the page confirms). */
  'bulk-end': [sessionIds: string[]]
  'bulk-delete': [sessionIds: string[]]
}>()

// Needs you / Working / Today / Earlier. groupSessions drops duplicate ids,
// a safety net in case the backend sends a session twice.
const sessionsRef = computed(() => (Array.isArray(props.sessions) ? props.sessions : []))
const { groups, order, collapsed, toggleGroup, now } = useSessionGroups(sessionsRef)
const shownGroups = computed(() => groups.value.filter(g => g.rows.length > 0))

// ── Select mode: checkboxes on the rows, then End / Delete in one go ──
const selecting = ref(false)
const selected = ref<Set<string>>(new Set())
const selectAnchor = ref<string | null>(null)
const actionsMenu = ref<HTMLDetailsElement | null>(null)

// Workers under a row count as sessions of its group.
const earlierIds = computed(() => {
  const earlier = groups.value.find(g => g.id === 'earlier')
  return earlier ? groupSessionIds(earlier) : []
})
const allIds = computed(() => groups.value.flatMap(groupSessionIds))
const selection = computed(() => summarizeSelection(sessionsRef.value, selected.value))
const selectedCount = computed(() => selection.value.ids.length)
const allSelected = computed(() => allIds.value.length > 0 && selectedCount.value === allIds.value.length)

function closeActionsMenu() {
  if (actionsMenu.value) actionsMenu.value.open = false
}
function startSelecting(preselect: string[] = []) {
  closeActionsMenu()
  selected.value = new Set(preselect)
  selectAnchor.value = null
  selecting.value = true
  // "Clean up Earlier": show what is about to be acted on.
  if (preselect.length && earlierIds.value.some(id => preselect.includes(id)) && collapsed.value.earlier !== false) {
    toggleGroup('earlier')
  }
}
function stopSelecting() {
  selecting.value = false
  selected.value = new Set()
  selectAnchor.value = null
}
function onToggleSelect(id: string, range: boolean) {
  const visible = order.value.map(s => s.id)
  selected.value = range ? selectRange(selected.value, visible, selectAnchor.value, id) : toggleSelected(selected.value, id)
  selectAnchor.value = id
}
function toggleGroupSelection(group: SessionGroup<any>) {
  selected.value = toggleGroupSelected(selected.value, groupSessionIds(group))
}
function ariaChecked(group: SessionGroup<any>): 'true' | 'false' | 'mixed' {
  const state = groupCheckState(groupSessionIds(group), selected.value)
  return state === 'all' ? 'true' : state === 'some' ? 'mixed' : 'false'
}
function selectAllOrNone() {
  selected.value = allSelected.value ? new Set() : new Set(allIds.value)
}
function emitBulk(kind: 'bulk-end' | 'bulk-delete', ids: string[]) {
  if (!ids.length) return
  if (kind === 'bulk-end') emit('bulk-end', [...ids])
  else emit('bulk-delete', [...ids])
}

// Sessions deleted (here or elsewhere) leave the selection; after a bulk
// delete empties it, select mode ends.
watch(allIds, (ids) => {
  if (!selecting.value) return
  const before = selected.value.size
  selected.value = pruneSelected(selected.value, ids)
  if (before > 0 && selected.value.size === 0) stopSelecting()
})

// Escape leaves select mode (the newest open thing closes first).
useClosable(selecting, stopSelecting)

defineExpose({ stopSelecting })

// Keep the open session's row in view when it changes from the keyboard
// (⌥⌘↑/↓, ⌘[ ⌘], ⌘1…9) or anywhere else.
const listEl = ref<HTMLElement | null>(null)
watch(() => props.activeSessionId, () => nextTick(() => {
  listEl.value?.querySelector<HTMLElement>('.session-item.active')?.scrollIntoView?.({ block: 'nearest' })
}))

// List keyboard navigation (only while the list itself has focus), over the
// rows on screen: collapsed groups are skipped.
const listFocused = ref(false)
const focusedId = ref<string | null>(null)
function onListFocus() {
  listFocused.value = true
  const ids = order.value.map(s => s.id)
  focusedId.value = props.activeSessionId && ids.includes(props.activeSessionId) ? props.activeSessionId : (ids[0] ?? null)
}
function moveFocus(index: number) {
  const id = order.value[index]?.id
  if (!id) return
  focusedId.value = id
  nextTick(() => {
    const rows = listEl.value?.querySelectorAll<HTMLElement>('.session-item') ?? []
    for (const row of rows) {
      if (row.dataset.sessionId === id) row.scrollIntoView?.({ block: 'nearest' })
    }
  })
}
function onListKeydown(event: KeyboardEvent) {
  if (event.target !== event.currentTarget || event.metaKey || event.ctrlKey || event.altKey || event.isComposing) return
  const list = order.value
  const n = list.length
  if (!n) return
  const current = Math.max(0, list.findIndex(s => s.id === focusedId.value))
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    moveFocus(Math.max(0, Math.min(n - 1, current + (event.key === 'ArrowDown' ? 1 : -1))))
  } else if (event.key === 'Home' || event.key === 'End') {
    event.preventDefault()
    moveFocus(event.key === 'Home' ? 0 : n - 1)
  } else if (selecting.value && (event.key === ' ' || event.key === 'Enter')) {
    const id = focusedId.value ?? list[0]?.id
    if (id) {
      event.preventDefault()
      onToggleSelect(id, event.shiftKey)
    }
  } else if (event.key === 'Enter') {
    const session = list.find(s => s.id === focusedId.value) ?? list[0]
    if (session) {
      event.preventDefault()
      emit('select', session.id)
    }
  }
}
</script>

<style scoped>
.sessions-sidebar {
  width: 300px;
  transition: width .2s ease;
  background: var(--card-bg);
  border-right: 1px solid var(--border-color);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
}

.sidebar-header {
  padding: 16px;
  border-bottom: 1px solid var(--border-color);
  flex-shrink: 0;
}

.header-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.sidebar-header h3 {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
  color: var(--text-primary);
}

.nav-mode-indicator {
  font-size: 1rem;
  animation: pulse 1s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.6;
  }
}

.session-buttons {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.btn-new-session {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 10px;
  background: var(--accent-purple);
  color: white;
  border: none;
  border-radius: 8px;
  font-size: 0.9rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-new-session:hover:not(:disabled) {
  background: var(--accent-purple-hover);
}

.btn-new-session:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-delete-all {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 10px;
  background: var(--bg-secondary);
  color: var(--text-secondary);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  font-size: 0.9rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-delete-all:hover:not(:disabled) {
  background: var(--bg-tertiary);
  border-color: var(--text-secondary);
  color: var(--text-primary);
}

.btn-delete-all:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-spinner-small {
  width: 16px;
  height: 16px;
  border: 2px solid var(--overlay-text);
  border-top-color: var(--overlay-text-active);
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.sessions-list {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
  min-height: 0;
}

.sessions-list:focus { outline: none; }
.sessions-list:focus-visible { box-shadow: inset 0 0 0 2px var(--accent-purple, #8b7cf6); border-radius: 8px; }

/* ── Groups: Needs you / Working / Today / Earlier ── */
.session-group {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.session-group + .session-group {
  margin-top: 10px;
}

.group-header {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 4px 8px 4px 10px;
  border: none;
  background: transparent;
  font-family: inherit;
  font-size: 0.68rem;
  font-weight: 700;
  letter-spacing: 0.07em;
  text-transform: uppercase;
  color: var(--text-muted, var(--text-secondary));
  text-align: left;
}
.group-header.collapsible {
  cursor: pointer;
  border-radius: 6px;
}
.group-header.collapsible:hover {
  color: var(--text-primary);
  background: var(--bg-secondary);
}
.group-needs .group-header {
  color: var(--status-warning, #b45309);
}
.group-count {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  letter-spacing: 0;
  opacity: 0.8;
}
.group-chevron {
  flex-shrink: 0;
  transition: transform 0.15s ease;
}
.group-chevron.closed {
  transform: rotate(-90deg);
}
@media (prefers-reduced-motion: reduce) {
  .group-chevron { transition: none; }
}

.no-sessions {
  text-align: center;
  padding: 32px 16px;
  color: var(--text-secondary);
  font-size: 0.9rem;
}

@media (min-width: 769px) { .sessions-sidebar.sessions-sidebar-wide { width: 400px; } }
@media (prefers-reduced-motion: reduce) { .sessions-sidebar { transition: none; } }

/* Responsive */
@media (max-width: 768px) {
  .sessions-sidebar {
    width: 100%;
    border-right: none;
  }

  .sidebar-header {
    padding: 8px 12px;
  }

  .header-top {
    margin-bottom: 6px;
  }

  .header-top h3 {
    font-size: 0.85rem;
  }

  .session-buttons {
    flex-direction: row;
    gap: 6px;
  }

  .btn-new-session,
  .btn-delete-all {
    font-size: 0.8rem;
    padding: 6px 8px;
    border-radius: 6px;
  }

  .sessions-list {
    max-height: 40vh;
    padding: 4px;
  }
}

@media (max-width: 480px) {
  .sidebar-header {
    padding: 6px 10px;
  }

  .header-top h3 {
    font-size: 0.8rem;
  }

  .btn-new-session,
  .btn-delete-all {
    font-size: 0.75rem;
    padding: 5px 6px;
  }

  .btn-delete-all span:not(.icon) {
    display: none;
  }
}

.sessions-sidebar { font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; }
.header-top { display: flex; justify-content: space-between; align-items: center; }
.session-actions { position: relative; }
.session-actions summary { cursor: pointer; list-style: none; padding: 0 8px; color: var(--text-secondary); font-size: 1.25rem; }
.session-actions summary::-webkit-details-marker { display: none; }
.session-actions-menu { position: absolute; right: 0; top: 100%; z-index: 10; width: 220px; display: flex; flex-direction: column; gap: 2px; padding: 4px; background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: 8px; box-shadow: 0 4px 12px var(--shadow-color); }
.session-actions-menu .btn-menu-item { width: 100%; display: flex; align-items: center; justify-content: flex-start; gap: 8px; padding: 8px 10px; border: none; border-radius: 6px; background: transparent; color: var(--text-primary); font: inherit; font-size: 0.85rem; text-align: left; cursor: pointer; }
.session-actions-menu .btn-menu-item:hover:not(:disabled) { background: var(--bg-tertiary); }
.session-actions-menu .btn-menu-item:disabled { opacity: 0.5; cursor: not-allowed; }
.session-actions-menu .btn-delete-all { color: var(--text-secondary); }

/* ── Select mode ── */
.select-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 40px;
  padding: 0 4px;
}
.select-count {
  flex: 1;
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--text-primary);
  font-variant-numeric: tabular-nums;
}
.btn-link {
  border: none;
  background: transparent;
  padding: 4px 6px;
  border-radius: 6px;
  font: inherit;
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--accent-purple, #8b5cf6);
  cursor: pointer;
}
.btn-link:hover { background: var(--bg-secondary); }

.group-select-box {
  flex-shrink: 0;
  width: 13px;
  height: 13px;
  border: 1.5px solid currentColor;
  border-radius: 3px;
  cursor: pointer;
  position: relative;
}
.group-select-box.all { background: var(--accent-purple, #8b5cf6); border-color: var(--accent-purple, #8b5cf6); }
.group-select-box.all::after {
  content: '';
  position: absolute;
  left: 3px;
  top: 0;
  width: 3px;
  height: 7px;
  border: solid var(--card-bg);
  border-width: 0 2px 2px 0;
  transform: rotate(45deg);
}
.group-select-box.some::after {
  content: '';
  position: absolute;
  left: 2px;
  right: 2px;
  top: 4px;
  height: 2px;
  background: currentColor;
}

.bulk-bar {
  flex-shrink: 0;
  display: flex;
  gap: 8px;
  padding: 10px 12px;
  border-top: 1px solid var(--border-color);
  background: var(--card-bg);
}
.btn-bulk {
  flex: 1;
  padding: 9px 10px;
  border-radius: 8px;
  border: 1px solid var(--border-color);
  background: var(--bg-secondary);
  color: var(--text-primary);
  font: inherit;
  font-size: 0.88rem;
  font-weight: 600;
  cursor: pointer;
  font-variant-numeric: tabular-nums;
}
.btn-bulk:hover:not(:disabled) { background: var(--bg-tertiary); }
.btn-bulk:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-bulk.danger {
  background: color-mix(in srgb, var(--status-error, #dc3545) 12%, var(--card-bg));
  border-color: color-mix(in srgb, var(--status-error, #dc3545) 45%, transparent);
  color: var(--status-error, #dc3545);
}
.btn-bulk.danger:hover:not(:disabled) { background: color-mix(in srgb, var(--status-error, #dc3545) 20%, var(--card-bg)); }
</style>
