<template>
  <Teleport to="body">
    <Transition name="palette">
      <div v-if="isOpen" class="palette-overlay" @mousedown.self="closePalette">
        <div
          class="palette"
          role="dialog"
          aria-modal="true"
          aria-label="Command palette"
        >
          <div class="palette-search">
            <svg class="palette-search-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" />
            </svg>
            <input
              ref="input"
              v-model="query"
              class="palette-input"
              type="text"
              role="combobox"
              aria-expanded="true"
              aria-controls="palette-results"
              :aria-activedescendant="active ? `palette-item-${active.key}` : undefined"
              aria-autocomplete="list"
              placeholder="Search sessions, projects, actions…"
              spellcheck="false"
              autocomplete="off"
              @keydown="onKeydown"
            >
            <kbd class="palette-esc">Esc</kbd>
          </div>

          <div id="palette-results" ref="list" class="palette-results" role="listbox" aria-label="Results">
            <template v-for="section in sections" :key="section.group">
              <div class="palette-group" role="presentation">{{ section.group }}</div>
              <div
                v-for="item in section.items"
                :id="`palette-item-${item.key}`"
                :key="item.key"
                class="palette-item"
                :class="{ active: item.index === activeIndex }"
                role="option"
                :aria-selected="item.index === activeIndex"
                :data-index="item.index"
                @mousemove="activeIndex = item.index"
                @click="execute(item)"
              >
                <span class="palette-item-icon" :style="item.color ? { '--dot': item.color } : undefined" aria-hidden="true">
                  <span v-if="item.color" class="palette-dot" />
                  <span v-else>{{ item.icon }}</span>
                </span>
                <span class="palette-item-text">
                  <span class="palette-item-title">{{ item.title }}</span>
                  <span v-if="item.subtitle" class="palette-item-subtitle">{{ item.subtitle }}</span>
                </span>
                <span v-if="item.badge" class="palette-badge">{{ item.badge }}</span>
                <kbd v-if="item.shortcut" class="palette-key">{{ item.shortcut }}</kbd>
              </div>
            </template>
            <div v-if="!flatItems.length" class="palette-empty">
              <template v-if="loading">Loading…</template>
              <template v-else>Nothing matches “{{ query }}”</template>
            </div>
          </div>

          <footer class="palette-footer">
            <span><kbd>↑</kbd><kbd>↓</kbd> move</span>
            <span><kbd>↵</kbd> open</span>
            <span><kbd>Esc</kbd> close</span>
            <span class="palette-footer-right">All shortcuts <kbd>{{ keys.label('help.shortcuts') }}</kbd></span>
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useSessionStore } from '~/stores/session/sessionStore'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import { useJustRecipes } from '~/composables/useJustRecipes'
import { useTheme } from '~/composables/useTheme'
import { useCommandPalette } from '~/composables/useCommandPalette'
import {
  useClosable, useShortcut, useShortcutLabels, useShortcutsSheet, hasShortcutHandler, runShortcut
} from '~/composables/useKeyboardShortcuts'
import { useChangelog } from '~/composables/useChangelog'
import { SHORTCUTS } from '~/utils/shortcuts/catalog'
import type { ShortcutId } from '~/utils/shortcuts/catalog'
import { fuzzyScore, containsWords } from '~/utils/fuzzy'
import { openOverviewAgent } from '~/utils/agents/openOverviewAgent'
import { desktopShell, desktopCheckForUpdates, desktopNotify, requestDesktopUpdateDialog } from '~/utils/desktop'

/**
 * The command palette (⌘K): one fuzzy search over sessions, projects,
 * actions (every shortcut, plus a few that have no key), Just recipes of the
 * current project and settings pages.
 */

type Group = 'Sessions' | 'Projects' | 'Actions' | 'Just' | 'Settings'
const GROUP_ORDER: Group[] = ['Actions', 'Sessions', 'Projects', 'Just', 'Settings']
const GROUP_LIMIT: Record<Group, number> = { Actions: 8, Sessions: 8, Projects: 6, Just: 6, Settings: 6 }

interface PaletteItem {
  key: string
  group: Group
  title: string
  subtitle?: string
  keywords?: string
  icon?: string
  color?: string
  badge?: string
  shortcut?: string
  /** Base rank for the empty query (higher first) */
  rank?: number
  run: () => void | Promise<void>
}

