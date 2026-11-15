<template>
  <NotchTheatre
    :scene="scene"
    :now="now"
    :notch-width="geometry.notchWidth"
    :menu-height="geometry.menuHeight"
    :ear="geometry.ear"
    :signed-out="signedOut"
    :error="error"
    :answer="answer"
    :resize="resize"
    @answered="forgetCue"
    @open="openSession"
  />
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import NotchTheatre from '~/components/notch/NotchTheatre.vue'
import { useNotchFeed } from '~/composables/notch/useNotchFeed'
import { sendNotchAnswer, type NotchAnswer } from '~/utils/notch/notchAnswer'
import { desktopNotchOpenSession, desktopNotchSetExpanded } from '~/utils/desktop'

/**
 * The Notch Theatre window's page (desktop app, cmd/eggify-desktop/notch.go).
 * The window passes the camera housing's size: ?nw=<housing width>&mh=<menu
 * bar height>&ear=<ear width>, all in points. app.vue keeps the agent
 * WebSocket and the overlays off this page.
 */
definePageMeta({ layout: false })
useHead({
  title: 'Notch Theatre',
  htmlAttrs: { class: 'notch-page' },
  style: [{ textContent: 'html.notch-page, html.notch-page body, html.notch-page #__nuxt { background: transparent !important; overflow: hidden; }' }],
})

const route = useRoute()
const num = (v: unknown, fallback: number, min: number, max: number) => {
  const n = Number(Array.isArray(v) ? v[0] : v)
  return Number.isFinite(n) && n >= min && n <= max ? n : fallback
}
const geometry = computed(() => ({
  notchWidth: num(route.query.nw, 0, 0, 400),
  menuHeight: num(route.query.mh, 32, 20, 64),
  ear: num(route.query.ear, 104, 40, 160),
}))

const { scene, now, error, signedOut, start, forgetCue } = useNotchFeed()

let apiKey: string | null = null
async function answer(a: NotchAnswer) {
  if (!apiKey) {
    const r = await fetch('/api/config/api-key', { credentials: 'same-origin' }).catch(() => null)
    const data = r?.ok ? await r.json().catch(() => null) : null
    apiKey = data?.apiKey || null
  }
  if (!apiKey) return { ok: false, error: 'Sign in to Eggify to answer from the notch.' }
  return sendNotchAnswer(a, apiKey)
}

const resize = (expanded: boolean) => desktopNotchSetExpanded(expanded).catch(() => false)

function openSession(id: string) {
  void desktopNotchOpenSession(id).then((ok) => {
    // In a plain browser tab (trying the page out) open it here instead.
    if (!ok) void navigateTo(id ? `/agents?session=${encodeURIComponent(id)}` : '/agents')
  }).catch(() => {})
}

onMounted(start)
</script>
