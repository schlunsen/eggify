<template>
  <transition name="modal-fade">
    <div v-if="show && question" class="question-modal-overlay" :class="{ zen: variant === 'zen' }" @click="handleCancel" tabindex="0" ref="modalOverlay">
      <!-- Dedicated card: an agent proposes a loop session with permission prompts off -->
      <div v-if="loop" class="question-modal loop-card" @click.stop data-testid="loop-session-card">
        <div class="modal-header">
          <div class="modal-icon">🔁</div>
          <div class="header-content">
            <h3 class="modal-title">Start a loop session?</h3>
            <p class="loop-lede">
              The agent wants to start a new session that works on its own until the check passes or a limit is hit.
            </p>
            <div class="loop-badges">
              <span class="loop-badge danger" title="Permission prompts are off: the loop runs every tool without asking">YOLO · no permission prompts</span>
              <span class="loop-badge" :class="{ danger: !loop.sandboxed }">{{ loop.sandboxed ? 'Sandboxed' : 'Not sandboxed' }}</span>
            </div>
          </div>
        </div>

        <div class="modal-body loop-body">
          <div class="loop-field">
            <div class="loop-label">Goal</div>
            <div class="loop-goal">{{ loop.goal }}</div>
          </div>
          <div class="loop-field">
            <div class="loop-label">Verify command</div>
            <pre v-if="loop.verify_command" class="loop-code">{{ loop.verify_command }}</pre>
            <div v-else class="loop-muted">None: it runs until the agent reports done or a limit is hit</div>
          </div>
          <div v-if="loop.context" class="loop-field">
            <div class="loop-label">Notes for the first turn</div>
            <div class="loop-goal loop-notes">{{ loop.context }}</div>
          </div>
          <dl class="loop-grid">
            <div><dt>Folder</dt><dd class="loop-path" :title="loop.working_directory">{{ shortPath(loop.working_directory) }}</dd></div>
            <div><dt>Model</dt><dd>{{ loop.model }}<span v-if="loop.provider && loop.provider !== 'claude'" class="loop-muted"> · {{ loop.provider }}</span></dd></div>
            <div><dt>Limits</dt><dd>{{ loop.max_iterations }} iterations · {{ loop.timeout_minutes }} min</dd></div>
          </dl>
          <p class="loop-muted loop-foot">
            It reports back to this session when it ends, and this agent can send it messages while it runs.
          </p>
        </div>

        <div class="modal-actions">
          <button class="btn btn-secondary" :disabled="!connected" data-testid="loop-deny" @click="answer(LOOP_DENY)">Deny</button>
          <button class="btn btn-secondary" :disabled="!connected" title="Hide for now; the agent keeps waiting" @click="handleCancel">Later</button>
          <button class="btn btn-secondary" :disabled="!connected" data-testid="loop-edit" title="Open these settings in Create Session to change them first" @click="answer(LOOP_EDIT)">Edit first</button>
          <button class="btn btn-primary btn-loop-start" :disabled="!connected" data-testid="loop-start" @click="answer(LOOP_START)">
            Start loop
          </button>
        </div>
        <div v-if="!connected" class="connection-warning">
          ⚠️ Connection lost - this request may no longer be valid
        </div>
      </div>

      <!-- Dedicated card: an agent proposes a worker session (another model builds or reviews) -->
      <div v-else-if="worker" class="question-modal loop-card" role="dialog" aria-labelledby="worker-card-title" @click.stop data-testid="worker-session-card">
        <div class="modal-header">
          <div class="modal-icon" aria-hidden="true">{{ worker.mode === 'review' ? '🔎' : '🛠️' }}</div>
          <div class="header-content">
            <h3 id="worker-card-title" class="modal-title">{{ workerProposalTitle(worker) }}</h3>
            <p class="loop-lede">
              {{ worker.mode === 'review'
                ? 'The agent wants another session to review the work and report back. It cannot change files.'
                : 'The agent wants to hand this task to another session, which works on it and reports back.' }}
            </p>
            <div class="loop-badges" data-testid="worker-badges">
              <span v-for="badge in workerBadges" :key="badge.text" class="loop-badge" :class="{ danger: badge.danger }" :title="badge.title">{{ badge.text }}</span>
            </div>
          </div>
        </div>

        <div class="modal-body loop-body">
          <div class="loop-field">
            <div class="loop-label">Task</div>
            <div class="loop-goal">{{ worker.task }}</div>
          </div>
          <div v-if="worker.context" class="loop-field">
            <div class="loop-label">Notes for the first turn</div>
            <div class="loop-goal loop-notes">{{ worker.context }}</div>
          </div>
          <dl class="loop-grid">
            <div><dt>Folder</dt><dd class="loop-path" :title="worker.working_directory">{{ shortPath(worker.working_directory) }}</dd></div>
            <div><dt>Model</dt><dd>{{ worker.model || 'Default' }}<span v-if="worker.provider && worker.provider !== 'claude'" class="loop-muted"> · {{ worker.provider }}</span></dd></div>
          </dl>
          <p class="loop-muted loop-foot">
            It runs without permission prompts inside the sandbox, reports back to this session after each turn, and this agent can send it messages.
          </p>
        </div>

        <div class="modal-actions">
          <button class="btn btn-secondary" :disabled="!connected" data-testid="worker-deny" @click="answer(WORKER_DENY)">Deny</button>
          <button class="btn btn-secondary" :disabled="!connected" title="Hide for now; the agent keeps waiting" @click="handleCancel">Later</button>
          <button class="btn btn-primary" :disabled="!connected" data-testid="worker-start" @click="answer(WORKER_START)">Start</button>
        </div>
        <div v-if="!connected" class="connection-warning">
          ⚠️ Connection lost - this request may no longer be valid
        </div>
      </div>

      <div v-else class="question-modal" @click.stop>
        <!-- Header -->
        <div class="modal-header">
          <div class="modal-icon">❓</div>
          <div class="header-content">
            <h3 class="modal-title">
              {{ question.header }}
              <span v-if="question.questionTotal && question.questionTotal > 1" class="modal-step">
                Question {{ question.questionIndex }} of {{ question.questionTotal }}
              </span>
            </h3>
            <p class="modal-subtitle">{{ question.question }}</p>
          </div>
        </div>

        <!-- Auto-pick timer (agent questions only; confirmations always wait) -->
        <div v-if="countdownMs !== null" class="auto-pick-bar" role="status" data-testid="auto-pick-countdown">
          <span class="auto-pick-text">
            <template v-if="countdownMs > 0">Auto-picks “{{ autoPickText }}” in <span class="auto-pick-time">{{ countdownLabel }}</span></template>
            <template v-else>Auto-picking “{{ autoPickText }}”…</template>
          </span>
          <button
            v-if="countdownMs > 0"
            type="button"
            class="auto-pick-hold"
            data-testid="auto-pick-hold"
            :disabled="!connected"
            title="Stop the timer: this question waits until you answer"
            @click="handleHold"
          >
            Keep waiting
          </button>
        </div>
        <div v-else-if="isHeld" class="auto-pick-bar held" role="status" data-testid="auto-pick-held">
          <span class="auto-pick-text">Waiting for you</span>
        </div>

        <!-- Options Body -->
        <div class="modal-body">
          <div class="options-grid">
            <button
              v-for="(option, optionIndex) in question.options"
              :key="option.label"
              @click="toggleOption(option.label)"
              :class="{ 'selected': isSelected(option.label) }"
              class="option-card"
              :disabled="!connected"
            >
              <div class="option-checkbox">
                <svg v-if="isSelected(option.label)" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
                  <polyline points="20,6 9,17 4,12"></polyline>
                </svg>
              </div>
              <div class="option-text">
                <div class="option-label">
                  {{ option.label }}
                  <span v-if="recommendedSet.has(optionIndex)" class="recommended-badge" data-testid="recommended-badge">Recommended</span>
                </div>
                <div class="option-description">{{ option.description }}</div>
              </div>
            </button>

            <!-- Other Option -->
            <button
              @click="toggleOther"
              :class="{ 'selected': otherSelected }"
              class="option-card other-option"
              :disabled="!connected"
            >
              <div class="option-checkbox">
                <svg v-if="otherSelected" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
                  <polyline points="20,6 9,17 4,12"></polyline>
                </svg>
              </div>
              <div class="option-text">
                <div class="option-label">Other</div>
                <div class="option-description">Provide your own answer</div>
              </div>
            </button>
          </div>

          <!-- Other Text Input -->
          <div v-if="otherSelected" class="other-input-container">
            <textarea
              ref="otherInputRef"
              v-model="otherText"
              placeholder="Type your answer here..."
              class="other-input"
              :disabled="!connected"
              rows="3"
              @keydown.enter.ctrl="submitAnswer"
              @keydown.enter.meta="submitAnswer"
            ></textarea>
            <div class="other-input-hint">Press Ctrl+Enter or ⌘+Enter to submit</div>
          </div>
        </div>

        <!-- Actions -->
        <div class="modal-actions">
          <button
            @click="handleDismiss"
            class="btn btn-secondary btn-dismiss"
            :disabled="!connected"
            title="The question is no longer relevant: tell the agent to go on without an answer and don't show it again"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="18" y1="6" x2="6" y2="18"></line>
              <line x1="6" y1="6" x2="18" y2="18"></line>
            </svg>
            Dismiss
          </button>
          <button
            @click="handleCancel"
            class="btn btn-secondary"
            :disabled="!connected"
            title="Hide for now; the agent keeps waiting and the question comes back when you return"
          >
            Later
          </button>
          <button
            @click="submitAnswer"
            class="btn btn-primary"
            :disabled="!connected || !hasSelection"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="20,6 9,17 4,12"></polyline>
            </svg>
            Submit Answer
          </button>
        </div>

        <!-- Connection Status -->
        <div v-if="!connected" class="connection-warning">
          ⚠️ Connection lost - this question may no longer be valid
        </div>
      </div>
    </div>
  </transition>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import type { UserQuestion } from '~/stores/session/types'
