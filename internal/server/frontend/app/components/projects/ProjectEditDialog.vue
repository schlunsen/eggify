<template>
  <div v-if="project" class="pe-overlay" @mousedown.self="emit('close')">
    <form
      class="pe-dialog"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="`pe-title-${uid}`"
      novalidate
      @submit.prevent="save"
      @keydown.esc.prevent.stop="emit('close')"
    >
      <h2 :id="`pe-title-${uid}`" class="pe-title">Edit project</h2>
      <p class="pe-path"><code>{{ displayPath }}</code></p>

      <label :for="`pe-name-${uid}`" class="pe-label">Name</label>
      <input :id="`pe-name-${uid}`" ref="nameInput" v-model="name" class="pe-input" type="text" autocomplete="off" :aria-invalid="!!nameError && tried">
      <p v-if="nameError && tried" class="pe-error">{{ nameError }}</p>

      <label :for="`pe-desc-${uid}`" class="pe-label">Description <span class="pe-muted">(optional)</span></label>
      <textarea :id="`pe-desc-${uid}`" v-model="description" class="pe-input pe-textarea" rows="2" placeholder="What is this project about?" />

      <span :id="`pe-color-${uid}`" class="pe-label">Color</span>
      <div class="pe-swatches" role="radiogroup" :aria-labelledby="`pe-color-${uid}`">
        <button
          v-for="c in colors"
          :key="c"
          type="button"
          role="radio"
          class="pe-swatch"
          :class="{ 'pe-swatch--on': color === c }"
          :style="{ '--swatch': c }"
          :aria-checked="color === c"
          :aria-label="`Color ${c}`"
          @click="color = c"
        />
      </div>

      <p v-if="error" class="pe-error" role="alert">{{ error }}</p>

      <div class="pe-actions">
        <button type="button" class="pe-btn pe-btn--secondary" @click="emit('close')">Cancel</button>
        <button type="submit" class="pe-btn pe-btn--primary" :disabled="saving">{{ saving ? 'Saving…' : 'Save' }}</button>
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { PROJECT_COLORS } from '~/utils/projects/projectSetup'

const props = defineProps<{ project: { id: string, name: string, path: string, display_path?: string, description?: string, color?: string } | null }>()
const emit = defineEmits<{ (e: 'close'): void, (e: 'saved', project: any): void }>()

const { fetchWithAuth } = useAuthenticatedFetch()
const uid = Math.random().toString(36).slice(2, 8)
const name = ref('')
const description = ref('')
const color = ref('')
const saving = ref(false)
const tried = ref(false)
const error = ref<string | null>(null)
const nameInput = ref<HTMLInputElement | null>(null)

const colors = computed(() => {
  const c = props.project?.color
  return c && !PROJECT_COLORS.includes(c) ? [c, ...PROJECT_COLORS] : PROJECT_COLORS
})
const displayPath = computed(() => props.project?.display_path || props.project?.path || '')
const nameError = computed(() => !name.value.trim() ? 'Give the project a name' : name.value.trim().length > 100 ? 'Keep the name under 100 characters' : null)

watch(() => props.project, (p) => {
  if (!p) return
  name.value = p.name
  description.value = p.description || ''
  color.value = p.color || PROJECT_COLORS[0]!
  tried.value = false
  error.value = null
  nextTick(() => { nameInput.value?.focus(); nameInput.value?.select() })
}, { immediate: true })

async function save() {
  tried.value = true
  if (nameError.value || !props.project) return
  saving.value = true
  error.value = null
  try {
    // Partial update: only these fields change (PUT keeps the rest).
    const res = await fetchWithAuth(`/api/projects/${props.project.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: name.value.trim(), description: description.value.trim(), color: color.value }),
    })
    const body = await res.json().catch(() => ({}))
    if (!res.ok) throw new Error(body.error || 'Could not save the project')
    emit('saved', body.project)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Could not save the project'
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.pe-overlay { position: fixed; inset: 0; z-index: 1000; display: flex; align-items: flex-start; justify-content: center; padding: 12vh 16px 16px; background: rgba(0, 0, 0, 0.45); backdrop-filter: blur(3px); }
.pe-dialog { width: 100%; max-width: 440px; padding: 20px 22px; background: var(--card-bg, var(--bg-secondary)); color: var(--text-primary); border: 1px solid var(--border-color); border-radius: 14px; box-shadow: 0 24px 64px var(--shadow-color, rgba(0, 0, 0, 0.3)); }
.pe-title { margin: 0; font-size: 1.05rem; font-weight: 650; }
.pe-path { margin: 4px 0 16px; font-size: 0.78rem; color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pe-path code { font-family: var(--font-mono, monospace); }
.pe-label { display: block; margin: 12px 0 6px; font-size: 0.8rem; font-weight: 600; }
.pe-muted { color: var(--text-secondary); font-weight: 400; }
.pe-input { width: 100%; padding: 9px 12px; font: inherit; font-size: 0.88rem; color: var(--text-primary); background: var(--bg-primary); border: 1px solid var(--border-color); border-radius: 8px; }
.pe-input:focus { outline: none; border-color: var(--accent-purple); box-shadow: 0 0 0 3px rgba(var(--accent-purple-rgb, 138, 108, 255), 0.15); }
.pe-textarea { resize: vertical; min-height: 56px; }
.pe-error { margin: 6px 0 0; font-size: 0.8rem; color: var(--status-error); }
.pe-swatches { display: flex; flex-wrap: wrap; gap: 8px; }
.pe-swatch { width: 22px; height: 22px; padding: 0; border-radius: 50%; border: 2px solid transparent; background: var(--swatch); box-shadow: inset 0 0 0 1px rgba(0, 0, 0, 0.12); cursor: pointer; }
.pe-swatch--on { border-color: var(--card-bg, #fff); box-shadow: 0 0 0 2px var(--swatch); }
.pe-swatch:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 3px; }
.pe-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 22px; }
.pe-btn { padding: 8px 14px; font: inherit; font-size: 0.85rem; font-weight: 600; border-radius: 8px; border: 1px solid transparent; cursor: pointer; }
.pe-btn:disabled { opacity: 0.55; cursor: not-allowed; }
.pe-btn--primary { background: var(--accent-purple); color: var(--bg-primary); }
.pe-btn--secondary { background: var(--bg-primary); color: var(--text-primary); border-color: var(--border-color); }
</style>
