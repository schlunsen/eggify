<template>
  <Transition name="theme-toast">
    <div v-if="visible && themeToast" class="theme-toast" role="status" aria-live="polite">
      <span class="theme-toast__icon" aria-hidden="true">{{ themeToast.theme.isDark ? '◐' : '◑' }}</span>
      <span class="theme-toast__name">{{ themeToast.theme.name }}</span>
    </div>
  </Transition>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { themeToast } from '~/composables/useThemeShortcuts'

// Brief "which theme is this" note after the next/previous theme shortcut.
const visible = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null

watch(themeToast, (t) => {
  if (!t) return
  visible.value = true
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => { visible.value = false }, 1600)
})

onBeforeUnmount(() => { if (timer) clearTimeout(timer) })
</script>

<style scoped>
.theme-toast {
  position: fixed;
  left: 50%;
  bottom: 24px;
  transform: translateX(-50%);
  z-index: 10000;
  display: flex;
  align-items: center;
  gap: 8px;
  max-width: calc(100vw - 32px);
  padding: 8px 14px;
  border-radius: 999px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  color: var(--text-primary);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.25);
  font-size: 13px;
  pointer-events: none;
}
.theme-toast__name { font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.theme-toast-enter-active, .theme-toast-leave-active { transition: opacity 0.18s, transform 0.18s; }
.theme-toast-enter-from, .theme-toast-leave-to { opacity: 0; transform: translate(-50%, 6px); }
@media (prefers-reduced-motion: reduce) {
  .theme-toast-enter-active, .theme-toast-leave-active { transition: none; }
}
</style>
