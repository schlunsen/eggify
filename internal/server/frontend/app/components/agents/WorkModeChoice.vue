<template>
  <div class="work-mode" data-testid="work-mode">
    <template v-if="available">
      <div id="work-mode-label" class="work-mode-title">How should it work?</div>
      <div class="work-mode-options" role="radiogroup" aria-labelledby="work-mode-label">
        <label
          v-for="id in WORK_MODES"
          :key="id"
          class="work-mode-option"
          :class="{ selected: current === id, risky: WORK_MODE_INFO[id].risky }"
          :data-testid="`work-mode-${id}`"
        >
          <input type="radio" name="work-mode" :value="id" :checked="current === id" @change="choose(id)" />
          <span class="work-mode-text">
            <span class="work-mode-name">{{ WORK_MODE_INFO[id].label }}</span>
            <span class="work-mode-desc">{{ WORK_MODE_INFO[id].description }}</span>
          </span>
        </label>
      </div>
      <p v-if="current === null" class="work-mode-note" data-testid="work-mode-custom">
        Custom permission mode ({{ permissionMode }}). Pick an option above to replace it.
      </p>
      <slot v-if="current === 'autopilot'" name="autopilot" />
      <slot v-else-if="current !== 'full'" name="ask" />
    </template>
    <template v-else>
      <p class="work-mode-note" data-testid="work-mode-unavailable">{{ reason }}</p>
      <slot name="classic" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  WORK_MODES, WORK_MODE_INFO, autopilotAvailable, initialWorkMode, readSavedWorkMode, saveWorkMode,
  workModeFields, workModeOfForm, workModeUnavailableReason, type WorkMode,
} from '~/utils/agents/workMode'

const props = defineProps<{
  permissionMode: string
  sandboxed: boolean | null | undefined
  provider?: string | null
  /** Start on the remembered/default option: false for forms a caller prefilled (loop proposals, handoffs). */
  applyDefault?: boolean
}>()
const emit = defineEmits<{
  (e: 'update:permissionMode', value: string): void
  (e: 'update:sandboxed', value: boolean | null): void
}>()

const available = computed(() => autopilotAvailable(props.provider))
const reason = computed(() => workModeUnavailableReason(props.provider))
const current = computed<WorkMode | null>(() => workModeOfForm(props.permissionMode, props.sandboxed))

const apply = (mode: WorkMode) => {
  const f = workModeFields(mode)
  emit('update:permissionMode', f.permissionMode)
  emit('update:sandboxed', f.sandboxed)
}

// Once the user picked an option, switching provider back and forth keeps it.
const chosen = ref(false)

const choose = (mode: WorkMode) => {
  chosen.value = true
  saveWorkMode(mode)
  apply(mode)
}

// A new form starts on the user's last choice, else Autopilot. Where Autopilot
// is not offered the form keeps what it had (today's defaults).
watch(
  () => [props.applyDefault, available.value] as const,
  ([should, ok]) => {
    if (!should || !ok || chosen.value) return
    const start = initialWorkMode(props.provider, readSavedWorkMode())
    if (start) apply(start)
  },
  { immediate: true },
)

defineExpose({ apply })
</script>

<style scoped>
.work-mode { display: flex; flex-direction: column; gap: 8px; }
.work-mode-title { font-weight: 600; }
.work-mode-options { display: flex; flex-direction: column; gap: 6px; }
.work-mode-option {
  display: flex; gap: 10px; align-items: flex-start; cursor: pointer;
  padding: 8px 10px; border-radius: 8px;
  border: 1px solid var(--border-color, rgba(127, 127, 127, 0.4));
}
.work-mode-option.selected { border-color: var(--accent-purple, currentColor); background: var(--overlay-bg, transparent); }
.work-mode-option.risky.selected { border-color: var(--accent-orange, #d97706); }
.work-mode-option.risky .work-mode-name { color: var(--accent-orange, #d97706); }
.work-mode-option input { margin-top: 3px; }
.work-mode-text { display: flex; flex-direction: column; gap: 2px; }
.work-mode-name { font-weight: 600; }
.work-mode-desc, .work-mode-note { font-size: 0.75rem; line-height: 1.4; color: var(--text-secondary, inherit); margin: 0; }
</style>
