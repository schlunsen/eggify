<template>
  <article class="cue" :class="`cue-${cue.type}`" :aria-label="`${cue.sessionName}: ${cue.title}`">
    <header class="cue-head">
      <span class="cue-bell" aria-hidden="true">🔔</span>
      <span class="cue-session">{{ cue.sessionName }}</span>
      <span v-if="autoIn" class="cue-auto" :title="`If nobody answers, Eggify picks the recommended option`">auto in {{ autoIn }}</span>
    </header>
    <p class="cue-title">{{ cue.title }}</p>
    <pre v-if="command" class="cue-command">{{ command }}</pre>

    <p v-if="error" class="cue-error" role="alert">{{ error }}</p>

    <div class="cue-actions">
      <template v-if="cue.openInApp">
        <button type="button" class="cue-btn primary" @click="$emit('open', cue.sessionId)">Answer in Eggify</button>
      </template>
      <template v-else-if="cue.type === 'permission'">
        <button type="button" class="cue-btn" :disabled="busy" @click="answerPermission(false)">Deny</button>
        <button type="button" class="cue-btn primary" :disabled="busy" @click="answerPermission(true)">Allow</button>
      </template>
      <template v-else>
        <button
          v-for="opt in cue.options"
          :key="opt.label"
          type="button"
          class="cue-btn"
          :class="{ primary: opt.recommended }"
          :disabled="busy"
          :title="opt.recommended ? 'Recommended' : undefined"
          @click="answerQuestion(opt.label)"
        >{{ opt.label.replace(/\s*\(Recommended\)\s*$/, '') }}</button>
      </template>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Cue } from '~/utils/notch/notchScene'
import { countdown } from '~/utils/notch/notchScene'
import type { AnswerResult, NotchAnswer } from '~/utils/notch/notchAnswer'

/**
 * One approval or question on the Notch Theatre's playbill. Permission cards
 * answer Allow / Deny; single-choice agent questions answer with an option.
 * Anything bigger (multi-select, loop proposals, confirmation cards) opens
 * the session in the main window.
 */
const props = defineProps<{
  cue: Cue
  now: number
  answer: (a: NotchAnswer) => Promise<AnswerResult>
}>()
const emit = defineEmits<{ answered: [key: string]; open: [sessionId: string] }>()

const busy = ref(false)
const error = ref('')

const command = computed(() => props.cue.permission?.details?.command || '')
const autoIn = computed(() => {
  const at = props.cue.question?.auto_answer_at
  const t = at ? Date.parse(at) : NaN
  return Number.isFinite(t) && t > props.now ? countdown(t, props.now) : ''
})

async function send(a: NotchAnswer) {
  busy.value = true
  error.value = ''
  const result = await props.answer(a)
  busy.value = false
  if (result.ok) emit('answered', props.cue.key)
  else error.value = result.error || 'The answer was not accepted.'
}

function answerPermission(approved: boolean) {
  const p = props.cue.permission
  if (!p) return
  void send({ type: 'permission', sessionId: p.session_id, permissionId: p.permission_id, approved })
}

function answerQuestion(label: string) {
  const q = props.cue.question
  if (!q) return
  void send({ type: 'question', sessionId: q.session_id, questionId: q.question_id, answers: [label] })
}
</script>

<style scoped>
.cue {
  background: var(--nt-paper);
  color: var(--nt-ink);
  border-radius: 6px;
  padding: 8px 10px 9px;
  box-shadow: inset 0 0 0 1px var(--nt-paper-rule), 0 2px 0 rgb(0 0 0 / 0.35);
  animation: cue-in 0.32s steps(4) both;
}
@keyframes cue-in { from { transform: translateY(-6px) rotate(-1.5deg); opacity: 0; } }
.cue-head { display: flex; align-items: center; gap: 6px; font-size: 11px; }
.cue-bell { animation: ring 1.2s steps(2) infinite; transform-origin: 50% 0; }
@keyframes ring { 0% { transform: rotate(-14deg); } 100% { transform: rotate(14deg); } }
.cue-session { font-weight: 600; flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cue-auto { font-variant-numeric: tabular-nums; opacity: 0.7; }
.cue-title { margin: 4px 0 0; font-family: var(--nt-serif); font-size: 14px; line-height: 1.25; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
.cue-command {
  margin: 6px 0 0; padding: 5px 7px; border-radius: 4px; max-height: 54px; overflow: auto;
  background: var(--nt-ink); color: var(--nt-paper); font: 11px/1.35 var(--nt-mono); white-space: pre-wrap; word-break: break-all;
}
.cue-error { margin: 6px 0 0; font-size: 11px; color: var(--nt-error); }
.cue-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 6px; margin-top: 8px; }
.cue-btn {
  font: 600 12px/1 var(--nt-sans); padding: 6px 11px; border-radius: 999px; cursor: pointer;
  background: transparent; color: var(--nt-ink); border: 1px solid var(--nt-ink);
}
.cue-btn.primary { background: var(--nt-velvet); border-color: var(--nt-velvet); color: var(--nt-paper); }
.cue-btn:disabled { opacity: 0.5; cursor: default; }
.cue-btn:focus-visible { outline: 2px solid var(--nt-gold); outline-offset: 2px; }
@media (prefers-reduced-motion: reduce) {
  .cue, .cue-bell { animation: none; }
}
</style>