import { shortenHome } from '~/utils/homeDir'
import { useClosable } from '~/composables/useKeyboardShortcuts'
import { LOOP_DENY, LOOP_EDIT, LOOP_START, loopProposalOf } from '~/utils/agents/loopProposal'
import { WORKER_DENY, WORKER_START, workerProposalBadges, workerProposalOf, workerProposalTitle } from '~/utils/agents/workerProposal'
import { autoPickLabels, formatCountdown, msUntil, parseRecommended } from '~/utils/agents/questionPolicy'

interface Props {
  show: boolean
  question?: UserQuestion | null
  connected?: boolean
  /** "zen" renders a dark glass dialog that matches zen mode. */
  variant?: 'default' | 'zen'
}

const props = withDefaults(defineProps<Props>(), {
  connected: true,
  variant: 'default',
})

const emit = defineEmits<{
  (e: 'submit', questionId: string, answers: string[]): void
  (e: 'cancel'): void
  (e: 'dismiss', questionId: string): void
  (e: 'hold', questionId: string): void
}>()

const modalOverlay = ref<HTMLElement | null>(null)

// A loop-session proposal gets its own card (null for ordinary questions).
const loop = computed(() => loopProposalOf(props.question))
// So does a worker-session proposal.
const worker = computed(() => workerProposalOf(props.question))
const workerBadges = computed(() => worker.value ? workerProposalBadges(worker.value) : [])

