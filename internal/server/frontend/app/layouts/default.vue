<template>
  <div id="app" :class="{ 'overview-open': overviewOpen }">
    <nav class="navbar" :inert="overviewOpen">
      <div
        v-if="projectColor && colorStyle !== 'off'"
        class="navbar-color-gradient"
        :class="{ 'navbar-color-glow': colorGlow === 'on' }"
        :style="navbarGradientStyle"
      ></div>
      <div class="navbar-container">
        <div class="nav-left">
          <NuxtLink to="/" class="nav-logo-link" title="Go to frontpage">
            <EggifyLogo :size="34" class="nav-logo" />
            <span v-if="selectedProject" class="nav-project-label" :class="'label-' + catActivity">{{ selectedProject.name }}</span>
          </NuxtLink>
        </div>
        <!-- Which Mac the desktop window shows (only with paired Macs) -->
        <div class="nav-center">
          <MachineChip />
        </div>
        <div class="nav-right">
          <button
            v-if="versionInfo.version"
            type="button"
            class="version-badge"
            title="What's new"
            aria-label="What's new"
            data-testid="version-badge"
            @click="openChangelog"
          >v{{ versionInfo.version }}<span v-if="changelogUnseen" class="version-badge__dot" aria-label="Updated" /></button>

          <!-- Project selector (promoted to header) -->
          <div class="nav-project-selector">
            <ProjectSelector />
          </div>

          <button class="overview-toggle" :title="shortcutLabels.title('Agent overview', 'overview.toggle')" aria-label="Open agent overview" :aria-keyshortcuts="shortcutLabels.aria('overview.toggle')" @click="toggleOverview">▥</button>

          <!-- Settings dropdown -->
          <div class="settings-menu" ref="settingsMenuRef" @keydown.esc.stop.prevent="closeSettingsMenu(); settingsButtonRef?.focus()">
            <button
              ref="settingsButtonRef"
              :aria-expanded="showSettingsMenu"
              aria-controls="header-settings"
              @click="toggleSettingsMenu"
              class="settings-button"
              :class="{ 'settings-button-active': showSettingsMenu }"
              :title="tunnelIndicator ? `Settings · ${tunnelIndicator.label}` : 'Settings'"
            >
              <span
                v-if="tunnelIndicator"
                class="settings-tunnel-badge"
                :class="`settings-tunnel-badge--${tunnelIndicator.state}`"
                aria-hidden="true"
              />
              <!-- Slimmer 3-line settings icon (horizontal sliders) -->
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
                <line x1="4" y1="6" x2="20" y2="6"/>
                <line x1="4" y1="12" x2="20" y2="12"/>
                <line x1="4" y1="18" x2="20" y2="18"/>
                <circle cx="8" cy="6" r="2" fill="currentColor" stroke="none"/>
                <circle cx="16" cy="12" r="2" fill="currentColor" stroke="none"/>
                <circle cx="10" cy="18" r="2" fill="currentColor" stroke="none"/>
              </svg>
            </button>

            <div v-if="showSettingsMenu" id="header-settings" class="settings-dropdown">
              <!-- Account section (user info + admin) -->
              <div v-if="isAuthenticated" class="settings-user-header">
                <UserAvatar
                  :avatar-image="user?.avatar_image"
                  :avatar-name="user?.avatar_name"
                  :avatar-color="user?.avatar_color"
                  size="small"
                />
                <div class="settings-user-info">
                  <span class="settings-user-name">{{ user?.username }}</span>
                  <span v-if="user?.isAdmin" class="settings-admin-badge">Admin</span>
                </div>
              </div>

              <div v-if="isAuthenticated" class="settings-dropdown-divider"></div>

              <!-- Account links -->
              <div v-if="isAuthenticated" class="settings-section-label">Account</div>
              <NuxtLink v-if="isAuthenticated" to="/profile" class="settings-dropdown-item" @click="closeSettingsMenu">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
                  <circle cx="12" cy="7" r="4"/>
                </svg>
                Profile
              </NuxtLink>
              <NuxtLink v-if="user?.isAdmin" to="/admin/users" class="settings-dropdown-item" @click="closeSettingsMenu">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
                  <circle cx="9" cy="7" r="4"/>
                  <path d="M23 21v-2a4 4 0 0 0-3-3.87"/>
                  <path d="M16 3.13a4 4 0 0 1 0 7.75"/>
                </svg>
                User Management
              </NuxtLink>

              <div v-if="isAuthenticated" class="settings-dropdown-divider"></div>

              <!-- Project section -->
              <div class="settings-section-label">Project</div>
              <NuxtLink
                v-if="selectedProject"
                :to="`/projects/${selectedProject.id}`"
                class="settings-dropdown-item"
                @click="closeSettingsMenu"
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z"/>
                  <path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"/>
                </svg>
                Project Settings
              </NuxtLink>
              <NuxtLink
                v-else
                to="/projects"
                class="settings-dropdown-item"
                @click="closeSettingsMenu"
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
                </svg>
                Manage Projects
              </NuxtLink>

              <div class="settings-dropdown-divider"></div>

              <!-- Tools section -->
              <div class="settings-section-label">Tools</div>
              <button @click="openJustPalette(); closeSettingsMenu()" class="settings-dropdown-item">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="4 17 10 11 4 5"/>
                  <line x1="12" y1="19" x2="20" y2="19"/>
                </svg>
                Just Commands
              </button>
              <button @click="openShortcutsDialog(); closeSettingsMenu()" class="settings-dropdown-item">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <rect x="2" y="4" width="20" height="16" rx="2"/>
                  <path d="M6 8h.01M10 8h.01M14 8h.01M18 8h.01M8 12h.01M12 12h.01M16 12h.01M7 16h10"/>
                </svg>
                Keyboard Shortcuts
                <span class="settings-shortcut">{{ shortcutLabels.label('help.shortcuts') }}</span>
              </button>
              <button @click="openChangelog(); closeSettingsMenu()" class="settings-dropdown-item" data-testid="settings-menu-whats-new">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M12 3l1.9 5.8H20l-4.9 3.6 1.9 5.8L12 14.6l-5 3.6 1.9-5.8L4 8.8h6.1z"/>
                </svg>
                What's New
                <span v-if="changelogUnseen" class="settings-whats-new-dot" aria-label="Updated" />
                <span v-if="versionInfo.version" class="settings-shortcut">v{{ versionInfo.version }}</span>
              </button>

              <div class="settings-dropdown-divider"></div>

              <!-- Appearance section -->
              <div class="settings-section-label">Appearance</div>
              <div class="settings-dropdown-item settings-theme-row">
                <ThemeSelector :show-label="false" />
                <ThemeToggle />
              </div>

              <!-- Remote access (ngrok) status while it is on or failed -->
              <template v-if="tunnelIndicator">
                <div class="settings-dropdown-divider"></div>
                <NuxtLink
                  to="/settings#access"
                  class="settings-dropdown-item settings-tunnel-status"
                  :class="`settings-tunnel-status--${tunnelIndicator.state}`"
                  :title="tunnelIndicator.detail"
                  @click="closeSettingsMenu()"
                >
                  <span class="tunnel-dot"></span>
                  <span class="settings-tunnel-label">{{ tunnelIndicator.label }}</span>
                  <span v-if="tunnelIndicator.short" class="settings-tunnel-url">{{ tunnelIndicator.short }}</span>
                </NuxtLink>
              </template>

              <!-- Logout -->
              <template v-if="isAuthenticated">
                <div class="settings-dropdown-divider"></div>
                <button @click="handleLogout" class="settings-dropdown-item settings-logout-item">
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
                    <polyline points="16 17 21 12 16 7"/>
                    <line x1="21" y1="12" x2="9" y2="12"/>
                  </svg>
                  Logout
                </button>
              </template>
            </div>
          </div>
        </div>
      </div>
    </nav>

    <div class="app-layout">
      <div class="editor-workspace" :inert="overviewOpen">
      <!-- Mobile sidebar overlay backdrop -->
      <div
        v-if="!isCollapsed"
        class="sidebar-backdrop"
        @click="toggleSidebar"
      ></div>
      <Sidebar />
      <main class="main-content" :class="{ 'main-content-expanded': isCollapsed }">
        <slot />
      </main>
      </div>
      <Transition name="overview-reveal">
        <AgentOverview v-if="overviewOpen" :navigation-error="overviewNavigationError" :active-session-id="sessionStore.activeSessionId" @close="toggleOverview" @select="openOverviewAgent" />
      </Transition>
      <!-- Every session and subagent in one tree (⇧⌘S) -->
      <AllAgentsOverlay :show="allAgentsOpen" :navigation-error="allAgentsNavigationError" @close="closeAllAgents" @open="openFromAllAgents" />
      <ChangelogModal />
    </div>

    <!-- Bottom bar: Just jobs + Active Sessions Toolbar -->
    <div class="bottom-bar-container" :inert="overviewOpen">
      <JustJobIndicator />
      <ToolbarActiveSessionsToolbar />
    </div>

    <!-- Debug Panel (slide up from bottom) -->
    <DebugDebugPanel />


    <!-- Shortcuts Dialog -->
    <ShortcutsDialog />

    <!-- Just Command Palette (also reachable from the command palette) -->
    <JustCommandPalette />
  </div>