const router = useRouter()
const sessionStore = useSessionStore()
const { selectedProject } = storeToRefs(sessionStore)
const { fetchWithAuth } = useAuthenticatedFetch()
const just = useJustRecipes()
const { toggleDarkMode, isDark } = useTheme()
const { isOpen, closePalette, togglePalette } = useCommandPalette()
const { openSheet } = useShortcutsSheet()
const { openChangelog } = useChangelog()
const keys = useShortcutLabels()

const query = ref('')
const activeIndex = ref(0)
const input = ref<HTMLInputElement | null>(null)
const list = ref<HTMLElement | null>(null)
const loading = ref(false)
const sessions = ref<any[]>([])
const projects = ref<any[]>([])
const availableActions = ref<Set<ShortcutId>>(new Set())

// ⌘K opens it (from anywhere, also while typing) and, while open, closes it.
useShortcut('palette.open', () => togglePalette())
useShortcut('palette.open', () => closePalette(), { scope: 'modal', when: () => isOpen.value })
useClosable(isOpen, closePalette, { modal: true })

let previousFocus: Element | null = null
// Set when an item navigates, so closing doesn't put focus back on the old page
let navigatedAway = false
watch(isOpen, async (open) => {
  if (open) {
    previousFocus = document.activeElement
    query.value = ''
    activeIndex.value = 0
    // Which actions can run here (session actions need an open session)
    availableActions.value = new Set(SHORTCUTS.filter(s => s.palette !== false && hasShortcutHandler(s.id, { ignoreModal: true })).map(s => s.id))
    await nextTick()
    input.value?.focus()
    void load()
  } else if (previousFocus instanceof HTMLElement && previousFocus.isConnected && !navigatedAway) {
    previousFocus.focus()
  }
  navigatedAway = false
})

async function load() {
  loading.value = true
  const projectId = selectedProject.value?.id
  await Promise.allSettled([
    fetchWithAuth('/api/agent/sessions?status=all').then(r => r.ok ? r.json() : null).then((d) => { if (d) sessions.value = d.sessions || [] }),
    fetchWithAuth('/api/projects').then(r => r.ok ? r.json() : null).then((d) => { if (d) projects.value = d.projects || [] }),
    projectId ? just.fetchRecipes(projectId) : Promise.resolve()
  ])
  loading.value = false
}

const projectById = computed(() => new Map(projects.value.map((p: any) => [p.id, p])))

function relative(ts?: string): string {
  if (!ts) return ''
  const s = Math.max(0, (Date.now() - new Date(ts).getTime()) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)} min ago`
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`
  return `${Math.floor(s / 86400)} d ago`
}

/** "…/code/eggify" for long paths */
function shortPath(path?: string): string {
  if (!path) return ''
  if (path.length <= 44) return path
  const parts = path.split('/').filter(Boolean)
  return `…/${parts.slice(-2).join('/')}`
}

function statusBadge(status?: string): string {
  if (status === 'processing' || status === 'thinking' || status === 'running') return 'working'
  if (status === 'ended') return 'ended'
  return ''
}

const actionIcons: Partial<Record<ShortcutId, string>> = {
  'session.new': '＋', 'project.new': '＋', 'settings.open': '⚙', 'overview.toggle': '▦', 'allagents.toggle': '⊞', 'project.switch': '⇄',
  'file.goto': '⌕', 'session.prev': '‹', 'session.next': '›', 'messages.search': '⌕', 'model.change': '◇',
  'subagents.toggle': '⋔', 'tab.terminal': '›_', 'tab.files': '▤', 'session.tag': '#', 'voice.dictate': '◉', 'agent.stop': '■',
  'sidebar.toggle': '▌', 'sidebar.right.toggle': '▐', 'view.zen': '◐', 'view.fullscreen': '⤢', 'theme.next': '◑', 'theme.previous': '◐', 'help.shortcuts': '⌨'
}

