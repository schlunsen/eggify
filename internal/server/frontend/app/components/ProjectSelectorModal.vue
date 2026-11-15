<template>
  <Transition name="ps">
      <div v-if="isOpen" class="modal-overlay" @mousedown.self="closeModal">
        <div class="ps" role="dialog" aria-modal="true" aria-label="Switch project">
          <div class="ps-search">
            <svg class="ps-search-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <circle cx="11" cy="11" r="7.5" />
              <path d="m20.5 20.5-4.2-4.2" />
            </svg>
            <input
              ref="searchInput"
              v-model="searchQuery"
              type="text"
              class="ps-input"
              placeholder="Switch project…"
              aria-label="Search projects"
              role="combobox"
              aria-expanded="true"
              aria-controls="ps-list"
              :aria-activedescendant="focusedId"
              autocomplete="off"
              spellcheck="false"
              @keydown="handleSearchKeydown"
            />
            <button v-if="searchQuery" class="ps-clear" aria-label="Clear search" @click="clearSearch">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </button>
            <span class="ps-count">{{ countLabel }}</span>
            <kbd class="ps-esc" role="button" title="Close" @click="closeModal">Esc</kbd>
            <button class="ps-cancel" @click="closeModal">Cancel</button>
          </div>

          <div id="ps-list" ref="list" class="ps-list" role="listbox" aria-label="Projects">
            <div v-if="loading && !projects.length" class="ps-empty">
              <EggLoader compact />
              <span>Gathering the troupe…</span>
            </div>
            <div v-else-if="!filteredProjects.length" class="ps-empty">
              <template v-if="searchQuery">
                <span>No project matches “{{ searchQuery.trim() }}”.</span>
                <button class="ps-link" @click="newProject">Add a new project</button>
              </template>
              <template v-else>
                <span>No projects yet.</span>
                <button class="ps-link" @click="newProject">Add your first project</button>
              </template>
            </div>

            <div
              v-for="(project, index) in filteredProjects"
              :id="`ps-opt-${project.id}`"
              :key="project.id"
              class="project-item"
              :class="{
                'project-item-focused': focusedIndex === index,
                'is-current': currentProject?.id === project.id,
                'is-missing': project.missing,
              }"
              role="option"
              :aria-selected="focusedIndex === index"
              :style="{ '--pc': project.color || 'var(--accent-purple)' }"
              @click="selectProject(project, $event.metaKey || $event.ctrlKey)"
              @mousemove="focusedIndex = index"
            >
              <span class="ps-tile" :class="{ 'has-logo': showLogo(project) }" aria-hidden="true">
                <img
                  v-if="showLogo(project)"
                  :src="project.icon_url"
                  alt=""
                  loading="lazy"
                  decoding="async"
                  draggable="false"
                  @error="brokenIcons.add(project.id)"
                />
                <span v-else class="ps-initials">{{ projectInitials(project.name) }}</span>
                <span v-if="(project.running_count || 0) > 0" class="ps-live" />
              </span>

              <span class="ps-body">
                <span class="ps-line">
                  <span class="project-name">
                    <template v-for="(seg, i) in highlight(project.name)" :key="i">
                      <mark v-if="seg.hit">{{ seg.text }}</mark><template v-else>{{ seg.text }}</template>
                    </template>
                  </span>
                  <span v-if="project.git?.branch" class="ps-chip ps-branch" :title="project.git.dirty ? 'Uncommitted changes' : 'Clean'">
                    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                      <circle cx="6" cy="5" r="2.2" /><circle cx="6" cy="19" r="2.2" /><circle cx="18" cy="7" r="2.2" />
                      <path d="M6 7.2v9.6M18 9.2c0 5-6 4-11 7.6" />
                    </svg>
                    <span class="ps-chip-text">{{ project.git.branch }}</span>
                    <span v-if="project.git.dirty" class="ps-dirty" aria-label="uncommitted changes" />
                  </span>
                  <span v-if="project.missing" class="ps-chip ps-warn">Folder missing</span>
                </span>
                <span v-if="describe(project)" class="ps-desc" :class="{ 'is-auto': !project.description }">{{ describe(project) }}</span>
                <span class="project-path">{{ project.display_path || project.path }}</span>
              </span>

              <span class="ps-meta">
                <span v-if="currentProject?.id === project.id" class="ps-current">Current</span>
                <span v-if="(project.running_count || 0) > 0" class="ps-running">{{ project.running_count }} running</span>
                <span v-else-if="project.last_activity" class="ps-when">{{ relativeTime(project.last_activity) }}</span>
                <span v-if="project.session_count" class="ps-sessions">
                  {{ project.session_count }} {{ project.session_count === 1 ? 'session' : 'sessions' }}
                </span>
              </span>
            </div>
          </div>

          <footer class="ps-footer">
            <button class="ps-new" @click="newProject">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                <path d="M12 5v14M5 12h14" />
              </svg>
              New project
              <kbd v-for="k in newProjectKeys" :key="k">{{ k }}</kbd>
            </button>
            <button class="ps-new ps-subtle" @click="manageProjects">Manage</button>
            <button v-if="currentProject" class="ps-new ps-subtle" title="Show every project's sessions" @click="clearSelection">
              Clear selection
            </button>
            <span class="ps-spacer" />
            <span class="ps-hints">
              <span><kbd>↑</kbd><kbd>↓</kbd> move</span>
              <span><kbd>↵</kbd> open</span>
              <span><kbd>{{ modKey }}</kbd><kbd>↵</kbd> new session</span>
            </span>
          </footer>
        </div>
      </div>
  </Transition>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch, nextTick, reactive } from 'vue'