</template>

<script setup>
import '~~/assets/css/main.css'
import AgentOverview from '~/components/agents/AgentOverview.vue'
import AllAgentsOverlay from '~/components/agents/AllAgentsOverlay.vue'
import MachineChip from '~/components/MachineChip.vue'
import ChangelogModal from '~/components/ChangelogModal.vue'
import { useChangelog } from '~/composables/useChangelog'
import { onDesktopShortcut, DESKTOP_WHATS_NEW_ID } from '~/utils/desktop'
import { requestSubagentFocus, subagentFocusRequest } from '~/utils/agents/subagentFocus'
import { openOverviewAgent as navigateToOverviewAgent } from '~/utils/agents/openOverviewAgent'
import { markOverviewReviewed } from '~/utils/agents/overviewState'
import { storeToRefs } from 'pinia'
import { useSessionStore } from '~/stores/session/sessionStore'
import { useProjectColorSettings } from '~/composables/useProjectColorSettings'
import { useDebugLogger } from '~/composables/useDebugLogger'
import { useTunnel } from '~/composables/useTunnel'
import { useCatActivity } from '~/composables/useCatActivity'
import { useShortcut, useClosable, useShortcutsSheet, useShortcutLabels, resolveHandler } from '~/composables/useKeyboardShortcuts'

