<template>
  <fieldset v-if="shown" class="sandbox-grants" data-testid="sandbox-grants">
    <legend class="grants-legend">Give this session access to</legend>
    <p v-if="!grants.length" class="grants-note">Could not load what can be granted.</p>
    <ul v-else class="grant-list">
      <li v-for="g in grants" :key="g.id" class="grant-item" :class="{ unavailable: !g.available }">
        <label class="grant-row">
          <input
            type="checkbox"
            :data-testid="`grant-${g.id}`"
            :checked="chosen.includes(g.id)"
            :disabled="!g.available"
            :aria-describedby="`grant-help-${g.id}`"
            @change="toggle(g.id, ($event.target as HTMLInputElement).checked)"
          />
          <span class="grant-title">{{ g.title }}</span>
        </label>
        <p :id="`grant-help-${g.id}`" class="grant-help">
          {{ g.description }}
          <span v-if="!g.available" class="grant-reason" :data-testid="`grant-reason-${g.id}`">{{ g.reason || 'Not available' }}.</span>
          <span v-else class="grant-risk">{{ g.risk }}</span>
        </p>
      </li>
    </ul>

    <div class="grant-paths" data-testid="grant-paths">
      <p class="grant-title">Another folder or file…</p>
      <ul v-if="paths.length" class="path-list">
        <li v-for="(p, i) in paths" :key="i" class="path-row">
          <input
            type="text"
            class="path-input"
            :value="p.path"
            :aria-label="`Folder or file ${i + 1}`"
            placeholder="~/data or /Users/you/notes.md"
            autocomplete="off"
            spellcheck="false"
            data-testid="grant-path-input"
            @input="setPath(i, ($event.target as HTMLInputElement).value)"
          />
          <select
            class="path-mode"
            :value="p.write ? 'write' : 'read'"
            :aria-label="`Access for folder or file ${i + 1}`"
            data-testid="grant-path-mode"
            @change="setWrite(i, ($event.target as HTMLSelectElement).value === 'write')"
          >
            <option value="read">Read</option>
            <option value="write">Read and write</option>
          </select>
          <button type="button" class="path-remove" :aria-label="`Remove folder or file ${i + 1}`" data-testid="grant-path-remove" @click="removePath(i)">×</button>
        </li>
      </ul>
      <button type="button" class="path-add" data-testid="grant-path-add" :disabled="paths.length >= max" @click="addPath">
        Add a folder or file
      </button>
      <p class="grant-help">Inside your home folder. The agent can read it, and write to it if you pick that. Eggify's own files and your hidden credentials can't be added here.</p>
    </div>
  </fieldset>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { sandboxSupportForProvider } from '~/utils/agents/sandbox'
import {
  MAX_GRANT_PATHS,
  readSavedGrants,
  saveGrants,
  type SandboxGrantInfo,
  type SandboxGrantPath,
} from '~/utils/agents/sandboxGrants'
import { useSandboxSettings } from '~/composables/useSandboxSettings'

const props = withDefaults(defineProps<{
  /** The sandbox toggle: null = follow the global default. */
  sandboxed?: boolean | null
  provider?: string | null
  /** Project the choice is remembered for ('' = nothing is remembered). */
  projectKey?: string
  /** v-model:grant-ids */
  grantIds?: string[]
  /** v-model:paths */
  paths?: SandboxGrantPath[]
}>(), { sandboxed: null, provider: null, projectKey: '', grantIds: () => [], paths: () => [] })
const emit = defineEmits<{
  (e: 'update:grantIds', value: string[]): void
  (e: 'update:paths', value: SandboxGrantPath[]): void
}>()

const max = MAX_GRANT_PATHS
const grants = ref<SandboxGrantInfo[]>([])
const defaultEnabled = ref<boolean | null>(null)

// Grants exist for sessions on the Claude CLI: Codex and OpenAI-compatible
// sessions have none yet.
const isClaudeCli = computed(() => {
  const p = (props.provider || 'claude').trim().toLowerCase()
  return p !== 'codex' && sandboxSupportForProvider(p) !== 'unsupported'
})
const sandboxOn = computed(() => {
  if (props.sandboxed === true || props.sandboxed === false) return props.sandboxed
  return defaultEnabled.value === true
})
const shown = computed(() => isClaudeCli.value && sandboxOn.value)