import { storeToRefs } from 'pinia'
import { useRouter } from 'vue-router'
import type { Project } from '~/types/projects'
import { useSessionStore } from '~/stores/session/sessionStore'
import { useClosable, useShortcutLabels } from '~/composables/useKeyboardShortcuts'
import EggLoader from '~/components/ui/EggLoader.vue'
import {
  filterProjects, sortProjects, projectInitials, relativeTime, type ProjectListItem,
} from '~/utils/projects/projectSetup'

type Row = ProjectListItem & Record<string, any>

const props = defineProps<{
  modelValue: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()

const router = useRouter()
const { fetchWithAuth } = useAuthenticatedFetch()
const sessionStore = useSessionStore()
const { selectedProject } = storeToRefs(sessionStore)
const shortcutKeys = useShortcutLabels()

const isOpen = computed({
  get: () => props.modelValue,
  set: (value) => emit('update:modelValue', value)
})

// Kept between openings so the list shows at once; refreshed on every open.
const projects = ref<Row[]>(cachedRows)
const loading = ref(false)
const searchQuery = ref('')
const searchInput = ref<HTMLInputElement | null>(null)
const list = ref<HTMLElement | null>(null)
const focusedIndex = ref(0)
const brokenIcons = reactive(new Set<string>())
const currentProject = computed(() => selectedProject.value)

const modKey = shortcutKeys.platform === 'mac' ? '⌘' : 'Ctrl'
const newProjectKeys = computed(() => shortcutKeys.parts('project.new'))

const queryTerms = computed(() => searchQuery.value.toLowerCase().trim().split(/\s+/).filter(Boolean))

/**
 * No query: the current project first, then running and recently used ones.
 * With a query: name matches before matches in the path or description.
 */
const filteredProjects = computed<Row[]>(() => {
  const sorted = sortProjects(projects.value, 'recent')
  const terms = queryTerms.value
  if (!terms.length) {
    const cur = currentProject.value?.id
    return cur ? [...sorted.filter(p => p.id === cur), ...sorted.filter(p => p.id !== cur)] : sorted
  }
  const rank = (p: Row) => {
    const name = p.name.toLowerCase()
    if (name.startsWith(terms[0]!)) return 0
    if (terms.every(t => name.includes(t))) return 1
    return 2
  }
  return filterProjects(sorted, searchQuery.value)
    .map((p, i) => ({ p, i, r: rank(p) }))
    .sort((a, b) => a.r - b.r || a.i - b.i)
    .map(x => x.p)
})

const focusedId = computed(() => {
  const p = filteredProjects.value[focusedIndex.value]
  return p ? `ps-opt-${p.id}` : undefined
})

const countLabel = computed(() => {
  const n = filteredProjects.value.length
  if (!projects.value.length) return ''
  return queryTerms.value.length ? `${n} of ${projects.value.length}` : `${n} ${n === 1 ? 'project' : 'projects'}`
})

function showLogo(p: Row): boolean {
  return !!p.icon_url && !brokenIcons.has(p.id)
}

function describe(p: Row): string {
  return (p.description || p.summary || '').trim()
}

/** Splits text into plain and matching parts for the search highlight. */
function highlight(text: string): { text: string, hit: boolean }[] {
  const terms = queryTerms.value
  if (!terms.length) return [{ text, hit: false }]
  const lower = text.toLowerCase()
  const hits = new Array(text.length).fill(false)
  for (const t of terms) {
    let at = lower.indexOf(t)
    while (at !== -1) {
      hits.fill(true, at, at + t.length)
      at = lower.indexOf(t, at + t.length)
    }
  }
  const out: { text: string, hit: boolean }[] = []
  for (let i = 0; i < text.length; i++) {
    const last = out[out.length - 1]
    if (last && last.hit === hits[i]) last.text += text[i]
    else out.push({ text: text[i]!, hit: hits[i] })
  }
  return out
}

const OVERVIEW_ONLY = ['display_path', 'last_activity', 'session_count', 'running_count', 'missing', 'git', 'icon_url', 'summary']

/** The store keeps plain projects, not the overview's extra fields. */
function toProject(row: Row): Project {
  const p: Record<string, any> = { ...row }
  for (const k of OVERVIEW_ONLY) delete p[k]
  return p as Project
}

const closeModal = () => {
  isOpen.value = false
  searchQuery.value = ''
  focusedIndex.value = 0
}
useClosable(isOpen, closeModal, { modal: true })

const clearSearch = () => {
  searchQuery.value = ''
  focusedIndex.value = 0
  searchInput.value?.focus()
}

const selectProject = async (project: Row, newSession = false) => {
  sessionStore.setSelectedProject(toProject(project))
  closeModal()
  // Keep the URL in sync so route navigation does not clear the new selection
  // when replacing a previous project's query parameter.
  const query: Record<string, string> = { project: project.id }
  if (newSession) query.new_session = '1'
  await router.push({ path: '/agents', query })
}

const manageProjects = async () => {
  closeModal()
  await router.push('/projects')
}

const clearSelection = () => {
  sessionStore.setSelectedProject(null)
  closeModal()
}

const newProject = async () => {
  closeModal()
  await router.push({ path: '/projects', query: { new: '1' } })
}

function move(delta: number) {
  const n = filteredProjects.value.length
  if (!n) return
  focusedIndex.value = Math.min(n - 1, Math.max(0, focusedIndex.value + delta))
}

/** Arrow keys, Page/Home/End and Enter (⌘/Ctrl+Enter starts a new session). */
function handleNavKey(e: KeyboardEvent): boolean {
  switch (e.key) {
    case 'ArrowDown': move(1); break
    case 'ArrowUp': move(-1); break
    case 'PageDown': move(5); break
    case 'PageUp': move(-5); break
    case 'Home':
      if (e.target === searchInput.value && searchQuery.value) return false
      focusedIndex.value = 0
      break
    case 'End':
      if (e.target === searchInput.value && searchQuery.value) return false
      focusedIndex.value = Math.max(0, filteredProjects.value.length - 1)
      break
    case 'Enter': {
      const p = filteredProjects.value[focusedIndex.value]
      if (p) selectProject(p, e.metaKey || e.ctrlKey)
      break
    }
    default: return false
  }
  e.preventDefault()
  return true
}

const handleSearchKeydown = (e: KeyboardEvent) => {
  if (e.isComposing) return
  // The document listener handles keys outside the input. Do not handle the
  // same event twice as it bubbles out of the search field.
  if (handleNavKey(e)) e.stopPropagation()
}

const handleKeyDown = (e: KeyboardEvent) => {
  // Escape: the shortcut registry's Escape stack (useClosable)
  if (!isOpen.value || e.defaultPrevented || e.isComposing) return
  handleNavKey(e)
}

let loadSeq = 0
/** Two passes, like the Projects page: rows at once, git state after. */
const fetchProjects = async () => {
  const seq = ++loadSeq
  loading.value = true
  try {
    const response = await fetchWithAuth('/api/projects/overview?git=0', { method: 'GET' })
    const data = await response.json()
    if (seq !== loadSeq) return
    const gitById = new Map(projects.value.map(p => [p.id, p.git]))
    projects.value = (data.projects || []).map((p: Row) => ({ ...p, git: p.git ?? gitById.get(p.id) ?? null }))
    cachedRows = projects.value
  } catch (error) {
    console.error('Failed to fetch projects:', error)
    return
  } finally {
    if (seq === loadSeq) loading.value = false
  }
  try {
    const response = await fetchWithAuth('/api/projects/overview', { method: 'GET' })
    const data = await response.json()
    if (seq !== loadSeq || !isOpen.value) return
    const byId = new Map<string, Row>((data.projects || []).map((p: Row) => [p.id, p]))
    projects.value = projects.value.map(p => byId.has(p.id) ? { ...p, ...byId.get(p.id) } : p)
    cachedRows = projects.value
  } catch { /* git state is optional */ }
}

// Open with the cursor on the row after the current project, the usual
// next stop when switching; on the current one when it is the only project.
function focusInitial() {
  const rows = filteredProjects.value
  const cur = currentProject.value?.id
  focusedIndex.value = cur && rows[0]?.id === cur && rows.length > 1 ? 1 : 0
}

watch(isOpen, async (open) => {
  if (!open) return
  brokenIcons.clear()
  focusInitial()
  await nextTick()
  // On phones the keyboard would cover half the list; tap the field to search.
  if (!window.matchMedia?.('(pointer: coarse)').matches) searchInput.value?.focus()
  const before = filteredProjects.value[focusedIndex.value]?.id
  await fetchProjects()
  // Keep the cursor on the same project if the list changed under it.
  if (!searchQuery.value) {
    const i = before ? filteredProjects.value.findIndex(p => p.id === before) : -1
    if (i >= 0) focusedIndex.value = i
    else focusInitial()
  }
})

watch(searchQuery, () => {
  focusedIndex.value = 0
})

watch(focusedIndex, async () => {
  await nextTick()
  const el = focusedId.value ? document.getElementById(focusedId.value) : null
  el?.scrollIntoView?.({ block: 'nearest' })
})

onMounted(() => {
  document.addEventListener('keydown', handleKeyDown)
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleKeyDown)
})
</script>

