<template>
  <div class="rt-list">
    <p v-if="error" class="rt-list__error" role="alert">{{ error }}</p>
    <div class="rt-list__grid">
      <AgentRuntimeCard
        v-for="id in ids"
        :key="id"
        :runtime-id="id"
        :runtime="byId(id)"
        :flow="logins[id]"
        :busy="busy[id]"
        :action-error="actionError[id]"
        :checking="!loaded && loading"
        @login="(mode) => onLogin(id, mode)"
        @cancel="cancelLogin(id)"
        @code="(c) => submitCode(id, c)"
        @recheck="recheck"
        @navigate="$emit('navigate')"
      />
    </div>
    <div class="rt-list__foot">
      <button type="button" class="rt-recheck" :aria-disabled="loading || undefined" @click="recheck">
        <span class="rt-recheck__icon" :class="{ 'rt-recheck__icon--spin': loading }" aria-hidden="true">↻</span>
        {{ loading ? 'Checking…' : 'Re-check' }}
      </button>
      <span class="rt-list__meta">Eggify checks <code>claude</code> and <code>codex</code> on this computer. It never installs anything by itself.</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import AgentRuntimeCard from './AgentRuntimeCard.vue'
import { useAgentRuntimes, type RuntimeId } from '~/composables/useAgentRuntimes'
import { desktopOpenExternal, isDesktop } from '~/utils/desktop'

const props = withDefaults(defineProps<{ autoload?: boolean }>(), { autoload: true })
defineEmits<{ navigate: [] }>()

const ids: RuntimeId[] = ['claude', 'codex']
const { byId, logins, busy, actionError, loading, loaded, error, load, startLogin, cancelLogin, submitCode, dismissFlow } = useAgentRuntimes()

const recheck = () => {
  if (loading.value) return
  ids.forEach(dismissFlow)
  load(true)
}

const onLogin = async (id: RuntimeId, mode?: string) => {
  const flow = await startLogin(id, mode)
  // Claude Code opens the browser itself. Codex hands us the URL: in the
  // desktop app open it in the system browser right away; on the web the card
  // shows an "Open sign-in page" link (a popup after an await gets blocked).
  if (flow && id === 'codex' && isDesktop()) {
    const url = flow.verification_url || flow.auth_url
    if (url) desktopOpenExternal(url).catch(() => {})
  }
}

onMounted(() => {
  if (props.autoload && !loaded.value) load()
})
</script>

<style scoped>
.rt-list { display: flex; flex-direction: column; gap: 14px; }
.rt-list__grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 14px; align-items: start; }
.rt-list__error {
  margin: 0; padding: 8px 12px; border-radius: 8px; font-size: 0.84rem;
  color: var(--status-error, #dc2626);
  background: color-mix(in srgb, var(--status-error, #dc2626) 9%, transparent);
}
.rt-list__foot { display: flex; flex-wrap: wrap; align-items: center; gap: 10px 14px; }
.rt-list__meta { font-size: 0.76rem; color: var(--text-muted, var(--text-secondary)); }
.rt-list__meta code { font-family: var(--font-mono, monospace); font-size: 0.74rem; }
.rt-recheck {
  display: inline-flex; align-items: center; gap: 6px; padding: 5px 12px; border-radius: 8px;
  border: 1px solid var(--border-color); background: transparent; color: var(--text-secondary);
  font: inherit; font-size: 0.8rem; font-weight: 600; cursor: pointer;
}
.rt-recheck:hover:not([aria-disabled]) { color: var(--text-primary); border-color: var(--accent-purple); }
.rt-recheck:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; }
.rt-recheck[aria-disabled] { opacity: 0.6; cursor: default; }
.rt-recheck__icon { display: inline-block; }
.rt-recheck__icon--spin { animation: rt-list-spin 1s linear infinite; }
@keyframes rt-list-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .rt-recheck__icon--spin { animation: none; } }
</style>
