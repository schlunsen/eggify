import { defineComponent, h, ref, shallowRef, onUpdated, nextTick } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

/**
 * Typing in the composer must not re-render the message list. The agents page
 * owns the composer text (v-model through ChatArea), so every keystroke
 * re-renders ChatArea; the list used to be rendered straight into its slot,
 * with a scoped slot per MessageBubble, and ~700 bubbles re-rendered per key.
 * This mounts ChatArea the way the page does (messages slot -> ChatMessageList)
 * and counts bubble renders while typing and while other props change.
 */

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
    }
  })
})
vi.mock('../../composables/useVoiceRecording', () => ({ useVoiceRecording: () => fake() }))
vi.mock('../../composables/useWhisperTranscription', () => ({ useWhisperTranscription: () => fake() }))
vi.mock('../../composables/useParakeetTranscription', () => ({ useParakeetTranscription: () => fake(), preloadParakeetModel: vi.fn() }))
vi.mock('../../composables/agents/useZenAnimation', () => ({ preloadZenScene: vi.fn() }))

// A MessageBubble that counts its renders
const bubbleRenders = { count: 0 }
vi.mock('../../components/agents/MessageBubble.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      name: 'MessageBubble',
      props: { message: Object, formatTime: Function, formatMessage: Function, showEditDiff: Boolean },
      setup(props) {
        return () => { bubbleRenders.count++; return h('div', { class: 'bubble' }, String((props.message as any)?.content ?? '')) }
      },
    }),
  }
})

import ChatArea from '../../components/agents/ChatArea.vue'
import ChatMessageList from '../../components/agents/ChatMessageList.vue'
import { __resetShortcuts } from '../../composables/useKeyboardShortcuts'

beforeEach(() => {
  __resetShortcuts()
  bubbleRenders.count = 0
  vi.stubGlobal('useAuthenticatedFetch', () => ({ fetchWithAuth: vi.fn(async () => ({ ok: false, json: async () => ({}) })) }))
  vi.stubGlobal('requestIdleCallback', () => 0)
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const N = 200
const messages = Array.from({ length: N }, (_, i) => ({
  id: `m${i}`, role: i % 2 ? 'assistant' : 'user', content: `message ${i}`, timestamp: new Date(),
  editToolData: i === 5 ? { filePath: '/a.ts', oldString: 'a', newString: 'b', status: 'completed' } : undefined,
}))

function mountPage() {
  const input = ref('')
  const tokens = ref(1)
  const viewMode = ref<'live' | 'zen'>('live')
  const list = shallowRef(messages)
  let listUpdates = 0
  // Stable like the page's imported formatters and compiler-cached handlers
  const formatTime = () => ''
  const formatMessage = (c: any) => String(c)
  const onListUpdated = () => { listUpdates++ }
  const onInput = (v: string) => { input.value = v }
  const Page = defineComponent({
    setup() {
      return () => h(ChatArea, {
        hasActiveSession: true, connected: true, isThinking: false, isProcessing: false,
        sessionId: 's1', projectId: 'p1', viewMode: viewMode.value,
        inputMessage: input.value,
        'onUpdate:inputMessage': onInput,
        tokenCount: tokens.value,
        messageCount: list.value.length,
        messages: list.value,
      }, {
        messages: () => h(ChatMessageList, {
          messages: list.value,
          formatTime,
          formatMessage,
          diffInChat: true,
          onVnodeUpdated: onListUpdated,
        }),
      })
    },
  })
  const w = mount(Page, {
    attachTo: document.body,
    global: {
      stubs: {
        ChatHeader: true, InitialPromptBanner: true, WorkspaceTrustBanner: true,
        FullscreenEditorModal: true, RecordingModal: true, ZenVoiceBar: true, SlashCommandAutocomplete: true,
        ZenMode: true, SessionTerminal: true, FileExplorer: true, ImageGallery: true, Teleport: true,
        ChatInput: defineComponent({
          props: { inputMessage: String },
          emits: ['update:inputMessage'],
          setup(props, { emit }) {
            return () => h('textarea', { class: 'composer', value: props.inputMessage, onInput: (e: Event) => emit('update:inputMessage', (e.target as HTMLTextAreaElement).value) })
          },
        }),
      },
    },
  })
  return { w, input, tokens, list, viewMode, listUpdates: () => listUpdates }
}

describe('Typing in the composer', () => {
  it('renders every bubble once, then not again while typing', async () => {
    const { w, input, listUpdates } = mountPage()
    await flushPromises()
    expect(w.findAll('.bubble')).toHaveLength(N)
    const afterMount = bubbleRenders.count
    expect(afterMount).toBe(N)

    const ta = w.get('.composer')
    for (const ch of 'hello world') {
      ;(ta.element as HTMLTextAreaElement).value += ch
      await ta.trigger('input')
      await nextTick()
    }
    expect(input.value).toBe('hello world')
    expect(bubbleRenders.count).toBe(afterMount)
    expect(listUpdates()).toBe(0)
    w.unmount()
  })

  it('other ChatArea props (context, tokens) do not re-render the list either', async () => {
    const { w, tokens, listUpdates } = mountPage()
    await flushPromises()
    const afterMount = bubbleRenders.count
    for (let i = 0; i < 5; i++) { tokens.value++; await nextTick() }
    expect(bubbleRenders.count).toBe(afterMount)
    expect(listUpdates()).toBe(0)
    w.unmount()
  })

  it('a new message renders only that bubble', async () => {
    const { w, list } = mountPage()
    await flushPromises()
    const afterMount = bubbleRenders.count
    list.value = [...list.value, { id: 'new', role: 'assistant', content: 'new one', timestamp: new Date(), editToolData: undefined }]
    await nextTick()
    expect(w.findAll('.bubble')).toHaveLength(N + 1)
    expect(bubbleRenders.count).toBe(afterMount + 1)
    w.unmount()
  })

  it('the composer height observer (Zen side panel) runs only while Zen is shown', async () => {
    const observing = new Set<unknown>()
    vi.stubGlobal('ResizeObserver', class {
      observe() { observing.add(this) }
      unobserve() {}
      disconnect() { observing.delete(this) }
    })
    vi.stubGlobal('matchMedia', () => ({ matches: true })) // reduced motion: no fade between views
    const { w, viewMode } = mountPage()
    await flushPromises()
    expect(observing.size).toBe(0)
    viewMode.value = 'zen'
    await flushPromises()
    await nextTick()
    expect(observing.size).toBe(1)
    viewMode.value = 'live'
    await flushPromises()
    await nextTick()
    expect(observing.size).toBe(0)
    viewMode.value = 'zen'
    await flushPromises()
    await nextTick()
    w.unmount()
    expect(observing.size).toBe(0)
  })
})