const overviewOpen = ref(false)
const overviewNavigationError = ref('')
let openingOverviewAgent = false
const { fetchWithAuth: fetchOverviewProject } = useAuthenticatedFetch()
let previousOverviewFocus = null

function toggleOverview() {
  if (!overviewOpen.value) {
    previousOverviewFocus = document.activeElement
    overviewOpen.value = true
  } else {
    overviewOpen.value = false
    nextTick(() => { if (previousOverviewFocus?.isConnected) previousOverviewFocus.focus() })
  }
}

// All agents overlay (⇧⌘S): every session and every subagent in one tree.
const allAgentsOpen = ref(false)
const allAgentsNavigationError = ref('')
let openingFromAllAgents = false
function toggleAllAgents() {
  allAgentsNavigationError.value = ''
  allAgentsOpen.value = !allAgentsOpen.value
}
function closeAllAgents() {
  allAgentsOpen.value = false
}
// Opens a session; for a subagent, that session's Subagents view with it selected.
async function openFromAllAgents({ session, agentId }) {
  if (openingFromAllAgents) return
  openingFromAllAgents = true
  allAgentsNavigationError.value = ''
  try {
    if (agentId) requestSubagentFocus(session.id, agentId)
    await navigateToOverviewAgent(session, { store: sessionStore, router, fetchWithAuth: fetchOverviewProject })
    allAgentsOpen.value = false
    overviewOpen.value = false
  } catch (error) {
    subagentFocusRequest.value = null
    allAgentsNavigationError.value = error.message || 'Could not open this agent. Please try again.'
  } finally {
    openingFromAllAgents = false
  }
}

