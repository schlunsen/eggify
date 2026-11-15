import { beforeEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { mount } from '@vue/test-utils'
import ChatArea from '../../components/agents/ChatArea.vue'

const fake = vi.hoisted(() => () => {
  const cache: Record<string, any> = {}
  return new Proxy({}, {
    get(_t, key: string) {
      if (!(key in cache)) {
        const fn: any = () => undefined
        fn.value = /text|error|transcript/i.test(key) ? '' : 0
        cache[key] = fn
      }
      return cache[key]
    },
  })
})
vi.mock('../../composables/useVoiceRecording', () => ({ useVoiceRecording: () => fake() }))
vi.mock('../../composables/useWhisperTranscription', () => ({ useWhisperTranscription: () => fake() }))
vi.mock('../../composables/useParakeetTranscription', () => ({ useParakeetTranscription: () => fake(), preloadParakeetModel: vi.fn() }))
vi.mock('../../composables/agents/useZenAnimation', () => ({ preloadZenScene: vi.fn() }))

const stubs = {
  ChatHeader: true, InitialPromptBanner: true, WorkspaceTrustBanner: true, FullscreenEditorModal: true,
  RecordingModal: true, ZenVoiceBar: true, SlashCommandAutocomplete: true, ZenMode: true, SessionTerminal: true,
  FileExplorer: true, ImageGallery: true, ChatInput: true, Teleport: true,
}

function mountArea(props: Record<string, any>) {
  return mount(ChatArea, {
    props: { hasActiveSession: false, inputMessage: '', connected: true, isThinking: false, isProcessing: false, messages: [], ...props } as any,
    slots: { 'first-project-nudge': () => h('div', { class: 'nudge-slot' }, 'nudge') },
    global: { stubs },
  })
}

describe('ChatArea first-project nudge', () => {
  beforeEach(() => {
    vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth: vi.fn(async () => ({ ok: false, status: 404, json: async () => ({}) })) }))
  })

  it('keeps the old empty state when the prop is off', () => {
    const w = mountArea({})
    expect(w.find('.empty-state').exists()).toBe(true)
    expect(w.find('.nudge-slot').exists()).toBe(false)
    expect(w.find('.has-nudge').exists()).toBe(false)
  })

  it('shows the slot instead when on and no session is open', () => {
    const w = mountArea({ showFirstProjectNudge: true })
    expect(w.find('.nudge-slot').exists()).toBe(true)
    expect(w.find('.empty-state').exists()).toBe(false)
    expect(w.find('.no-session-selected.has-nudge').exists()).toBe(true)
  })

  it('shows the chat, not the nudge, when a session is open', () => {
    const w = mountArea({ showFirstProjectNudge: true, hasActiveSession: true, sessionId: 's1' })
    expect(w.find('.nudge-slot').exists()).toBe(false)
    expect(w.find('.no-session-selected').exists()).toBe(false)
    expect(w.find('.chat-content').exists()).toBe(true)
  })
})
