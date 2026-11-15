<template>
  <div class="pp-page">
    <div class="pp-shell">
      <header class="pp-header">
        <div>
          <h1>Projects</h1>
          <p>Each project is a folder your agents work in.</p>
        </div>
        <button type="button" class="pp-btn pp-btn--primary pp-new" @click="openDialog(null)">
          <ProjectsGlyph name="plus" />
          New project
        </button>
      </header>

      <div v-if="projects.length > 0" class="pp-toolbar">
        <div class="pp-search">
          <ProjectsGlyph name="search" class="pp-search__icon" />
          <input
            ref="searchInput"
            v-model="query"
            type="search"
            class="pp-search__input"
            placeholder="Search projects"
            aria-label="Search projects"
            @keydown.esc="query = ''"
          >
          <button v-if="query" type="button" class="pp-search__clear" aria-label="Clear search" @click="query = ''; searchInput?.focus()">
            <ProjectsGlyph name="x" :size="14" />
          </button>
        </div>
        <div class="pp-sort" role="radiogroup" aria-label="Sort projects">
          <button
            v-for="opt in sortOptions"
            :key="opt.value"
            type="button"
            role="radio"
            class="pp-sort__btn"
            :class="{ 'pp-sort__btn--on': sort === opt.value }"
            :aria-checked="sort === opt.value"
            :data-sort="opt.value"
            @click="setSort(opt.value)"
          >{{ opt.label }}</button>
        </div>
      </div>

      <div v-if="loading && projects.length === 0" class="pp-list" aria-busy="true" aria-label="Loading projects">
        <div v-for="i in 4" :key="i" class="pp-row pp-row--skeleton"><span /><span /></div>
      </div>

      <div v-else-if="loadError" class="pp-message pp-message--error" role="alert">
        <ProjectsGlyph name="alert" :size="18" />
        <span>{{ loadError }}</span>
        <button type="button" class="pp-btn pp-btn--secondary" @click="load()">Try again</button>
      </div>

      <!-- Empty state -->
      <section v-else-if="projects.length === 0" class="pp-empty" aria-labelledby="pp-empty-title">
        <div class="pp-empty__art" aria-hidden="true"><ProjectsGlyph name="folder" :size="28" /></div>
        <h2 id="pp-empty-title">No projects yet</h2>
        <p>Add a folder you already work in, clone a repository, or start from scratch.</p>
        <div class="pp-empty__options">
          <button v-for="opt in startOptions" :key="opt.mode" type="button" class="pp-start" :data-mode="opt.mode" @click="openDialog(opt.mode)">
            <span class="pp-start__icon"><ProjectsGlyph :name="opt.icon" :size="20" /></span>
            <span class="pp-start__title">{{ opt.title }}</span>
            <span class="pp-start__desc">{{ opt.desc }}</span>
          </button>
        </div>
      </section>

      <template v-else>
        <p class="pp-count" aria-live="polite">
          <template v-if="query">{{ visible.length }} of {{ projects.length }} projects</template>
          <template v-else>{{ projects.length }} {{ projects.length === 1 ? 'project' : 'projects' }}<template v-if="runningTotal"> · {{ runningTotal }} running {{ runningTotal === 1 ? 'session' : 'sessions' }}</template></template>
        </p>

        <div v-if="visible.length === 0" class="pp-message">
          <span>No projects match “{{ query }}”.</span>
          <button type="button" class="pp-btn pp-btn--ghost" @click="query = ''">Clear search</button>
        </div>

        <ul v-else class="pp-list" aria-label="Projects">
          <li
            v-for="p in visible"
            :key="p.id"
            class="pp-row"
            :class="{ 'pp-row--current': p.id === currentProjectId, 'pp-row--missing': p.missing, 'pp-row--menu': menuFor === p.id }"
            :style="{ '--project-color': p.color || 'var(--accent-purple)' }"
            :data-project-id="p.id"
          >
            <button type="button" class="pp-row__main" :aria-label="`Open ${p.name}`" @click="openProject(p)">
              <span class="pp-avatar" aria-hidden="true">{{ initials(p.name) }}</span>
              <span class="pp-row__text">
                <span class="pp-row__title">
                  <span class="pp-row__name">{{ p.name }}</span>
                  <span v-if="p.id === currentProjectId" class="pp-chip pp-chip--current">Current</span>
                  <span v-if="p.missing" class="pp-chip pp-chip--warn">Folder not found</span>
                </span>
                <span class="pp-row__path" :title="p.path">{{ p.display_path || shorten(p.path) }}</span>
                <span class="pp-row__meta">
                  <span v-if="p.git?.is_repo && p.git.branch" class="pp-meta pp-meta--branch" :title="p.git.dirty ? 'Uncommitted changes' : 'Clean'">
                    <ProjectsGlyph name="branch" :size="13" />
                    <span>{{ p.git.branch }}</span>
                    <span v-if="p.git.dirty" class="pp-dirty" aria-label="uncommitted changes">●</span>
                  </span>
                  <span v-if="(p.running_count || 0) > 0" class="pp-meta pp-meta--running">
                    <span class="pp-pulse" aria-hidden="true" />{{ p.running_count }} running
                  </span>
                  <span class="pp-meta">{{ p.session_count || 0 }} {{ (p.session_count || 0) === 1 ? 'session' : 'sessions' }}</span>
                  <span v-if="p.last_activity" class="pp-meta" :title="p.last_activity">Active {{ ago(p.last_activity) }}</span>
                  <span v-else class="pp-meta pp-meta--quiet">No activity yet</span>
                </span>
              </span>
            </button>

            <div class="pp-row__actions">
              <button type="button" class="pp-btn pp-btn--secondary pp-btn--sm" :disabled="p.missing" :data-action="'start'" @click="startSession(p)">
                <ProjectsGlyph name="play" :size="12" />
                <span>Start session</span>
              </button>
              <div class="pp-menu">
                <button
                  type="button"
                  class="pp-icon-btn"
                  :aria-label="`More actions for ${p.name}`"
                  aria-haspopup="menu"
                  :aria-expanded="menuFor === p.id"
                  :data-action="'menu'"
                  @click.stop="toggleMenu(p.id)"
                >
                  <ProjectsGlyph name="more" />
                </button>
                <div v-if="menuFor === p.id" class="pp-menu__list" role="menu" @keydown.esc.stop="menuFor = null" @click.stop>
                  <button type="button" role="menuitem" class="pp-menu__item" data-action="edit" @click="editProject(p)"><ProjectsGlyph name="pencil" :size="14" /> Rename &amp; color…</button>
                  <button type="button" role="menuitem" class="pp-menu__item" data-action="copy" @click="copyPath(p)"><ProjectsGlyph name="copy" :size="14" /> Copy path</button>
                  <button v-if="canReveal" type="button" role="menuitem" class="pp-menu__item" data-action="reveal" :disabled="p.missing" @click="reveal(p)"><ProjectsGlyph name="folderOpen" :size="14" /> Show in Finder</button>
                  <button type="button" role="menuitem" class="pp-menu__item" data-action="details" @click="openProject(p)"><ProjectsGlyph name="external" :size="14" /> Project details</button>
                  <hr class="pp-menu__sep">
                  <button type="button" role="menuitem" class="pp-menu__item pp-menu__item--danger" data-action="remove" @click="askRemove(p)"><ProjectsGlyph name="remove" :size="14" /> Remove from Eggify…</button>
                </div>
              </div>
            </div>
          </li>
        </ul>
      </template>
    </div>

    <NewProjectDialog
      :open="dialogOpen"
      :initial-mode="dialogMode"
      @close="closeDialog"
      @created="onCreated"
      @start-session="(p: any) => { closeDialog(); startSession(p) }"
      @open-project="(p: any) => { closeDialog(); openProject(p) }"
      @open-existing="(p: any) => { closeDialog(); openProject(p) }"
      @navigate="(path: string) => { closeDialog(); router.push(path) }"
    />

    <ProjectEditDialog :project="editing" @close="editing = null" @saved="onSaved" />

    <!-- Remove confirmation: never touches files on disk -->
    <div v-if="removing" class="pp-overlay" @mousedown.self="removing = null">
      <div class="pp-confirm" role="alertdialog" aria-modal="true" aria-labelledby="pp-remove-title" aria-describedby="pp-remove-desc" @keydown.esc.prevent.stop="removing = null">
        <h2 id="pp-remove-title">Remove “{{ removing.name }}” from Eggify?</h2>
        <p id="pp-remove-desc">
          The folder <code>{{ removing.display_path || shorten(removing.path) }}</code> and its files stay on disk.
          <template v-if="removing.session_count">Its {{ removing.session_count }} {{ removing.session_count === 1 ? 'session stays' : 'sessions stay' }} in your history.</template>
          You can add the folder again any time.
        </p>
        <p v-if="removeError" class="pp-confirm__error" role="alert">{{ removeError }}</p>
        <div class="pp-confirm__actions">
          <button ref="removeCancel" type="button" class="pp-btn pp-btn--secondary" @click="removing = null">Cancel</button>
          <button type="button" class="pp-btn pp-btn--danger" :disabled="removeBusy" @click="confirmRemove">{{ removeBusy ? 'Removing…' : 'Remove project' }}</button>
        </div>
      </div>
    </div>

    <div class="pp-toast" :class="{ 'pp-toast--on': !!toast }" role="status" aria-live="polite">{{ toast }}</div>
  </div>