async function openOverviewAgent(agent) {
  if (openingOverviewAgent) return
  openingOverviewAgent = true
  overviewNavigationError.value = ''
  try {
    // The agent opens in the view it was last left in (Live or Zen).
    await navigateToOverviewAgent(agent, { store: sessionStore, router, fetchWithAuth: fetchOverviewProject })
    markOverviewReviewed(agent)
    overviewOpen.value = false
  } catch (error) {
    overviewNavigationError.value = error.message || 'Could not open this agent. Please try again.'
  } finally {
    openingOverviewAgent = false
  }
}

// Initialize theme system
useTheme()

// Debug panel toggle (always accessible, independent of toolbar)
const { isVisible: debugPanelVisible, toggle: toggleDebugPanel } = useDebugLogger()

// Tunnel status for navbar indicator
const { status: tunnelStatus, startPolling: startTunnelPolling, stopPolling: stopTunnelPolling } = useTunnel()
const tunnelIndicator = computed(() => {
  const t = tunnelStatus.value
  if (!t) return null
  switch (t.status) {
    case 'connected': {
      const host = (t.public_url || '').replace(/^https?:\/\//, '')
      return { state: 'connected', label: 'Remote access on', short: host, detail: t.public_url || '' }
    }
    case 'connecting':
      return { state: 'connecting', label: 'Remote access connecting', short: '', detail: t.message || '' }
    case 'error':
      return { state: 'error', label: 'Remote access failed', short: '', detail: t.error || '' }
    default:
      return null
  }
})

// Cat activity state for project label animation
const { activity: catActivity } = useCatActivity()

// Get selected project color for navbar gradient
const sessionStore = useSessionStore()
const { selectedProject } = storeToRefs(sessionStore)
const projectColor = computed(() => selectedProject.value?.color || '')

// Project color display settings
const { colorStyle, colorGlow } = useProjectColorSettings()

const navbarGradientStyle = computed(() => {
  const c = projectColor.value
  if (!c) return {}
  if (colorStyle.value === 'solid') {
    return { background: `${c}20` }
  }
  return { background: `linear-gradient(to bottom, ${c}35, ${c}10 60%, transparent 100%)` }
})

// Initialize authentication
const { isAuthenticated, user, logout, checkAuthStatus } = useAuth()
const showSettingsMenu = ref(false)
const settingsMenuRef = ref(null)
const settingsButtonRef = ref(null)
const router = useRouter()

// Settings menu
const toggleSettingsMenu = () => {
  showSettingsMenu.value = !showSettingsMenu.value
}

const closeSettingsMenu = () => {
  showSettingsMenu.value = false
}

// Handle logout
const handleLogout = async () => {
  try {
    await logout()
    router.push('/login')
  } catch (error) {
    console.error('Logout failed:', error)
  } finally {
    closeSettingsMenu()
  }
}

// Initialize sidebar state
const { isCollapsed, toggleSidebar, collapseSidebar } = useSidebar()

// Keyboard shortcuts (registry: composables/useKeyboardShortcuts.ts)
const { openSheet: openShortcutsDialog, toggleSheet: toggleShortcutsSheet } = useShortcutsSheet()
const shortcutLabels = useShortcutLabels()

// Just Command Palette (extracted so it's available in template)
const { openPalette: openJustPalette } = useJustRecipes()

const { unseen: changelogUnseen, checkForUpdate: checkChangelogForUpdate, openChangelog } = useChangelog()

// Version info
const versionInfo = ref({
  version: '',
  name: ''
})

// Load version info
async function loadVersion() {
  try {
    const { data } = await useFetch('/api/version')
    if (data.value) {
      versionInfo.value = data.value
      checkChangelogForUpdate(data.value.version)
    }
  } catch (error) {
    // Error loading version
  }
}

// Open analytics in new tab
function openAnalytics() {
  window.open('http://localhost:3333', '_blank')
}

// Auto-collapse sidebar on mobile so it starts hidden
const isMobile = () => typeof window !== 'undefined' && window.innerWidth <= 768

// Load version on mount
onMounted(async () => {
  // On mobile, always start with sidebar collapsed (hidden)
  if (isMobile() && !isCollapsed.value) {
    collapseSidebar()
  }

  loadVersion()
  startTunnelPolling(5000)

  // Desktop app: Help → What's New
  onDesktopShortcut((id) => { if (id === DESKTOP_WHATS_NEW_ID) openChangelog() })
    .then((off) => { if (unmounted) off(); else offWhatsNewMenu = off })
    .catch(() => {})

  // Check authentication status
  await checkAuthStatus()

  // Close menus on click outside
  document.addEventListener('click', (e) => {
    if (settingsMenuRef.value && !settingsMenuRef.value.contains(e.target)) {
      showSettingsMenu.value = false
    }
  })

})

let offWhatsNewMenu = null
let unmounted = false

// Cleanup on unmount
onUnmounted(() => {
  unmounted = true
  stopTunnelPolling()
  offWhatsNewMenu?.()
})

// ⌘N outside the agents page: open it, then let its own (session-scope)
// handler create the session.
function createNewSession() {
  router.push('/agents').then(() => setTimeout(() => {
    const h = resolveHandler('session.new')
    if (h && h.scope !== 'global') h.run({ source: 'palette' })
  }, 100))
}

function toggleFullscreen() {
  if (typeof document === 'undefined') return
  if (document.fullscreenElement) void document.exitFullscreen?.()
  else void document.documentElement.requestFullscreen?.().catch(() => {})
}

useShortcut('overview.toggle', toggleOverview)
// All agents (⇧⌘S): opens from anywhere; while open (it is modal) the same key closes it.
useShortcut('allagents.toggle', toggleAllAgents)
useShortcut('allagents.toggle', closeAllAgents, { scope: 'modal', when: () => allAgentsOpen.value })
useShortcut('sidebar.toggle', () => toggleSidebar())
useShortcut('session.new', createNewSession)
useShortcut('settings.open', () => router.push('/settings'))
useShortcut('help.shortcuts', toggleShortcutsSheet)
// Browser full screen outside Zen (Zen registers its own, higher-priority one)
useShortcut('view.fullscreen', toggleFullscreen)
useClosable(overviewOpen, toggleOverview)
</script>

<style scoped>
#app {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--bg-primary);
  overflow: hidden;
}

