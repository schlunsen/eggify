import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { useSessionActions } from '../../composables/agents/useSessionActions'

vi.mock('../../composables/useStores', () => ({
  useSettings: () => ({ saveSessionDefaults: vi.fn(), getSessionDefaults: () => null }),
}))

// Switching to a session that already has messages ("restored" sessions) first
// re-sends create_session. In v2.8.5–2.8.7 a stray line after that send threw
// (it referenced the create form, undefined here), so subscribe_session and
// load_messages were never sent and the chat sat behind the loader until a
// reload. This replays that switch.
function actions(send: ReturnType<typeof vi.fn>, sessions: any[]) {
  const r = <T>(v: T) => ref(v) as any
  return useSessionActions({
    agentWs: { connected: true, send },
    sessions: r(sessions), activeSessionId: r(null), messages: r({}), messagesLoaded: r(new Set()),
    showCreateSessionModal: r(false), showResumeModal: r(false), selectedResumeSession: r(null),
    availableSessions: r([]), loadingSessions: r(false), creatingSession: r(false), resumingSession: r(false),
    sessionPermissions: r(new Map()), awaitingToolResults: r(new Set()), todoHideTimers: r(new Map()),
    sessionToolStats: r(new Map()), sessionPermissionStats: r(new Map()), isUserNearBottom: r(true),
    sessionForm: r({}), resumeForm: r({}), availableAgents: r([]), selectedAgentPreview: r(null),
    loadingAgents: r(false), availableProviders: r([]), currentProvider: r(null), loadingProviders: r(false),
    projectAreas: r([]), currentProjectId: r(null), fetchProjectAreas: async () => {},
    scrollToBottom: () => {}, focusMessageInput: () => {}, cleanupSessionData: () => {},
  } as any)
}

describe('switching sessions', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => ({}) })))
  })

  it('loads the messages of a restored session with history', async () => {
    const send = vi.fn(() => true)
    const sessions = [{ id: 's-old', message_count: 41, status: 'idle', options: { working_directory: '/tmp/p' } }]
    await actions(send, sessions).selectSession('s-old')
    const types = send.mock.calls.map(c => c[0].type)
    expect(types).toEqual(expect.arrayContaining(['create_session', 'subscribe_session', 'load_messages']))
    expect(types.indexOf('load_messages')).toBeGreaterThan(types.indexOf('create_session'))
    expect(send.mock.calls.find(c => c[0].type === 'load_messages')![0].session_id).toBe('s-old')
  })

  it('loads the messages of a session without history', async () => {
    const send = vi.fn(() => true)
    await actions(send, [{ id: 's-new', message_count: 0, status: 'idle', options: {} }]).selectSession('s-new')
    const types = send.mock.calls.map(c => c[0].type)
    expect(types).not.toContain('create_session')
    expect(types).toEqual(expect.arrayContaining(['subscribe_session', 'load_messages']))
  })
})
