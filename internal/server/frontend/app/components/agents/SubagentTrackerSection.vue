<template>
  <div class="subagent-tracker-section">
    <div class="tracker-header">
      <button
        type="button"
        class="tracker-toggle"
        :aria-expanded="expanded"
        aria-controls="subagent-tracker-content"
        @click="expanded = !expanded"
      >
        <Icon name="mdi:robot-outline" size="18" class="header-icon" />
        <span class="header-title">Subagents</span>
        <span class="header-summary" :class="{ running: runningCount > 0, 'has-failures': failedCount > 0 }">{{ summary }}</span>
        <svg
          class="expand-chevron"
          :class="{ expanded }"
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          aria-hidden="true"
        >
          <polyline points="9 18 15 12 9 6"></polyline>
        </svg>
      </button>
      <button
        v-if="agents.length > 0"
        type="button"
        class="open-view-btn"
        :title="subagentsTitle"
        aria-label="Open the subagents view"
        @click="$emit('open-view')"
      >⤢</button>
    </div>

    <transition name="expand">
      <div v-if="expanded" id="subagent-tracker-content" class="tracker-content">
        <!-- Agent cards -->
        <div
          v-for="agent in visibleAgents"
          :key="agent.agent_id"
          class="agent-card"
          :class="[agent.status, { compact: agent.status === 'completed', 'just-finished': isRecentlyFinished(agent.agent_id) }]"
          @click="$emit('view-agent', agent.agent_id)"
        >
          <div class="agent-top-row">
            <div class="agent-info">
              <span class="agent-status-icon">
                <template v-if="agent.status === 'running'">
                  <span class="agent-status-dot running"></span>
                </template>
                <template v-else-if="agent.status === 'completed'">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" class="status-check-icon">
                    <polyline points="20 6 9 17 4 12"></polyline>
                  </svg>
                </template>
                <template v-else-if="agent.status === 'failed'">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" class="status-x-icon">
                    <line x1="18" y1="6" x2="6" y2="18"></line>
                    <line x1="6" y1="6" x2="18" y2="18"></line>
                  </svg>
                </template>
                <template v-else>
                  <span class="agent-status-dot" :class="agent.status"></span>
                </template>
              </span>
              <span class="agent-desc">{{ agent.description || agent.subagent_type }}</span>
            </div>
            <button
              class="agent-dismiss-btn"
              :title="agent.status === 'running' ? 'Hide (agent continues running)' : 'Dismiss'"
              @click.stop="dismissAgentCard(agent.agent_id)"
            >&times;</button>
            <span class="agent-time">{{ formatElapsedTime(agent) }}</span>
          </div>

          <template v-if="agent.status !== 'completed'">
            <div class="agent-meta-row">
              <div v-if="agent.subagent_type" class="agent-type-badge">
                {{ agent.subagent_type }}
              </div>
              <span v-if="agent.status === 'completed'" class="agent-completed-label">completed</span>
              <span v-else-if="agent.status === 'failed'" class="agent-failed-label">failed</span>
            </div>

            <!-- Progress bar for running agents -->
            <div v-if="agent.status === 'running' && agent.progress > 0" class="mini-progress">
              <div
                class="mini-progress-fill"
                :style="{ width: `${Math.round(agent.progress * 100)}%` }"
              ></div>
            </div>

            <!-- Completed progress bar (full, green) -->
            <div v-if="agent.status === 'completed'" class="mini-progress completed">
              <div class="mini-progress-fill completed" style="width: 100%"></div>
            </div>

            <!-- Last output preview -->
            <div v-if="agent.last_output" class="agent-output-preview">
              {{ truncate(agent.last_output, 80) }}
            </div>

          </template>

          <!-- Error message -->
          <div v-if="agent.error_message" class="agent-error-preview">
            {{ truncate(agent.error_message, 80) }}
          </div>
        </div>

        <button v-if="hiddenDoneCount > 0 || showAllDone" type="button" class="show-more-btn" @click="showAllDone = !showAllDone">
          {{ showAllDone ? 'Show fewer finished' : `Show ${hiddenDoneCount} more finished` }}
        </button>

        <!-- Empty state -->
        <div v-if="agents.length === 0" class="empty-state">
          <span class="empty-icon">
            <Icon name="mdi:robot-off-outline" size="20" />
          </span>
          <span class="empty-text">No subagents spawned yet</span>
        </div>
      </div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useBackgroundAgents, type BackgroundAgent } from '~/composables/agents/useBackgroundAgents'
