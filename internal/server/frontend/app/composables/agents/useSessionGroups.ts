import { computed, ref, type Ref } from 'vue'
import { useSessionStore } from '~/stores/session/sessionStore'
import { useBackgroundAgents } from '~/composables/agents/useBackgroundAgents'
import { subagentsWorkingCount } from '~/utils/agents/subagentsWorking'
import { useLoopClock } from '~/utils/agents/scheduledLoop'
import {
  DEFAULT_COLLAPSED,
  groupSessions,
  visibleSessionOrder,
  waitingLabel,
  type GroupableSession,
  type SessionGroupId,
} from '~/utils/agents/sessionGroups'

export const SIDEBAR_COLLAPSED_GROUPS_KEY = 'eggify-sidebar-collapsed-groups'

function loadCollapsed(): Record<string, boolean> {
  try {
    const raw = JSON.parse(localStorage.getItem(SIDEBAR_COLLAPSED_GROUPS_KEY) || '{}')
    if (raw && typeof raw === 'object' && !Array.isArray(raw)) {
      const out: Record<string, boolean> = { ...DEFAULT_COLLAPSED }
      for (const [k, v] of Object.entries(raw)) if (typeof v === 'boolean') out[k] = v
      return out
    }
  } catch { /* blocked storage or bad JSON */ }
  return { ...DEFAULT_COLLAPSED }
}

// Shared by the sidebar (what it shows) and the page's session shortcuts (the
// order they step through), so both agree on which groups are collapsed.
const collapsed = ref<Record<string, boolean>>(loadCollapsed())

/** Re-reads the saved state (tests, or another tab). */
export function reloadCollapsedGroups(): void {
  collapsed.value = loadCollapsed()
}

export function toggleSessionGroup(id: SessionGroupId): void {
  collapsed.value = { ...collapsed.value, [id]: !(collapsed.value[id] ?? DEFAULT_COLLAPSED[id] ?? false) }
  try {
    localStorage.setItem(SIDEBAR_COLLAPSED_GROUPS_KEY, JSON.stringify(collapsed.value))
  } catch { /* blocked storage: the state lasts for this page load */ }
}

type ListedSession = GroupableSession & {
  subagents_running?: number
  pending_permissions?: number
  pending_questions?: number
}

/** The sidebar's groups (utils/agents/sessionGroups.ts) for a list of sessions, plus their on-screen order. */
export function useSessionGroups<T extends ListedSession>(sessions: Ref<readonly T[] | null | undefined>) {
  const sessionStore = useSessionStore()
  const { getAgentsForSession, hasFullList } = useBackgroundAgents()
  const now = useLoopClock()

  const groups = computed(() => groupSessions<T>(Array.isArray(sessions.value) ? sessions.value : [], {
    now: now.value,
    waitingFor: s => waitingLabel(sessionStore.permissions[s.id] as any, sessionStore.questions[s.id] as any, s),
    subagentsWorking: s => subagentsWorkingCount(getAgentsForSession(s.id), hasFullList(s.id), s.subagents_running),
  }))

  const order = computed(() => visibleSessionOrder(groups.value, collapsed.value))

  return { groups, order, collapsed, toggleGroup: toggleSessionGroup, now }
}
