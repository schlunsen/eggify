<template>
  <div v-if="open" class="np-overlay" @mousedown.self="requestClose">
    <div
      ref="dialogEl"
      class="np-dialog"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="titleId"
      :aria-describedby="subtitleId"
      @keydown="onDialogKeydown"
    >
      <header class="np-header">
        <button
          v-if="step === 'form' && !jobRunning"
          type="button"
          class="np-back"
          aria-label="Back to all options"
          @click="goChoose"
        >
          <ProjectsGlyph name="arrowLeft" />
        </button>
        <div class="np-header__text">
          <h2 :id="titleId" class="np-title">{{ headerTitle }}</h2>
          <p :id="subtitleId" class="np-subtitle">{{ headerSubtitle }}</p>
        </div>
        <button type="button" class="np-close" aria-label="Close" :disabled="jobRunning" @click="requestClose">
          <ProjectsGlyph name="x" />
        </button>
      </header>

      <!-- Step 1: pick a starting point -->
      <div v-if="step === 'choose'" class="np-body">
        <div class="np-options" role="list">
          <button
            v-for="(opt, i) in options"
            :key="opt.mode"
            :ref="el => { optionEls[i] = el as HTMLButtonElement }"
            type="button"
            role="listitem"
            class="np-option"
            :data-mode="opt.mode"
            @click="chooseMode(opt.mode)"
            @keydown.down.prevent="focusOption(i + 1)"
            @keydown.up.prevent="focusOption(i - 1)"
          >
            <span class="np-option__icon"><ProjectsGlyph :name="opt.icon" :size="20" /></span>
            <span class="np-option__text">
              <span class="np-option__title">{{ opt.title }}</span>
              <span class="np-option__desc">{{ opt.desc }}</span>
            </span>
            <ProjectsGlyph class="np-option__chevron" name="chevronRight" />
          </button>
        </div>
        <p v-if="defaults" class="np-foot-note">
          New folders go in <code>{{ defaults.projects_dir_display }}</code> by default. Change it in
          <a href="/settings#git" @click.prevent="emit('navigate', '/settings#git')">Settings → Git</a>.
        </p>
      </div>

      <!-- Step 2: the form for the chosen mode -->
      <form v-else-if="step === 'form'" class="np-body" novalidate @submit.prevent="submit">
        <!-- Existing folder -->
        <template v-if="mode === 'existing'">
          <div class="np-field">
            <label :for="ids.path" class="np-label">Folder</label>
            <div class="np-path-row">
              <div class="np-combo">
                <input
                  :id="ids.path"
                  ref="firstInput"
                  v-model="form.path"
                  type="text"
                  class="np-input np-input--mono"
                  :placeholder="pathPlaceholder"
                  autocomplete="off"
                  spellcheck="false"
                  role="combobox"
                  :aria-expanded="showSuggestions"
                  :aria-controls="ids.suggest"
                  :aria-activedescendant="activeSuggestion >= 0 ? `${ids.suggest}-${activeSuggestion}` : undefined"
                  :aria-invalid="showErrors && !!pathError"
                  :aria-describedby="`${ids.path}-status`"
                  @input="onPathInput"
                  @focus="onPathInput"
                  @blur="hideSuggestionsSoon"
                  @keydown="onPathKeydown"
                >
                <ul v-if="showSuggestions" :id="ids.suggest" class="np-suggest" role="listbox" aria-label="Matching folders">
                  <li
                    v-for="(s, i) in suggestions"
                    :id="`${ids.suggest}-${i}`"
                    :key="s.path"
                    role="option"
                    class="np-suggest__item"
                    :class="{ 'np-suggest__item--active': i === activeSuggestion }"
                    :aria-selected="i === activeSuggestion"
                    @mousedown.prevent="acceptSuggestion(s)"
                  >
                    <ProjectsGlyph name="folder" :size="14" />
                    <span>{{ s.display }}</span>
                  </li>
                </ul>
              </div>
              <button v-if="desktop" type="button" class="np-btn np-btn--secondary" @click="pickFolder('path')">
                <ProjectsGlyph name="folderOpen" /> Choose…
              </button>
            </div>
            <p :id="`${ids.path}-status`" class="np-status" :class="statusClass(existingStatus)" aria-live="polite">
              <template v-if="existingStatus">
                <ProjectsGlyph :name="statusIcon(existingStatus.kind)" :size="14" />
                <span>{{ existingStatus.text }}</span>
                <button
                  v-if="existingStatus.project"
                  type="button"
                  class="np-link"
                  @click="emit('open-existing', existingStatus.project)"
                >Open it</button>
              </template>
              <template v-else>
                {{ desktop ? 'Choose a folder, or type its path.' : 'Type the full path, e.g. ~/code/my-app. Suggestions appear as you type.' }}
              </template>
            </p>
          </div>
        </template>

        <!-- Clone -->
        <template v-else-if="mode === 'clone'">
          <div class="np-field">
            <label :for="ids.source" class="np-label">Repository</label>
            <input
              :id="ids.source"
              ref="firstInput"
              v-model="form.source"
              type="text"
              class="np-input np-input--mono"
              placeholder="https://github.com/owner/repo or owner/repo"
              autocomplete="off"
              spellcheck="false"
              :disabled="jobRunning"
              :aria-invalid="showErrors && !!sourceError"
              :aria-describedby="`${ids.source}-hint`"
              @input="onSourceInput"
            >
            <p :id="`${ids.source}-hint`" class="np-status" :class="{ 'np-status--error': showErrors && !!sourceError }">
              <template v-if="showErrors && sourceError">
                <ProjectsGlyph name="alert" :size="14" /><span>{{ sourceError }}</span>
              </template>
              <template v-else>
                HTTPS URLs and GitHub <code>owner/repo</code> work.
                <a href="/git" class="np-link" @click.prevent="emit('navigate', '/git')">Search GitHub…</a>
              </template>
            </p>
          </div>
        </template>

        <!-- Empty -->
        <template v-else-if="mode === 'empty'">
          <div class="np-field">
            <label :for="ids.name" class="np-label">Project name</label>
            <input
              :id="ids.name"
              ref="firstInput"
              v-model="form.name"
              type="text"
              class="np-input"
              placeholder="My new project"
              autocomplete="off"
              :aria-invalid="showErrors && !!nameError"
              @input="onEmptyNameInput"
            >
            <p v-if="showErrors && nameError" class="np-status np-status--error"><ProjectsGlyph name="alert" :size="14" /><span>{{ nameError }}</span></p>
          </div>
        </template>

        <!-- Destination (clone + empty) -->
        <div v-if="mode === 'clone' || mode === 'empty'" class="np-field">
          <span class="np-label" :id="`${ids.parent}-label`">Location</span>
          <div class="np-dest" :aria-labelledby="`${ids.parent}-label`" role="group">
            <input
              :id="ids.parent"
              v-model="form.parent"
              type="text"
              class="np-input np-input--mono np-dest__parent"
              aria-label="Parent folder"
              spellcheck="false"
              autocomplete="off"
              :disabled="jobRunning"
              @input="scheduleTargetInspect"
            >
            <span class="np-dest__sep" aria-hidden="true">/</span>
            <input
              :id="ids.folder"
              v-model="form.folder"
              type="text"
              class="np-input np-input--mono np-dest__folder"
              aria-label="Folder name"
              spellcheck="false"
              autocomplete="off"
              :placeholder="mode === 'clone' ? 'repo' : 'folder'"
              :disabled="jobRunning"
              @input="onFolderInput"
            >
            <button v-if="desktop" type="button" class="np-btn np-btn--ghost" :disabled="jobRunning" title="Choose the parent folder" @click="pickFolder('parent')">
              <ProjectsGlyph name="folderOpen" />
              <span class="sr-only">Choose the parent folder</span>
            </button>
          </div>
          <p class="np-status" :class="statusClass(targetStatus)" aria-live="polite">
            <template v-if="targetStatus">
              <ProjectsGlyph :name="statusIcon(targetStatus.kind)" :size="14" />
              <span>{{ targetStatus.text }}</span>
              <button v-if="targetStatus.project" type="button" class="np-link" @click="emit('open-existing', targetStatus.project)">Open it</button>
            </template>
          </p>
        </div>

        <!-- Name (existing + clone; empty has it first) -->
        <div v-if="mode !== 'empty'" class="np-field">
          <label :for="ids.name" class="np-label">Project name</label>
          <input
            :id="ids.name"
            v-model="form.name"
            type="text"
            class="np-input"
            :placeholder="namePlaceholder"
            autocomplete="off"
            :disabled="jobRunning"
            :aria-invalid="showErrors && !!nameError"
            @input="nameTouched = true"
          >
          <p v-if="showErrors && nameError" class="np-status np-status--error"><ProjectsGlyph name="alert" :size="14" /><span>{{ nameError }}</span></p>
        </div>

        <div class="np-field np-field--inline">
          <span class="np-label" :id="ids.color">Color</span>
          <div class="np-swatches" role="radiogroup" :aria-labelledby="ids.color">
            <button
              v-for="c in colors"
              :key="c"
              type="button"
              role="radio"
              class="np-swatch"
              :class="{ 'np-swatch--on': form.color === c }"
              :style="{ '--swatch': c }"
              :aria-checked="form.color === c"
              :aria-label="`Color ${c}`"
              :tabindex="form.color === c ? 0 : -1"
              :disabled="jobRunning"
              @click="form.color = c"
              @keydown.right.prevent="moveColor(1)"
              @keydown.left.prevent="moveColor(-1)"
            />
          </div>
        </div>

        <label v-if="mode === 'empty'" class="np-check">
          <input v-model="form.gitInit" type="checkbox" :disabled="defaults ? !defaults.git_available : false">
          <span>Initialize a git repository</span>
        </label>
        <label v-if="mode === 'clone'" class="np-check">
          <input v-model="form.shallow" type="checkbox" :disabled="jobRunning">
          <span>Shallow clone <span class="np-muted">(latest commit only, faster for big repos)</span></span>
        </label>

        <!-- Clone progress -->
        <div v-if="job" class="np-progress" :class="`np-progress--${job.status}`" role="status" aria-live="polite">
          <div class="np-progress__head">
            <span>{{ jobLabel }}</span>
            <span v-if="job.status === 'running'" class="np-muted">{{ job.percent }}%</span>
          </div>
          <div class="np-progress__bar" role="progressbar" :aria-valuenow="job.percent" aria-valuemin="0" aria-valuemax="100" :aria-label="jobLabel">
            <span :style="{ width: `${Math.max(job.percent, job.status === 'running' ? 3 : 0)}%` }" />
          </div>
          <pre v-if="job.error || (job.output && job.output.length)" class="np-progress__log">{{ job.error || job.output.slice(-4).join('\n') }}</pre>
        </div>

        <div v-if="error" class="np-error" role="alert">
          <ProjectsGlyph name="alert" :size="16" />
          <span>{{ error }}</span>
          <button v-if="errorProject" type="button" class="np-link" @click="emit('open-existing', errorProject)">Open the existing project</button>
        </div>

        <footer class="np-footer">
          <span class="np-footer__hint" aria-hidden="true"><kbd>↵</kbd> to {{ submitVerb.toLowerCase() }} · <kbd>esc</kbd> to close</span>
          <button v-if="jobRunning" type="button" class="np-btn np-btn--secondary" @click="cancelJob">Cancel clone</button>
          <button v-else type="button" class="np-btn np-btn--secondary" @click="requestClose">Cancel</button>
          <button type="submit" class="np-btn np-btn--primary" :disabled="submitting || jobRunning">
            {{ submitting || jobRunning ? busyVerb : existingProject ? 'Open project' : submitVerb }}
          </button>
        </footer>
      </form>

      <!-- Step 3: done -->
      <div v-else-if="step === 'done' && created" class="np-body np-done">
        <div class="np-done__badge" :style="{ '--swatch': created.color || 'var(--accent-purple)' }">
          <ProjectsGlyph name="check" :size="22" />
        </div>
        <p class="np-done__name">{{ created.name }}</p>
        <p class="np-done__path"><code>{{ shorten(created.path) }}</code></p>
        <p v-if="warning" class="np-status np-status--warn"><ProjectsGlyph name="alert" :size="14" /><span>{{ warning }}</span></p>
        <div class="np-done__actions">
          <button ref="doneButton" type="button" class="np-btn np-btn--primary" @click="emit('start-session', created)">
            <ProjectsGlyph name="play" :size="14" /> Start a session here
          </button>
          <button type="button" class="np-btn np-btn--secondary" @click="emit('open-project', created)">Open project</button>
          <button type="button" class="np-btn np-btn--ghost" @click="emit('close')">Done</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import ProjectsGlyph from './ProjectsGlyph.vue'
