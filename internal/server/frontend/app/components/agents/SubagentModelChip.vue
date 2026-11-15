<template>
  <span
    v-if="label"
    class="subagent-model-chip"
    :class="{ requested }"
    :title="title || undefined"
    data-testid="subagent-model"
  >{{ label }}</span>
</template>

<script setup lang="ts">
// The model a subagent runs on ("Haiku 4.5"); dimmer while it is only the
// model its launch asked for (see utils/agents/modelLabel.ts).
defineProps<{ label: string; title?: string; requested?: boolean }>()
</script>

<style scoped>
.subagent-model-chip {
  display: inline-flex;
  align-items: center;
  padding: 0 6px;
  border-radius: 999px;
  border: 1px solid var(--border-color, rgba(127, 127, 127, 0.3));
  color: var(--text-secondary);
  font-size: 0.72rem;
  line-height: 1.5;
  white-space: nowrap;
  flex-shrink: 0;
}
.subagent-model-chip.requested {
  opacity: 0.65;
  border-style: dashed;
}
</style>
