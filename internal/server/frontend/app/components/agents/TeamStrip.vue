<template>
  <!--
    The build–review work of a session that started workers (a team
    orchestrator, or any session with worker sessions): which step it is at and
    who works on it. A chip opens that worker's session.
    Logic: utils/agents/team.ts.
  -->
  <div class="team-strip" role="group" :aria-label="`Team: ${stageLabel}`" data-testid="team-strip" :data-stage="progress.stage">
    <ol class="team-steps" aria-label="Steps">
      <li
        v-for="(step, index) in steps"
        :key="step.id"
        class="team-step"
        :class="{ current: step.current, done: step.done, problem: step.current && progress.stage === 'stopped' }"
        :aria-current="step.current ? 'step' : undefined"
        :data-step="step.id"
      >
        <span v-if="index > 0" class="team-arrow" aria-hidden="true">→</span>
        <span class="team-step-label">{{ step.label }}</span>
      </li>
    </ol>
    <span class="team-stage" data-testid="team-stage">{{ stageLabel }}</span>
    <button
      v-if="now"
      type="button"
      class="team-now"
      data-testid="team-now"
      :title="`${WORKER_ROLE_LABELS[now.worker.role]} worker: ${now.action.full}`"
      @click="$emit('open', now.worker.sessionId)"
    >
      <span class="team-now-icon" aria-hidden="true">{{ now.action.icon }}</span>
      <span class="team-now-text">{{ now.action.text }}</span>
    </button>
    <span v-if="progress.workers.length" class="team-workers">
      <button
        v-for="worker in progress.workers"
        :key="worker.sessionId"
        type="button"
        class="team-chip"
        :class="[`status-${worker.status}`, { current: worker.current }]"
        :title="chipTitle(worker)"
        :aria-label="chipTitle(worker)"
        data-testid="team-worker-chip"
        :data-session-id="worker.sessionId"
        @click="$emit('open', worker.sessionId)"
      >
        <span class="team-dot" :class="`status-${worker.status}`" aria-hidden="true"></span>
        <span class="team-role">{{ WORKER_ROLE_LABELS[worker.role] }}</span>
        <span v-if="worker.modelLabel" class="team-model">{{ worker.modelLabel }}</span>
        <span v-if="worker.role === 'review' && worker.verdict" class="team-verdict" :class="worker.verdict">{{ VERDICT_LABELS[worker.verdict] }}</span>
      </button>
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useWorkerActivity } from '~/composables/agents/useWorkerActivity'
import { subagentNowAction } from '~/utils/agents/subagentActivity'
import {
  TEAM_STAGE_LABELS,
  TEAM_STEPS,
  VERDICT_LABELS,
  WORKER_ROLE_LABELS,
  WORKER_STATUS_LABELS,
  reviewStepLabel,
  type TeamProgress,
  type TeamStep,
  type TeamWorkerView,
} from '~/utils/agents/team'

const props = defineProps<{ progress: TeamProgress }>()

defineEmits<{ open: [sessionId: string] }>()

const STEP_LABELS: Record<TeamStep, string> = { plan: 'Plan', build: 'Build', review: 'Review', merge: 'Merge' }

const steps = computed(() => {
  const at = TEAM_STEPS.indexOf(props.progress.step)
  return TEAM_STEPS.map((id, index) => ({
    id,
    label: id === 'review' ? reviewStepLabel(props.progress) : STEP_LABELS[id],
    current: index === at,
    done: index < at,
  }))
})

const stageLabel = computed(() => TEAM_STAGE_LABELS[props.progress.stage])

// What the working worker does right now (the reviewer while it reviews, else
// the builder, else any working worker), from its worker_activity lines.
const { getWorkerLines } = useWorkerActivity()
const now = computed(() => {
  const { builder, reviewer, workers } = props.progress
  const worker = [reviewer, builder, ...workers].find(w => w?.status === 'working')
  if (!worker) return null
  const lines = getWorkerLines(worker.sessionId)
  if (!lines.length) return null
  return { worker, action: subagentNowAction(lines) }
})

/** "Open build worker · Sonnet 5.5 · Working · Changes requested" */
function chipTitle(worker: TeamWorkerView): string {
  const parts = [
    `Open ${worker.role} worker`,
    worker.modelLabel,
    WORKER_STATUS_LABELS[worker.status],
    worker.verdict ? VERDICT_LABELS[worker.verdict] : '',
    worker.role === 'build' && worker.branch ? `branch ${worker.branch}` : '',
  ]
  return parts.filter(Boolean).join(' · ')
}
</script>

<style scoped>
.team-strip {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 12px;
  margin: 8px 16px 0;
  padding: 6px 12px;
  border: 1px solid var(--border-color);
  border-radius: 10px;
  background: var(--bg-secondary);
  color: var(--text-secondary);
  font-size: 12px;
}
.team-steps {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.team-step { display: inline-flex; align-items: center; gap: 6px; }
.team-arrow { opacity: 0.5; }
.team-step.done .team-step-label { color: var(--text-primary); }
.team-step.current .team-step-label {
  padding: 1px 8px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--accent-purple) 14%, transparent);
  color: var(--text-primary);
  font-weight: 600;
}
.team-step.current.problem .team-step-label {
  background: color-mix(in srgb, var(--status-warning) 18%, transparent);
}
.team-stage { color: var(--text-secondary); }
.team-now {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  max-width: min(420px, 100%);
  padding: 0;
  border: 0;
  background: none;
  color: var(--text-primary);
  font: inherit;
  cursor: pointer;
}
.team-now:hover .team-now-text { text-decoration: underline; }
.team-now:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; border-radius: 4px; }
.team-now-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.team-workers { display: flex; flex-wrap: wrap; gap: 6px; margin-left: auto; }
.team-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  padding: 2px 9px;
  border: 1px solid var(--border-color);
  border-radius: 999px;
  background: var(--bg-primary);
  color: var(--text-secondary);
  font: inherit;
  cursor: pointer;
}
.team-chip.current { color: var(--text-primary); }
.team-chip:hover { border-color: var(--accent-purple); }
.team-chip:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }
.team-chip.status-ended { opacity: 0.65; }
.team-role { font-weight: 600; }
.team-model { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.team-dot {
  flex-shrink: 0;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-secondary);
  opacity: 0.5;
}
.team-dot.status-working {
  background: var(--status-success);
  opacity: 1;
  animation: team-dot-pulse 1.6s ease-in-out infinite;
}
.team-dot.status-waiting { background: var(--status-warning); opacity: 1; }
.team-dot.status-ended { opacity: 0.3; }
.team-verdict { padding-left: 6px; border-left: 1px solid var(--border-color); }
.team-verdict.approve { color: var(--status-success); }
.team-verdict.changes_requested { color: var(--status-warning); }
@keyframes team-dot-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}
@media (prefers-reduced-motion: reduce) {
  .team-dot.status-working { animation: none; }
}
</style>