import { desktopSelectFolder, isDesktop } from '~/utils/desktop'
import { setHomeDir } from '~/utils/homeDir'
import { announceUserRequired, isUserRequiredBody } from '~/utils/userRequired'
import {
  PROJECT_COLORS, folderNameError, folderNameFromPath, folderNameFromProjectName, guessRepo,
  joinPath, PROJECT_START_OPTIONS, randomProjectColor, shortenPath, type ProjectSetupMode,
} from '~/utils/projects/projectSetup'

interface MiniProject { id: string, name: string, path: string, color?: string }
interface Inspect {
  path: string
  display_path: string
  suggested_name: string
  exists: boolean
  is_dir: boolean
  writable?: boolean
  empty?: boolean
  readable?: boolean
  parent_exists?: boolean
  can_create?: boolean
  create_error?: string
  git?: { is_repo: boolean, branch?: string, remote?: string, dirty?: boolean }
  existing_project?: MiniProject
}
interface Status { kind: 'ok' | 'info' | 'warn' | 'error' | 'pending', text: string, project?: MiniProject }
interface CloneJob { id: string, status: 'running' | 'done' | 'error' | 'cancelled', stage: string, percent: number, output: string[], error?: string, project?: any }

const props = withDefaults(defineProps<{ open: boolean, initialMode?: ProjectSetupMode | null }>(), { initialMode: null })
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'created', project: any): void
  (e: 'start-session', project: any): void
  (e: 'open-project', project: any): void
  (e: 'open-existing', project: MiniProject): void
  (e: 'navigate', path: string): void
}>()

