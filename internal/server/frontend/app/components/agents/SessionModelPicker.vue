<template>
  <section class="model-picker" aria-labelledby="quick-model-heading">
    <div class="picker-heading">
      <h3 id="quick-model-heading">Choose a model</h3>
      <button type="button" class="browse-button" :aria-expanded="browsing" @click="toggleBrowse" @keydown.enter.stop>{{ browsing ? 'Done' : 'All models' }}</button>
    </div>
    <p class="picker-help">Your most-used models. Star favourites to keep them here.</p>
    <div ref="choicesEl" class="quick-models" role="radiogroup" aria-label="Model" @keydown="handleKey">
      <button v-for="(choice, index) in shortcuts" :key="modelKey(choice)" type="button" role="radio"
        class="quick-model" :class="{ selected: isSelected(choice) }" :aria-checked="isSelected(choice)"
        :tabindex="isSelected(choice) || (!selectedShortcut && index === 0) ? 0 : -1"
        :disabled="disabled" @click="select(choice)">
        <span class="model-number">{{ index + 1 }}</span>
        <span class="model-label"><strong>{{ choice.model }}</strong><small>{{ choiceSubtitle(choice) }}<span v-if="pins.includes(modelKey(choice))"> · ★</span></small></span>
        <span class="selection-dot" aria-hidden="true"></span>
      </button>
    </div>
    <p v-if="!shortcuts.length" class="picker-help">{{ disabled ? 'Loading models…' : 'No models available. Set up an agent to get started.' }}</p>
    <p v-else class="keyboard-help"><kbd>↑</kbd><kbd>↓</kbd> or <kbd>1</kbd>–<kbd>{{ shortcuts.length }}</kbd> to choose <span>↵ to start</span></p>
    <p v-if="currentChoice && !selectedShortcut" class="current-choice">Selected: <strong>{{ currentChoice.model }}</strong> · {{ choiceSubtitle(currentChoice) }}</p>
    <div v-if="browsing" class="model-browser">
      <input ref="searchEl" v-model="search" type="search" placeholder="Find a model or provider…" aria-label="Search models" @keydown.enter.stop.prevent="selectFirstResult" />
      <p class="picker-help">{{ pins.length }}/{{ MAX_MODEL_PINS }} favourites pinned<span v-if="pins.length >= MAX_MODEL_PINS"> · Unstar one to pin another.</span></p>
      <div class="all-models">
        <div v-for="choice in filtered" :key="modelKey(choice)" class="model-result">
          <button type="button" class="result-select" :class="{ selected: isSelected(choice) }" :disabled="disabled" @click="selectFromBrowser(choice)" @keydown.enter.stop>
            <strong>{{ choice.model }}</strong><small>{{ choiceSubtitle(choice) }}</small>
          </button>
          <button type="button" class="pin-button" :aria-label="`${pins.includes(modelKey(choice)) ? 'Unpin' : 'Pin'} ${choice.model} (${choice.providerName})`" :aria-pressed="pins.includes(modelKey(choice))"
            :disabled="!pins.includes(modelKey(choice)) && pins.length >= MAX_MODEL_PINS" @click="togglePin(choice)" @keydown.enter.stop>{{ pins.includes(modelKey(choice)) ? '★' : '☆' }}</button>
        </div>
        <p v-if="!filtered.length" class="picker-help">No models match your search.</p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { MAX_MODEL_PINS, loadModelPins, saveModelPins, rankModelChoices, modelKey, type ModelChoice, type ModelProvider } from '~/utils/agents/modelShortcuts'
import { modelFullName } from '~/utils/agents/modelLabel'
const props = defineProps<{ providers: ModelProvider[]; sessions: any[]; provider: string; model: string; disabled?: boolean }>()
const emit = defineEmits<{ select: [choice: ModelChoice]; launch: [] }>()
const savedPins = ref(loadModelPins())
const pins = computed(() => savedPins.value.filter(key => props.providers.some(p => p.models.some(model => modelKey({ provider: p.id, model }) === key))))
const browsing = ref(false)
const search = ref('')
const choicesEl = ref<HTMLElement>()
const searchEl = ref<HTMLInputElement>()
// Keep shortcut positions stable while choosing a model.
const initialChoice = { provider: props.provider, model: props.model }
const ranked = computed(() => rankModelChoices(props.providers, props.sessions, pins.value, initialChoice))
const shortcuts = computed(() => ranked.value.slice(0, MAX_MODEL_PINS))
const filtered = computed(() => ranked.value.filter(c => `${c.model} ${c.providerName} ${modelFullName(c.model)}`.toLowerCase().includes(search.value.toLowerCase())))