const allItems = computed<PaletteItem[]>(() => {
  const items: PaletteItem[] = []

  // Actions: every shortcut that can run here…
  for (const def of SHORTCUTS) {
    if (def.palette === false || !availableActions.value.has(def.id)) continue
    if (def.id === 'view.fullscreen' && keys.shell === 'wails') continue // the window's own (View menu)
    items.push({
      key: `action:${def.id}`,
      group: 'Actions',
      title: def.label,
      subtitle: def.description,
      icon: actionIcons[def.id] || '•',
      shortcut: keys.label(def.id),
      rank: def.id === 'session.new' ? 3 : def.id === 'project.new' ? 2 : 1,
      run: () => {
        if (def.id === 'help.shortcuts') openSheet()
        else runShortcut(def.id, { source: 'palette' })
      }
    })
  }
  // …and a few without a key
  items.push({
    key: 'action:theme',
    group: 'Actions',
    title: isDark.value ? 'Switch to light theme' : 'Switch to dark theme',
    keywords: 'theme dark light mode appearance toggle',
    icon: '◑',
    run: () => toggleDarkMode()
  })
  if (selectedProject.value) {
    items.push({
      key: 'action:just',
      group: 'Actions',
      title: 'Just commands…',
      subtitle: `Recipes in ${selectedProject.value.name}`,
      keywords: 'just recipe run command justfile',
      icon: '›_',
      run: () => just.openPalette()
    })
  }
  if (desktopShell() === 'wails') {
    items.push({
      key: 'action:updates',
      group: 'Actions',
      title: 'Check for updates',
      keywords: 'update upgrade version release',
      icon: '↻',
      run: checkForUpdates
    })
  }

  // Sessions, most recently active first
  const sorted = [...sessions.value].sort((a, b) =>
    new Date(b.updated_at || b.created_at || 0).getTime() - new Date(a.updated_at || a.created_at || 0).getTime())
  sorted.forEach((s, i) => {
    const project = projectById.value.get(s.project_id)
    const name = s.selected_avatar?.name || s.options?.agent_name || `Session ${String(s.id).slice(0, 8)}`
    const isActive = s.id === sessionStore.activeSessionId
    const parts = [project?.name, s.git_branch || s.options?.workspace?.branch, relative(s.updated_at)].filter(Boolean)
    items.push({
      key: `session:${s.id}`,
      group: 'Sessions',
      title: name,
      subtitle: parts.join(' · '),
      keywords: `${s.id} ${s.model_name || ''} ${project?.path || ''}`,
      color: project?.color || s.selected_avatar?.color || '#9d8bff',
      badge: isActive ? 'open' : statusBadge(s.status),
      rank: -i,
      run: async () => {
        navigatedAway = true
        await openOverviewAgent(s, { store: sessionStore, router, fetchWithAuth })
      }
    })
  })

  // Projects
  for (const p of projects.value) {
    items.push({
      key: `project:${p.id}`,
      group: 'Projects',
      title: p.name,
      subtitle: shortPath(p.path),
      keywords: p.path,
      color: p.color || '#9aa0a6',
      badge: p.id === selectedProject.value?.id ? 'current' : '',
      run: async () => {
        navigatedAway = true
        sessionStore.setSelectedProject(p)
        await router.push({ path: '/agents', query: { project: p.id } })
      }
    })
  }

  // Just recipes of the current project
  const projectId = selectedProject.value?.id
  if (projectId) {
    for (const r of just.recipes.value) {
      items.push({
        key: `just:${r.name}`,
        group: 'Just',
        title: `just ${r.name}`,
        subtitle: r.description || (r.parameters?.length ? `Arguments: ${r.parameters.join(', ')}` : ''),
        icon: '▸',
        run: () => {
          // Recipes that take arguments open in the Just palette
          if (r.parameters?.length) just.openPalette()
          else void just.runRecipe(projectId, r.name)
        }
      })
    }
  }

  // Settings pages
  const settingsPages: Array<[string, string, string]> = [
    ['Settings', '/settings', 'preferences configuration'],
    ['Remote access', '/settings#access', 'ngrok tunnel phone'],
    ['Agents & logins', '/settings#agent-logins', 'claude codex cli login'],
    ['Sandbox', '/settings#sandbox', 'security network egress'],
    ['Voice input', '/settings#voice', 'dictation speech whisper parakeet'],
    ['Appearance', '/settings#appearance', 'theme color'],
    ['Providers', '/providers', 'api key model anthropic openai deepseek'],
    ['Connectors', '/connectors', 'n0 mcp oauth integrations'],
    ['Hooks', '/hooks', 'claude hooks'],
    ['Skills', '/skills', 'skill manager review'],
    ['Themes', '/themes', 'appearance colors'],
    ['Projects', '/projects', 'manage projects'],
    ['Profile', '/profile', 'account password avatar'],
    ['Help', '/help', 'documentation guide shortcuts']
  ]
  items.push({
    key: 'action:whats-new',
    group: 'Settings',
    title: "What's new",
    subtitle: 'Release notes',
    keywords: 'changelog release notes version update',
    icon: '✦',
    run: () => { openChangelog() }
  })
  for (const [title, path, words] of settingsPages) {
    items.push({
      key: `settings:${path}`,
      group: 'Settings',
      title,
      subtitle: path,
      keywords: words,
      icon: '⚙',
      run: () => { navigatedAway = true; void router.push(path) }
    })
  }
  return items
})

