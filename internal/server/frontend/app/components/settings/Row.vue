<template>
  <div class="settings-row" :class="{ 'settings-row--stacked': stacked }">
    <div v-if="title || description || $slots.info" class="settings-row__info">
      <h3 v-if="title" class="settings-row__title">
        <label v-if="labelFor" :for="labelFor">{{ title }}</label>
        <template v-else>{{ title }}</template>
      </h3>
      <p v-if="description" class="settings-row__description">{{ description }}</p>
      <slot name="info" />
    </div>
    <div v-if="$slots.default" class="settings-row__control">
      <slot />
    </div>
    <div v-if="$slots.below" class="settings-row__below">
      <slot name="below" />
    </div>
  </div>
</template>

<script setup lang="ts">
// A single setting: title + description on the left and a compact control on
// the right. `stacked` puts wide controls (choice grids, forms) underneath.
defineProps<{ title?: string, description?: string, stacked?: boolean, labelFor?: string }>()
</script>

<style scoped>
.settings-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  column-gap: 24px;
  row-gap: 12px;
  padding: 18px 0;
}

.settings-row--stacked {
  grid-template-columns: minmax(0, 1fr);
}

.settings-row__info {
  min-width: 0;
}

.settings-row__title {
  margin: 0;
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--text-primary);
}

.settings-row__title label {
  cursor: pointer;
}

.settings-row__description {
  margin: 3px 0 0;
  font-size: 0.8rem;
  line-height: 1.5;
  color: var(--text-secondary);
}

.settings-row__control {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  min-width: 0;
}

.settings-row--stacked .settings-row__control {
  display: block;
}

.settings-row__below {
  grid-column: 1 / -1;
  min-width: 0;
}

@media (max-width: 640px) {
  .settings-row {
    grid-template-columns: minmax(0, 1fr);
  }
  .settings-row__control {
    justify-content: flex-start;
  }
}
</style>
