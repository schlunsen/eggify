<template>
  <div
    class="choice-group"
    role="radiogroup"
    :aria-label="label"
    :style="{ '--choice-columns': columns ?? options.length }"
  >
    <label
      v-for="option in options"
      :key="option.value"
      class="choice"
      :class="{ 'choice--active': modelValue === option.value }"
    >
      <input
        type="radio"
        class="choice__input"
        :name="name"
        :value="option.value"
        :checked="modelValue === option.value"
        @change="select(option.value)"
      />
      <span v-if="option.icon" class="choice__icon"><SettingsGlyph :name="option.icon" :size="16" /></span>
      <span class="choice__text">
        <span class="choice__label">{{ option.label }}</span>
        <span v-if="option.hint" class="choice__hint">{{ option.hint }}</span>
      </span>
      <span class="choice__check" aria-hidden="true"><SettingsGlyph name="check" :size="12" /></span>
    </label>
  </div>
</template>

<script setup lang="ts">
import type { SettingsChoice } from '~/types/settings'

const props = withDefaults(defineProps<{
  modelValue: string
  options: SettingsChoice[]
  name: string
  label?: string
  /** Tiles per row on wide screens (defaults to one row); collapses on narrow screens. */
  columns?: number
}>(), { columns: undefined, label: undefined })

const emit = defineEmits<{
  'update:modelValue': [value: string]
  change: [value: string]
}>()

function select(value: string) {
  if (value === props.modelValue) return
  emit('update:modelValue', value)
  emit('change', value)
}
</script>

<style scoped>
.choice-group {
  display: grid;
  grid-template-columns: repeat(var(--choice-columns), minmax(0, 1fr));
  gap: 8px;
}

@media (max-width: 760px) {
  .choice-group {
    grid-template-columns: repeat(auto-fit, minmax(min(200px, 100%), 1fr));
  }
}

.choice {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--border-color);
  border-radius: 8px;
  background: var(--bg-secondary);
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}

.choice:hover {
  border-color: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.55);
}

.choice--active {
  border-color: var(--accent-purple);
  background: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.08);
}

.choice__input {
  position: absolute;
  opacity: 0;
  pointer-events: none;
}

.choice:has(.choice__input:focus-visible) {
  outline: 2px solid var(--accent-purple);
  outline-offset: 2px;
}

.choice__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  flex-shrink: 0;
  border-radius: 6px;
  color: var(--text-secondary);
  background: var(--bg-primary);
}

.choice--active .choice__icon {
  color: var(--accent-purple);
  background: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.12);
}

.choice__text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  padding-right: 14px;
}

.choice__label {
  font-size: 0.84rem;
  font-weight: 600;
  color: var(--text-primary);
}

.choice__hint {
  font-size: 0.75rem;
  line-height: 1.4;
  color: var(--text-secondary);
}

.choice__check {
  position: absolute;
  top: 8px;
  right: 8px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  color: #fff;
  background: var(--accent-purple);
  opacity: 0;
  transform: scale(0.6);
  transition: opacity 0.15s, transform 0.15s;
}

.choice--active .choice__check {
  opacity: 1;
  transform: scale(1);
}
</style>