<script lang="ts">
let cachedRows: any[] = []
</script>

<style scoped>
.modal-overlay {
  position: fixed;
  inset: 0;
  z-index: 10000;
  display: flex;
  justify-content: center;
  align-items: flex-start;
  padding: 10vh 16px 16px;
  background: rgba(8, 8, 16, 0.5);
  backdrop-filter: blur(3px);
}

.ps {
  width: min(720px, 100%);
  max-height: min(680px, 80vh);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--card-bg);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: 16px;
  box-shadow: 0 1px 0 rgba(255, 255, 255, 0.05) inset, 0 32px 90px rgba(0, 0, 0, 0.38), 0 4px 16px rgba(0, 0, 0, 0.14);
}

.ps-enter-active, .ps-leave-active { transition: opacity 0.16s ease; }
.ps-enter-active .ps, .ps-leave-active .ps { transition: transform 0.18s cubic-bezier(0.2, 0.9, 0.3, 1.2), opacity 0.16s ease; }
.ps-enter-from, .ps-leave-to { opacity: 0; }
.ps-enter-from .ps, .ps-leave-to .ps { transform: translateY(-8px) scale(0.985); opacity: 0; }

/* ── Search ─────────────────────────────────────────────────────────── */
.ps-search {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 16px 18px;
  border-bottom: 1px solid var(--border-color);
}
.ps-search-icon { color: var(--text-secondary); flex-shrink: 0; }
.ps-input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  font-size: 1.08rem;
}
.ps-input:focus,
.ps-input:focus-visible {
  outline: none;
  border: none;
  box-shadow: none;
  background: transparent;
  padding: 0;
}
.ps-input::placeholder { color: var(--text-muted, var(--text-secondary)); opacity: 0.85; }
.ps-clear {
  display: inline-flex;
  padding: 4px;
  border: none;
  border-radius: 6px;
  background: none;
  color: var(--text-secondary);
  cursor: pointer;
}
.ps-clear:hover { background: var(--bg-secondary); color: var(--text-primary); }
.ps-count {
  flex-shrink: 0;
  font-size: 0.75rem;
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
}
.ps-esc { cursor: pointer; }
.ps-cancel {
  display: none;
  flex-shrink: 0;
  border: none;
  background: none;
  padding: 6px 2px 6px 6px;
  font: inherit;
  font-size: 0.95rem;
  color: var(--accent-purple);
  cursor: pointer;
}