interface Ranked extends PaletteItem { score: number; index: number }

const sections = computed(() => {
  const q = query.value.trim()
  const byGroup = new Map<Group, Ranked[]>()
  for (const item of allItems.value) {
    let score: number | null
    if (!q) {
      // Empty query: actions, recent sessions and projects only
      if (item.group === 'Just' || item.group === 'Settings') continue
      score = item.rank ?? 0
    } else {
      // Fuzzy on the title; the subtitle/keywords only by whole words, so a
      // long path doesn't match everything
      const t = fuzzyScore(q, item.title)
      const k = containsWords(q, `${item.title} ${item.subtitle || ''} ${item.keywords || ''}`) ? 10 : null
      score = t !== null ? t + 5 : k
      if (score === null) continue
    }
    const list = byGroup.get(item.group) ?? []
    list.push({ ...item, score, index: 0 })
    byGroup.set(item.group, list)
  }
  const limit = (g: Group) => !q && g === 'Actions' ? 6 : !q && g === 'Projects' ? 4 : !q && g === 'Sessions' ? 5 : GROUP_LIMIT[g]
  const out = [...byGroup.entries()].map(([group, items]) => ({
    group,
    items: items.sort((a, b) => b.score - a.score).slice(0, limit(group)),
    best: Math.max(...items.map(i => i.score))
  }))
  // With a query the best-matching group comes first; otherwise a fixed order.
  out.sort((a, b) => q ? b.best - a.best : GROUP_ORDER.indexOf(a.group) - GROUP_ORDER.indexOf(b.group))
  let index = 0
  for (const s of out) for (const item of s.items) item.index = index++
  return out
})

const flatItems = computed(() => sections.value.flatMap(s => s.items))
const active = computed(() => flatItems.value[activeIndex.value])

watch(query, () => { activeIndex.value = 0 })
watch(activeIndex, async (i) => {
  await nextTick()
  list.value?.querySelector<HTMLElement>(`[data-index="${i}"]`)?.scrollIntoView({ block: 'nearest' })
})

function onKeydown(e: KeyboardEvent) {
  if (e.isComposing) return
  const n = flatItems.value.length
  if (e.key === 'ArrowDown' || (e.key === 'n' && e.ctrlKey && !e.metaKey)) {
    e.preventDefault()
    if (n) activeIndex.value = (activeIndex.value + 1) % n
  } else if (e.key === 'ArrowUp' || (e.key === 'p' && e.ctrlKey && !e.metaKey)) {
    e.preventDefault()
    if (n) activeIndex.value = (activeIndex.value - 1 + n) % n
  } else if (e.key === 'Home' && n) {
    e.preventDefault(); activeIndex.value = 0
  } else if (e.key === 'End' && n) {
    e.preventDefault(); activeIndex.value = n - 1
  } else if (e.key === 'Enter') {
    e.preventDefault()
    if (active.value) void execute(active.value)
  }
}

async function execute(item: PaletteItem) {
  closePalette()
  await nextTick()
  try {
    await item.run()
  } catch (err) {
    console.error('[palette]', err)
  }
}

async function checkForUpdates() {
  try {
    const state = await desktopCheckForUpdates()
    if (!state) return
    if (state.update_available || state.blocked) requestDesktopUpdateDialog()
    else if (state.check_error) await desktopNotify('Update check failed', state.check_error)
    else await desktopNotify("You're up to date", `Eggify ${state.current_version} is the latest version.`)
  } catch (err: any) {
    await desktopNotify('Update check failed', err?.message || String(err)).catch(() => {})
  }
}

