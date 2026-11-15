<template>
  <!--
    Create Session → Team: this session orchestrates, and the workers it starts
    build and review on the models picked here (server: agents/team.go). The
    session's own model picker above is the orchestrator's.
  -->
  <section class="team-setup" :class="{ on: modelValue.enabled }" data-testid="team-setup">
    <label class="team-toggle" for="team-enabled">
      <input
        id="team-enabled"
        type="checkbox"
        role="switch"
        class="team-toggle-input"
        :checked="modelValue.enabled"
        :aria-checked="modelValue.enabled"
        :disabled="disabled"
        aria-describedby="team-toggle-hint"
        data-testid="team-toggle"
        @change="update({ enabled: ($event.target as HTMLInputElement).checked })"
        @keydown.enter.stop.prevent="update({ enabled: !modelValue.enabled })"
      />
      <span class="team-toggle-switch" aria-hidden="true"></span>
      <span class="team-toggle-text">Team</span>
      <small id="team-toggle-hint" class="team-toggle-hint">This session plans; a builder and a reviewer do the work</small>
    </label>

    <div v-if="modelValue.enabled" class="team-fields">
      <div class="team-row">
        <div v-for="role in ROLES" :key="role.id" class="team-field">
          <label :for="`team-${role.id}`">{{ role.label }} <span class="team-hint">{{ role.hint }}</span></label>
          <select
            :id="`team-${role.id}`"
            class="team-select"
            :value="keyOf(modelValue[role.id])"
            :disabled="disabled"
            :data-testid="`team-${role.id}`"
            @change="pick(role.id, ($event.target as HTMLSelectElement).value)"
            @keydown.enter.stop
          >
            <option value="" disabled>Pick a model…</option>
            <optgroup v-for="provider in providers" :key="provider.id" :label="provider.name">
              <option v-for="model in provider.models" :key="model" :value="modelKey({ provider: provider.id, model })">{{ optionLabel(model) }}</option>
            </optgroup>
          </select>
        </div>
        <div class="team-field team-field-rounds">
          <label for="team-rounds">Review rounds</label>
          <input
            id="team-rounds"
            class="team-select"
            type="number"
            min="1"
            :max="TEAM_MAX_REVIEW_ROUNDS"
            step="1"
            :value="modelValue.maxReviewRounds"
            :disabled="disabled"
            data-testid="team-rounds"
            @change="setRounds(($event.target as HTMLInputElement).value)"
          />
        </div>
      </div>
      <p class="team-note" data-testid="team-note">
        The workers start without asking, sandboxed, on these models: the builder in its own git worktree, the reviewer read-only.
        Claude models, Anthropic-compatible providers and Codex can be sandboxed; other models are refused.
      </p>
      <p v-if="problem" class="team-problem" role="status" data-testid="team-problem">{{ problem }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { modelKey, type ModelProvider } from '~/utils/agents/modelShortcuts'
import { modelFullName } from '~/utils/agents/modelLabel'
import {
  TEAM_MAX_REVIEW_ROUNDS,
  normalizeReviewRounds,
  teamSetupProblem,
  type TeamMember,
  type TeamSetup,
} from '~/utils/agents/team'

const props = defineProps<{
  modelValue: TeamSetup
  providers: Pick<ModelProvider, 'id' | 'name' | 'models'>[]
  disabled?: boolean
}>()

const emit = defineEmits<{ 'update:modelValue': [value: TeamSetup] }>()

const ROLES = [
  { id: 'builder', label: 'Builder', hint: '(writes the code)' },
  { id: 'reviewer', label: 'Reviewer', hint: '(read-only)' },
] as const

const problem = computed(() => teamSetupProblem(props.modelValue))

function update(patch: Partial<TeamSetup>) {
  emit('update:modelValue', { ...props.modelValue, ...patch })
}

/** The select's value; '' (the placeholder) for a model no provider offers any more. */
function keyOf(member: TeamMember): string {
  const offered = props.providers.some(p => p.id === member.provider && p.models.includes(member.model))
  return offered ? modelKey(member) : ''
}

function pick(role: 'builder' | 'reviewer', key: string) {
  for (const provider of props.providers) {
    const model = provider.models.find(m => modelKey({ provider: provider.id, model: m }) === key)
    if (model) return update({ [role]: { provider: provider.id, model } })
  }
}

function setRounds(value: string) {
  update({ maxReviewRounds: normalizeReviewRounds(value) })
}

/** "deepseek-flash · V4.1 Flash" when the id hides the version. */
function optionLabel(model: string): string {
  const full = modelFullName(model)
  return full ? `${model} · ${full}` : model
}
</script>

<style scoped>
.team-setup {
  margin-top: 12px;
  padding: 10px 12px;
  border: 1px solid var(--border-color);
  border-radius: 10px;
  background: var(--bg-primary);
}
.team-setup.on { border-color: color-mix(in srgb, var(--accent-purple) 55%, var(--border-color)); }
.team-toggle {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 10px;
  cursor: pointer;
}
/* Kept focusable: only hidden from sight. */
.team-toggle-input {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
.team-toggle-switch {
  position: relative;
  flex-shrink: 0;
  width: 36px;
  height: 20px;
  border-radius: 10px;
  background: var(--border-color);
  transition: background 0.2s;
}
.team-toggle-switch::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--bg-primary);
  transition: transform 0.2s;
}
.team-toggle-input:checked + .team-toggle-switch { background: var(--accent-purple); }
.team-toggle-input:checked + .team-toggle-switch::after { transform: translateX(16px); }
.team-toggle-input:focus-visible + .team-toggle-switch { outline: 2px solid var(--accent-purple); outline-offset: 3px; }
.team-toggle-input:disabled + .team-toggle-switch { opacity: 0.5; }
.team-toggle-text { color: var(--text-primary); font-size: 0.9rem; font-weight: 600; }
.team-toggle-hint { color: var(--text-secondary); font-size: 0.75rem; }
.team-fields { margin-top: 10px; }
.team-row { display: flex; flex-wrap: wrap; gap: 10px; }
.team-field { flex: 1 1 180px; min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.team-field-rounds { flex: 0 0 110px; }
.team-field label { color: var(--text-secondary); font-size: 0.8rem; }
.team-hint { opacity: 0.7; font-size: 0.72rem; }
.team-select {
  box-sizing: border-box;
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border-color);
  border-radius: 8px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  font: inherit;
  font-size: 0.85rem;
}
.team-select:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }
.team-note,
.team-problem {
  margin: 8px 0 0;
  color: var(--text-secondary);
  font-size: 0.75rem;
  line-height: 1.45;
}
.team-problem { color: var(--status-warning); }
@media (prefers-reduced-motion: reduce) {
  .team-toggle-switch,
  .team-toggle-switch::after { transition: none; }
}
</style>
