<template>
  <!--
    The quiet line at the top of a worker session's own chat:
    "Build worker of <parent> · branch worker/x" / "Review worker of <parent> · read-only".
    The parent's name opens it (plain text when the parent is gone).
  -->
  <p class="worker-note" data-testid="worker-note">
    {{ note.lead }}{{ ' ' }}
    <button
      v-if="parentId"
      type="button"
      class="worker-note-parent"
      :title="`Open ${note.parentName}`"
      data-testid="worker-note-parent"
      @click="$emit('open', parentId)"
    >{{ note.parentName }}</button>
    <span v-else class="worker-note-gone" title="That session is no longer in the list">{{ note.parentName }}</span>
    <span v-if="note.detail"> · {{ note.detail }}</span>
  </p>
</template>

<script setup lang="ts">
defineProps<{
  note: { lead: string; parentName: string; detail: string }
  /** The parent session, '' when it is no longer in the list. */
  parentId?: string
}>()

defineEmits<{ open: [sessionId: string] }>()
</script>

<style scoped>
.worker-note {
  margin: 6px 16px 0;
  padding: 0 2px;
  color: var(--text-secondary);
  font-size: 12px;
}
.worker-note-parent {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--accent-purple);
  font: inherit;
  cursor: pointer;
  text-decoration: underline;
  text-underline-offset: 2px;
}
.worker-note-parent:focus-visible { outline: 2px solid var(--accent-purple); outline-offset: 2px; border-radius: 3px; }
.worker-note-gone { color: var(--text-primary); }
</style>
