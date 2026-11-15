import { watch, onUnmounted, type Ref } from 'vue'
import { getZenTTS } from '~/composables/useZenTTS'

/**
 * Read-aloud belongs to the session it was started in. When the active session
 * changes (sidebar, ⌘1-9 / ⌘[ ⌘], the Zen switcher, the agent overview) or the
 * agents page is left, a reading that is playing or still being synthesised
 * fades out (~400 ms), the worker drops its remaining chunks and music ducking
 * ends. A re-render of the same session does nothing.
 */
export function useReadAloudSessionGuard(activeSessionId: Ref<string | null | undefined>) {
  const fadeIfBusy = () => {
    const tts = getZenTTS()
    if (tts.isPlaying.value || tts.isLoading.value || tts.retrying.value) void tts.fadeOut()
  }
  watch(activeSessionId, (id, old) => {
    if (old && id !== old) fadeIfBusy()
  })
  onUnmounted(fadeIfBusy)
}
