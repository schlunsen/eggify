<template>
  <section :id="id" class="settings-section" :aria-labelledby="`${id}-title`">
    <header class="settings-section__header">
      <span class="settings-section__icon"><SettingsGlyph :name="icon" :size="18" /></span>
      <div>
        <h2 :id="`${id}-title`" class="settings-section__title">{{ title }}</h2>
        <p v-if="subtitle" class="settings-section__subtitle">{{ subtitle }}</p>
      </div>
    </header>
    <div class="settings-section__body">
      <slot />
    </div>
  </section>
</template>

<script setup lang="ts">
defineProps<{ id: string, title: string, subtitle?: string, icon: string }>()
</script>

<style scoped>
.settings-section {
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 12px;
  scroll-margin-top: 24px;
}

.settings-section__header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 18px 22px;
  border-bottom: 1px solid var(--border-color);
}

.settings-section__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: 8px;
  flex-shrink: 0;
  color: var(--accent-purple);
  background: rgba(var(--accent-purple-rgb, 138, 108, 255), 0.1);
}

.settings-section__title {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: var(--text-primary);
}

.settings-section__subtitle {
  margin: 2px 0 0;
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.settings-section__body {
  padding: 4px 22px;
}

.settings-section__body > :deep(.settings-row + .settings-row) {
  border-top: 1px solid var(--border-color);
}

@media (max-width: 640px) {
  .settings-section__header { padding: 16px; }
  .settings-section__body { padding: 4px 16px; }
}
</style>
