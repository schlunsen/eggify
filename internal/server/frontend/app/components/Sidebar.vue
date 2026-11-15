<template>
  <nav class="sidebar" :class="{ 'sidebar-collapsed': isCollapsed }" aria-label="Main">
    <div
      v-if="projectColor && colorStyle !== 'off'"
      class="sidebar-color-gradient"
      :class="{ 'sidebar-color-glow': colorGlow === 'on' }"
      :style="sidebarGradientStyle"
    ></div>
    <div class="sidebar-content">
      <div class="sidebar-scroll">
        <div v-for="group in navGroups" :key="group.label" class="nav-group">
          <div v-if="!isCollapsed" class="nav-group-label">{{ group.label }}</div>
          <div v-else class="nav-group-divider" aria-hidden="true"></div>
          <NuxtLink
            v-for="item in group.items"
            :key="item.to"
            :to="item.to"
            class="nav-item"
            :class="{ 'nav-item-active': isActive(item.to) }"
            :aria-current="isActive(item.to) ? 'page' : undefined"
            :title="isCollapsed ? item.label : undefined"
          >
            <span class="nav-icon">
              <!-- eslint-disable-next-line vue/no-v-html -- static icon markup from this file -->
              <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" v-html="item.icon"></svg>
              <span
                v-if="item.badge && reviewCounts[item.badge] > 0"
                class="review-badge"
                :title="`${reviewCounts[item.badge]} agent-written ${item.badge} waiting for your review`"
              >{{ reviewCounts[item.badge] }}</span>
            </span>
            <span class="nav-label" v-show="!isCollapsed">{{ item.label }}</span>
          </NuxtLink>
        </div>
      </div>

      <div class="sidebar-footer">
        <NuxtLink
          v-for="item in footerItems"
          :key="item.to"
          :to="item.to"
          class="nav-item"
          :class="{ 'nav-item-active': isActive(item.to) }"
          :aria-current="isActive(item.to) ? 'page' : undefined"
          :title="isCollapsed ? item.label : undefined"
        >
          <span class="nav-icon">
            <!-- eslint-disable-next-line vue/no-v-html -- static icon markup from this file -->
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" v-html="item.icon"></svg>
          </span>
          <span class="nav-label" v-show="!isCollapsed">{{ item.label }}</span>
        </NuxtLink>

        <button
          type="button"
          class="nav-item sidebar-collapse-toggle"
          :title="isCollapsed ? 'Expand sidebar' : 'Collapse sidebar'"
          :aria-label="isCollapsed ? 'Expand sidebar' : 'Collapse sidebar'"
          :aria-expanded="!isCollapsed"
          @click="toggleSidebar"
        >
          <span class="nav-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <rect width="18" height="18" x="3" y="3" rx="2"/>
              <path d="M9 3v18"/>
              <path class="collapse-chevron" :class="{ 'chevron-collapsed': isCollapsed }" d="m16 15-3-3 3-3"/>
            </svg>
          </span>
          <span class="nav-label" v-show="!isCollapsed">Collapse</span>
        </button>
      </div>
    </div>
  </nav>
</template>