</template>

<script setup lang="ts">
import { copyText } from '~/utils/clipboard'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import ProjectsGlyph from '~/components/projects/ProjectsGlyph.vue'
import NewProjectDialog from '~/components/projects/NewProjectDialog.vue'
import ProjectEditDialog from '~/components/projects/ProjectEditDialog.vue'
import { useSessionStore } from '~/stores/session/sessionStore'
import { canRevealFolder, desktopRevealFolder } from '~/utils/desktop'
import {
  filterProjects, projectInitials, relativeTime, shortenPath, sortProjects,
  type ProjectListItem, type ProjectSetupMode, type ProjectSort,
} from '~/utils/projects/projectSetup'

type Row = ProjectListItem & Record<string, any>

const route = useRoute()
const router = useRouter()
const sessionStore = useSessionStore()
const { fetchWithAuth } = useAuthenticatedFetch()

const SORT_KEY = 'eggify-projects-sort'
const sortOptions: { value: ProjectSort, label: string }[] = [
  { value: 'recent', label: 'Recent' },
  { value: 'name', label: 'Name' },
]
const startOptions: { mode: ProjectSetupMode, icon: string, title: string, desc: string }[] = [
  { mode: 'existing', icon: 'folderOpen', title: 'Open a folder', desc: 'Use a project already on this computer.' },
  { mode: 'clone', icon: 'download', title: 'Clone a repository', desc: 'From GitHub or any HTTPS git URL.' },
  { mode: 'empty', icon: 'folderPlus', title: 'Start empty', desc: 'A fresh folder, optionally with git.' },
]