.bottom-bar-container {
  flex-shrink: 0;
  position: relative;
}

.navbar {
  flex-shrink: 0;
  z-index: 1000;
  background: var(--bg-secondary);
  backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--border-color);
  padding: 0.5rem 0;
  transition: background-color 0.3s ease;
  position: relative;
  overflow: visible;
}


.navbar-color-gradient {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  pointer-events: none;
  z-index: 0;
  transition: background 0.3s ease;
}

.navbar-color-glow {
  animation: navbarGlow 3s ease-in-out infinite;
}

@keyframes navbarGlow {
  0%, 100% { opacity: 0.7; }
  50% { opacity: 1; }
}

.navbar-container {
  width: 100%;
  padding: 0 20px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  position: relative;
}

.nav-left {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.nav-logo-link {
  display: flex;
  align-items: center;
  gap: 8px;
  text-decoration: none;
  color: var(--text-primary);
  transition: opacity 0.2s ease;
}

.nav-logo-link:hover {
  opacity: 0.8;
}

.nav-logo {
  flex-shrink: 0;
}

.nav-project-label {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--text-primary);
  letter-spacing: -0.01em;
  line-height: 1;
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  transition: color 0.3s ease, opacity 0.3s ease, transform 0.3s ease, filter 0.3s ease;
}