const { fetchWithAuth } = useAuthenticatedFetch()
const desktop = ref(false)
const colors = PROJECT_COLORS

const uid = Math.random().toString(36).slice(2, 8)
const titleId = `np-title-${uid}`
const subtitleId = `np-sub-${uid}`
const ids = {
  path: `np-path-${uid}`, suggest: `np-suggest-${uid}`, source: `np-source-${uid}`, name: `np-name-${uid}`,
  parent: `np-parent-${uid}`, folder: `np-folder-${uid}`, color: `np-color-${uid}`,
}

const options = PROJECT_START_OPTIONS

const step = ref<'choose' | 'form' | 'done'>('choose')
const mode = ref<ProjectSetupMode>('existing')
const defaults = ref<{ home: string, projects_dir: string, projects_dir_display: string, git_available: boolean } | null>(null)

const form = reactive({
  name: '', path: '', source: '', parent: '~/projects', folder: '',
  color: randomProjectColor(), gitInit: true, shallow: false,
})
const nameTouched = ref(false)
const folderTouched = ref(false)
const showErrors = ref(false)

const submitting = ref(false)
const error = ref<string | null>(null)
const errorProject = ref<MiniProject | null>(null)
const job = ref<CloneJob | null>(null)
const created = ref<any | null>(null)
const warning = ref<string | null>(null)

const dialogEl = ref<HTMLElement | null>(null)
const firstInput = ref<HTMLInputElement | null>(null)
const doneButton = ref<HTMLButtonElement | null>(null)
const optionEls = ref<(HTMLButtonElement | null)[]>([])
let restoreFocusTo: HTMLElement | null = null

const jobRunning = computed(() => job.value?.status === 'running')