defineExpose({ query, sections, execute })
</script>

<style scoped>
.palette-overlay {
  position: fixed;
  inset: 0;
  z-index: 10050;
  display: flex;
  justify-content: center;
  align-items: flex-start;
  padding: 12vh 16px 16px;
  background: rgba(10, 10, 20, 0.38);
  backdrop-filter: blur(2px);
}

.palette {
  width: min(640px, 100%);
  max-height: min(560px, 76vh);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--card-bg);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: 14px;
  box-shadow: 0 1px 0 rgba(255, 255, 255, 0.04) inset, 0 28px 80px rgba(0, 0, 0, 0.32), 0 4px 14px rgba(0, 0, 0, 0.12);
}

.palette-search {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border-color);
}
.palette-search-icon { color: var(--text-secondary); flex-shrink: 0; }
.palette-input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  font-size: 1.02rem;
}
.palette-input::placeholder { color: var(--text-muted, var(--text-secondary)); opacity: 0.8; }

.palette-results {
  flex: 1;
  overflow-y: auto;
  padding: 6px 8px 8px;
  overscroll-behavior: contain;
}

.palette-group {
  padding: 10px 10px 4px;
  font-size: 0.68rem;
  font-weight: 650;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--text-secondary);
}

.palette-item {
  display: flex;
  align-items: center;
  gap: 11px;
  min-height: 40px;
  padding: 6px 10px;
  border-radius: 9px;
  cursor: pointer;
  scroll-margin: 6px;
}
.palette-item.active {
  background: var(--primary-subtle, rgba(139, 124, 246, 0.12));
  box-shadow: inset 0 0 0 1px var(--primary-border, rgba(139, 124, 246, 0.3));
}

.palette-item-icon {
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 7px;
  font-size: 0.8rem;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  border: 1px solid color-mix(in srgb, var(--border-color) 80%, transparent);
}
.palette-item.active .palette-item-icon { color: var(--accent-purple); }
.palette-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--dot);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--dot) 22%, transparent);
}

.palette-item-text { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.palette-item-title {
  font-size: 0.92rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.palette-item-subtitle {
  font-size: 0.75rem;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.palette-badge {
  flex-shrink: 0;
  font-size: 0.68rem;
  padding: 2px 7px;
  border-radius: 999px;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
}

.palette-key, .palette-footer kbd, .palette-esc {
  display: inline-flex;
  flex-shrink: 0;
  letter-spacing: 0.06em;
  white-space: nowrap;
  min-width: 20px;
  height: 20px;
  padding: 0 5px;
  align-items: center;
  justify-content: center;
  font: 500 0.7rem/1 ui-sans-serif, -apple-system, 'Segoe UI', system-ui, sans-serif;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  border-bottom-width: 2px;
  border-radius: 5px;
}
.palette-esc { flex-shrink: 0; }

.palette-empty {
  padding: 28px 12px;
  text-align: center;
  font-size: 0.88rem;
  color: var(--text-secondary);
}

.palette-footer {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 9px 16px;
  font-size: 0.72rem;
  color: var(--text-secondary);
  border-top: 1px solid var(--border-color);
  background: color-mix(in srgb, var(--bg-secondary) 70%, transparent);
}
.palette-footer span { display: inline-flex; align-items: center; gap: 4px; }
.palette-footer-right { margin-left: auto; }

.palette-enter-active, .palette-leave-active { transition: opacity 0.12s ease; }
.palette-enter-active .palette, .palette-leave-active .palette { transition: transform 0.12s ease; }
.palette-enter-from, .palette-leave-to { opacity: 0; }
.palette-enter-from .palette, .palette-leave-to .palette { transform: translateY(-6px) scale(0.985); }

@media (max-width: 640px) {
  .palette-overlay { padding-top: 12px; }
  .palette-footer span:not(.palette-footer-right) { display: none; }
  .palette-key { display: none; }
}
@media (prefers-reduced-motion: reduce) {
  .palette-enter-active, .palette-leave-active, .palette-enter-active .palette, .palette-leave-active .palette { transition: none; }
}
</style>