function answer(label: string) {
  if (props.connected && props.question) emit('submit', props.question.id, [label])
}

// ── Auto-pick (question policy) ───────────────────────────────────────────
// Confirmation cards (requires_user, loop proposals) never auto-answer.
const isAgentQuestion = computed(() => !!props.question && !props.question.requires_user && !loop.value && !worker.value)
const recommendedSet = computed(() =>
  new Set(isAgentQuestion.value ? parseRecommended(props.question?.recommended, props.question?.options?.length) : []))
const autoPickText = computed(() =>
  autoPickLabels(props.question).map(l => l.replace(/\s*\(recommended\)\s*$/i, '')).join(', '))
const isHeld = computed(() => isAgentQuestion.value && !!props.question?.held)

const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | null = null
const countdownMs = computed<number | null>(() => {
  if (!props.show || !isAgentQuestion.value || props.question?.held) return null
  return msUntil(props.question?.auto_answer_at, now.value)
})
const countdownLabel = computed(() => formatCountdown(countdownMs.value ?? 0))
const timerActive = computed(() => props.show && isAgentQuestion.value && !props.question?.held && !!props.question?.auto_answer_at)
watch(timerActive, (active) => {
  if (ticker) { clearInterval(ticker); ticker = null }
  if (!active) return
  now.value = Date.now()
  ticker = setInterval(() => { now.value = Date.now() }, 1000)
}, { immediate: true })