// ── Header copy ──────────────────────────────────────────────────────────
const headerTitle = computed(() => {
  if (step.value === 'done') return 'Project ready'
  if (step.value === 'choose') return 'New project'
  return options.find(o => o.mode === mode.value)!.title
})
const headerSubtitle = computed(() => {
  if (step.value === 'done') return 'Start working in it right away, or open it later from Projects.'
  if (step.value === 'choose') return 'A project is a folder your agents work in. How do you want to start?'
  if (mode.value === 'existing') return 'Eggify keeps your files where they are.'
  if (mode.value === 'clone') return 'The repository is cloned into a new folder.'
  return 'Eggify creates the folder for you.'
})
const submitVerb = computed(() => mode.value === 'existing' ? 'Add project' : mode.value === 'clone' ? 'Clone' : 'Create project')
const busyVerb = computed(() => mode.value === 'clone' ? 'Cloning…' : mode.value === 'existing' ? 'Adding…' : 'Creating…')
const pathPlaceholder = computed(() => `${defaults.value?.projects_dir_display || '~/projects'}/my-app`)
const namePlaceholder = computed(() => mode.value === 'clone' ? (guessRepo(form.source)?.name || 'Repository name') : (folderNameFromPath(form.path) || 'My project'))

const shorten = (p: string) => shortenPath(p, defaults.value?.home)

// ── Validation ───────────────────────────────────────────────────────────
const nameError = computed(() => {
  const n = form.name.trim()
  if (!n) return 'Give the project a name'
  if (n.length > 100) return 'Keep the name under 100 characters'
  return null
})
const sourceError = computed(() => {
  if (!form.source.trim()) return 'Enter a repository URL or owner/repo'
  if (!guessRepo(form.source)) return 'That does not look like a repository URL'
  if (/^(http|ssh|file|git):\/\//i.test(form.source.trim())) return 'Only HTTPS URLs can be cloned'
  return null
})
const folderError = computed(() => folderNameError(form.folder.trim()))

// ── Inspecting paths ─────────────────────────────────────────────────────
const existingInspect = ref<Inspect | null>(null)
const existingInspecting = ref(false)
const targetInspect = ref<Inspect | null>(null)
let existingTimer: ReturnType<typeof setTimeout> | null = null
let targetTimer: ReturnType<typeof setTimeout> | null = null
let inspectSeq = 0

async function inspect(path: string): Promise<Inspect | null> {
  try {
    const res = await fetchWithAuth(`/api/projects/setup/inspect?path=${encodeURIComponent(path)}`)
    const body = await res.json().catch(() => ({}))
    if (res.status === 403 && isUserRequiredBody(body)) {
      error.value = 'Sign in to Eggify to create projects.'
      return null
    }
    if (!res.ok) return { error: body.error } as any
    return body as Inspect
  } catch {
    return null
  }
}

function scheduleExistingInspect() {
  if (existingTimer) clearTimeout(existingTimer)
  const path = form.path.trim()
  if (!path) { existingInspect.value = null; return }
  existingInspecting.value = true
  const seq = ++inspectSeq
  existingTimer = setTimeout(async () => {
    const r = await inspect(path)
    if (seq !== inspectSeq || form.path.trim() !== path) return
    existingInspecting.value = false
    existingInspect.value = r
    if (r && r.exists && r.is_dir && !nameTouched.value && !r.existing_project) {
      form.name = r.suggested_name
    }
  }, 220)
}

const targetPath = computed(() => joinPath(form.parent, form.folder.trim()))
function scheduleTargetInspect() {
  if (targetTimer) clearTimeout(targetTimer)
  targetInspect.value = null
  if (!form.parent.trim() || folderError.value) return
  const path = targetPath.value
  targetTimer = setTimeout(async () => {
    const r = await inspect(path)
    if (targetPath.value === path) targetInspect.value = r
  }, 220)
}

const existingStatus = computed<Status | null>(() => {
  if (!form.path.trim()) return showErrors.value ? { kind: 'error', text: 'Choose a folder' } : null
  const r = existingInspect.value as any
  if (existingInspecting.value && !r) return { kind: 'pending', text: 'Checking…' }
  if (!r) return null
  if (r.error) return { kind: 'error', text: r.error }
  if (r.existing_project) return { kind: 'info', text: `Already a project: ${r.existing_project.name}.`, project: r.existing_project }
  if (!r.exists) return { kind: 'error', text: 'No folder at this path.' }
  if (!r.is_dir) return { kind: 'error', text: 'That is a file, not a folder.' }
  const parts: string[] = []
  if (r.git?.is_repo) {
    parts.push(`Git repository${r.git.branch ? ` on ${r.git.branch}` : ''}`)
    if (r.git.remote) parts.push(remoteLabel(r.git.remote))
    if (r.git.dirty) parts.push('uncommitted changes')
  } else {
    parts.push(r.empty ? 'Empty folder' : 'Folder (not a git repository)')
  }
  if (r.writable === false) return { kind: 'warn', text: `${parts.join(' · ')} · read-only for Eggify` }
  return { kind: 'ok', text: parts.join(' · ') }
})

const targetStatus = computed<Status | null>(() => {
  if (!form.parent.trim()) return { kind: 'error', text: 'Choose where the folder goes' }
  if (form.folder.trim() && folderError.value) return { kind: 'error', text: folderError.value }
  if (!form.folder.trim()) return showErrors.value ? { kind: 'error', text: 'Enter a folder name' } : { kind: 'info', text: mode.value === 'clone' ? 'The folder is named after the repository.' : 'The folder is named after the project.' }
  const r = targetInspect.value as any
  if (!r) return { kind: 'info', text: `Will create ${targetPath.value}` }
  if (r.error) return { kind: 'error', text: r.error }
  if (r.exists) {
    if (r.existing_project) return { kind: 'error', text: `${r.display_path} is already the project ${r.existing_project.name}.`, project: r.existing_project }
    return { kind: 'error', text: `${r.display_path} already exists. Pick another folder name${mode.value === 'clone' ? '' : ', or use "Open an existing folder"'}.` }
  }
  if (r.can_create === false) return { kind: 'error', text: r.create_error || 'Eggify cannot create a folder here.' }
  return { kind: 'ok', text: `Will create ${r.display_path}` }
})

function remoteLabel(remote: string): string {
  const g = guessRepo(remote)
  try {
    const host = remote.includes('://') ? new URL(remote).host : (remote.match(/@([^:]+):/)?.[1] || '')
    return g?.slug ? `${host ? host + '/' : ''}${g.slug}` : remote
  } catch { return remote }
}

const statusIcon = (kind: Status['kind']) => kind === 'ok' ? 'checkCircle' : kind === 'info' || kind === 'pending' ? 'info' : 'alert'
const statusClass = (s: Status | null) => s ? `np-status--${s.kind}` : ''

/** The typed folder is already a project: Enter opens it instead. */
const existingProject = computed(() => mode.value === 'existing' ? existingStatus.value?.project || null : null)

const pathError = computed(() => {
  const s = existingStatus.value
  return s && (s.kind === 'error' || s.project) ? s.text : null
})

const blockingError = computed<string | null>(() => {
  if (mode.value === 'existing') {
    if (!form.path.trim()) return 'Choose a folder'
    if (pathError.value) return pathError.value
    return nameError.value
  }
  if (mode.value === 'clone' && sourceError.value) return sourceError.value
  if (nameError.value) return nameError.value
  const t = targetStatus.value
  if (!form.folder.trim()) return 'Enter a folder name'
  if (t?.kind === 'error') return t.text
  return null
})

// ── Inputs ───────────────────────────────────────────────────────────────
function onSourceInput() {
  const g = guessRepo(form.source)
  if (g) {
    if (!folderTouched.value) form.folder = g.name
    if (!nameTouched.value) form.name = g.name
  }
  scheduleTargetInspect()
}
function onEmptyNameInput() {
  nameTouched.value = true
  if (!folderTouched.value) form.folder = folderNameFromProjectName(form.name)
  scheduleTargetInspect()
}
function onFolderInput() {
  folderTouched.value = form.folder.trim() !== ''
  scheduleTargetInspect()
}

// Autocomplete for the typed path (web; also handy on desktop).
const suggestions = ref<{ path: string, display: string }[]>([])
const activeSuggestion = ref(-1)
const suggestionsOpen = ref(false)
const showSuggestions = computed(() => suggestionsOpen.value && suggestions.value.length > 0)
let suggestTimer: ReturnType<typeof setTimeout> | null = null
let blurTimer: ReturnType<typeof setTimeout> | null = null

function onPathInput() {
  scheduleExistingInspect()
  if (suggestTimer) clearTimeout(suggestTimer)
  const prefix = form.path
  if (!prefix.trim() || !/^[~/]/.test(prefix.trim())) { suggestions.value = []; return }
  suggestTimer = setTimeout(async () => {
    try {
      const res = await fetchWithAuth(`/api/projects/setup/complete?prefix=${encodeURIComponent(prefix)}`)
      if (!res.ok || form.path !== prefix) return
      const body = await res.json()
      suggestions.value = (body.suggestions || []).filter((s: any) => s.display !== prefix.replace(/\/$/, ''))
      activeSuggestion.value = -1
      suggestionsOpen.value = true
    } catch { /* offline: no suggestions */ }
  }, 120)
}
function hideSuggestionsSoon() {
  blurTimer = setTimeout(() => { suggestionsOpen.value = false }, 120)
}
function acceptSuggestion(s: { display: string }) {
  if (blurTimer) clearTimeout(blurTimer)
  form.path = s.display + '/'
  suggestionsOpen.value = false
  nextTick(() => { firstInput.value?.focus(); onPathInput() })
}
function onPathKeydown(e: KeyboardEvent) {
  if (!showSuggestions.value) return
  if (e.key === 'ArrowDown') { e.preventDefault(); activeSuggestion.value = (activeSuggestion.value + 1) % suggestions.value.length }
  else if (e.key === 'ArrowUp') { e.preventDefault(); activeSuggestion.value = activeSuggestion.value <= 0 ? suggestions.value.length - 1 : activeSuggestion.value - 1 }
  else if ((e.key === 'Enter' || e.key === 'Tab') && activeSuggestion.value >= 0) { e.preventDefault(); acceptSuggestion(suggestions.value[activeSuggestion.value]!) }
  else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); suggestionsOpen.value = false }
}