const projects = ref<Row[]>([])
const home = ref<string>('')
const loading = ref(true)
const loadError = ref<string | null>(null)
const query = ref('')
const sort = ref<ProjectSort>(readSort())
const searchInput = ref<HTMLInputElement | null>(null)
const canReveal = ref(false)

const currentProjectId = computed(() => sessionStore.selectedProject?.id || null)
const visible = computed(() => sortProjects(filterProjects(projects.value, query.value), sort.value))
const runningTotal = computed(() => projects.value.reduce((n, p) => n + (p.running_count || 0), 0))

const initials = projectInitials
const shorten = (p: string) => shortenPath(p, home.value)
const ago = (iso: string) => relativeTime(iso)

function readSort(): ProjectSort {
  try {
    const v = typeof localStorage !== 'undefined' ? localStorage.getItem(SORT_KEY) : null
    return v === 'name' ? 'name' : 'recent'
  } catch { return 'recent' }
}
function setSort(v: ProjectSort) {
  sort.value = v
  try { localStorage.setItem(SORT_KEY, v) } catch { /* private mode */ }
}

// ── Loading ──────────────────────────────────────────────────────────────
// Two passes: rows render from the database at once, git state follows.
let loadSeq = 0
async function load() {
  const seq = ++loadSeq
  loadError.value = null
  loading.value = true
  try {
    const res = await fetchWithAuth('/api/projects/overview?git=0')
    if (!res.ok) throw new Error(`Could not load projects (${res.status})`)
    const body = await res.json()
    if (seq !== loadSeq) return
    const gitById = new Map(projects.value.map(p => [p.id, p.git]))
    projects.value = (body.projects || []).map((p: Row) => ({ ...p, git: p.git ?? gitById.get(p.id) ?? null }))
    home.value = body.home || ''
  } catch (err) {
    if (seq === loadSeq) loadError.value = err instanceof Error ? err.message : 'Could not load projects'
    return
  } finally {
    if (seq === loadSeq) loading.value = false
  }
  loadGit(seq)
}

