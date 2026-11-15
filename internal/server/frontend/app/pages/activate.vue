<template>
  <div class="activate-page">
    <div class="bg-grain" aria-hidden="true" />
    <div class="activate-container">
      <div v-if="!authChecked" class="activate-wait" data-testid="activate-checking"><EggLoader /></div>
      <LicenseActivation v-else @done="continueToApp" />
      <p class="activate-footer">Eggify v{{ version }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import LicenseActivation from '~/components/license/LicenseActivation.vue'
import { activationNeedsLogin } from '~/utils/license'

// Full-page activation screen of a licensed build (see utils/license.ts).
// On a fresh install it comes before /setup; once an account exists it needs
// a signed-in user, so it sends a signed-out visitor to /login first.
definePageMeta({ layout: false })

const router = useRouter()
const { checkAuthStatus } = useAuth()
const version = ref('')
// The card only shows once we know the visitor may see it, so a signed-out
// visitor never sees the form flash before the sign-in redirect.
const authChecked = ref(false)

async function continueToApp() {
  const status = await checkAuthStatus().catch(() => null)
  router.push(status?.needs_setup ? '/setup' : '/')
}

onMounted(async () => {
  fetch('/api/version').then(r => r.ok ? r.json() : null).then((d) => { version.value = d?.version || '' }).catch(() => {})
  try {
    const status = await checkAuthStatus()
    if (activationNeedsLogin(status)) {
      await router.replace({ path: '/login', query: { redirect: '/activate' } })
      return
    }
  } catch {
    // the card shows its own errors
  }
  authChecked.value = true
})
</script>

<style scoped>
.activate-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-primary);
  padding: 20px 16px;
  position: relative;
}

.bg-grain {
  position: fixed;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  opacity: 0.3;
  background-image: url("data:image/svg+xml,%3Csvg viewBox='0 0 256 256' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='noise'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.9' numOctaves='4' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23noise)' opacity='0.04'/%3E%3C/svg%3E");
}

.activate-container {
  width: 100%;
  max-width: 480px;
  position: relative;
  z-index: 1;
}

.activate-wait {
  display: flex;
  justify-content: center;
  padding: 40px;
}

.activate-footer {
  margin: 14px 0 0;
  text-align: center;
  font-size: 0.75rem;
  color: var(--text-muted);
}
</style>