async function pickFolder(target: 'path' | 'parent') {
  try {
    const picked = await desktopSelectFolder()
    if (!picked) return
    if (target === 'path') {
      form.path = shorten(picked)
      scheduleExistingInspect()
    } else {
      form.parent = shorten(picked)
      scheduleTargetInspect()
    }
  } catch (err) {
    error.value = `Could not open the folder picker: ${err instanceof Error ? err.message : String(err)}`
  }
}

function moveColor(delta: number) {
  const i = colors.indexOf(form.color)
  const next = colors[(i + delta + colors.length) % colors.length]!
  form.color = next
  nextTick(() => (dialogEl.value?.querySelector('.np-swatch--on') as HTMLElement | null)?.focus())
}

// ── Steps ────────────────────────────────────────────────────────────────
function resetForm() {
  Object.assign(form, {
    name: '', path: '', source: '', folder: '',
    parent: defaults.value?.projects_dir_display || '~/projects',
    color: randomProjectColor(), gitInit: defaults.value ? defaults.value.git_available : true, shallow: false,
  })
  nameTouched.value = false
  folderTouched.value = false
  showErrors.value = false
  error.value = null
  errorProject.value = null
  job.value = null
  created.value = null
  warning.value = null
  existingInspect.value = null
  targetInspect.value = null
  suggestions.value = []
}