async function loadGit(seq: number) {
  try {
    const res = await fetchWithAuth('/api/projects/overview')
    if (!res.ok || seq !== loadSeq) return
    const body = await res.json()
    if (seq !== loadSeq) return
    const byId = new Map<string, Row>((body.projects || []).map((p: Row) => [p.id, p]))
    projects.value = projects.value.map(p => byId.has(p.id) ? { ...p, ...byId.get(p.id) } : p)
  } catch { /* git state is optional */ }
}

// ── New project dialog (also opened by /projects?new=1) ──────────────────
const dialogOpen = ref(false)
const dialogMode = ref<ProjectSetupMode | null>(null)
const MODES: ProjectSetupMode[] = ['existing', 'clone', 'empty']

function openDialog(mode: ProjectSetupMode | null) {
  dialogMode.value = mode
  dialogOpen.value = true
}
function closeDialog() {
  dialogOpen.value = false
  if (route.query.new !== undefined) {
    const { new: _new, ...rest } = route.query
    router.replace({ path: route.path, query: rest })
  }
}

watch(() => route.query.new, (val, old) => {
  const v = Array.isArray(val) ? val[0] : val
  if (v === undefined || v === null) {
    // Param removed (e.g. back navigation) while the dialog was opened by it.
    if (old !== undefined && dialogOpen.value) dialogOpen.value = false
    return
  }
  const mode = MODES.includes(v as ProjectSetupMode) ? (v as ProjectSetupMode) : null
  if (v === '0' || v === 'false') return
  openDialog(mode)
}, { immediate: true })

function onCreated(p: Row) {
  selectProject(p)
  load()
}

// ── Row actions ──────────────────────────────────────────────────────────
function selectProject(p: Row) {
  const project = { ...p } as any
  sessionStore.setSelectedProject(project)
  try {
    localStorage.setItem('selectedProjectId', p.id)
    localStorage.setItem('selectedProjectObj', JSON.stringify(project))
  } catch { /* private mode */ }
}

function openProject(p: Row) {
  menuFor.value = null
  sessionStore.syncSelectedProjectForSession(p as any)
  router.push(`/projects/${p.id}`)
}

/** Opens the agents page with Create Session prefilled for this project. */
function startSession(p: Row) {
  selectProject(p)
  router.push({ path: '/agents', query: { project: p.id, new_session: '1' } })
}

const menuFor = ref<string | null>(null)
function toggleMenu(id: string) {
  menuFor.value = menuFor.value === id ? null : id
  if (menuFor.value) nextTick(() => (document.querySelector(`[data-project-id="${id}"] .pp-menu__item`) as HTMLElement | null)?.focus())
}
function onDocClick() { menuFor.value = null }
function onDocKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && menuFor.value) menuFor.value = null
}

const editing = ref<Row | null>(null)
function editProject(p: Row) {
  menuFor.value = null
  editing.value = p
}
function onSaved(updated: Row) {
  editing.value = null
  if (!updated) return
  projects.value = projects.value.map(p => p.id === updated.id ? { ...p, ...updated } : p)
  if (sessionStore.selectedProject?.id === updated.id) {
    sessionStore.selectedProject = { ...sessionStore.selectedProject, ...updated } as any
    try { localStorage.setItem('selectedProjectObj', JSON.stringify(sessionStore.selectedProject)) } catch { /* ignore */ }
  }
  showToast('Project saved')
}