function handleHold() {
  if (props.connected && props.question) emit('hold', props.question.id)
}

function shortPath(p: string): string {
  return shortenHome(p || '')
}
const otherInputRef = ref<HTMLTextAreaElement | null>(null)
const selectedAnswers = ref<string[]>([])
const otherSelected = ref(false)
const otherText = ref('')

const hasSelection = computed(() => {
  if (otherSelected.value) {
    return otherText.value.trim().length > 0
  }
  return selectedAnswers.value.length > 0
})

// Reset selection and focus the modal whenever it is shown or a different
// question is swapped in (multi-question calls reuse the open modal).
watch([() => props.show, () => props.question?.id], ([show]) => {
  if (show) {
    selectedAnswers.value = []
    otherSelected.value = false
    otherText.value = ''
    nextTick(() => {
      modalOverlay.value?.focus()
    })
  }
})

// Focus text input when "Other" is selected
watch(() => otherSelected.value, (selected) => {
  if (selected) {
    nextTick(() => {
      otherInputRef.value?.focus()
    })
  }
})

function toggleOption(label: string) {
  if (!props.question) return

  // Deselect "Other" when selecting a predefined option
  if (!props.question.multiSelect) {
    otherSelected.value = false
    otherText.value = ''
  }

  if (props.question.multiSelect) {
    // Multi-select mode: toggle selection
    const index = selectedAnswers.value.indexOf(label)
    if (index >= 0) {
      selectedAnswers.value.splice(index, 1)
    } else {
      selectedAnswers.value.push(label)
    }
  } else {
    // Single-select mode: replace selection
    selectedAnswers.value = [label]
  }
}

function toggleOther() {
  if (!props.question) return

  if (props.question.multiSelect) {
    // Multi-select mode: toggle "Other"
    otherSelected.value = !otherSelected.value
    if (!otherSelected.value) {
      otherText.value = ''
    }
  } else {
    // Single-select mode: select "Other" and clear other selections
    otherSelected.value = true
    selectedAnswers.value = []
  }
}

function isSelected(label: string): boolean {
  return selectedAnswers.value.includes(label)
}

function submitAnswer() {
  if (!props.question || !hasSelection.value) return

  let answers: string[]

  if (otherSelected.value && otherText.value.trim()) {
    if (props.question.multiSelect) {
      // Include both selected options and the custom text
      answers = [...selectedAnswers.value, `Other: ${otherText.value.trim()}`]
    } else {
      // Just the custom text
      answers = [`Other: ${otherText.value.trim()}`]
    }
  } else {
    answers = selectedAnswers.value
  }

  emit('submit', props.question.id, answers)
}

function handleDismiss() {
  if (props.connected && props.question) {
    emit('dismiss', props.question.id)
  }
}

function handleCancel() {
  if (props.connected) {
    emit('cancel')
  }
}

// Handle keyboard shortcuts
const handleKeyDown = (event: KeyboardEvent) => {
  if (!props.show || event.defaultPrevented || event.isComposing) return

  // Never start a YOLO loop, or a worker, on a stray Enter: the card needs a click.
  if (loop.value || worker.value) return

  // Don't intercept Enter in textarea unless Ctrl/Cmd is pressed
  if (event.key === 'Enter' && !otherSelected.value && hasSelection.value && props.connected) {
    event.preventDefault()
    event.stopPropagation()
    submitAnswer()
  }
}
// Escape: the shortcut registry's Escape stack
useClosable(() => props.show && !!props.question, handleCancel, { modal: true, when: () => props.connected })

onMounted(() => {
  window.addEventListener('keydown', handleKeyDown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeyDown)
  if (ticker) clearInterval(ticker)
})
</script>

<style scoped>
/* Loop session card */
.loop-lede {
  margin: 0.35rem 0 0.6rem;
  color: var(--text-secondary);
  font-size: 0.9rem;
  line-height: 1.45;
}