function chooseMode(m: ProjectSetupMode) {
  mode.value = m
  error.value = null
  errorProject.value = null
  showErrors.value = false
  step.value = 'form'
  if (m !== 'existing') scheduleTargetInspect()
  nextTick(() => firstInput.value?.focus())
}
function goChoose() {
  step.value = 'choose'
  error.value = null
  nextTick(() => focusOption(options.findIndex(o => o.mode === mode.value)))
}
function focusOption(i: number) {
  const n = options.length
  optionEls.value[((i % n) + n) % n]?.focus()
}

function requestClose() {
  if (jobRunning.value) return
  emit('close')
}

// ── Submit ───────────────────────────────────────────────────────────────
async function submit() {
  if (submitting.value || jobRunning.value) return
  if (existingProject.value) {
    emit('open-existing', existingProject.value)
    return
  }
  showErrors.value = true
  error.value = null
  errorProject.value = null
  // Make sure the latest typed path has been checked before deciding.
  if (mode.value === 'existing' && form.path.trim() && (!existingInspect.value || existingInspecting.value)) {
    existingInspect.value = await inspect(form.path.trim())
    existingInspecting.value = false
    if (existingInspect.value?.exists && !nameTouched.value && !form.name.trim()) form.name = existingInspect.value.suggested_name
  }
  if (blockingError.value) {
    focusFirstInvalid()
    return
  }
  const body: Record<string, unknown> = { mode: mode.value, name: form.name.trim(), color: form.color }
  if (mode.value === 'existing') body.path = form.path.trim()
  else {
    body.parent = form.parent.trim()
    body.folder = form.folder.trim()
    if (mode.value === 'clone') { body.source = form.source.trim(); if (form.shallow) body.depth = 1 }
    else body.git_init = form.gitInit
  }
  submitting.value = true
  try {
    const res = await fetchWithAuth('/api/projects/setup', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
    })
    const data = await res.json().catch(() => ({}))
    if (res.status === 403 && isUserRequiredBody(data)) {
      announceUserRequired(data.error, data.code)
      error.value = 'Sign in to Eggify to create projects.'
      return
    }
    if (res.status === 409) {
      error.value = data.error || 'This folder is already a project.'
      errorProject.value = data.existing_project || null
      return
    }
    if (!res.ok) {
      error.value = data.error || `Something went wrong (${res.status}).`
      return
    }
    if (res.status === 202 && data.job) {
      job.value = data.job
      pollJob(data.job.id)
      return
    }
    finish(data.project, data.warning)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Network error'
  } finally {
    submitting.value = false
  }
}

function focusFirstInvalid() {
  nextTick(() => {
    const el = dialogEl.value?.querySelector('[aria-invalid="true"]') as HTMLElement | null
    ;(el || firstInput.value)?.focus()
  })
}

let pollTimer: ReturnType<typeof setTimeout> | null = null
async function pollJob(id: string) {
  if (pollTimer) clearTimeout(pollTimer)
  try {
    const res = await fetchWithAuth(`/api/projects/setup/jobs/${id}`)
    if (res.ok) {
      const data = await res.json()
      job.value = data.job
    }
  } catch { /* keep polling */ }
  const j = job.value
  if (!props.open || !j) return
  if (j.status === 'running') {
    pollTimer = setTimeout(() => pollJob(id), 500)
  } else if (j.status === 'done' && j.project) {
    finish(j.project)
  }
}

async function cancelJob() {
  if (!job.value) return
  try { await fetchWithAuth(`/api/projects/setup/jobs/${job.value.id}`, { method: 'DELETE' }) } catch { /* ignore */ }
}

const jobLabel = computed(() => {
  const j = job.value
  if (!j) return ''
  if (j.status === 'error') return 'Clone failed'
  if (j.status === 'cancelled') return 'Clone cancelled'
  if (j.status === 'done') return 'Cloned'
  return j.stage && j.stage !== 'Starting' ? `${j.stage}…` : 'Connecting…'
})

function finish(project: any, warn?: string) {
  created.value = project
  warning.value = warn || null
  step.value = 'done'
  emit('created', project)
  nextTick(() => doneButton.value?.focus())
}

// ── Keyboard & focus ─────────────────────────────────────────────────────
function onDialogKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    e.stopPropagation()
    requestClose()
    return
  }
  if (e.key !== 'Tab' || !dialogEl.value) return
  const focusables = Array.from(dialogEl.value.querySelectorAll<HTMLElement>(
    'button:not([disabled]), input:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])',
  )).filter(el => el.offsetParent !== null || el === document.activeElement)
  if (!focusables.length) return
  const first = focusables[0]!
  const last = focusables[focusables.length - 1]!
  if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
  else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
}

async function loadDefaults() {
  try {
    const res = await fetchWithAuth('/api/projects/setup/defaults')
    const body = await res.json().catch(() => ({}))
    if (res.ok) {
      defaults.value = body
      setHomeDir(body.home)
      if (!form.parent || form.parent === '~/projects') form.parent = body.projects_dir_display
      form.gitInit = !!body.git_available
    } else if (res.status === 403 && isUserRequiredBody(body)) {
      error.value = 'Sign in to Eggify to create projects.'
    }
  } catch { /* defaults stay */ }
}

watch(() => props.open, async (isOpen) => {
  if (isOpen) {
    restoreFocusTo = (typeof document !== 'undefined' ? document.activeElement : null) as HTMLElement | null
    desktop.value = isDesktop()
    resetForm()
    step.value = 'choose'
    await nextTick()
    if (props.initialMode) chooseMode(props.initialMode)
    else focusOption(0)
    await loadDefaults()
    if (step.value === 'form' && mode.value !== 'existing') scheduleTargetInspect()
  } else {
    if (pollTimer) clearTimeout(pollTimer)
    restoreFocusTo?.focus?.()
    restoreFocusTo = null
  }
}, { immediate: true })

