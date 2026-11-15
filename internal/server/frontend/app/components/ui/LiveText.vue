<template>{{ text(now) }}</template>

<script setup lang="ts">
// Text that depends on the current time (durations, "5m ago"). It re-renders
// on its own clock, so the view around it does not have to.
import { useSharedNow } from '~/composables/useSharedNow'

const props = withDefaults(defineProps<{
  text: (now: number) => string
  interval?: number
}>(), { interval: 1000 })

const now = useSharedNow(props.interval)
</script>