// Only ids this machine can give now count as checked.
const chosen = computed(() => props.grantIds.filter((id) => grants.value.some((g) => g.id === id && g.available)))

const remember = (grantIds: string[], paths: SandboxGrantPath[]) => {
  if (props.projectKey) saveGrants(props.projectKey, { grants: grantIds, paths })
}

const toggle = (id: string, on: boolean) => {
  const next = on ? [...new Set([...props.grantIds, id])] : props.grantIds.filter((g) => g !== id)
  emit('update:grantIds', next)
  remember(next, props.paths)
}
const setPaths = (next: SandboxGrantPath[]) => {
  emit('update:paths', next)
  remember(props.grantIds, next)
}
const addPath = () => {
  if (props.paths.length < max) setPaths([...props.paths, { path: '', write: false }])
}
const removePath = (i: number) => setPaths(props.paths.filter((_, j) => j !== i))
const setPath = (i: number, value: string) => setPaths(props.paths.map((p, j) => (j === i ? { ...p, path: value } : p)))
const setWrite = (i: number, write: boolean) => setPaths(props.paths.map((p, j) => (j === i ? { ...p, write } : p)))

// A project's remembered choice is applied when it is shown or the project
// changes; a project with none starts with nothing checked.
const applySaved = () => {
  const saved = readSavedGrants(props.projectKey)
  const available = new Set(grants.value.filter((g) => g.available).map((g) => g.id))
  emit('update:grantIds', saved ? saved.grants.filter((id) => available.has(id)) : [])
  emit('update:paths', saved ? saved.paths : [])
}
const clear = () => {
  if (props.grantIds.length > 0) emit('update:grantIds', [])
  if (props.paths.length > 0) emit('update:paths', [])
}
watch(() => props.projectKey, () => { if (shown.value) applySaved() })

// Nothing is granted unless the list is on screen (sandbox off, or a runtime
// without grants), so a hidden choice is never sent.
watch(shown, (on) => {
  if (!on) clear()
  else if (grants.value.length > 0) applySaved()
})

onMounted(async () => {
  const { load } = useSandboxSettings()
  const s = await load()
  defaultEnabled.value = s ? s.default_enabled : false
  grants.value = s?.grants ?? []
  if (shown.value) applySaved()
  else clear()
})
</script>

<style scoped>
.sandbox-grants {
  margin: 6px 0 0;
  padding: 8px 10px;
  border: 1px solid var(--border-color, rgba(127, 127, 127, 0.4));
  border-radius: 6px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}
.grants-legend,
.grant-title {
  font-size: 0.8rem;
  font-weight: 600;
  margin: 0;
}
.grant-list,
.path-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.grant-row {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
}
.grant-item.unavailable .grant-row {
  cursor: not-allowed;
  opacity: 0.6;
}
.grant-help,
.grants-note {
  margin: 0 0 0 24px;
  font-size: 0.72rem;
  line-height: 1.4;
  color: var(--text-secondary, inherit);
}
.grant-paths .grant-help {
  margin-left: 0;
}
.grant-reason {
  font-style: italic;
}
.grant-paths {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.path-row {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
}
.path-input {
  flex: 1 1 12rem;
  min-width: 0;
  padding: 4px 8px;
  border: 1px solid var(--border-color, rgba(127, 127, 127, 0.4));
  border-radius: 4px;
  background: var(--input-bg, transparent);
  color: var(--text-primary, inherit);
  font: inherit;
  font-size: 0.8rem;
}
.path-mode {
  padding: 4px 6px;
  border: 1px solid var(--border-color, rgba(127, 127, 127, 0.4));
  border-radius: 4px;
  background: var(--input-bg, transparent);
  color: var(--text-primary, inherit);
  font: inherit;
  font-size: 0.8rem;
}
.path-add,
.path-remove {
  align-self: flex-start;
  min-height: 28px;
  padding: 2px 10px;
  border: 1px solid var(--border-color, rgba(127, 127, 127, 0.4));
  border-radius: 4px;
  background: transparent;
  color: var(--text-primary, inherit);
  font: inherit;
  font-size: 0.78rem;
  cursor: pointer;
}
.path-add:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
.path-add:focus-visible,
.path-remove:focus-visible,
.path-input:focus-visible,
.path-mode:focus-visible {
  outline: 2px solid var(--accent-color, currentColor);
  outline-offset: 1px;
}
</style>
