<template>
  <Shader class="working-edge-shader" disable-telemetry @unavailable="$emit('unavailable')">
    <FlowingGradient
      :color-a="colors[0]"
      :color-b="colors[1]"
      :color-c="colors[2]"
      :color-d="colors[3]"
      :speed="0.18"
      :distortion="0.3"
    />
  </Shader>
</template>

<script setup lang="ts">
// Loaded on demand by WorkingEdge.vue, so the shaders library stays out of the main bundle.
import { FlowingGradient, Shader } from 'shaders/vue'
import type { EdgeColors } from '~/utils/workingEdge'

defineProps<{ colors: EdgeColors }>()
defineEmits<{ unavailable: [] }>()
</script>

<style scoped>
.working-edge-shader {
  position: absolute;
  inset: 0;
}
</style>