.loop-badges {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem;
}

.loop-badge {
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  padding: 0.2rem 0.55rem;
  border-radius: 999px;
  border: 1px solid var(--border-color);
  color: var(--text-secondary);
  background: var(--bg-secondary);
}

.loop-badge.danger {
  color: var(--status-error);
  border-color: var(--status-error);
  background: color-mix(in srgb, var(--status-error) 10%, transparent);
}

.loop-body {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.loop-label,
.loop-grid dt {
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-secondary);
  margin-bottom: 0.3rem;
}

.loop-goal {
  color: var(--text-primary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.5;
  max-height: 14rem;
  overflow-y: auto;
}

.loop-notes {
  max-height: 8rem;
  font-size: 0.9rem;
}

.loop-code {
  margin: 0;
  padding: 0.6rem 0.75rem;
  border-radius: 0.5rem;
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  color: var(--text-primary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.82rem;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.loop-muted {
  color: var(--text-secondary);
  font-size: 0.85rem;
}

.loop-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 0.75rem;
  margin: 0;
}

.loop-grid dd {
  margin: 0;
  color: var(--text-primary);
  font-size: 0.9rem;
  overflow-wrap: anywhere;
}

.loop-path {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.82rem !important;
}

.loop-foot {
  margin: 0;
}

.btn.btn-loop-start {
  background: var(--status-error);
}

.btn.btn-loop-start:hover:not(:disabled) {
  background: var(--status-error);
  filter: brightness(1.08);
}

/* Modal Transitions */
.modal-fade-enter-active,
.modal-fade-leave-active {
  transition: opacity 0.25s ease;
}

.modal-fade-enter-from,
.modal-fade-leave-to {
  opacity: 0;
}

/* Overlay */
.question-modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(4px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 10050; /* above fullscreen zen mode (9999) */
  outline: none;
}

/* Modal Container */
.question-modal {
  background: var(--bg-primary);
  border-radius: 0.75rem;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.3);
  max-width: 600px;
  width: 90%;
  max-height: 90vh;
  overflow-y: auto;
  position: relative;
}

/* Header */
.modal-header {
  display: flex;
  align-items: flex-start;
  gap: 1rem;
  padding: 2rem;
  border-bottom: 1px solid var(--border-color);
}

.modal-icon {
  font-size: 2rem;
  flex-shrink: 0;
  line-height: 1;
}

.header-content {
  flex: 1;
}

.modal-title {
  margin: 0;
  font-size: 0.875rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #3b82f6;
  margin-bottom: 0.5rem;
}

.modal-step {
  display: inline-block;
  margin-left: 0.5rem;
  padding: 0.125rem 0.5rem;
  border-radius: 999px;
  font-size: 0.6875rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  text-transform: none;
  color: var(--text-secondary, #9ca3af);
  background: var(--bg-tertiary, rgba(148, 163, 184, 0.15));
  vertical-align: middle;
}

.modal-subtitle {
  margin: 0;
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--text-primary);
  line-height: 1.5;
  /* Approval questions list the tool's arguments on separate lines. */
  white-space: pre-line;
  overflow-wrap: anywhere;
}

/* Auto-pick timer */
.auto-pick-bar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.6rem 2rem;
  border-bottom: 1px solid var(--border-color);
  background: var(--bg-secondary);
  font-size: 0.85rem;
  color: var(--text-secondary);
}

.auto-pick-text {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
}

.auto-pick-time {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  color: var(--text-primary);
}

.auto-pick-hold {
  flex-shrink: 0;
  padding: 0.3rem 0.75rem;
  border: 1px solid var(--border-color);
  border-radius: 999px;
  background: var(--bg-primary);
  color: var(--text-primary);
  font: inherit;
  font-size: 0.8rem;
  font-weight: 600;
  cursor: pointer;
}

.auto-pick-hold:hover:not(:disabled) {
  border-color: var(--text-secondary);
}

.auto-pick-hold:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.recommended-badge {
  display: inline-block;
  margin-left: 0.4rem;
  padding: 0.05rem 0.45rem;
  border-radius: 999px;
  border: 1px solid var(--border-color);
  font-size: 0.65rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--text-secondary);
  vertical-align: middle;
}

