<template>
  <div class="sandbox-toggle" :class="{ unsupported: support === 'unsupported' }">
    <label class="sandbox-row">
      <input
        type="checkbox"
        data-testid="sandbox-toggle"
        aria-describedby="sandbox-toggle-help"
        :checked="effective"
        :disabled="support === 'unsupported'"
        @change="onChange"
      />
      <span class="sandbox-label">Filesystem sandbox</span>
      <span v-if="defaultEnabled !== null && modelValue === null" class="sandbox-default">default</span>
    </label>
    <p id="sandbox-toggle-help" class="sandbox-help">
      <template v-if="support === 'unsupported'">
        Not available for this provider: its tools run inside Eggify itself, which has no sandbox.
      </template>
      <template v-else-if="isCodex">
        Restricts writes to the workspace and temporary directories. Eggify's protected secrets stay
        unreadable and protected configuration stays read-only. Network permission is separate below.
      </template>
      <template v-else>
        Bash runs in a sandbox: Eggify's keys and database are unreadable and network access is
        limited to the allowlist in Settings.
        <template v-if="support === 'depends'"> Only works if this provider uses an Anthropic-compatible URL.</template>
      </template>
    </p>
    <template v-if="isCodex">
      <label class="sandbox-row">
        <input type="checkbox" data-testid="codex-network-toggle"
          :checked="networkAccess === true" :disabled="!effective || !canAuthorizeNetwork"
          aria-describedby="codex-network-help" @change="onNetworkChange" />
        <span>Allow unrestricted network for this session</span>
      </label>
      <p id="codex-network-help" class="sandbox-help">
        Off by default. Allows command network access for SSH, Git and package installs to any host,
        including local and private services. Eggify applies no domain allowlist.
        Host or administrator restrictions may still apply. Filesystem protections remain;
        this does not grant access to protected credentials or writes outside the sandbox.
        Requires the filesystem sandbox and a signed-in admin (or the only user).
        Fixed at creation: start a new session to change it; restarting an existing session keeps its choice.
        Handovers and other new sessions start with network access off.
      </p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { sandboxSupportForProvider } from '~/utils/agents/sandbox'
import { useSandboxSettings } from '~/composables/useSandboxSettings'

const props = defineProps<{
  /** null = follow the global default. */
  modelValue: boolean | null | undefined
  provider?: string | null
  networkAccess?: boolean
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean | null): void
  (e: 'update:networkAccess', value: boolean): void
}>()

const support = computed(() => sandboxSupportForProvider(props.provider))
const defaultEnabled = ref<boolean | null>(null)
const canAuthorizeNetwork = ref(false)
const isCodex = computed(() => props.provider?.trim().toLowerCase() === 'codex')

const effective = computed(() => {
  if (support.value === 'unsupported') return false
  if (props.modelValue === true || props.modelValue === false) return props.modelValue
  return defaultEnabled.value === true
})

const onChange = (event: Event) => {
  const checked = (event.target as HTMLInputElement).checked
  emit('update:modelValue', checked)
  if (!checked) emit('update:networkAccess', false)
}

const onNetworkChange = (event: Event) => {
  emit('update:networkAccess', effective.value && canAuthorizeNetwork.value &&
    (event.target as HTMLInputElement).checked)
}
watch([isCodex, effective], ([codex, sandboxed]) => {
  if (!codex || !sandboxed) emit('update:networkAccess', false)
})

// An unsupported provider must never be sent as sandboxed (the backend would
// refuse every prompt), so pin an explicit false while it is selected.
// Switching back to a supported provider returns to the default.
let pinned = false
watch(support, (s, prev) => {
  if (s === 'unsupported') {
    if (props.modelValue !== false) {
      pinned = true
      emit('update:modelValue', false)
    }
  } else if (prev === 'unsupported' && pinned) {
    pinned = false
    emit('update:modelValue', null)
  }
}, { immediate: true })

onMounted(async () => {
  const { load } = useSandboxSettings()
  const s = await load()
  defaultEnabled.value = s ? s.default_enabled : false
  canAuthorizeNetwork.value = s?.can_authorize_codex_network === true
})
</script>

<style scoped>
.sandbox-toggle {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.sandbox-row {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  font-weight: 600;
}
.sandbox-toggle.unsupported .sandbox-row {
  cursor: not-allowed;
  opacity: 0.6;
}
.sandbox-default {
  font-size: 0.7rem;
  font-weight: 500;
  padding: 0 6px;
  border-radius: 4px;
  border: 1px solid var(--border-color, rgba(127, 127, 127, 0.4));
  color: var(--text-secondary, inherit);
}
.sandbox-help {
  margin: 0;
  font-size: 0.75rem;
  color: var(--text-secondary, inherit);
  line-height: 1.4;
}
</style>