async function copyPath(p: Row) {
  menuFor.value = null
  try {
    await copyText(p.path)
    showToast('Path copied')
  } catch {
    showToast(p.path)
  }
}

async function reveal(p: Row) {
  menuFor.value = null
  try { await desktopRevealFolder(p.path) } catch (err) { showToast(`Could not open the folder: ${err instanceof Error ? err.message : err}`) }
}

const removing = ref<Row | null>(null)
const removeBusy = ref(false)
const removeError = ref<string | null>(null)
const removeCancel = ref<HTMLButtonElement | null>(null)
function askRemove(p: Row) {
  menuFor.value = null
  removing.value = p
  removeError.value = null
  nextTick(() => removeCancel.value?.focus())
}
async function confirmRemove() {
  const p = removing.value
  if (!p) return
  removeBusy.value = true
  removeError.value = null
  try {
    const res = await fetchWithAuth(`/api/projects/${p.id}`, { method: 'DELETE' })
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || 'Could not remove the project')
    projects.value = projects.value.filter(x => x.id !== p.id)
    if (sessionStore.selectedProject?.id === p.id) sessionStore.setSelectedProject(null)
    removing.value = null
    showToast(`Removed ${p.name}. Its files are untouched.`)
  } catch (err) {
    removeError.value = err instanceof Error ? err.message : 'Could not remove the project'
  } finally {
    removeBusy.value = false
  }
}

const toast = ref<string | null>(null)
let toastTimer: ReturnType<typeof setTimeout> | null = null
function showToast(msg: string) {
  toast.value = msg
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value = null }, 2600)
}

let refreshTimer: ReturnType<typeof setInterval> | null = null
function onVisible() { if (document.visibilityState === 'visible') loadGit(loadSeq) }

onMounted(() => {
  canReveal.value = canRevealFolder()
  load()
  document.addEventListener('click', onDocClick)
  document.addEventListener('keydown', onDocKey)
  document.addEventListener('visibilitychange', onVisible)
  // Keep running counts fresh without hammering git (cached server-side).
  refreshTimer = setInterval(() => { if (document.visibilityState === 'visible' && !dialogOpen.value) loadGit(loadSeq) }, 20000)
})
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick)
  document.removeEventListener('keydown', onDocKey)
  document.removeEventListener('visibilitychange', onVisible)
  if (refreshTimer) clearInterval(refreshTimer)
  if (toastTimer) clearTimeout(toastTimer)
})
</script>

<style scoped>
.pp-page {
  height: 100%;
  width: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  background: var(--bg-primary);
}
.pp-shell {
  max-width: 980px;
  margin: 0 auto;
  padding: 40px 40px 96px;
}

.pp-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 24px;
}
.pp-header h1 {
  margin: 0 0 4px;
  font-size: 1.75rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--text-primary);
}
.pp-header p { margin: 0; font-size: 0.9rem; color: var(--text-secondary); }

