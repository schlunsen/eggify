import { onMounted, ref } from 'vue'

export type ChatWidth = 'full' | 'reading'

const storageKey = 'eggify-chat-width'

export function useChatWidth() {
  const chatWidth = ref<ChatWidth>('full')

  onMounted(() => {
    try {
      const saved = window.localStorage.getItem(storageKey)
      if (saved === 'full' || saved === 'reading') chatWidth.value = saved
    } catch {
      chatWidth.value = 'full'
    }
  })

  function setChatWidth(value: ChatWidth) {
    chatWidth.value = value
    try {
      window.localStorage.setItem(storageKey, value)
    } catch {}
  }

  return { chatWidth, setChatWidth }
}
