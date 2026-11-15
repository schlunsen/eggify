<template>
  <!--
    A worker session in the sidebar: one line, indented under the session that
    started it (utils/agents/sessionGroups.ts). Role, model, status and, when
    there is one, the review verdict. A click opens it; in select mode it is a
    checkbox like any session row.
  -->
  <div
    class="session-item worker-row"
    :class="[`status-${view.status}`, { active: isActive, focused: isFocused, selectable, selected: selectable && selected }]"
    :data-session-id="session.id"
    :title="title"
    :aria-selected="selectable ? selected : undefined"
    data-testid="worker-session-row"
    @click="onClick"
  >
    <span
      v-if="selectable"
      class="worker-select-box"
      :class="{ checked: selected }"
      role="checkbox"
      :aria-checked="selected"
      :aria-label="`Select ${view.role} worker`"
      data-testid="session-select-box"
    >
      <svg v-if="selected" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.5" aria-hidden="true">
        <polyline points="20 6 9 17 4 12" />
      </svg>
    </span>
    <span class="worker-branch-line" aria-hidden="true">└</span>
    <span class="worker-dot" :class="`status-${view.status}`" aria-hidden="true"></span>
    <span class="worker-role" :class="view.role">{{ WORKER_ROLE_LABELS[view.role] }}</span>
    <span v-if="view.modelLabel" class="worker-model">{{ view.modelLabel }}</span>
    <span class="worker-status">{{ WORKER_STATUS_SHORT[view.status] }}</span>
    <span v-if="view.verdict" class="worker-verdict" :class="view.verdict" data-testid="worker-verdict">{{ VERDICT_LABELS[view.verdict] }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { VERDICT_LABELS, WORKER_ROLE_LABELS, WORKER_STATUS_LABELS, type TeamWorkerView, type WorkerStatus } from '~/utils/agents/team'

const props = withDefaults(defineProps<{
  session: { id: string }
  view: TeamWorkerView
  isActive?: boolean
  isFocused?: boolean
  /** Select mode: a click toggles the row's checkbox instead of opening it. */
  selectable?: boolean
  selected?: boolean
}>(), {
  isActive: false,
  isFocused: false,
  selectable: false,
  selected: false,
})

const emit = defineEmits<{
  select: [sessionId: string]
  'toggle-select': [sessionId: string, range: boolean]
}>()

const WORKER_STATUS_SHORT: Record<WorkerStatus, string> = { working: 'working', waiting: 'paused', idle: 'idle', ended: 'ended' }

const title = computed(() => [
  `${WORKER_ROLE_LABELS[props.view.role]} worker`,
  props.view.model,
  WORKER_STATUS_LABELS[props.view.status],
  props.view.verdict ? VERDICT_LABELS[props.view.verdict] : '',
  props.view.role === 'build' && props.view.branch ? `branch ${props.view.branch}` : '',
].filter(Boolean).join(' · '))

function onClick(event: MouseEvent) {
  if (props.selectable) emit('toggle-select', props.session.id, event.shiftKey)
  else emit('select', props.session.id)
}
</script>

<style scoped>
.worker-row {
  position: relative;
  display: flex;
  align-items: center;
  gap: 0.4rem;
  min-width: 0;
  margin-left: 1.5rem;
  padding: 0.2rem 0.6rem 0.2rem 0.4rem;
  border-radius: 8px;
  color: var(--text-secondary);
  font-size: 0.75rem;
  cursor: pointer;
  white-space: nowrap;
}
.worker-row:hover { background: var(--bg-secondary); }
.worker-row.active { background: var(--overlay-bg-active); color: var(--text-primary); }
.worker-row.focused { box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--accent-purple) 45%, transparent); }
.worker-row.selectable { user-select: none; }
.worker-row.selected { background: var(--overlay-bg-active, var(--bg-secondary)); }
.worker-row.status-ended { opacity: 0.6; }
.worker-branch-line { opacity: 0.45; }
.worker-dot {
  flex-shrink: 0;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-secondary);
  opacity: 0.45;
}
.worker-dot.status-working {
  background: var(--status-success);
  opacity: 1;
  animation: worker-dot-pulse 1.6s ease-in-out infinite;
}
.worker-dot.status-waiting { background: var(--status-warning); opacity: 1; }
.worker-role {
  flex-shrink: 0;
  padding: 0 6px;
  border: 1px solid var(--border-color);
  border-radius: 999px;
  color: var(--text-primary);
  font-weight: 600;
}
.worker-model { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
.worker-status { flex-shrink: 0; }
.worker-verdict { flex-shrink: 0; margin-left: auto; overflow: hidden; text-overflow: ellipsis; }
.worker-verdict.approve { color: var(--status-success); }
.worker-verdict.changes_requested { color: var(--status-warning); }
.worker-select-box {
  flex-shrink: 0;
  width: 14px;
  height: 14px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1.5px solid var(--text-secondary);
  border-radius: 4px;
  background: var(--card-bg);
  color: var(--card-bg);
}
.worker-select-box.checked { background: var(--accent-purple); border-color: var(--accent-purple); }
@keyframes worker-dot-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}
@media (prefers-reduced-motion: reduce) {
  .worker-dot.status-working { animation: none; }
}
</style>
