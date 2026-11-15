<template>
  <svg
    v-if="qr"
    class="qr"
    :width="size"
    :height="size"
    :viewBox="`0 0 ${qr.size} ${qr.size}`"
    shape-rendering="crispEdges"
    role="img"
    :aria-label="label || `QR code for ${value}`"
    data-testid="qr-code"
  >
    <rect :width="qr.size" :height="qr.size" fill="#fff" />
    <path :d="qr.path" fill="#111" />
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { encode } from 'uqr'

// A QR code as an inline SVG path (dependency-free generator `uqr`). Always
// dark modules on a white plate, whatever the theme, so phone cameras read it.
const props = withDefaults(defineProps<{ value: string, size?: number, label?: string }>(), { size: 168 })

const qr = computed(() => {
  if (!props.value) return null
  const { data, size } = encode(props.value, { ecc: 'M', border: 2 })
  let path = ''
  data.forEach((row, y) => {
    let x = 0
    while (x < size) {
      if (!row[x]) { x++; continue }
      const start = x
      while (x < size && row[x]) x++
      path += `M${start} ${y}h${x - start}v1h-${x - start}z`
    }
  })
  return { size, path }
})
</script>

<style scoped>
.qr {
  display: block;
  border-radius: 8px;
}
</style>
