<template>
  <div class="vr-page">
    <div ref="stage" class="vr-stage" />

    <div class="vr-hud">
      <NuxtLink to="/agents" class="vr-back">← Agents</NuxtLink>
      <h1>Agent Space</h1>
      <p class="vr-counts">
        <span class="dot attention" /> {{ counts.attention }} need you
        <span class="dot working" /> {{ counts.working }} working
        <span class="dot recent" /> {{ counts.recent }} recent
      </p>
      <p v-if="emulating" class="vr-badge">Emulating Meta Quest 3 (IWER)</p>
      <p v-if="error" class="vr-error">{{ error }}</p>
      <p v-else-if="!loading && !spaceAgents.length" class="vr-hint">No agent sessions in the last 24 hours.</p>
      <p v-else class="vr-hint">Drag to look around · tap an orb for its activity · <strong>Enter XR</strong> on a Quest</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useAgentOverview } from '~/composables/agents/useAgentOverview'
import { overviewState } from '~/utils/agents/overviewState'
import { createAgentSpace, type AgentSpace, type SpaceAgent } from '~/composables/vr/useAgentSpace'

definePageMeta({ layout: false })
useHead({ title: 'Agent Space · Eggify' })

const route = useRoute()
const stage = ref<HTMLElement | null>(null)
const space = shallowRef<AgentSpace | null>(null)
const emulating = ref(false)

const { agents, projects, details, loading, error, counts, filter } = useAgentOverview()
filter.value = 'all'

const spaceAgents = computed<SpaceAgent[]>(() => agents.value.map(agent => {
  const state = overviewState(agent)
  return {
    id: agent.id,
    name: agent.selected_avatar?.name || agent.options?.agent_name || `Agent ${agent.id.slice(0, 6)}`,
    category: state.category,
    label: state.label,
    model: agent.model_name || agent.options?.model,
    branch: agent.options?.workspace?.branch || agent.git_branch,
    project: agent.project_id ? projects.value[agent.project_id]?.name : undefined,
    subRunning: agent.subagents_running || 0,
    subTotal: agent.subagents_total || 0,
    logs: (details.value[agent.id]?.logs || []).map(l => ({ kind: l.kind, text: l.text }))
  }
}))

watch(spaceAgents, value => space.value?.update(value), { deep: true })

async function installEmulator() {
  // Meta's Immersive Web Emulation Runtime: a Quest 3 in the desktop browser.
  // Loaded lazily and only on request (?emulate=1), so production users never pay for it.
  const { XRDevice, metaQuest3 } = await import('iwer')
  const device = new XRDevice(metaQuest3)
  device.installRuntime({ forceInstall: true })
  const { DevUI } = await import('@iwer/devui')
  new DevUI(device)
  emulating.value = true
  ;(window as any).__eggifyXR = { device }
}

onMounted(async () => {
  if (route.query.emulate) {
    try {
      await installEmulator()
    } catch (e) {
      console.error('[vr] failed to start WebXR emulator', e)
    }
  }
  if (!stage.value) return
  // XRButton checks navigator.xr support at creation, so the emulator must be installed first.
  space.value = createAgentSpace(stage.value)
  space.value.update(spaceAgents.value)
  if (emulating.value) (window as any).__eggifyXR.space = space.value
})

onBeforeUnmount(() => {
  space.value?.dispose()
  space.value = null
})
</script>

<style scoped>
.vr-page {
  position: fixed;
  inset: 0;
  background: #05070d;
  color: #e2e8f0;
  font-family: Inter, system-ui, sans-serif;
}
.vr-stage {
  position: absolute;
  inset: 0;
}
.vr-hud {
  position: absolute;
  top: 16px;
  left: 16px;
  max-width: min(420px, calc(100vw - 32px));
  padding: 14px 18px;
  border-radius: 14px;
  background: rgba(8, 12, 22, 0.72);
  border: 1px solid rgba(148, 163, 184, 0.2);
  backdrop-filter: blur(8px);
  pointer-events: auto;
}
.vr-hud h1 {
  margin: 6px 0 4px;
  font-size: 1.35rem;
  font-weight: 600;
  color: #f8fafc;
}
.vr-back {
  color: #94a3b8;
  text-decoration: none;
  font-size: 0.9rem;
}
.vr-counts {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 10px;
  margin: 0;
  font-size: 0.95rem;
}
.dot {
  display: inline-block;
  width: 10px;
  height: 10px;
  border-radius: 50%;
}
.dot.attention { background: #f59e0b; }
.dot.working { background: #34d399; }
.dot.recent { background: #64748b; }
.vr-badge {
  display: inline-block;
  margin: 10px 0 0;
  padding: 3px 10px;
  border-radius: 999px;
  background: rgba(56, 189, 248, 0.15);
  color: #7dd3fc;
  font-size: 0.8rem;
}
.vr-hint,
.vr-error {
  margin: 10px 0 0;
  font-size: 0.9rem;
  color: #94a3b8;
}
.vr-error { color: #fca5a5; }
</style>