onBeforeUnmount(() => {
  for (const t of [existingTimer, targetTimer, suggestTimer, blurTimer, pollTimer]) if (t) clearTimeout(t)
})

defineExpose({ chooseMode, submit })
</script>

<style scoped>
.np-overlay {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 10vh 16px 16px;
  background: rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(3px);
  animation: np-fade 0.14s ease-out;
}

.np-dialog {
  width: 100%;
  max-width: 560px;
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  background: var(--card-bg, var(--bg-secondary));
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: 14px;
  box-shadow: 0 24px 64px var(--shadow-color, rgba(0, 0, 0, 0.3));
  animation: np-rise 0.16s ease-out;
  overflow: hidden;
}

@keyframes np-fade { from { opacity: 0; } }
@keyframes np-rise { from { opacity: 0; transform: translateY(8px) scale(0.99); } }
@media (prefers-reduced-motion: reduce) {
  .np-overlay, .np-dialog { animation: none; }
}

.np-header {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 20px 22px 14px;
}
.np-header__text { flex: 1; min-width: 0; }
.np-title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 650;
  letter-spacing: -0.01em;
}
.np-subtitle {
  margin: 3px 0 0;
  font-size: 0.85rem;
  color: var(--text-secondary);
}
.np-back, .np-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  flex-shrink: 0;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}
.np-back:hover, .np-close:hover:not(:disabled) { background: var(--overlay-bg-hover, rgba(127, 127, 127, 0.12)); color: var(--text-primary); }
.np-close:disabled { opacity: 0.4; cursor: not-allowed; }

.np-body {
  padding: 4px 22px 20px;
  overflow-y: auto;
}

/* Options */
.np-options { display: flex; flex-direction: column; gap: 8px; }
.np-option {
  display: flex;
  align-items: center;
  gap: 14px;
  width: 100%;
  padding: 14px 14px;
  text-align: left;
  font: inherit;
  color: inherit;
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  border-radius: 10px;
  cursor: pointer;
  transition: border-color 0.12s, background 0.12s, transform 0.12s;
}
.np-option:hover { border-color: var(--primary-border-hover, var(--accent-purple)); background: var(--card-hover, var(--bg-primary)); }
.np-option:focus-visible { outline: none; border-color: var(--accent-purple); box-shadow: 0 0 0 3px rgba(var(--accent-purple-rgb, 138, 108, 255), 0.18); }
.np-option__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  flex-shrink: 0;
  border-radius: 10px;
  color: var(--accent-purple);
  background: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.1);
}
.np-option__text { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.np-option__title { font-weight: 600; font-size: 0.95rem; }
.np-option__desc { font-size: 0.82rem; color: var(--text-secondary); }
.np-option__chevron { color: var(--text-muted, var(--text-secondary)); flex-shrink: 0; }

.np-foot-note {
  margin: 14px 2px 0;
  font-size: 0.78rem;
  color: var(--text-secondary);
}
.np-foot-note a { color: var(--accent-purple); }

/* Fields */
.np-field { margin-bottom: 16px; }
.np-field--inline { display: flex; align-items: center; gap: 14px; }
.np-field--inline .np-label { margin: 0; }
.np-label {
  display: block;
  margin-bottom: 6px;
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--text-primary);
}
.np-input {
  width: 100%;
  min-width: 0;
  padding: 9px 12px;
  font: inherit;
  font-size: 0.88rem;
  color: var(--text-primary);
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  transition: border-color 0.12s, box-shadow 0.12s;
}
.np-input:focus { outline: none; border-color: var(--accent-purple); box-shadow: 0 0 0 3px rgba(var(--accent-purple-rgb, 138, 108, 255), 0.15); }
.np-input:disabled { opacity: 0.6; }
.np-input[aria-invalid="true"] { border-color: var(--status-error); }
.np-input::placeholder { color: var(--text-muted, var(--text-secondary)); opacity: 0.8; }
.np-input--mono { font-family: var(--font-mono, ui-monospace, monospace); font-size: 0.82rem; }

.np-path-row { display: flex; gap: 8px; }
.np-combo { position: relative; flex: 1; min-width: 0; }
.np-suggest {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  right: 0;
  z-index: 5;
  margin: 0;
  padding: 4px;
  list-style: none;
  max-height: 220px;
  overflow-y: auto;
  background: var(--card-bg, var(--bg-secondary));
  border: 1px solid var(--border-color);
  border-radius: 8px;
  box-shadow: 0 10px 28px var(--shadow-color, rgba(0, 0, 0, 0.25));
}
.np-suggest__item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border-radius: 6px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.8rem;
  color: var(--text-secondary);
  cursor: pointer;
}
.np-suggest__item--active, .np-suggest__item:hover { background: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.12); color: var(--text-primary); }

.np-dest { display: flex; align-items: center; gap: 6px; }
.np-dest__parent { flex: 1.3; }
.np-dest__folder { flex: 1; }
.np-dest__sep { color: var(--text-muted, var(--text-secondary)); font-family: var(--font-mono, monospace); }

