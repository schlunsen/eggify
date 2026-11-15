import { watch } from 'vue'
import type { RouteLocationNormalizedLoaded, Router } from 'vue-router'
import type { useSessionStore } from '../../stores/session/sessionStore'

// Resolve the project before selecting a linked session, using the editor's
// normal selection callback to subscribe and load its conversation history.
export function useAgentRouteNavigation({ route, router, sessionStore, connected, selectSession, fetchWithAuth, startNewSession }: {
  route: RouteLocationNormalizedLoaded
  router: Pick<Router, 'replace'>
  sessionStore: ReturnType<typeof useSessionStore>
  connected: () => boolean
  selectSession: (id: string) => Promise<void>
  fetchWithAuth: (url: string) => Promise<Response>
  /**
   * Opens Create Session for a project (from ?project=<id>&new_session=1), in
   * `folder` when ?cwd= names a folder inside the project (a worktree).
   */
  startNewSession?: (project: { id: string, path: string }, folder?: string) => unknown
}) {
  // Watch for project query parameter changes in URL
  // Only react when the param actively changes while on the agents page
  // (not when navigating away to a different route)
  watch([() => route.query.project, () => route.query.session], async ([projectId], previous) => {
    const oldProjectId = previous?.[0]
    // Skip if we've navigated away from the agents page — the watcher can fire
    // during route transitions before the component is unmounted
    if (route.path !== '/agents') return

    if (projectId && typeof projectId === 'string') {
      // If the store already has this project selected, skip
      if (sessionStore.selectedProject?.id === projectId) return

      // Fetch the full project object before setting it
      try {
        const response = await fetchWithAuth(`/api/projects/${projectId}`)
        if (response.ok) {
          const { project } = await response.json()
          // Ignore a late response if another navigation has already started.
          if (route.query.project !== projectId || route.path !== '/agents') return
          if (!project || project.id !== projectId) throw new Error('Invalid project response')
          sessionStore.setSelectedProject(project)
        }
      } catch (err) {
        console.error('Failed to fetch project for selector:', err)
      }
    } else if (projectId === undefined && oldProjectId !== undefined) {
      // Only clear if the project param was explicitly removed (had a value before)
      sessionStore.setSelectedProject(null)
    }
  }, { immediate: true })

  // Overview links also work when arriving from another page. Wait until both
  // the session list and its project are ready before using normal selection.
  watch([
    () => route.path === '/agents' ? route.query.session : undefined,
    () => connected(),
    () => sessionStore.sessions.some(s => s.id === route.query.session),
    () => !route.query.project || sessionStore.selectedProject?.id === route.query.project
  ] as const, async ([sessionId, connected, exists, projectReady]) => {
    if (typeof sessionId === 'string' && connected && exists && projectReady) {
      await selectSession(sessionId)
      if (route.query.session === sessionId) {
        const { session: _selected, ...query } = route.query
        await router.replace({ path: route.path, query })
      }
    }
  }, { immediate: true })

  // "Start a session here" links: once connected and the project is selected,
  // open Create Session prefilled with the project's folder, then drop the flag.
  if (startNewSession) {
    watch([
      () => route.path === '/agents' ? route.query.new_session : undefined,
      () => connected(),
      () => sessionStore.selectedProject?.id,
    ] as const, async ([flag, isConnected, selectedId]) => {
      if (!flag || !isConnected) return
      const projectId = typeof route.query.project === 'string' ? route.query.project : undefined
      if (projectId && selectedId !== projectId) return
      const project = sessionStore.selectedProject as { id: string, path: string } | null
      const cwd = typeof route.query.cwd === 'string' ? route.query.cwd : ''
      const { new_session: _flag, cwd: _cwd, ...query } = route.query
      await router.replace({ path: route.path, query })
      if (project?.path) await startNewSession(project, folderInProject(project.path, cwd))
    }, { immediate: true })
  }
}

/** cwd when it is a folder inside the project's folder (a worktree), else the project's folder. */
export function folderInProject(projectPath: string, cwd: string): string {
  const root = projectPath.replace(/\/+$/, '')
  if (!cwd || cwd.includes('/../') || cwd.endsWith('/..') || !cwd.startsWith(root + '/')) return projectPath
  return cwd
}