import { useShortcutLabels } from '~/composables/useKeyboardShortcuts'
const subagentsTitle = useShortcutLabels().title('Open the subagents view', 'subagents.toggle')

interface Props {
  sessionId: string
}

const props = defineProps<Props>()

defineEmits<{
  (e: 'view-agent', agentId: string): void
  (e: 'open-view'): void
}>()

const expanded = ref(false)
const dismissedIds = ref<Set<string>>(new Set())

const {
  getAgentsForSession,
  getRunningAgentsForSession,
  formatElapsedTime,
  isRecentlyFinished,
} = useBackgroundAgents()

const agents = computed(() => {
  // Access tick to force reactivity on each second
  tick.value
  return getAgentsForSession(props.sessionId).filter(a => !dismissedIds.value.has(a.agent_id))
})
// Running and failed agents stay as full cards, finished ones become one-line rows (newest
// first) and only the latest few show, so twenty subagents don't turn the sidebar into a wall.
const DONE_VISIBLE = 5
const showAllDone = ref(false)
const finishedTime = (a: BackgroundAgent) => Date.parse(a.completed_at || a.updated_at || a.created_at || '') || 0
const statusRank = (status: string) => status === 'running' ? 0 : status === 'completed' ? 3 : status === 'failed' ? 1 : 2
const orderedAgents = computed(() => {
  const list = agents.value.map((agent, index) => ({ agent, index }))
  list.sort((x, y) => {
    const rx = statusRank(x.agent.status)
    const ry = statusRank(y.agent.status)
    if (rx !== ry) return rx - ry
    if (rx === 3) return finishedTime(y.agent) - finishedTime(x.agent)
    return x.index - y.index
  })
  return list.map(item => item.agent)
})
const doneAgents = computed(() => orderedAgents.value.filter(a => a.status === 'completed'))
const hiddenDoneCount = computed(() => showAllDone.value ? 0 : Math.max(0, doneAgents.value.length - DONE_VISIBLE))
const visibleAgents = computed(() => {
  if (showAllDone.value) return orderedAgents.value
  const hidden = new Set(doneAgents.value.slice(DONE_VISIBLE).map(a => a.agent_id))
  return orderedAgents.value.filter(a => !hidden.has(a.agent_id))
})
const runningCount = computed(() =>
  agents.value.filter(a => a.status === 'running').length
)
const finishedCount = computed(() =>
  agents.value.filter(a => a.status === 'completed' || a.status === 'failed').length
)
const failedCount = computed(() =>
  agents.value.filter(a => a.status === 'failed').length
)

// One line for the collapsed row: "2 running · 1 done", "1 failed", "None".
const summary = computed(() => {
  if (agents.value.length === 0) return 'None'
  const parts: string[] = []
  if (runningCount.value) parts.push(`${runningCount.value} running`)
  const done = finishedCount.value - failedCount.value
  if (done) parts.push(`${done} done`)
  if (failedCount.value) parts.push(`${failedCount.value} failed`)
  return parts.join(' · ') || `${agents.value.length}`
})

const dismissAgentCard = (agentId: string) => {
  dismissedIds.value.add(agentId)
}

// Auto-expand when an agent finishes (if collapsed)
const autoExpandOnFinish = () => {
  if (!expanded.value) {
    for (const agent of agents.value) {
      if ((agent.status === 'completed' || agent.status === 'failed') && isRecentlyFinished(agent.agent_id)) {
        expanded.value = true
        break
      }
    }
  }
}

