<template>
  <div class="project-selector">
    <button
      class="selector-button"
      :aria-label="`Switch project: ${currentProject?.name || 'No project'}`"
      :title="shortcutLabels.title('Switch project', 'project.switch')"
      :aria-keyshortcuts="shortcutLabels.aria('project.switch')"
      @click="openProjectSelector?.()"
    >
      <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" aria-hidden="true">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
      </svg>
      <span v-if="currentProject?.color" class="selector-color-dot" :style="{ backgroundColor: currentProject.color }" aria-hidden="true"></span>
      <span class="current-project">{{ currentProject?.name || 'No Project' }}</span>
      <svg class="chevron" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
        <path fill-rule="evenodd" d="M5.293 7.293a1 1 0 011.414 0L10 10.586l3.293-3.293a1 1 0 111.414 1.414l-4 4a1 1 0 01-1.414 0l-4-4a1 1 0 010-1.414z" clip-rule="evenodd" />
      </svg>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, inject, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import type { Project } from '~/types/projects'
import { useSessionStore } from '~/stores/session/sessionStore'
import { useShortcutLabels } from '~/composables/useKeyboardShortcuts'

// The header button opens the app-wide project picker (ProjectSelectorModal,
// mounted in app.vue), the same one ⇧⌘P opens.
const openProjectSelector = inject<(() => void) | undefined>('openProjectSelector', undefined)
const shortcutLabels = useShortcutLabels()

const { fetchWithAuth } = useAuthenticatedFetch()
const sessionStore = useSessionStore()
const { selectedProject } = storeToRefs(sessionStore)

// Use sessionStore as single source of truth for selected project.
// If the store value is null (e.g. component remounted during page transition),
// instantly recover from the full cached project object in localStorage.
const currentProject = computed(() => {
  if (selectedProject.value) return selectedProject.value

  // Instant fallback: recover from cached project object in localStorage
  if (typeof window !== 'undefined') {
    const cachedObj = localStorage.getItem('selectedProjectObj')
    if (cachedObj) {
      try {
        const project = JSON.parse(cachedObj)
        if (project?.id && project?.name) {
          // Re-sync the store
          sessionStore.syncSelectedProjectForSession(project)
          return project
        }
      } catch {}
    }
  }

  return null
})

onMounted(async () => {
  // Only restore from localStorage if the store doesn't already have a selected project
  // (e.g., it was already set by navigating to a project detail page)
  if (sessionStore.selectedProject) return
  try {
    const response = await fetchWithAuth('/api/projects', { method: 'GET' })
    const data = await response.json()
    if (!sessionStore.selectedProject) {
      sessionStore.restoreSelectedProject((data.projects || []) as Project[])
    }
  } catch (error) {
    console.error('Failed to fetch projects:', error)
  }
})
</script>

<style scoped>
.project-selector {
  position: relative;
}

.selector-button {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.85rem;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 0.5rem;
  color: var(--text-primary);
  font-size: 0.85rem;
  font-family: inherit;
  min-height: 36px;
  cursor: pointer;
  transition: all 0.2s;
  max-width: 200px;
}

.selector-button:hover {
  background: var(--card-hover);
  border-color: var(--accent-purple);
}

.selector-color-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  box-shadow: 0 0 0 1px var(--overlay-border);
}

.selector-button:focus-visible {
  outline: 2px solid var(--accent-cyan);
  outline-offset: 2px;
}

.selector-button-active {
  border-color: var(--accent-purple);
  box-shadow: 0 0 0 3px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.1);
}

.icon {
  width: 1.25rem;
  height: 1.25rem;
  flex-shrink: 0;
}

.current-project {
  flex: 1;
  text-align: left;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.chevron {
  width: 1rem;
  height: 1rem;
  flex-shrink: 0;
  opacity: 0.6;
}

@media (max-width: 768px) {
  .selector-button {
    padding: 0.375rem 0.625rem;
    font-size: 0.8rem;
    gap: 0.375rem;
    max-width: 150px;
  }

  .icon {
    width: 1rem;
    height: 1rem;
  }

  .chevron {
    width: 0.85rem;
    height: 0.85rem;
  }
}

@media (max-width: 480px) {
  .selector-button {
    max-width: 132px;
    min-height: 34px;
    padding: 0.375rem 0.5rem;
    font-size: 0.78rem;
  }

  /* The colour dot says which project it is; the folder icon is noise. */
  .selector-button .icon {
    display: none;
  }
}
</style>