/** "DeepSeek · V4.1 Flash" when the id hides the version, else the provider. */
function choiceSubtitle(c: { model: string, providerName: string }): string {
  const full = modelFullName(c.model)
  return full ? `${c.providerName} · ${full}` : c.providerName
}
const isSelected = (c: ModelChoice) => c.provider === props.provider && c.model === props.model
const selectedShortcut = computed(() => shortcuts.value.some(isSelected))
const currentChoice = computed(() => ranked.value.find(isSelected))
function select(choice: ModelChoice) { if (!props.disabled) emit('select', choice) }
async function focus() {
  await nextTick()
  const buttons = choicesEl.value?.querySelectorAll<HTMLButtonElement>('[role="radio"]')
  const index = shortcuts.value.findIndex(isSelected)
  buttons?.[Math.max(0, index)]?.focus()
}
async function toggleBrowse() {
  browsing.value = !browsing.value
  await nextTick()
  if (browsing.value) searchEl.value?.focus()
  else focus()
}
function selectFromBrowser(choice: ModelChoice) {
  select(choice)
  browsing.value = false
  focus()
}
function selectFirstResult() { if (filtered.value[0]) selectFromBrowser(filtered.value[0]) }
function togglePin(choice: ModelChoice) {
  const key = modelKey(choice)
  savedPins.value = pins.value.includes(key) ? pins.value.filter(p => p !== key) : [...pins.value, key].slice(0, MAX_MODEL_PINS)
  saveModelPins(savedPins.value)
}
function handleKey(event: KeyboardEvent) {
  if (event.isComposing || event.metaKey || event.ctrlKey || event.altKey || props.disabled) return
  const index = shortcuts.value.findIndex(isSelected)
  let next = -1
  if (/^[1-9]$/.test(event.key)) next = Number(event.key) - 1
  if (['ArrowDown', 'ArrowRight'].includes(event.key)) next = (index + 1) % shortcuts.value.length
  if (['ArrowUp', 'ArrowLeft'].includes(event.key)) next = (index < 0 ? shortcuts.value.length - 1 : (index - 1 + shortcuts.value.length) % shortcuts.value.length)
  if (event.key === 'Enter') {
    event.preventDefault()
    event.stopPropagation()
    if (!event.repeat) emit('launch')
    return
  }
  if (next >= 0 && shortcuts.value[next]) {
    event.preventDefault()
    event.stopPropagation()
    select(shortcuts.value[next])
    focus()
  }
}
defineExpose({ focus })
</script>

<style scoped>
.picker-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
h3 { margin: 0; font-size: .95rem; color: var(--text-primary); }
.browse-button { color: var(--accent-purple); background: transparent; border: 0; padding: 6px 0 6px 12px; cursor: pointer; font: inherit; font-size: .8rem; }
.picker-help, .keyboard-help, .current-choice { color: var(--text-secondary); font-size: .75rem; margin: 8px 0 12px; line-height: 1.5; }
.quick-models { display: grid; gap: 8px; }
.quick-model { display: flex; align-items: center; gap: 12px; width: 100%; text-align: left; padding: 13px 14px; border: 1px solid var(--border-color); border-radius: 10px; background: var(--bg-primary); color: var(--text-primary); cursor: pointer; font: inherit; }
.quick-model.selected { border-color: var(--accent-purple); background: color-mix(in srgb, var(--accent-purple) 9%, var(--bg-primary)); }
button:focus-visible, input:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 3px; }
.model-number { display: grid; place-items: center; width: 25px; height: 25px; border: 1px solid var(--border-color); border-radius: 6px; color: var(--text-secondary); font-size: .75rem; flex-shrink: 0; }
.model-label { flex: 1; min-width: 0; }
strong { display: block; font-size: .85rem; overflow-wrap: anywhere; }
small { display: block; font-size: .7rem; margin-top: 3px; color: var(--text-secondary); }
.selection-dot { width: 12px; height: 12px; border: 1px solid var(--border-color); border-radius: 50%; flex-shrink: 0; }
.selected .selection-dot { background: var(--accent-purple); border-color: var(--accent-purple); box-shadow: inset 0 0 0 3px var(--bg-primary); }
.keyboard-help { display: flex; align-items: center; gap: 5px; margin-bottom: 0; }
.keyboard-help span { margin-left: auto; }
kbd { font: inherit; border: 1px solid var(--border-color); border-radius: 4px; padding: 0 4px; }
.model-browser { margin-top: 14px; padding-top: 14px; border-top: 1px solid var(--border-color); }
input { box-sizing: border-box; width: 100%; padding: 10px 12px; color: var(--text-primary); background: var(--bg-primary); border: 1px solid var(--border-color); border-radius: 8px; font: inherit; font-size: .85rem; }
.all-models { max-height: 220px; overflow-y: auto; }
.model-result { display: flex; gap: 6px; }
.result-select { flex: 1; text-align: left; border: 0; border-radius: 6px; padding: 10px; background: transparent; color: var(--text-primary); cursor: pointer; }
.result-select:hover, .result-select.selected { background: var(--bg-secondary); }
.pin-button { border: 0; padding: 10px; background: transparent; color: var(--text-secondary); font-size: 1.2rem; cursor: pointer; }
.pin-button[aria-pressed="true"] { color: var(--accent-purple); }
button:disabled { opacity: .4; cursor: default; }
</style>