// Force re-render every second for elapsed time updates
const tick = ref(0)
let tickInterval: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  tickInterval = setInterval(() => {
    tick.value++
    autoExpandOnFinish()
  }, 1000)
})

onUnmounted(() => {
  if (tickInterval) clearInterval(tickInterval)
})

const truncate = (text: string, maxLen: number): string => {
  if (text.length <= maxLen) return text
  return text.substring(0, maxLen) + '...'
}
</script>

<style scoped>
.open-view-btn {
  flex-shrink: 0;
  margin-right: 8px;
  padding: 2px 6px;
  font-size: 12px;
  line-height: 1;
  color: var(--text-secondary);
  background: transparent;
  border: 1px solid var(--border-color);
  border-radius: 5px;
  cursor: pointer;
}
.open-view-btn:hover { color: var(--text-primary); border-color: #9d8bff; }
.open-view-btn:focus-visible { outline: 2px solid #9d8bff; outline-offset: 2px; }

.subagent-tracker-section {
  overflow: hidden;
}

.tracker-header {
  display: flex;
  align-items: center;
}

.tracker-toggle {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
  min-height: 44px;
  padding: 0 12px;
  background: transparent;
  border: 0;
  color: var(--text-primary);
  font: inherit;
  font-size: 0.8125rem;
  text-align: left;
  cursor: pointer;
}

.tracker-toggle:hover {
  background: var(--overlay-bg-hover);
}

.tracker-toggle:focus-visible {
  outline: 2px solid var(--accent-purple);
  outline-offset: -2px;
}

.header-icon {
  flex-shrink: 0;
  color: var(--text-secondary);
}

.header-title {
  flex-shrink: 0;
  font-weight: 500;
}

.header-summary {
  flex: 1;
  min-width: 0;
  text-align: right;
  font-size: 0.75rem;
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.header-summary.running {
  color: var(--status-processing, #c2185b);
  font-weight: 500;
}

.header-summary.has-failures {
  color: var(--status-error, #ef4444);
}

.expand-chevron {
  flex-shrink: 0;
  color: var(--text-secondary);
  transition: transform 0.2s ease;
}

.expand-chevron.expanded {
  transform: rotate(90deg);
}

.tracker-content {
  padding: 0 10px 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: min(420px, 50vh);
  overflow-y: auto;
  scrollbar-width: thin;
}

.agent-card {
  background: var(--bg-secondary);
  border-radius: 8px;
  padding: 8px 10px;
  cursor: pointer;
  border-left: 3px solid var(--accent-purple);
  transition: all 0.3s ease;
  position: relative;
  overflow: hidden;
  flex-shrink: 0;
}

.agent-card.compact {
  padding: 5px 10px;
}

.show-more-btn {
  flex-shrink: 0;
  align-self: stretch;
  padding: 5px 10px;
  border: 1px dashed var(--border-color, rgba(255, 255, 255, 0.15));
  border-radius: 8px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 0.75rem;
  cursor: pointer;
}

.show-more-btn:hover {
  color: var(--text-primary);
}

.agent-card:hover {
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.12);
  transform: translateX(2px);
}

.agent-card.completed {
  border-left-color: var(--status-success, #22c55e);
}

.agent-card.completed.just-finished {
  animation: completion-flash 1.5s ease-out;
}

.agent-card.failed {
  border-left-color: var(--status-error, #ef4444);
}

.agent-card.failed.just-finished {
  animation: failure-flash 1.5s ease-out;
}

.agent-card.waiting {
  border-left-color: var(--status-warning, #f59e0b);
}

@keyframes completion-flash {
  0% {
    background: rgba(34, 197, 94, 0.3);
    box-shadow: 0 0 12px rgba(34, 197, 94, 0.4);
  }
  100% {
    background: var(--bg-secondary);
    box-shadow: none;
  }
}

@keyframes failure-flash {
  0% {
    background: rgba(239, 68, 68, 0.25);
    box-shadow: 0 0 12px rgba(239, 68, 68, 0.3);
  }
  100% {
    background: var(--bg-secondary);
    box-shadow: none;
  }
}

.agent-top-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.agent-info {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  flex: 1;
}

.agent-status-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 16px;
  height: 16px;
}

.status-check-icon {
  color: var(--status-success, #22c55e);
}

.status-x-icon {
  color: var(--status-error, #ef4444);
}

.agent-status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--accent-purple);
}

.agent-status-dot.running {
  background: var(--accent-purple);
  animation: dot-pulse 1.5s ease-in-out infinite;
}

.agent-status-dot.waiting {
  background: var(--status-warning, #f59e0b);
}

@keyframes dot-pulse {
  0%, 100% { opacity: 1; box-shadow: 0 0 0 0 rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4); }
  50% { opacity: 0.6; box-shadow: 0 0 0 4px rgba(var(--accent-purple-rgb, 139, 92, 246), 0); }
}

.agent-desc {
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.agent-dismiss-btn {
  background: none;
  border: none;
  color: var(--text-muted);
  font-size: 1rem;
  cursor: pointer;
  padding: 0 2px;
  line-height: 1;
  opacity: 0;
  transition: all 0.15s ease;
  flex-shrink: 0;
}

.agent-card:hover .agent-dismiss-btn {
  opacity: 0.6;
}

.agent-dismiss-btn:hover {
  opacity: 1 !important;
  color: var(--status-error, #ef4444);
}

.agent-time {
  font-size: 0.7rem;
  color: var(--text-muted);
  white-space: nowrap;
  font-family: 'Monaco', 'Menlo', monospace;
  flex-shrink: 0;
}

.agent-meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 4px;
}

.agent-type-badge {
  display: inline-block;
  font-size: 0.65rem;
  padding: 1px 6px;
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.15);
  color: var(--accent-purple);
  border-radius: 4px;
  font-weight: 500;
}

.agent-completed-label {
  font-size: 0.65rem;
  font-weight: 600;
  color: var(--status-success, #22c55e);
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.agent-failed-label {
  font-size: 0.65rem;
  font-weight: 600;
  color: var(--status-error, #ef4444);
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.mini-progress {
  height: 3px;
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.15);
  border-radius: 2px;
  overflow: hidden;
  margin-top: 6px;
}

.mini-progress-fill {
  height: 100%;
  background: linear-gradient(90deg, var(--accent-purple), #a855f7);
  border-radius: 2px;
  transition: width 0.3s ease;
}

.mini-progress.completed {
  background: rgba(34, 197, 94, 0.15);
}

.mini-progress-fill.completed {
  background: linear-gradient(90deg, var(--status-success, #22c55e), #4ade80);
}

.agent-output-preview {
  margin-top: 4px;
  font-size: 0.7rem;
  color: var(--text-muted);
  font-family: 'Monaco', 'Menlo', monospace;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding: 3px 6px;
  background: rgba(0, 0, 0, 0.15);
  border-radius: 4px;
}

.agent-error-preview {
  margin-top: 4px;
  font-size: 0.7rem;
  color: var(--status-error, #ef4444);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding: 3px 6px;
  background: rgba(239, 68, 68, 0.08);
  border-radius: 4px;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 16px 8px;
  opacity: 0.5;
}

.empty-icon {
  color: var(--text-muted);
  opacity: 0.6;
}

.empty-text {
  font-size: 0.75rem;
  color: var(--text-muted);
  text-align: center;
}

/* Expand transition */
.expand-enter-active,
.expand-leave-active {
  transition: all 0.2s ease;
  overflow: hidden;
}

.expand-enter-from,
.expand-leave-to {
  opacity: 0;
  max-height: 0;
  padding-top: 0;
  padding-bottom: 0;
}
</style>