/* Body / Options Grid */
.modal-body {
  padding: 2rem;
}

.options-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
  gap: 1rem;
}

.option-card {
  display: flex;
  align-items: flex-start;
  gap: 1rem;
  padding: 1.25rem;
  background: var(--bg-secondary);
  border: 2px solid var(--border-color);
  border-radius: 0.5rem;
  cursor: pointer;
  transition: all 0.2s;
  text-align: left;
  width: 100%;
}

.option-card:hover:not(:disabled) {
  background: var(--bg-tertiary);
  border-color: #3b82f6;
  transform: translateY(-2px);
  box-shadow: 0 4px 12px rgba(59, 130, 246, 0.15);
}

.option-card.selected {
  background: rgba(59, 130, 246, 0.1);
  border-color: #3b82f6;
  box-shadow: inset 0 0 0 3px rgba(59, 130, 246, 0.05);
}

.option-card:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.option-checkbox {
  width: 24px;
  height: 24px;
  border: 2px solid var(--border-color);
  border-radius: 0.375rem;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-primary);
  flex-shrink: 0;
  transition: all 0.2s;
}

.option-card.selected .option-checkbox {
  background: #3b82f6;
  border-color: #3b82f6;
}

.option-card.selected .option-checkbox svg {
  color: white;
}

.option-text {
  flex: 1;
}

.option-label {
  font-weight: 600;
  font-size: 1rem;
  color: var(--text-primary);
  margin-bottom: 0.25rem;
}

.option-description {
  font-size: 0.875rem;
  color: var(--text-secondary);
  line-height: 1.4;
}

/* Other Option Styling */
.other-option {
  border-style: dashed;
}

.other-input-container {
  margin-top: 1rem;
  padding-top: 1rem;
  border-top: 1px solid var(--border-color);
}

.other-input {
  width: 100%;
  padding: 1rem;
  border: 2px solid var(--border-color);
  border-radius: 0.5rem;
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-size: 1rem;
  font-family: inherit;
  resize: vertical;
  min-height: 80px;
  transition: border-color 0.2s, box-shadow 0.2s;
}

.other-input:focus {
  outline: none;
  border-color: #3b82f6;
  box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.1);
}

.other-input::placeholder {
  color: var(--text-secondary);
  opacity: 0.7;
}

.other-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.other-input-hint {
  margin-top: 0.5rem;
  font-size: 0.75rem;
  color: var(--text-secondary);
  text-align: right;
}

/* Actions */
.modal-actions {
  display: flex;
  gap: 1rem;
  padding: 2rem;
  border-top: 1px solid var(--border-color);
  justify-content: flex-end;
}

.btn {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.625rem 1.25rem;
  border: none;
  border-radius: 0.375rem;
  font-weight: 600;
  font-size: 0.875rem;
  cursor: pointer;
  transition: all 0.2s;
  white-space: nowrap;
}

.btn-primary {
  background: #3b82f6;
  color: white;
}

.btn-primary:hover:not(:disabled) {
  background: #2563eb;
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(59, 130, 246, 0.3);
}

.btn-primary:disabled {
  background: #93c5fd;
  cursor: not-allowed;
}

.btn-secondary {
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
}

.btn-secondary:hover:not(:disabled) {
  background: var(--bg-tertiary);
  border-color: var(--text-secondary);
  transform: translateY(-1px);
}

.btn-dismiss {
  margin-right: auto;
}

.btn-secondary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Connection Warning */
.connection-warning {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  background: rgba(248, 113, 113, 0.1);
  border-top: 1px solid var(--status-error);
  padding: 0.75rem;
  font-size: 0.875rem;
  font-weight: 500;
  color: var(--status-error);
  text-align: center;
  border-radius: 0 0 0.75rem 0.75rem;
}

/* Responsive */
@media (max-width: 640px) {
  .options-grid {
    grid-template-columns: 1fr;
  }

  .auto-pick-bar {
    padding: 0.6rem 1.5rem;
  }

  .modal-header {
    padding: 1.5rem;
  }

  .modal-body {
    padding: 1.5rem;
  }

  .modal-actions {
    padding: 1.5rem;
    flex-direction: column;
  }

  .btn {
    width: 100%;
    justify-content: center;
  }
}