.np-status {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 6px;
  margin: 6px 2px 0;
  min-height: 1.2em;
  font-size: 0.78rem;
  line-height: 1.35;
  color: var(--text-secondary);
}
.np-status :deep(svg) { flex-shrink: 0; margin-top: 1px; }
.np-status--ok { color: var(--status-success); }
.np-status--warn { color: var(--status-warning); }
.np-status--error { color: var(--status-error); }
.np-status code, .np-foot-note code { font-family: var(--font-mono, monospace); font-size: 0.95em; padding: 1px 4px; border-radius: 4px; background: var(--code-bg, rgba(127, 127, 127, 0.12)); color: var(--text-primary); }

.np-link {
  padding: 0;
  border: none;
  background: none;
  font: inherit;
  color: var(--accent-purple);
  text-decoration: underline;
  text-underline-offset: 2px;
  cursor: pointer;
}

.np-swatches { display: flex; flex-wrap: wrap; gap: 8px; }
.np-swatch {
  width: 22px;
  height: 22px;
  padding: 0;
  border-radius: 50%;
  border: 2px solid transparent;
  background: var(--swatch);
  box-shadow: inset 0 0 0 1px rgba(0, 0, 0, 0.12);
  cursor: pointer;
  transition: transform 0.1s;
}
.np-swatch:hover { transform: scale(1.1); }
.np-swatch--on { border-color: var(--card-bg, #fff); box-shadow: 0 0 0 2px var(--swatch); }
.np-swatch:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 3px; }

.np-check {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 2px 0 14px;
  font-size: 0.85rem;
  cursor: pointer;
}
.np-check input { accent-color: var(--accent-purple); width: 15px; height: 15px; }
.np-muted { color: var(--text-secondary); font-weight: 400; }

.np-progress {
  margin: 4px 0 14px;
  padding: 12px;
  border: 1px solid var(--border-color);
  border-radius: 10px;
  background: var(--bg-primary);
}
.np-progress__head { display: flex; justify-content: space-between; font-size: 0.82rem; font-weight: 600; margin-bottom: 8px; }
.np-progress__bar { height: 6px; border-radius: 999px; background: var(--overlay-bg-active, rgba(127, 127, 127, 0.18)); overflow: hidden; }
.np-progress__bar span { display: block; height: 100%; border-radius: inherit; background: var(--accent-purple); transition: width 0.3s ease; }
.np-progress--error .np-progress__bar span { background: var(--status-error); }
.np-progress--done .np-progress__bar span { background: var(--status-success); }
.np-progress__log {
  margin: 10px 0 0;
  max-height: 90px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--font-mono, monospace);
  font-size: 0.72rem;
  color: var(--text-secondary);
}
.np-progress--error .np-progress__log { color: var(--status-error); }

.np-error {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0 0 14px;
  padding: 10px 12px;
  border-radius: 8px;
  font-size: 0.84rem;
  color: var(--status-error);
  background: color-mix(in srgb, var(--status-error) 10%, transparent);
  border: 1px solid color-mix(in srgb, var(--status-error) 30%, transparent);
}

.np-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin: 18px -22px -20px;
  padding: 14px 22px;
  border-top: 1px solid var(--border-color);
  background: var(--header-bg, transparent);
}
.np-footer__hint { margin-right: auto; font-size: 0.74rem; color: var(--text-muted, var(--text-secondary)); }
.np-footer__hint kbd {
  font-family: var(--font-mono, monospace);
  font-size: 0.7rem;
  padding: 1px 5px;
  border-radius: 4px;
  border: 1px solid var(--border-color);
  background: var(--bg-primary);
}

.np-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 8px 14px;
  font: inherit;
  font-size: 0.85rem;
  font-weight: 600;
  border-radius: 8px;
  border: 1px solid transparent;
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.12s, border-color 0.12s, opacity 0.12s;
}
.np-btn:disabled { opacity: 0.55; cursor: not-allowed; }
.np-btn:focus-visible { outline: none; box-shadow: 0 0 0 3px rgba(var(--accent-purple-rgb, 138, 108, 255), 0.25); }
.np-btn--primary { background: var(--accent-purple); color: var(--bg-primary); }
.np-btn--primary:hover:not(:disabled) { background: var(--accent-purple-hover, var(--accent-purple)); }
.np-btn--secondary { background: var(--bg-primary); color: var(--text-primary); border-color: var(--border-color); }
.np-btn--secondary:hover:not(:disabled) { border-color: var(--overlay-border-hover, var(--text-secondary)); }
.np-btn--ghost { background: transparent; color: var(--text-secondary); }
.np-btn--ghost:hover:not(:disabled) { background: var(--overlay-bg-hover, rgba(127, 127, 127, 0.12)); color: var(--text-primary); }

/* Done */
.np-done { text-align: center; padding-top: 10px; padding-bottom: 26px; }
.np-done__badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 52px;
  height: 52px;
  margin-bottom: 12px;
  border-radius: 50%;
  color: #fff;
  background: var(--swatch);
  box-shadow: 0 6px 18px color-mix(in srgb, var(--swatch) 40%, transparent);
}
.np-done__name { margin: 0; font-size: 1.05rem; font-weight: 650; }
.np-done__path { margin: 4px 0 0; font-size: 0.8rem; color: var(--text-secondary); }
.np-done__path code { font-family: var(--font-mono, monospace); }
.np-done .np-status { justify-content: center; }
.np-done__actions { display: flex; justify-content: center; flex-wrap: wrap; gap: 8px; margin-top: 20px; }

.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }

@media (max-width: 560px) {
  .np-overlay { padding: 16px 12px; align-items: flex-end; }
  .np-dialog { max-height: 92vh; }
  .np-dest { flex-wrap: wrap; }
  .np-dest__parent { flex-basis: 100%; }
  .np-footer__hint { display: none; }
}
</style>