/* Buttons */
.pp-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  padding: 9px 16px;
  font: inherit;
  font-size: 0.86rem;
  font-weight: 600;
  border-radius: 9px;
  border: 1px solid transparent;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.12s, border-color 0.12s, box-shadow 0.12s, opacity 0.12s;
}
.pp-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.pp-btn:focus-visible { outline: none; box-shadow: 0 0 0 3px rgba(var(--accent-purple-rgb, 138, 108, 255), 0.25); }
.pp-btn--primary { background: var(--accent-purple); color: var(--bg-primary); box-shadow: 0 2px 10px rgba(var(--accent-purple-rgb, 138, 108, 255), 0.25); }
.pp-btn--primary:hover { background: var(--accent-purple-hover, var(--accent-purple)); }
.pp-btn--secondary { background: var(--card-bg); color: var(--text-primary); border-color: var(--border-color); }
.pp-btn--secondary:hover:not(:disabled) { border-color: var(--primary-border-hover, var(--accent-purple)); color: var(--accent-purple); }
.pp-btn--ghost { background: transparent; color: var(--accent-purple); }
.pp-btn--danger { background: var(--status-error); color: #fff; }
.pp-btn--sm { padding: 6px 11px; font-size: 0.8rem; border-radius: 8px; }

/* Toolbar */
.pp-toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 10px; }
.pp-search { position: relative; flex: 1; min-width: 0; }
.pp-search__icon { position: absolute; left: 12px; top: 50%; transform: translateY(-50%); color: var(--text-muted, var(--text-secondary)); pointer-events: none; }
.pp-search__input {
  width: 100%;
  padding: 9px 34px 9px 36px;
  font: inherit;
  font-size: 0.88rem;
  color: var(--text-primary);
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 9px;
}
.pp-search__input::-webkit-search-cancel-button { display: none; }
.pp-search__input:focus { outline: none; border-color: var(--accent-purple); box-shadow: 0 0 0 3px rgba(var(--accent-purple-rgb, 138, 108, 255), 0.15); }
.pp-search__input::placeholder { color: var(--text-muted, var(--text-secondary)); }
.pp-search__clear { position: absolute; right: 8px; top: 50%; transform: translateY(-50%); display: inline-flex; padding: 4px; border: none; border-radius: 6px; background: transparent; color: var(--text-secondary); cursor: pointer; }
.pp-search__clear:hover { background: var(--overlay-bg-hover, rgba(127, 127, 127, 0.12)); }

.pp-sort { display: inline-flex; padding: 3px; gap: 2px; border: 1px solid var(--border-color); border-radius: 9px; background: var(--card-bg); }
.pp-sort__btn { padding: 5px 12px; font: inherit; font-size: 0.8rem; font-weight: 600; color: var(--text-secondary); background: transparent; border: none; border-radius: 6px; cursor: pointer; }
.pp-sort__btn--on { color: var(--text-primary); background: var(--overlay-bg-active, rgba(127, 127, 127, 0.14)); }
.pp-sort__btn:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 1px; }

.pp-count { margin: 0 2px 10px; font-size: 0.78rem; color: var(--text-secondary); }

/* List */
.pp-list {
  margin: 0;
  padding: 0;
  list-style: none;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 12px;
}
.pp-row {
  position: relative;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 14px 0 0;
}
/* Skip layout/paint of off-screen rows (long lists); not while a menu is open,
   because paint containment would clip it. */
.pp-row:not(.pp-row--menu) {
  content-visibility: auto;
  contain-intrinsic-size: auto 76px;
}
.pp-row + .pp-row { border-top: 1px solid var(--border-color); }
.pp-row:first-child { border-radius: 12px 12px 0 0; }
.pp-row:last-child { border-radius: 0 0 12px 12px; }
.pp-row:only-child { border-radius: 12px; }
.pp-row:hover { background: var(--card-hover, var(--overlay-bg-hover)); }
.pp-row--current::before {
  content: '';
  position: absolute;
  left: 0;
  top: 12px;
  bottom: 12px;
  width: 3px;
  border-radius: 0 3px 3px 0;
  background: var(--project-color);
}
.pp-row__main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px 0 14px 18px;
  font: inherit;
  text-align: left;
  color: inherit;
  background: none;
  border: none;
  cursor: pointer;
}
.pp-row__main:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: -2px; border-radius: 10px; }

.pp-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  flex-shrink: 0;
  border-radius: 10px;
  font-size: 0.8rem;
  font-weight: 700;
  letter-spacing: 0.02em;
  color: var(--project-color);
  background: color-mix(in srgb, var(--project-color) 14%, transparent);
  border: 1px solid color-mix(in srgb, var(--project-color) 30%, transparent);
}
.pp-row--missing .pp-avatar { filter: grayscale(0.8); opacity: 0.7; }

