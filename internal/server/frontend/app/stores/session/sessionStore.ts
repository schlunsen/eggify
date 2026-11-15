/**
 * Session Store
 *
 * Manages agent sessions, messages, and permissions with full CRUD operations.
 * Provides a single source of truth for session state across the application.
 */

import { defineStore } from 'pinia'
import { toRaw } from 'vue'
import type {
  Message,
  Permission,
  UserQuestion,
  Session,
  SessionFilter,
  SessionState,
  ToolUse,
  Project
} from './types'

/**
 * id → index lookup for a session's message array, so upserts don't scan the
 * whole list. Kept outside reactive state and keyed by the raw array: every
 * store mutation replaces the array, so an entry is only trusted while its
 * recorded length still matches, and a hit is verified before use.
 */
interface MessageIndex {
  byId: Map<string, number>
  length: number
}
const messageIndexCache = new WeakMap<object, MessageIndex>()

function buildMessageIndex(raw: Message[]): MessageIndex {
  const byId = new Map<string, number>()
  for (let i = 0; i < raw.length; i++) {
    const id = raw[i]?.id
    // Keep the first occurrence, matching findIndex semantics
    if (id !== undefined && !byId.has(id)) byId.set(id, i)
  }
  const index = { byId, length: raw.length }
  messageIndexCache.set(raw, index)
  return index
}

function getMessageIndex(list: Message[]): MessageIndex {
  const raw = toRaw(list)
  const cached = messageIndexCache.get(raw)
  if (cached && cached.length === raw.length) return cached
  return buildMessageIndex(raw)
}

/** Index of the message with `id` in `list`, or -1 (same result as findIndex). */
function findMessageIndex(list: Message[], id: string): number {
  const raw = toRaw(list)
  let index = getMessageIndex(raw)
  let i = index.byId.get(id)
  if (i !== undefined && raw[i]?.id === id) return i
  if (i !== undefined) {
    // Array was changed in place since the index was built — rebuild once
    index = buildMessageIndex(raw)
    i = index.byId.get(id)
    return i !== undefined ? i : -1
  }
  return -1
}

/**
 * Upsert `incoming` into a copy of `current`: existing ids are replaced in
 * place, new ones appended in order. Returns the new array (one copy total)
 * and registers its id index so later lookups stay O(1).
 */
function upsertMessages(current: Message[], incoming: Message[]): Message[] {
  const raw = toRaw(current)
  // The id map is handed over to the new array (it only ever grows, and hits
  // are verified, so the old array can't be misled by it).
  const byId = getMessageIndex(raw).byId
  messageIndexCache.delete(raw)
  const next = raw.slice()
  for (const message of incoming) {
    let i = byId.get(message.id)
    if (i !== undefined && next[i]?.id !== message.id) {
      // Stale entry — fall back to a scan
      i = next.findIndex((m) => m.id === message.id)
      if (i < 0) i = undefined
      else byId.set(message.id, i)
    }
    if (i !== undefined) {
      next[i] = message
    } else {
      byId.set(message.id, next.length)
      next.push(message)
    }
  }
  messageIndexCache.set(next, { byId, length: next.length })
  return next
}

/** How many recently opened sessions keep their messages in memory. */
export const KEEP_MESSAGES_FOR_SESSIONS = 5