/* ─── Label: IDLE — gentle fade pulse ─── */
.nav-project-label.label-idle {
  animation: labelFadePulse 4s ease-in-out infinite;
}

/* ─── Label: SLEEPING — dimmed, slow breathe ─── */
.nav-project-label.label-sleeping {
  color: var(--text-secondary);
  animation: labelBreathe 4s ease-in-out infinite;
}

/* ─── Label: ACTIVE — colorful glow pulse ─── */
.nav-project-label.label-active {
  animation: labelColorCycle 2s ease-in-out infinite;
  filter: brightness(1.2);
}

/* ─── Label: STREAMING — rainbow shimmer + scale pulse ─── */
.nav-project-label.label-streaming {
  animation: labelRainbow 1.5s linear infinite, labelPulseScale 0.8s ease-in-out infinite;
  filter: brightness(1.3);
}

/* ─── Label: ERROR — red flash ─── */
.nav-project-label.label-error {
  color: var(--color-error, #ef4444);
  animation: labelErrorFlash 1s ease-in-out infinite;
}

/* ─── Label: BACKGROUND — subtle orbit drift ─── */
.nav-project-label.label-background {
  opacity: 0.65;
  animation: labelDrift 2.5s ease-in-out infinite;
}

/* ─── Label: ALERT — bounce pop ─── */
.nav-project-label.label-alert {
  animation: labelBouncePop 0.5s cubic-bezier(0.34, 1.56, 0.64, 1);
}

/* ─── Label keyframes ─── */
@keyframes labelFadePulse {
  0%, 90%, 100% { opacity: 1; }
  95% { opacity: 0.5; }
}

@keyframes labelBreathe {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(0.97); }
}