.pp-row__text { min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.pp-row__title { display: flex; align-items: center; gap: 8px; min-width: 0; }
.pp-row__name { font-size: 0.95rem; font-weight: 600; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pp-row__path { font-family: var(--font-mono, ui-monospace, monospace); font-size: 0.76rem; color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: ltr; }
.pp-row__meta { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 14px; margin-top: 2px; font-size: 0.76rem; color: var(--text-secondary); }
.pp-meta { display: inline-flex; align-items: center; gap: 5px; }
.pp-meta--branch { font-family: var(--font-mono, ui-monospace, monospace); color: var(--text-primary); max-width: 220px; }
.pp-meta--branch > span:first-of-type { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pp-dirty { color: var(--status-warning); font-size: 0.6rem; }
.pp-meta--running { color: var(--status-success); font-weight: 600; }
.pp-meta--quiet { color: var(--text-muted, var(--text-secondary)); opacity: 0.85; }
.pp-pulse { width: 7px; height: 7px; border-radius: 50%; background: var(--status-success); box-shadow: 0 0 0 0 color-mix(in srgb, var(--status-success) 60%, transparent); animation: pp-pulse 1.8s infinite; }
@keyframes pp-pulse { 70% { box-shadow: 0 0 0 6px transparent; } 100% { box-shadow: 0 0 0 0 transparent; } }
@media (prefers-reduced-motion: reduce) { .pp-pulse { animation: none; } }

.pp-chip { flex-shrink: 0; padding: 1px 7px; font-size: 0.68rem; font-weight: 600; border-radius: 999px; }
.pp-chip--current { color: var(--project-color); background: color-mix(in srgb, var(--project-color) 13%, transparent); }
.pp-chip--warn { color: var(--status-warning); background: color-mix(in srgb, var(--status-warning) 14%, transparent); }

.pp-row__actions { display: flex; align-items: center; gap: 6px; flex-shrink: 0; }
.pp-icon-btn { display: inline-flex; align-items: center; justify-content: center; width: 32px; height: 32px; border: 1px solid transparent; border-radius: 8px; background: transparent; color: var(--text-secondary); cursor: pointer; }
.pp-icon-btn:hover, .pp-icon-btn[aria-expanded="true"] { background: var(--overlay-bg-hover, rgba(127, 127, 127, 0.12)); color: var(--text-primary); }
.pp-icon-btn:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 1px; }

.pp-menu { position: relative; }
.pp-menu__list {
  position: absolute;
  right: 0;
  top: calc(100% + 4px);
  z-index: 20;
  min-width: 210px;
  padding: 5px;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 10px;
  box-shadow: 0 12px 32px var(--shadow-color, rgba(0, 0, 0, 0.25));
}
.pp-menu__item { display: flex; align-items: center; gap: 9px; width: 100%; padding: 7px 10px; font: inherit; font-size: 0.83rem; text-align: left; color: var(--text-primary); background: none; border: none; border-radius: 7px; cursor: pointer; }
.pp-menu__item:hover:not(:disabled), .pp-menu__item:focus-visible { outline: none; background: var(--overlay-bg-hover, rgba(127, 127, 127, 0.12)); }
.pp-menu__item:disabled { opacity: 0.45; cursor: not-allowed; }
.pp-menu__item--danger { color: var(--status-error); }
.pp-menu__sep { margin: 4px 6px; border: none; border-top: 1px solid var(--border-color); }

/* Skeleton */
.pp-row--skeleton { padding: 18px; gap: 14px; }
.pp-row--skeleton span { display: block; height: 12px; border-radius: 6px; background: var(--overlay-bg-active, rgba(127, 127, 127, 0.14)); animation: pp-shimmer 1.2s ease-in-out infinite alternate; }
.pp-row--skeleton span:first-child { width: 38px; height: 38px; border-radius: 10px; }
.pp-row--skeleton span:last-child { width: 40%; }
@keyframes pp-shimmer { to { opacity: 0.45; } }

/* Messages */
.pp-message { display: flex; align-items: center; justify-content: center; gap: 10px; padding: 36px 16px; font-size: 0.9rem; color: var(--text-secondary); background: var(--card-bg); border: 1px dashed var(--border-color); border-radius: 12px; }
.pp-message--error { color: var(--status-error); border-style: solid; }

/* Empty state */
.pp-empty { padding: 48px 24px 40px; text-align: center; background: var(--card-bg); border: 1px solid var(--border-color); border-radius: 14px; }
.pp-empty__art { display: inline-flex; align-items: center; justify-content: center; width: 60px; height: 60px; margin-bottom: 14px; border-radius: 16px; color: var(--accent-purple); background: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.1); }
.pp-empty h2 { margin: 0; font-size: 1.2rem; font-weight: 650; color: var(--text-primary); }
.pp-empty > p { margin: 6px auto 26px; max-width: 420px; font-size: 0.9rem; color: var(--text-secondary); }
.pp-empty__options { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; max-width: 720px; margin: 0 auto; }
.pp-start { display: flex; flex-direction: column; align-items: flex-start; gap: 4px; padding: 18px; font: inherit; text-align: left; color: inherit; background: var(--bg-primary); border: 1px solid var(--border-color); border-radius: 12px; cursor: pointer; transition: border-color 0.12s, transform 0.12s, box-shadow 0.12s; }
.pp-start:hover { border-color: var(--primary-border-hover, var(--accent-purple)); transform: translateY(-1px); box-shadow: 0 6px 18px var(--shadow-color-light, rgba(0, 0, 0, 0.08)); }
.pp-start:focus-visible { outline: none; border-color: var(--accent-purple); box-shadow: 0 0 0 3px rgba(var(--accent-purple-rgb, 138, 108, 255), 0.2); }
.pp-start__icon { display: inline-flex; align-items: center; justify-content: center; width: 38px; height: 38px; margin-bottom: 8px; border-radius: 10px; color: var(--accent-purple); background: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.1); }
.pp-start__title { font-size: 0.92rem; font-weight: 600; color: var(--text-primary); }
.pp-start__desc { font-size: 0.8rem; color: var(--text-secondary); }