/* ===== Zen variant: dark glass dialog over the (always dark) zen scene ===== */
.question-modal-overlay.zen {
  background: rgba(4, 2, 12, 0.35);
  backdrop-filter: blur(3px);
}

.question-modal-overlay.zen .question-modal {
  color: rgba(255, 255, 255, 0.9);
  background: rgba(10, 8, 22, 0.72);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-left: 3px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.7);
  border-radius: 18px;
  box-shadow: 0 24px 70px rgba(0, 0, 0, 0.55), 0 0 60px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.12);
}

.question-modal-overlay.zen .modal-header,
.question-modal-overlay.zen .modal-actions {
  border-color: rgba(255, 255, 255, 0.08);
  background: transparent;
}

.question-modal-overlay.zen .modal-icon {
  display: none;
}

.question-modal-overlay.zen .modal-title {
  font-size: 0.65rem;
  font-weight: 600;
  letter-spacing: 0.8px;
  text-transform: uppercase;
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 1);
  filter: brightness(1.6);
}

.question-modal-overlay.zen .modal-step {
  color: rgba(255, 255, 255, 0.5);
  background: rgba(255, 255, 255, 0.06);
}

.question-modal-overlay.zen .modal-subtitle {
  font-size: 1.1rem;
  font-weight: 500;
  color: #fff;
}

.question-modal-overlay.zen .option-card {
  color: rgba(255, 255, 255, 0.85);
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.1);
  box-shadow: none;
}

.question-modal-overlay.zen .option-card:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.08);
  border-color: rgba(255, 255, 255, 0.25);
  transform: none;
  box-shadow: none;
}

.question-modal-overlay.zen .option-card.selected {
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.18);
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.8);
  box-shadow: 0 0 20px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.2);
}

.question-modal-overlay.zen .other-option {
  border-style: dashed;
}

.question-modal-overlay.zen .option-checkbox {
  border-color: rgba(255, 255, 255, 0.3);
  background: transparent;
}

.question-modal-overlay.zen .option-card.selected .option-checkbox {
  border-color: rgb(var(--accent-purple-rgb, 139, 92, 246));
  background: rgb(var(--accent-purple-rgb, 139, 92, 246));
  color: #fff;
}

.question-modal-overlay.zen .option-label {
  color: #fff;
}

.question-modal-overlay.zen .option-description,
.question-modal-overlay.zen .other-input-hint {
  color: rgba(255, 255, 255, 0.55);
}

.question-modal-overlay.zen .auto-pick-bar {
  background: rgba(255, 255, 255, 0.04);
  border-color: rgba(255, 255, 255, 0.08);
  color: rgba(255, 255, 255, 0.6);
}

.question-modal-overlay.zen .auto-pick-time {
  color: #fff;
}

.question-modal-overlay.zen .auto-pick-hold {
  color: rgba(255, 255, 255, 0.85);
  background: rgba(255, 255, 255, 0.06);
  border-color: rgba(255, 255, 255, 0.14);
}

.question-modal-overlay.zen .recommended-badge {
  color: rgba(255, 255, 255, 0.6);
  border-color: rgba(255, 255, 255, 0.2);
}

.question-modal-overlay.zen .other-input {
  color: #fff;
  background: rgba(0, 0, 0, 0.3);
  border-color: rgba(255, 255, 255, 0.12);
}

.question-modal-overlay.zen .other-input::placeholder {
  color: rgba(255, 255, 255, 0.35);
}

.question-modal-overlay.zen .btn {
  border-radius: 999px;
}

.question-modal-overlay.zen .btn-secondary {
  color: rgba(255, 255, 255, 0.8);
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.14);
}

.question-modal-overlay.zen .btn-secondary:hover:not(:disabled) {
  color: #fff;
  background: rgba(255, 255, 255, 0.12);
}

.question-modal-overlay.zen .btn-primary {
  color: #fff;
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.9);
  border: 1px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 1);
  box-shadow: 0 0 20px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.35);
}

.question-modal-overlay.zen .btn-primary:disabled {
  color: rgba(255, 255, 255, 0.4);
  background: rgba(255, 255, 255, 0.06);
  border-color: rgba(255, 255, 255, 0.1);
  box-shadow: none;
}
</style>