export const useSessionStore = defineStore('session', {
  state: (): SessionState => ({
    sessions: [],
    activeSessionId: null,
    messages: {},
    messagesLoaded: new Set(),
    hasMoreMessages: {} as Record<string, boolean>,
    recentSessionIds: [],
    permissions: {},
    questions: {},
    loading: false,
    error: null,
    filter: 'all',
    selectedProject: null,
    recentActivity: {}
  }),

  getters: {
    /**
     * Get the currently active session
     */
    activeSession: (state) => {
      if (!state.activeSessionId) return null
      const sessions = Array.isArray(state.sessions) ? state.sessions : []
      return sessions.find((s) => s.id === state.activeSessionId) || null
    },

    /**
     * Get messages for the active session
     */
    activeMessages: (state) => {
      if (!state.activeSessionId) return []
      return state.messages[state.activeSessionId] || []
    },

    /**
     * Get permissions for the active session
     */
    activePermissions: (state) => {
      if (!state.activeSessionId) return []
      return state.permissions[state.activeSessionId] || []
    },

    /**
     * Get questions for the active session
     */
    activeQuestions: (state) => {
      if (!state.activeSessionId) return []
      return state.questions[state.activeSessionId] || []
    },

    /**
     * Get sessions filtered by status and project
     */
    filteredSessions: (state) => {
      const sessions = Array.isArray(state.sessions) ? state.sessions : []

      const filtered = sessions.filter((session: any) => {
        // Defensive check - ensure session object exists
        if (!session || typeof session !== 'object') return false

        // Apply status filter
        // 'active' means NOT ended (includes idle, processing, etc.)
        const statusMatch =
          state.filter === 'all' ||
          (state.filter === 'active' && session.status !== 'ended') ||
          (state.filter === 'ended' && session.status === 'ended')

        // Apply project filter (backend uses snake_case project_id)
        const projectMatch =
          !state.selectedProject ||
          (session.project_id && session.project_id === state.selectedProject.id)

        return statusMatch && projectMatch
      })

      // Sort by created_at descending (newest first) for consistent ordering across refreshes
      filtered.sort((a: any, b: any) => {
        const dateA = a.created_at ? new Date(a.created_at).getTime() : 0
        const dateB = b.created_at ? new Date(b.created_at).getTime() : 0
        return dateB - dateA
      })

      return filtered
    },

    /**
     * Get pending permissions across all sessions
     */
    pendingPermissions: (state) => {
      const pending: Permission[] = []
      Object.values(state.permissions).forEach((perms) => {
        pending.push(...perms.filter((p) => p.status === 'pending'))
      })
      return pending
    },

    /**
     * Get total message count across all sessions
     */
    totalMessageCount: (state) => {
      return Object.values(state.messages).reduce((sum, msgs) => sum + msgs.length, 0)
    },

    /**
     * Get total token count across all sessions
     */
    totalTokenCount: (state) => {
      const sessions = Array.isArray(state.sessions) ? state.sessions : []
      return sessions.reduce((sum, session) => {
        if (!session || typeof session !== 'object') return sum
        return sum + (session.tokenCount || 0)
      }, 0)
    },

    /**
     * Get total cost across all sessions
     */
    totalCost: (state) => {
      const sessions = Array.isArray(state.sessions) ? state.sessions : []
      return sessions.reduce((sum, session) => {
        if (!session || typeof session !== 'object') return sum
        return sum + (session.cost || 0)
      }, 0)
    },

    /**
     * Check if messages are loaded for a specific session
     */
    areMessagesLoaded: (state) => {
      return (sessionId: string) => state.messagesLoaded.has(sessionId)
    },

    /**
     * Get avatar information for a specific session
     */
    getSessionAvatar: (state) => {
      return (sessionId: string) => {
        const session = state.sessions.find((s) => s.id === sessionId)
        if (!session) return null

        return {
          avatarName: session.avatarName,
          avatarImage: session.avatarImage,
          avatarColor: session.avatarColor,
          selected_avatar_id: session.selected_avatar_id
        }
      }
    }
  },

  actions: {
    /**
     * Set the active session
     */
    setActiveSession(sessionId: string | null) {
      this.activeSessionId = sessionId
      // Clear recent activity pulse when user switches to this session
      if (sessionId) {
        delete this.recentActivity[sessionId]
      }
      if (sessionId) this.forgetOldSessionMessages(sessionId)
      // Ensure messages array exists for the session
      if (sessionId && !this.messages[sessionId]) {
        this.messages[sessionId] = []
      }
      if (sessionId && !this.permissions[sessionId]) {
        this.permissions[sessionId] = []
      }

      // Persist as last active session for the current project
      if (typeof window !== 'undefined' && sessionId && this.selectedProject) {
        const lastSessionMap = JSON.parse(localStorage.getItem('lastActiveSessionPerProject') || '{}')
        lastSessionMap[this.selectedProject.id] = sessionId
        localStorage.setItem('lastActiveSessionPerProject', JSON.stringify(lastSessionMap))
      }
    },

    /**
     * Keep the messages of the last KEEP_MESSAGES_FOR_SESSIONS opened sessions
     * only. Every message stays deeply reactive in the store, so a tab that
     * visited many long sessions grew by gigabytes and froze. Opening a session
     * reloads its latest page anyway (selectSession), so a forgotten one comes
     * back on the next visit. Permissions and questions are kept.
     */
    forgetOldSessionMessages(openedId: string) {
      const recent = [openedId, ...this.recentSessionIds.filter(id => id !== openedId)]
      this.recentSessionIds = recent.slice(0, KEEP_MESSAGES_FOR_SESSIONS)
      const keep = new Set(this.recentSessionIds)
      for (const id of Object.keys(this.messages)) {
        if (keep.has(id)) continue
        delete this.messages[id]
        delete this.hasMoreMessages[id]
        this.messagesLoaded.delete(id)
      }
    },

    /**
     * Create a new session
     */
    createSession(session: Session) {
      // Ensure sessions is an array
      if (!Array.isArray(this.sessions)) {
        this.sessions = []
      }
      const existing = this.sessions.findIndex((s) => s.id === session.id)
      if (existing >= 0) {
        this.sessions[existing] = session
      } else {
        this.sessions.unshift(session)
      }
      // Initialize empty messages and permissions
      if (!this.messages[session.id]) {
        this.messages[session.id] = []
      }
      if (!this.permissions[session.id]) {
        this.permissions[session.id] = []
      }
    },

    /** Refresh the list without discarding the conversation currently on screen. */
    refreshSessions(sessions: Session[]) {
      const ids = new Set(sessions.map(session => session.id))
      this.sessions = sessions
      if (this.activeSessionId && !ids.has(this.activeSessionId)) this.activeSessionId = null
      for (const id of Object.keys(this.messages)) {
        if (!ids.has(id)) delete this.messages[id]
      }
      for (const id of Object.keys(this.permissions)) {
        if (!ids.has(id)) delete this.permissions[id]
      }
      for (const id of this.messagesLoaded) {
        if (!ids.has(id)) this.messagesLoaded.delete(id)
      }
    },

    /**
     * Update an existing session
     */
    updateSession(sessionId: string, updates: Partial<Session>) {
      // Ensure sessions is an array
      if (!Array.isArray(this.sessions)) {
        this.sessions = []
        return
      }
      const session = this.sessions.find((s) => s.id === sessionId)
      if (session) {
        Object.assign(session, updates, {
          updatedAt: new Date()
        })
      }
    },

    /**
     * YOLO mode was toggled (yolo_mode_toggled). The sidebar switch reads it
     * from the session's options, so without this it snapped back off.
     */
    setSessionYoloMode(sessionId: string, enabled: boolean) {
      const session = Array.isArray(this.sessions) ? this.sessions.find((s) => s.id === sessionId) : undefined
      if (!session) return
      session.options = {
        ...(session.options ?? {}),
        dangerously_skip_permissions: enabled,
        allow_dangerously_skip_permissions: enabled
      }
    },

    /**
     * A session's question policy changed (its override and/or the policy in
     * effect). Leaves updated_at alone so the sidebar order doesn't move.
     */
    setSessionQuestionPolicy(sessionId: string, policy: Session['question_policy'], effective?: Session['question_policy_effective']) {
      const session = Array.isArray(this.sessions) ? this.sessions.find((s) => s.id === sessionId) : undefined
      if (!session) return
      session.question_policy = policy ?? null
      if (effective !== undefined) session.question_policy_effective = effective
    },

    /**
     * The global question policy changed: every session without an override
     * now follows it.
     */
    applyDefaultQuestionPolicy(policy: NonNullable<Session['question_policy_effective']>) {
      if (!Array.isArray(this.sessions)) return
      for (const session of this.sessions) {
        if (!session.question_policy) session.question_policy_effective = { ...policy }
      }
    },

    /**
     * End a session
     */
    endSession(sessionId: string) {
      // Ensure sessions is an array
      if (!Array.isArray(this.sessions)) {
        this.sessions = []
        return
      }
      const session = this.sessions.find((s) => s.id === sessionId)
      if (session) {
        session.status = 'ended'
        session.updatedAt = new Date()
      }
    },

    /**
     * Delete a session
     */
    deleteSession(sessionId: string) {
      // Ensure sessions is an array
      if (!Array.isArray(this.sessions)) {
        this.sessions = []
        return
      }
      this.sessions = this.sessions.filter((s) => s.id !== sessionId)
      delete this.messages[sessionId]
      delete this.permissions[sessionId]
      if (this.activeSessionId === sessionId) {
        this.activeSessionId = this.sessions.length > 0 ? this.sessions[0].id : null
      }
    },

    /**
     * Add a message to a session
     */
    addMessage(sessionId: string, message: Message) {
      this.addMessages(sessionId, [message])
    },

    /**
     * Add (or replace, by id) several messages in one state update.
     * Same result as calling addMessage for each message in order, but the
     * array is copied once instead of once per message — use it for history
     * loads.
     */
    addMessages(sessionId: string, messages: Message[]) {
      if (messages.length === 0) {
        if (!this.messages[sessionId]) this.messages[sessionId] = []
        return
      }
      // Use $patch for atomic state update - prevents race conditions
      this.$patch((state) => {
        // Always assign a new array reference to ensure reactivity
        // (Vue reactivity doesn't always detect an in-place .push() here)
        state.messages[sessionId] = upsertMessages(state.messages[sessionId] || [], messages)

        // Update session message count — use max of loaded count and existing count
        // to avoid overriding the authoritative total_count from the server
        if (Array.isArray(state.sessions)) {
          const session = state.sessions.find((s) => s.id === sessionId)
          if (session) {
            session.messageCount = Math.max(state.messages[sessionId].length, session.message_count || 0)
          }
        }

        // Mark recent activity for non-active sessions (drives sidebar pulse animation)
        if (sessionId !== state.activeSessionId) {
          state.recentActivity[sessionId] = Date.now()
        }
      })
    },

    /**
     * Update a message in a session
     */
    updateMessage(sessionId: string, messageId: string, updates: Partial<Message>) {
      // Use $patch for atomic state update - prevents race conditions
      this.$patch((state) => {
        if (!state.messages[sessionId]) return

        const current = state.messages[sessionId]
        const messageIndex = findMessageIndex(current, messageId)
        if (messageIndex >= 0) {
          // Create new array reference to ensure reactivity. Ids don't move,
          // so the new array can reuse the existing id index.
          const index = getMessageIndex(current)
          const updated = toRaw(current).slice()
          updated[messageIndex] = { ...updated[messageIndex], ...updates }
          messageIndexCache.set(updated, index)
          state.messages[sessionId] = updated
        }
      })
    },

    /**
     * Remove a message from a session
     */
    removeMessage(sessionId: string, messageId: string) {
      if (!this.messages[sessionId]) return
      this.messages[sessionId] = this.messages[sessionId].filter((m) => m.id !== messageId)
    },

    /**
     * Clear all messages for a session
     */
    clearSessionMessages(sessionId: string) {
      this.messages[sessionId] = []
    },

    /**
     * Prepend older messages to the beginning of a session's message list.
     * Used for "load more" pagination when scrolling up.
     */
    prependMessages(sessionId: string, messages: Message[]) {
      if (!this.messages[sessionId]) {
        this.messages[sessionId] = []
      }
      // Filter out duplicates by ID
      const existingIds = new Set(this.messages[sessionId].map(m => m.id))
      const newMessages = messages.filter(m => !existingIds.has(m.id))
      this.messages[sessionId] = [...newMessages, ...this.messages[sessionId]]
    },

    /**
     * Add or update a permission
     */
    addPermission(sessionId: string, permission: Permission) {
      if (!this.permissions[sessionId]) {
        this.permissions[sessionId] = []
      }
      const existing = this.permissions[sessionId].findIndex((p) => p.id === permission.id)
      if (existing >= 0) {
        this.permissions[sessionId][existing] = permission
      } else {
        this.permissions[sessionId].unshift(permission)
      }
    },

    /**
     * Update a permission status
     */
    updatePermission(sessionId: string, permissionId: string, status: 'approved' | 'denied') {
      if (!this.permissions[sessionId]) return
      const permission = this.permissions[sessionId].find((p) => p.id === permissionId)
      if (permission) {
        permission.status = status
        permission.timestamp = new Date()
      }
    },

    /**
     * Remove a permission from a session
     */
    removePermission(sessionId: string, permissionId: string) {
      if (!this.permissions[sessionId]) return
      this.permissions[sessionId] = this.permissions[sessionId].filter((p) => p.id !== permissionId)
    },

    /**
     * Clear all permissions for a session
     */
    clearSessionPermissions(sessionId: string) {
      this.permissions[sessionId] = []
    },

    /**
     * Add a question to a session
     */
    addQuestion(sessionId: string, question: UserQuestion) {
      if (!this.questions[sessionId]) {
        this.questions[sessionId] = []
      }
      const existing = this.questions[sessionId].findIndex((q) => q.id === question.id)
      if (existing >= 0) {
        this.questions[sessionId][existing] = question
      } else {
        this.questions[sessionId].unshift(question)
      }
    },

    /**
     * Update a waiting question's auto-answer timer (user_question_timer, or
     * "Keep waiting" pressed here). Returns the updated question, if known.
     */
    updateQuestionTimer(sessionId: string, questionId: string, timer: { auto_answer_at?: string | null; held?: boolean }) {
      const question = (this.questions[sessionId] || []).find((q) => q.id === questionId)
      if (!question) return undefined
      if (timer.auto_answer_at !== undefined) question.auto_answer_at = timer.auto_answer_at
      if (timer.held !== undefined) question.held = timer.held
      return question
    },

    /**
     * Answer a question and update its status
     */
    answerQuestion(sessionId: string, questionId: string, answers: string[]) {
      if (!this.questions[sessionId]) return
      const question = this.questions[sessionId].find((q) => q.id === questionId)
      if (question) {
        question.status = 'answered'
        question.answers = answers
        question.timestamp = new Date()
      }
    },

    /**
     * Remove a question from a session
     */
    removeQuestion(sessionId: string, questionId: string) {
      if (!this.questions[sessionId]) return
      this.questions[sessionId] = this.questions[sessionId].filter((q) => q.id !== questionId)
    },

    /**
     * Clear all questions for a session
     */
    clearSessionQuestions(sessionId: string) {
      this.questions[sessionId] = []
    },

    /**
     * Update session avatar information
     */
    updateSessionAvatar(sessionId: string, avatarData: { avatarName?: string; avatarImage?: string; avatarColor?: string }) {
      const session = this.sessions.find((s) => s.id === sessionId)
      if (session) {
        if (avatarData.avatarName !== undefined) {
          session.avatarName = avatarData.avatarName
        }
        if (avatarData.avatarImage !== undefined) {
          session.avatarImage = avatarData.avatarImage
        }
        if (avatarData.avatarColor !== undefined) {
          session.avatarColor = avatarData.avatarColor
        }
      }
    },

    /**
     * Mark messages as loaded for a session
     */
    markMessagesLoaded(sessionId: string) {
      this.messagesLoaded.add(sessionId)
    },

    /**
     * Set session filter
     */
    setFilter(filter: SessionFilter) {
      this.filter = filter
    },

    /**
     * Set selected project
     * @param project - Project object or null to clear selection
     */
    setSelectedProject(project: Project | null) {
      // Save the current active session for the outgoing project before switching
      if (typeof window !== 'undefined' && this.activeSessionId && this.selectedProject) {
        const lastSessionMap = JSON.parse(localStorage.getItem('lastActiveSessionPerProject') || '{}')
        lastSessionMap[this.selectedProject.id] = this.activeSessionId
        localStorage.setItem('lastActiveSessionPerProject', JSON.stringify(lastSessionMap))
      }

      this.selectedProject = project

      // Try to restore the last active session for the new project
      if (project && typeof window !== 'undefined') {
        const lastSessionMap = JSON.parse(localStorage.getItem('lastActiveSessionPerProject') || '{}')
        const lastSessionId = lastSessionMap[project.id]

        if (lastSessionId) {
          // Verify the session still exists and belongs to this project
          const session = this.sessions.find(s => s.id === lastSessionId && s.project_id === project.id)
          if (session) {
            this.activeSessionId = lastSessionId
          } else {
            // Session no longer exists, fall back to most recent session for this project
            this.activeSessionId = null
            this._autoSelectRecentSession(project.id)
            // Clean up stale entry
            delete lastSessionMap[project.id]
            localStorage.setItem('lastActiveSessionPerProject', JSON.stringify(lastSessionMap))
          }
        } else {
          // No saved session - auto-select the most recent one for this project
          this.activeSessionId = null
          this._autoSelectRecentSession(project.id)
        }
      } else if (this.activeSessionId) {
        // Clearing project selection - clear active session if it doesn't match
        const activeSession = this.sessions.find(s => s.id === this.activeSessionId)
        if (activeSession && project && activeSession.project_id !== project.id) {
          this.activeSessionId = null
        }
      }

      // Persist to localStorage (both ID and full object for instant recovery)
      if (typeof window !== 'undefined') {
        if (project) {
          localStorage.setItem('selectedProjectId', project.id)
          localStorage.setItem('selectedProjectObj', JSON.stringify(project))
        } else {
          localStorage.removeItem('selectedProjectId')
          localStorage.removeItem('selectedProjectObj')
        }
      }
    },

    /**
     * Auto-select the most recent session for a given project
     * @param projectId - The project ID to find sessions for
     */
    _autoSelectRecentSession(projectId: string) {
      const projectSessions = this.sessions
        .filter(s => s.project_id === projectId)
        .sort((a, b) => {
          const dateA = a.created_at ? new Date(a.created_at).getTime() : 0
          const dateB = b.created_at ? new Date(b.created_at).getTime() : 0
          return dateB - dateA
        })

      if (projectSessions.length > 0) {
        this.activeSessionId = projectSessions[0].id
      }
    },

    /**
     * Restore selected project from localStorage
     * Used during component mount to restore previous selection
     * @param projects - List of available projects to find the saved one
     */
    restoreSelectedProject(projects: Project[]) {
      if (typeof window === 'undefined') return

      // Don't overwrite if the store already has a selected project
      // (e.g., set programmatically via syncSelectedProjectForSession)
      if (this.selectedProject) return

      const savedProjectId = localStorage.getItem('selectedProjectId')
      if (savedProjectId) {
        // Try to find in the provided project list first
        const project = projects.find(p => p.id === savedProjectId)
        if (project) {
          this.selectedProject = project
        } else {
          // Try the cached full object from localStorage
          const cachedObj = localStorage.getItem('selectedProjectObj')
          if (cachedObj) {
            try {
              const parsed = JSON.parse(cachedObj)
              if (parsed && parsed.id === savedProjectId) {
                this.selectedProject = parsed
                return
              }
            } catch {}
          }
          // Project no longer exists, clear it
          this.selectedProject = null
          localStorage.removeItem('selectedProjectId')
          localStorage.removeItem('selectedProjectObj')
        }
      }
    },

    /**
     * Sync selected project to match a session's project.
     * Unlike setSelectedProject, this does NOT clear the active session.
     * Used when switching sessions (e.g., from zen mode) to keep the project
     * selector in sync with the newly active session.
     * @param project - The project object to sync to
     */
    syncSelectedProjectForSession(project: Project) {
      // Guard: never set undefined/null as the project
      if (!project || !project.id) {
        return
      }

      // If already matches, do nothing
      if (this.selectedProject && this.selectedProject.id === project.id) {
        return
      }

      // Update selected project WITHOUT clearing active session
      this.selectedProject = project

      // Persist to localStorage (both ID and full object for instant recovery)
      if (typeof window !== 'undefined') {
        localStorage.setItem('selectedProjectId', project.id)
        localStorage.setItem('selectedProjectObj', JSON.stringify(project))
      }
    },

    /**
     * Set selected project by ID
     * Useful when you only have the project ID (e.g., from URL query params)
     * and need to look up the full project object
     * @param projectId - Project ID to select
     * @param projects - List of available projects to search
     */
    setSelectedProjectById(projectId: string | null, projects: Project[]) {
      if (!projectId) {
        this.setSelectedProject(null)
        return
      }

      const project = projects.find(p => p.id === projectId)
      if (project) {
        this.setSelectedProject(project)
      } else {
        // Project not found, clear selection
        this.setSelectedProject(null)
      }
    },

    /**
     * Set loading state
     */
    setLoading(loading: boolean) {
      this.loading = loading
    },

    /**
     * Set error message
     */
    setError(error: string | null) {
      this.error = error
    },

    /**
     * Clear all sessions (optionally filtered by project)
     */
    clearAll(projectId?: string) {
      if (projectId) {
        // Only clear sessions for the specified project
        const sessionIdsToRemove = this.sessions
          .filter(s => s.project_id === projectId)
          .map(s => s.id)

        // Remove sessions
        this.sessions = this.sessions.filter(s => s.project_id !== projectId)

        // If the active session was removed, clear it
        if (this.activeSessionId && sessionIdsToRemove.includes(this.activeSessionId)) {
          this.activeSessionId = null
        }

        // Clean up messages, permissions, etc. for removed sessions
        sessionIdsToRemove.forEach(sessionId => {
          delete this.messages[sessionId]
          this.messagesLoaded.delete(sessionId)
          delete this.permissions[sessionId]
        })
      } else {
        // Clear all sessions (original behavior)
        this.sessions = []
        this.activeSessionId = null
        this.messages = {}
        this.messagesLoaded = new Set()
        this.permissions = {}
      }
      this.error = null
    },

    /**
     * Mark a session as having recent activity (for sidebar pulse animation).
     * Only marks non-active sessions so the currently viewed session doesn't pulse.
     */
    markRecentActivity(sessionId: string) {
      if (sessionId !== this.activeSessionId) {
        this.recentActivity[sessionId] = Date.now()
      }
    },

    /**
     * Clear recent activity for a session (e.g. when user switches to it)
     */
    clearRecentActivity(sessionId: string) {
      delete this.recentActivity[sessionId]
    }
  }
})