<script setup>
import { computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { storeToRefs } from 'pinia'
import { usePendingReview } from '~/composables/usePendingReview'
import { useSessionStore } from '~/stores/session/sessionStore'
import { useProjectColorSettings } from '~/composables/useProjectColorSettings'

// Inner SVG markup for each nav icon (24x24, stroke-based, Lucide-style).
const ICONS = {
  agents: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/><path d="M8 9h8"/><path d="M8 13h5"/>',
  projects: '<path d="M3 7v10a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-6l-2-2H5a2 2 0 0 0-2 2z"/>',
  git: '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
  skills: '<path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/>',
  hooks: '<path d="M18 16.98h-5.99c-1.1 0-1.95.94-2.48 1.9A4 4 0 0 1 2 17c.01-.7.2-1.4.57-2"/><path d="m6 17 3.13-5.78c.53-.97.1-2.18-.5-3.1a4 4 0 1 1 6.89-4.06"/><path d="m12 6 3.13 5.73C15.66 12.7 16.9 13 18 13a4 4 0 0 1 0 8"/>',
  connectors: '<path d="M12 22v-5"/><path d="M9 8V2"/><path d="M15 8V2"/><path d="M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8Z"/>',
  providers: '<rect width="16" height="16" x="4" y="4" rx="2"/><rect width="6" height="6" x="9" y="9" rx="1"/><path d="M15 2v2"/><path d="M15 20v2"/><path d="M2 15h2"/><path d="M2 9h2"/><path d="M20 15h2"/><path d="M20 9h2"/><path d="M9 2v2"/><path d="M9 20v2"/>',
  processes: '<rect x="2" y="3" width="20" height="7" rx="2"/><rect x="2" y="14" width="20" height="7" rx="2"/><line x1="6" y1="6.5" x2="6.01" y2="6.5"/><line x1="6" y1="17.5" x2="6.01" y2="17.5"/>',
  stats: '<path d="M3 3v18h18"/><path d="M18 17V9"/><path d="M13 17V5"/><path d="M8 17v-3"/>',
  settings: '<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>',
  help: '<circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><line x1="12" y1="17" x2="12.01" y2="17"/>'
}

const navGroups = [
  {
    label: 'Workspace',
    items: [
      { to: '/agents', label: 'Agents', icon: ICONS.agents },
      { to: '/projects', label: 'Projects', icon: ICONS.projects, badge: 'memories' },
      { to: '/git', label: 'Git Search', icon: ICONS.git }
    ]
  },
  {
    label: 'Toolkit',
    items: [
      { to: '/skills', label: 'Skills', icon: ICONS.skills, badge: 'skills' },
      { to: '/hooks', label: 'Hooks', icon: ICONS.hooks },
      { to: '/connectors', label: 'Connectors', icon: ICONS.connectors },
      { to: '/providers', label: 'Providers', icon: ICONS.providers }
    ]
  },
  {
    label: 'System',
    items: [
      { to: '/process-manager', label: 'Processes', icon: ICONS.processes },
      { to: '/stats', label: 'Stats', icon: ICONS.stats }
    ]
  }
]

const footerItems = [
  { to: '/settings', label: 'Settings', icon: ICONS.settings },
  { to: '/help', label: 'Help', icon: ICONS.help }
]

// Use sidebar state
const { isCollapsed, toggleSidebar, collapseSidebar } = useSidebar()
const route = useRoute()

// A nav item stays highlighted on its sub-pages (e.g. /projects/:id).
const isActive = (to) => route.path === to || route.path.startsWith(`${to}/`)

// Pending review badges (agent-written memories/skills). Refreshed on
// navigation and once a minute.
const { counts: reviewCounts, loadCounts: loadReviewCounts } = usePendingReview()
let reviewTimer = null
onMounted(() => {
  loadReviewCounts()
  reviewTimer = setInterval(loadReviewCounts, 60000)
})
onBeforeUnmount(() => {
  if (reviewTimer) clearInterval(reviewTimer)
})
watch(() => route.path, () => loadReviewCounts())

// Auto-collapse sidebar on mobile when navigating to a new page
watch(() => route.path, () => {
  if (typeof window !== 'undefined' && window.innerWidth <= 768 && !isCollapsed.value) {
    collapseSidebar()
  }
})

// Get selected project color for gradient
const sessionStore = useSessionStore()
const { selectedProject } = storeToRefs(sessionStore)
const projectColor = computed(() => selectedProject.value?.color || '')

// Project color display settings
const { colorStyle, colorGlow } = useProjectColorSettings()

const sidebarGradientStyle = computed(() => {
  const c = projectColor.value
  if (!c) return {}
  if (colorStyle.value === 'solid') {
    return { background: `${c}25` }
  }
  // gradient (default)
  return { background: `linear-gradient(to right, ${c}40, ${c}20 50%, ${c}08 75%, transparent 100%)` }
})
</script>

<style scoped>
.sidebar {
  --nav-accent: var(--accent-purple);
  --nav-active-bg: color-mix(in srgb, var(--nav-accent) 13%, transparent);
  --nav-hover-bg: color-mix(in srgb, var(--text-primary) 6%, transparent);

  width: 264px;
  background: var(--card-bg);
  border-right: 1px solid var(--border-color);
  transition: width 0.25s ease;
  flex-shrink: 0;
  position: relative;
  overflow: hidden;
}

.sidebar-color-gradient {
  position: absolute;
  inset: 0;
  pointer-events: none;
  z-index: 0;
  transition: background 0.3s ease;
  border-radius: inherit;
}

.sidebar-color-glow {
  animation: sidebarGlow 3s ease-in-out infinite;
}

@keyframes sidebarGlow {
  0%, 100% { opacity: 0.7; }
  50% { opacity: 1; }
}

.sidebar-collapsed {
  width: 76px;
}

.sidebar-content {
  display: flex;
  flex-direction: column;
  height: 100%;
  position: relative;
  z-index: 1;
}

.sidebar-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 22px 12px 8px;
  scrollbar-width: thin;
}

