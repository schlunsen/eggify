<script setup>
import { ref, onMounted, watch, provide } from 'vue'
import '../assets/css/main.css'
import { useAgentWebSocket } from '~/composables/useAgentWebSocket'
import { getProjectSubscriptionManager } from '~/composables/agents/useProjectSubscriptionManager'
import { useProjectSubscriptionsStore } from '~/stores/projects/projectSubscriptionsStore'
import { useAppShortcuts } from '~/composables/useAppShortcuts'
import { useThemeShortcuts } from '~/composables/useThemeShortcuts'
import { useAuth } from '~/composables/useAuth'
import { useTheme } from '~/composables/useTheme'
import { useSettingsStore } from '~/stores/settings/settingsStore'
import { useAgentRuntimes } from '~/composables/useAgentRuntimes'
import { isNotchPath } from '~/utils/notch/notchRoute'

// Initialize theme system
const { isDark } = useTheme()

// Initialize authentication
const { checkAuthStatus, isAuthenticated, authEnabled, user: authUser } = useAuth()

// The Notch Theatre window (/notch) gets the page alone: no agent WebSocket
// (it would take the sessions' approvals from the main window), no overlays.
const route = useRoute()
const bare = isNotchPath(route.path)

// Initialize global WebSocket connection at app level (persists across page navigation)
// This prevents creating a new connection every time the user navigates to /agents
const agentWs = useAgentWebSocket()

// Initialize Pinia project subscriptions store with WebSocket instance
const projectSubscriptionsStore = useProjectSubscriptionsStore()
projectSubscriptionsStore.initializeWebSocket(agentWs)

// Keep legacy subscription manager for backward compatibility
const subscriptionManager = getProjectSubscriptionManager()
subscriptionManager.setWebSocketInstance(agentWs)

// Make the WebSocket instance available to all child components via provide/inject
provide('agentWs', agentWs)
provide('subscriptionManager', subscriptionManager)
provide('projectSubscriptionsStore', projectSubscriptionsStore)

// Project selector modal state
const showProjectSelector = ref(false)
// The header's project button opens the same picker as ⇧⌘P.
provide('openProjectSelector', () => { showProjectSelector.value = true })

// App-wide shortcuts (registry: composables/useKeyboardShortcuts.ts)
useAppShortcuts({
  openProjectSelector: () => { showProjectSelector.value = true },
  navigate: path => navigateTo(path),
  enabled: () => isAuthenticated.value || !authEnabled.value
})

// Next / previous theme (⌥⌘] / ⌥⌘[): every page, signed in or not
useThemeShortcuts()

// Initialize settings store
const settingsStore = useSettingsStore()

// Agent CLI onboarding: the desktop app opens "Set up your agents" once, on
// first startup (after sign-in, since logging a CLI in needs a signed-in user).
const agentRuntimes = useAgentRuntimes()
const maybeShowAgentOnboarding = () => {
  if (bare) return
  if ((isAuthenticated.value || !authEnabled.value) && agentRuntimes.shouldShowFirstRun()) {
    agentRuntimes.openWizard('first-run')
  }
}

onMounted(async () => {
  // Check authentication status on app mount
  await checkAuthStatus()

  // Sync user profile to settings store with avatar data
  if (isAuthenticated.value && authUser.value) {
    settingsStore.setUser({
      username: authUser.value.username,
      email: authUser.value.email,
      isAdmin: authUser.value.isAdmin,
      avatarId: authUser.value.avatar_id || null,
      avatarImage: authUser.value.avatar_image,
      avatarName: authUser.value.avatar_name,
      avatarColor: authUser.value.avatar_color
    })
  }

  // Connect WebSocket if authenticated
  if (isAuthenticated.value && !bare) {
    agentWs.reconnect()
  }
  maybeShowAgentOnboarding()
})

// Watch for authentication changes and reconnect WebSocket when user logs in
watch(isAuthenticated, (newAuthStatus) => {
  if (newAuthStatus) {
    if (bare) return
    // User just logged in, connect the WebSocket with the new API key
    agentWs.reconnect()
    maybeShowAgentOnboarding()
  } else {
    // User logged out, disconnect WebSocket
    agentWs.disconnect()
    settingsStore.logout()
  }
})

// Watch for user profile changes and sync to settings store
watch(authUser, (newUser) => {
  if (newUser) {
    settingsStore.setUser({
      username: newUser.username,
      email: newUser.email,
      isAdmin: newUser.isAdmin,
      avatarId: newUser.avatar_id || null,
      avatarImage: newUser.avatar_image,
      avatarName: newUser.avatar_name,
      avatarColor: newUser.avatar_color
    })
  }
}, { deep: true })
</script>

<template>
  <div class="app-root">
    <NuxtRouteAnnouncer />
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>

    <template v-if="!bare">
      <!-- Command palette (⌘K) -->
      <CommandPalette v-if="isAuthenticated || !authEnabled" />

      <!-- Project Selector Modal -->
      <ProjectSelectorModal
        v-model="showProjectSelector"
      />

      <!-- Questions / approvals from sessions you are not looking at -->
      <NeedsYouToast v-if="isAuthenticated || !authEnabled" />

      <!-- "Requires a signed-in user" refusals from the server -->
      <SignInRequiredToast />

      <!-- Theme name after the next/previous theme shortcut -->
      <ThemeToast />

      <!-- Wails desktop app only: "Update available — Restart to update" -->
      <DesktopUpdateToast />

      <!-- Agent CLI onboarding (first desktop startup, Create Session nudge, Settings) -->
      <OnboardingAgentSetupWizard v-if="isAuthenticated || !authEnabled" />
    </template>
  </div>
</template>

<style scoped>
.app-root {
  width: 100%;
  height: 100vh;
  overflow: hidden;
}
</style>