/* Confirm */
.pp-overlay { position: fixed; inset: 0; z-index: 1000; display: flex; align-items: flex-start; justify-content: center; padding: 14vh 16px 16px; background: rgba(0, 0, 0, 0.45); backdrop-filter: blur(3px); }
.pp-confirm { width: 100%; max-width: 440px; padding: 22px; background: var(--card-bg); color: var(--text-primary); border: 1px solid var(--border-color); border-radius: 14px; box-shadow: 0 24px 64px var(--shadow-color, rgba(0, 0, 0, 0.3)); }
.pp-confirm h2 { margin: 0 0 8px; font-size: 1.05rem; font-weight: 650; }
.pp-confirm p { margin: 0; font-size: 0.86rem; line-height: 1.5; color: var(--text-secondary); }
.pp-confirm code { font-family: var(--font-mono, monospace); font-size: 0.9em; color: var(--text-primary); word-break: break-all; }
.pp-confirm__error { margin-top: 10px !important; color: var(--status-error) !important; }
.pp-confirm__actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 20px; }

.pp-toast { position: fixed; left: 50%; bottom: 28px; z-index: 1100; transform: translate(-50%, 12px); max-width: min(90vw, 520px); padding: 9px 16px; font-size: 0.84rem; color: var(--bg-primary); background: var(--text-primary); border-radius: 9px; box-shadow: 0 8px 24px var(--shadow-color, rgba(0, 0, 0, 0.25)); opacity: 0; pointer-events: none; transition: opacity 0.16s, transform 0.16s; }
.pp-toast--on { opacity: 1; transform: translate(-50%, 0); }

@media (max-width: 720px) {
  .pp-shell { padding: 24px 16px 80px; }
  .pp-header { align-items: flex-start; flex-direction: column; }
  .pp-new { align-self: stretch; }
  .pp-empty__options { grid-template-columns: 1fr; }
  .pp-row { flex-wrap: wrap; padding: 0 12px 12px 0; }
  .pp-row__main { flex-basis: 100%; padding-bottom: 6px; }
  .pp-row__actions { margin-left: 70px; }
}
</style>
