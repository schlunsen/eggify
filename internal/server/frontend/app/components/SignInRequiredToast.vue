<template>
  <Transition name="sign-in-toast">
    <div v-if="message" class="sign-in-toast" role="alert" aria-live="assertive">
      <span class="sign-in-toast__text">{{ message }}</span>
      <NuxtLink v-if="!adminOnly" :to="loginLink" class="sign-in-toast__action" @click="message = ''">Sign in</NuxtLink>
      <button type="button" class="sign-in-toast__close" aria-label="Dismiss" @click="message = ''">×</button>
    </div>
  </Transition>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { USER_REQUIRED_EVENT } from '~/utils/userRequired'

// Shown when the server refuses a privileged action because the request did
// not come from a signed-in user (see utils/userRequired.ts).
const message = ref('')
// admin_required: signed in, but not an admin; signing in again won't help.
const adminOnly = ref(false)
let hideTimer: ReturnType<typeof setTimeout> | null = null

const route = useRoute()
const loginLink = computed(() => ({ path: '/login', query: { redirect: route.fullPath } }))

function onUserRequired(event: Event) {
  const detail = (event as CustomEvent<{ message: string; admin?: boolean }>).detail
  adminOnly.value = !!detail?.admin
  message.value = detail?.message || 'Sign in to Eggify and try again.'
  if (hideTimer) clearTimeout(hideTimer)
  hideTimer = setTimeout(() => { message.value = '' }, 10000)
}

onMounted(() => window.addEventListener(USER_REQUIRED_EVENT, onUserRequired))
onBeforeUnmount(() => {
  window.removeEventListener(USER_REQUIRED_EVENT, onUserRequired)
  if (hideTimer) clearTimeout(hideTimer)
})
</script>

<style scoped>
.sign-in-toast {
  position: fixed;
  right: 16px;
  bottom: 16px;
  z-index: 10000;
  display: flex;
  align-items: center;
  gap: 12px;
  max-width: min(480px, calc(100vw - 32px));
  padding: 12px 14px;
  border-radius: 10px;
  background: #3b1219;
  border: 1px solid #e5484d;
  color: #ffdbdc;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
  font-size: 14px;
  line-height: 1.4;
}
.sign-in-toast__text { flex: 1; }
.sign-in-toast__action {
  color: #fff;
  font-weight: 600;
  text-decoration: underline;
  white-space: nowrap;
}
.sign-in-toast__close {
  background: none;
  border: none;
  color: inherit;
  font-size: 18px;
  cursor: pointer;
  padding: 0 2px;
}
.sign-in-toast-enter-active, .sign-in-toast-leave-active { transition: opacity 0.2s, transform 0.2s; }
.sign-in-toast-enter-from, .sign-in-toast-leave-to { opacity: 0; transform: translateY(8px); }
</style>