/* ── List ───────────────────────────────────────────────────────────── */
.ps-list {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
  overscroll-behavior: contain;
  scroll-padding: 8px;
}

.project-item {
  position: relative;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 12px 14px;
  border-radius: 12px;
  cursor: pointer;
  user-select: none;
  -webkit-tap-highlight-color: transparent;
}
.project-item:active { background: var(--primary-subtle, rgba(139, 124, 246, 0.12)); }
.project-item + .project-item { margin-top: 2px; }
.project-item-focused {
  background: var(--primary-subtle, rgba(139, 124, 246, 0.12));
  box-shadow: inset 0 0 0 1px var(--primary-border, rgba(139, 124, 246, 0.3));
}
.project-item-focused::before {
  content: '';
  position: absolute;
  left: 0;
  top: 14px;
  bottom: 14px;
  width: 3px;
  border-radius: 0 3px 3px 0;
  background: var(--pc);
}
.project-item.is-missing .ps-tile,
.project-item.is-missing .ps-body { opacity: 0.55; }

/* Logo tile: the project's logo on a neutral plate, or its initials on its colour. */
.ps-tile {
  position: relative;
  flex-shrink: 0;
  width: 48px;
  height: 48px;
  border-radius: 12px;
  display: grid;
  place-items: center;
  background: linear-gradient(145deg, color-mix(in srgb, var(--pc) 92%, white), color-mix(in srgb, var(--pc) 70%, black));
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.18), inset 0 0 0 1px rgba(255, 255, 255, 0.12);
}
.ps-tile.has-logo {
  background: color-mix(in srgb, var(--bg-secondary) 70%, var(--card-bg));
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.14), inset 0 0 0 1px color-mix(in srgb, var(--pc) 45%, var(--border-color));
}
.ps-tile img {
  width: 100%;
  height: 100%;
  padding: 6px;
  object-fit: contain;
  border-radius: 12px;
}
.ps-initials {
  font-weight: 700;
  font-size: 1rem;
  letter-spacing: 0.02em;
  color: #fff;
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.25);
}
.ps-live {
  position: absolute;
  right: -3px;
  bottom: -3px;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: var(--accent-green, #22c55e);
  border: 2px solid var(--card-bg);
  animation: ps-pulse 2s ease-in-out infinite;
}
@keyframes ps-pulse {
  50% { box-shadow: 0 0 0 4px color-mix(in srgb, var(--accent-green, #22c55e) 25%, transparent); }
}

.ps-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.ps-line {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.project-name {
  min-width: 3ch;
  font-weight: 600;
  font-size: 0.98rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.project-name mark {
  background: color-mix(in srgb, var(--accent-purple) 26%, transparent);
  color: inherit;
  border-radius: 3px;
  padding: 0 1px;
}
.ps-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  /* Chips give way before the project name does. */
  flex-shrink: 100;
  min-width: 0;
  max-width: min(180px, 45%);
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  padding: 1px 7px;
  border-radius: 999px;
  font-size: 0.7rem;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
}
.ps-branch svg { flex-shrink: 0; opacity: 0.8; }
.ps-chip-text { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
.ps-dirty {
  width: 6px;
  height: 6px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--accent-orange, #f59e0b);
}
.ps-warn { color: var(--accent-orange, #f59e0b); border-color: color-mix(in srgb, var(--accent-orange, #f59e0b) 40%, transparent); }

.ps-desc {
  font-size: 0.82rem;
  line-height: 1.35;
  color: var(--text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.project-path {
  font-family: ui-monospace, 'SF Mono', Menlo, monospace;
  font-size: 0.7rem;
  color: var(--text-muted, var(--text-secondary));
  opacity: 0.8;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ps-meta {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 4px;
  font-size: 0.72rem;
  color: var(--text-secondary);
  text-align: right;
}
.ps-current {
  padding: 1px 8px;
  border-radius: 999px;
  font-weight: 600;
  font-size: 0.68rem;
  color: var(--pc);
  background: color-mix(in srgb, var(--pc) 14%, transparent);
  border: 1px solid color-mix(in srgb, var(--pc) 40%, transparent);
}
.ps-running { color: var(--accent-green, #22c55e); font-weight: 600; }
.ps-sessions { opacity: 0.75; }

.ps-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 40px 16px;
  font-size: 0.9rem;
  color: var(--text-secondary);
}
.ps-link {
  border: none;
  background: none;
  padding: 0;
  font: inherit;
  color: var(--accent-purple);
  cursor: pointer;
}
.ps-link:hover { text-decoration: underline; }

/* ── Footer ─────────────────────────────────────────────────────────── */
.ps-footer {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 10px 12px 10px 10px;
  border-top: 1px solid var(--border-color);
  background: var(--bg-secondary);
  font-size: 0.74rem;
  color: var(--text-secondary);
}
.ps-new {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px;
  border-radius: 8px;
  border: 1px solid transparent;
  background: none;
  font: inherit;
  font-weight: 600;
  color: var(--text-primary);
  cursor: pointer;
}
.ps-new:hover { background: var(--card-bg); border-color: var(--border-color); }
.ps-new kbd:first-of-type { margin-left: 4px; }
.ps-subtle { font-weight: 500; color: var(--text-secondary); }
.ps-subtle:hover { color: var(--text-primary); }
.ps-spacer { flex: 1; }
.ps-hints { display: flex; gap: 14px; }
.ps-hints span { display: inline-flex; align-items: center; gap: 3px; }

kbd {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 20px;
  padding: 0 5px;
  font: 500 0.7rem/1 ui-sans-serif, -apple-system, 'Segoe UI', system-ui, sans-serif;
  color: var(--text-secondary);
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-bottom-width: 2px;
  border-radius: 5px;
}
.ps-search kbd { background: var(--bg-secondary); }

/* Phones: a full-height sheet clear of the notch and home indicator. */
@media (max-width: 600px) {
  .modal-overlay {
    padding: max(8px, env(safe-area-inset-top)) 0 0;
    align-items: stretch;
  }
  .ps {
    max-height: none;
    height: calc(100dvh - max(8px, env(safe-area-inset-top)));
    border-radius: 16px 16px 0 0;
    border-bottom: none;
  }
  .ps-search { padding: 12px 14px; }
  /* 16px keeps iOS Safari from zooming in when the field is focused. */
  .ps-input { font-size: 16px; }
  .ps-cancel { display: inline-flex; }
  .ps-list { padding: 6px; }
  .project-item { padding: 12px 10px; gap: 12px; min-height: 64px; }
  .ps-footer { padding-bottom: max(10px, env(safe-area-inset-bottom)); justify-content: space-between; }
  .ps-new { padding: 10px 12px; font-size: 0.9rem; }
  .ps-spacer { display: none; }
  .ps-hints, .ps-count, .ps-esc, .ps-new kbd { display: none; }
  .ps-tile { width: 40px; height: 40px; border-radius: 10px; }
  .project-path { display: none; }
  .ps-sessions { display: none; }
}

@media (prefers-reduced-motion: reduce) {
  .ps-live { animation: none; }
  .ps-enter-active .ps, .ps-leave-active .ps { transition: opacity 0.16s ease; }
  .ps-enter-from .ps, .ps-leave-to .ps { transform: none; }
}
</style>