.nav-group + .nav-group {
  margin-top: 18px;
}

.nav-group-label {
  padding: 0 14px 8px;
  font-family: var(--font-primary);
  font-size: 0.7rem;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--text-muted);
  white-space: nowrap;
}

.nav-group-divider {
  height: 1px;
  margin: 0 14px 10px;
  background: var(--border-color);
}

.nav-group:first-child .nav-group-divider {
  display: none;
}

.nav-item {
  position: relative;
  display: flex;
  align-items: center;
  gap: 14px;
  width: 100%;
  min-height: 46px;
  padding: 0 14px;
  margin-bottom: 2px;
  border: none;
  border-radius: 10px;
  background: transparent;
  color: var(--text-secondary);
  font-family: var(--font-primary);
  font-size: 0.975rem;
  font-weight: 500;
  text-align: left;
  text-decoration: none;
  white-space: nowrap;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;
}

.nav-item:hover {
  background: var(--nav-hover-bg);
  color: var(--text-primary);
}

.nav-item-active,
.nav-item-active:hover {
  background: var(--nav-active-bg);
  color: var(--nav-accent);
  font-weight: 600;
}

/* Accent bar on the leading edge of the active item */
.nav-item-active::before {
  content: '';
  position: absolute;
  left: -12px;
  top: 10px;
  bottom: 10px;
  width: 3px;
  border-radius: 0 3px 3px 0;
  background: var(--nav-accent);
}

.sidebar-collapsed .nav-item {
  justify-content: center;
  padding: 0;
}

.nav-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  position: relative;
}

.nav-label {
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Count of agent-written items waiting for review */
.review-badge {
  position: absolute;
  top: -7px;
  right: -10px;
  min-width: 17px;
  height: 17px;
  padding: 0 4px;
  border-radius: 999px;
  background: #f59e0b;
  color: #111;
  font-size: 0.62rem;
  font-weight: 700;
  line-height: 17px;
  text-align: center;
  box-shadow: 0 0 0 2px var(--card-bg);
}

.sidebar-footer {
  flex-shrink: 0;
  padding: 10px 12px 12px;
  border-top: 1px solid var(--border-color);
}

.sidebar-collapse-toggle {
  color: var(--text-muted);
  font-size: 0.9rem;
}

.collapse-chevron {
  transform-origin: 14.5px 12px;
  transition: transform 0.25s ease;
}

.chevron-collapsed {
  transform: scaleX(-1);
}

@media (prefers-reduced-motion: reduce) {
  .sidebar,
  .collapse-chevron,
  .nav-item {
    transition: none;
  }
}

@media (max-width: 768px) {
  /* On mobile, sidebar is always hidden by default and slides in as an overlay */
  .sidebar {
    position: fixed;
    top: 0;
    left: 0;
    bottom: 0;
    z-index: 2000;
    width: 280px;
    transform: translateX(-100%);
    transition: transform 0.3s ease;
    box-shadow: 4px 0 24px var(--shadow-color);
  }

  /* When sidebar is open (NOT collapsed), slide it in */
  .sidebar:not(.sidebar-collapsed) {
    transform: translateX(0);
  }

  /* When collapsed on mobile, keep it hidden and same width for smooth animation */
  .sidebar.sidebar-collapsed {
    width: 280px;
    transform: translateX(-100%);
  }

  .sidebar-scroll {
    padding-top: 20px;
  }

  /* Always show labels on mobile since sidebar is an overlay */
  .nav-label {
    display: inline !important;
  }

  .sidebar-collapsed .nav-item {
    justify-content: flex-start;
    padding: 0 14px;
  }

  .sidebar-collapse-toggle {
    display: none;
  }
}
</style>