@keyframes labelColorCycle {
  0%, 100% { color: var(--accent-purple, #a855f7); }
  33% { color: var(--accent-cyan, #22d3ee); }
  66% { color: var(--accent-purple, #a855f7); }
}

@keyframes labelRainbow {
  0% { color: #a855f7; }
  16% { color: #ec4899; }
  33% { color: #f43f5e; }
  50% { color: #f97316; }
  66% { color: #22d3ee; }
  83% { color: #3b82f6; }
  100% { color: #a855f7; }
}

@keyframes labelPulseScale {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(1.08); }
}

@keyframes labelErrorFlash {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}

@keyframes labelDrift {
  0%, 100% { transform: translateX(0); }
  25% { transform: translateX(2px); }
  75% { transform: translateX(-2px); }
}

@keyframes labelBouncePop {
  0% { transform: scale(1); }
  40% { transform: scale(1.25); }
  70% { transform: scale(0.9); }
  100% { transform: scale(1); }
}

.nav-right {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  min-width: 0;
}

/* Which Mac the desktop window shows (MachineChip): on the bar's true middle,
   out of the flex flow, so the sides keep their layout. The chip itself
   shrinks to its dot, then hides, when the sides leave too little room. */
.nav-center {
  position: absolute;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
  max-width: 100%;
  display: flex;
  justify-content: center;
  z-index: 1;
}


/* Project selector in navbar */
.nav-project-selector {
  flex-shrink: 1;
  min-width: 0;
}

/* Settings Menu */
.settings-menu {
  position: relative;
  flex-shrink: 0;
}

.settings-button {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: 1px solid var(--border-color);
  background: transparent;
  color: var(--text-secondary);
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.25s ease;
}

.settings-button:hover {
  color: var(--text-primary);
  border-color: var(--text-muted);
  background: var(--overlay-bg-hover);
}

.settings-button-active {
  color: var(--accent-purple);
  border-color: var(--accent-purple);
  background: color-mix(in srgb, var(--accent-purple) 10%, transparent);
}

.settings-dropdown {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  box-shadow: 0 8px 24px var(--shadow-color);
  width: 280px;
  max-width: calc(100vw - 24px);
  z-index: 1000;
  animation: slideDown 0.2s ease;
  padding: 4px 0;
}

.settings-section-label {
  padding: 8px 16px 4px;
  font-size: 0.6875rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-muted);
}

.settings-dropdown-item {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 10px 16px;
  background: none;
  border: none;
  color: var(--text-primary);
  font-size: 0.875rem;
  font-family: inherit;
  cursor: pointer;
  transition: all 0.15s ease;
  text-align: left;
  text-decoration: none;
}

.settings-button:focus-visible,
.settings-dropdown-item:focus-visible,
.nav-logo-link:focus-visible {
  outline: 2px solid var(--accent-cyan);
  outline-offset: 2px;
}

.settings-dropdown-item:hover {
  background: var(--bg-secondary);
}

.settings-shortcut {
  margin-left: auto;
  font-size: 0.75rem;
  color: var(--text-muted);
  background: var(--bg-primary);
  padding: 2px 6px;
  border-radius: 4px;
  font-family: monospace;
}

.settings-dropdown-divider {
  height: 1px;
  background: var(--border-color);
  margin: 4px 0;
}

.settings-project-selector {
  cursor: default;
}

.settings-project-selector:hover {
  background: none;
}

.settings-theme-row {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: default;
}

.settings-theme-row:hover {
  background: none;
}

/* User header inside settings dropdown */
.settings-user-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
}

.settings-user-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.settings-user-name {
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.settings-admin-badge {
  display: inline-block;
  background: var(--badge-bg);
  color: var(--text-primary);
  border: 1px solid var(--overlay-border);
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 0.625rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  width: fit-content;
}

.settings-logout-item {
  color: var(--status-error);
}

.settings-logout-item:hover {
  background: color-mix(in srgb, var(--status-error) 8%, transparent);
}

/* Remote access status inside settings */
.settings-tunnel-status {
  color: var(--status-success);
  font-size: 0.8125rem;
  text-decoration: none;
}

.settings-tunnel-status--connecting {
  color: var(--status-warning, #f59e0b);
}

.settings-tunnel-label {
  white-space: nowrap;
}

.settings-tunnel-url {
  min-width: 0;
  margin-left: auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.6875rem;
  color: var(--text-secondary);
}

.settings-tunnel-status--error {
  color: var(--status-error, #ef4444);
}

.tunnel-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}

.settings-tunnel-status--connecting .tunnel-dot,
.settings-tunnel-badge--connecting {
  animation: tunnel-pulse 1.1s ease-in-out infinite;
}

/* Small dot on the settings button while remote access is on / failing */
.settings-button {
  position: relative;
}

.settings-tunnel-badge {
  position: absolute;
  top: 4px;
  right: 4px;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--status-success);
  box-shadow: 0 0 0 2px var(--bg-primary);
}

.settings-tunnel-badge--connecting {
  background: var(--status-warning, #f59e0b);
}

.settings-tunnel-badge--error {
  background: var(--status-error, #ef4444);
}

@keyframes tunnel-pulse {
  50% { opacity: 0.35; }
}

.version-badge {
  color: var(--text-secondary);
  padding: 3px 8px;
  border-radius: 4px;
  font-size: 0.6875rem;
  font-weight: 500;
  letter-spacing: 0.02em;
  font-family: var(--font-mono);
  border: 1px solid var(--border-color);
  background: var(--overlay-bg);
  position: relative;
  cursor: pointer;
}

.version-badge:hover {
  color: var(--text-primary);
}

.version-badge__dot {
  position: absolute;
  top: -3px;
  right: -3px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent-purple);
}

.settings-whats-new-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent-purple);
}

@keyframes slideDown {
  from {
    opacity: 0;
    transform: translateY(-10px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.app-layout {
  position: relative;
  display: flex;
  flex: 1;
  overflow: hidden;
  min-height: 0;
}

.editor-workspace { display: flex; flex: 1; min-width: 0; min-height: 0; overflow: hidden; }
.overview-toggle { border: 1px solid var(--border-color); border-radius: 6px; background: transparent; color: var(--text-secondary); font-size: 22px; cursor: pointer; padding: 2px 9px; }
.editor-workspace :deep(.sidebar), .editor-workspace :deep(.sessions-sidebar), .editor-workspace :deep(.metrics-sidebar) { transition: transform .32s cubic-bezier(.22,.7,.25,1), opacity .25s ease; }
.editor-workspace :deep(.chat-area-with-metrics) { transition: opacity .22s ease; }
.overview-open .editor-workspace :deep(.sidebar), .overview-open .editor-workspace :deep(.sessions-sidebar) { transform: translateX(-110%); opacity: 0; }
.overview-open .editor-workspace :deep(.metrics-sidebar) { transform: translateX(110%); opacity: 0; }
.overview-open .editor-workspace :deep(.chat-area-with-metrics) { opacity: 0; }
.overview-reveal-enter-active { transition: opacity .28s .1s ease, transform .35s .08s cubic-bezier(.22,.7,.25,1); }
.overview-reveal-leave-active { transition: opacity .18s ease, transform .22s ease; }
.overview-reveal-enter-from, .overview-reveal-leave-to { opacity: 0; transform: translateY(18px) scale(.985); }
@media (prefers-reduced-motion: reduce) {
  .editor-workspace :deep(.sidebar), .editor-workspace :deep(.sessions-sidebar), .editor-workspace :deep(.metrics-sidebar), .editor-workspace :deep(.chat-area-with-metrics), .overview-reveal-enter-active, .overview-reveal-leave-active { transition: none; }
}

.main-content {
  flex: 1;
  overflow: hidden;
  transition: all 0.3s ease;
  min-height: 0;
}

.main-content-expanded {
  margin-left: 0;
}

/* Mobile sidebar backdrop overlay */
.sidebar-backdrop {
  display: none;
}

@media (max-width: 768px) {
  .navbar {
    padding: 0.25rem 0;
    overflow: visible;
  }

  .navbar-container {
    padding: 0 8px;
    gap: 4px;
    overflow: visible;
  }

  .nav-left {
    gap: 0.25rem;
    flex-shrink: 0;
  }

  .nav-right {
    gap: 0.25rem;
    flex-shrink: 1;
    overflow: visible;
  }


  .version-badge {
    display: none;
  }

  .nav-project-label {
    display: none;
  }

  .nav-logo-link {
    gap: 0.35rem;
  }


  /* Keep the settings panel within the viewport on small screens. */
  .settings-dropdown {
    max-height: calc(100dvh - 64px);
    overflow-y: auto;
    overscroll-behavior: contain;
  }

  /* Sidebar becomes a slide-over overlay on mobile */
  .sidebar-backdrop {
    display: block;
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 1999;
    animation: fadeIn 0.2s ease;
  }

  @keyframes fadeIn {
    from { opacity: 0; }
    to { opacity: 1; }
  }

  /* Main content takes full width on mobile */
  .main-content {
    margin-left: 0 !important;
  }

}

@media (max-width: 480px) {
  .navbar-container {
    padding: 0 10px;
  }

  .settings-button {
    width: 32px;
    height: 32px;
  }

}


</style>