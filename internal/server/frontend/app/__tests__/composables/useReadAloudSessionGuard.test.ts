import { describe, it, expect, vi, beforeEach } from 'vitest'
import { defineComponent, h, nextTick, ref, type Ref } from 'vue'
import { mount } from '@vue/test-utils'

const tts = {
  isPlaying: ref(false),
  isLoading: ref(false),
  retrying: ref(false),
  fadeOut: vi.fn(async () => {}),
}
vi.mock('~/composables/useZenTTS', () => ({ getZenTTS: () => tts }))

const { useReadAloudSessionGuard } = await import('../../composables/agents/useReadAloudSessionGuard')

function mountWith(id: Ref<string | null>) {
  return mount(defineComponent({
    setup() {
      useReadAloudSessionGuard(id)
      return () => h('div', id.value ?? '')
    },
  }))
}

beforeEach(() => {
  tts.isPlaying.value = false
  tts.isLoading.value = false
  tts.retrying.value = false
  tts.fadeOut.mockClear()
})

describe('useReadAloudSessionGuard', () => {
  it('fades out a reading when the session changes', async () => {
    const id = ref<string | null>('a')
    mountWith(id)
    tts.isPlaying.value = true
    id.value = 'b'
    await nextTick()
    expect(tts.fadeOut).toHaveBeenCalledTimes(1)
  })

  it('fades out a reading that is still loading', async () => {
    const id = ref<string | null>('a')
    mountWith(id)
    tts.isLoading.value = true
    id.value = 'b'
    await nextTick()
    expect(tts.fadeOut).toHaveBeenCalledTimes(1)
  })

  it('leaves the reading alone on a re-render of the same session or when idle', async () => {
    const id = ref<string | null>('a')
    const w = mountWith(id)
    tts.isPlaying.value = true
    await w.setProps({})
    id.value = 'a'
    await nextTick()
    expect(tts.fadeOut).not.toHaveBeenCalled()
    tts.isPlaying.value = false
    id.value = 'c'
    await nextTick()
    expect(tts.fadeOut).not.toHaveBeenCalled()
  })

  it('does not stop a reading when the first session is selected', async () => {
    const id = ref<string | null>(null)
    mountWith(id)
    tts.isPlaying.value = true
    id.value = 'a'
    await nextTick()
    expect(tts.fadeOut).not.toHaveBeenCalled()
  })

  it('fades out when the page is left', () => {
    const id = ref<string | null>('a')
    const w = mountWith(id)
    tts.isPlaying.value = true
    w.unmount()
    expect(tts.fadeOut).toHaveBeenCalledTimes(1)
  })
})
