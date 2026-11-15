<template>
  <p class="settings-note" :class="`settings-note--${variant}`" :role="variant === 'error' ? 'alert' : undefined">
    <SettingsGlyph :name="variant === 'error' ? 'alert' : variant === 'success' ? 'check' : 'info'" :size="14" />
    <span><slot /></span>
  </p>
</template>

<script setup lang="ts">
withDefaults(defineProps<{ variant?: 'info' | 'error' | 'success' }>(), { variant: 'info' })
</script>

<style scoped>
.settings-note {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 10px 0 0;
  padding: 8px 12px;
  border-radius: 8px;
  font-size: 0.78rem;
  line-height: 1.45;
  color: var(--text-secondary);
  background: var(--bg-secondary);
}

.settings-note :deep(svg) {
  flex-shrink: 0;
  margin-top: 2px;
}

.settings-note--error {
  color: var(--status-error, #ef4444);
  background: rgba(239, 68, 68, 0.08);
}

.settings-note--success {
  color: var(--status-success, #10b981);
  background: rgba(16, 185, 129, 0.08);
}
</style>
