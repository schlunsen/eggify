<template>
  <Teleport :to="teleportTarget">
    <Transition name="tag-pop">
      <div v-if="open" class="tag-pop-overlay" @mousedown.self="close">
        <div class="tag-pop" role="dialog" aria-modal="true" :aria-label="`Tags for ${sessionName || 'this session'}`">
          <div class="tag-pop__head">
            <span class="tag-pop__title">Add tag</span>
            <span v-if="sessionName" class="tag-pop__session">{{ sessionName }}</span>
          </div>

          <div class="tag-pop__field" @click="inputEl?.focus()">
            <span v-for="tag in tags" :key="tag" class="tag-pop__chip">
              {{ tag }}
              <button type="button" class="tag-pop__chip-x" :aria-label="`Remove tag ${tag}`" @click.stop="remove(tag)">×</button>
            </span>
            <input
              ref="inputEl"
              v-model="query"
              class="tag-pop__input"
              type="text"
              :placeholder="tags.length ? 'Another tag…' : 'Tag this session…'"
              autocomplete="off"
              spellcheck="false"
              role="combobox"
              aria-autocomplete="list"
              :aria-expanded="suggestions.length > 0"
              aria-controls="tag-pop-suggestions"
              :aria-activedescendant="highlight >= 0 ? `tag-pop-s-${highlight}` : undefined"
              @keydown="onKeydown"
              @input="onInput"
            >
          </div>

          <ul v-if="suggestions.length" id="tag-pop-suggestions" class="tag-pop__suggestions" role="listbox" aria-label="Existing tags">
            <li
              v-for="(s, i) in suggestions"
              :id="`tag-pop-s-${i}`"
              :key="s"
              role="option"
              :aria-selected="i === highlight"
              class="tag-pop__suggestion"
              :class="{ 'is-active': i === highlight }"
              @mousedown.prevent="add([s])"
              @mousemove="highlight = i"
            >{{ s }}</li>
          </ul>

          <div class="tag-pop__hint">Enter or comma adds · Esc closes</div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useClosable, useShortcut } from '~/composables/useKeyboardShortcuts'
import { addSessionTags, knownSessionTags, removeSessionTag, sessionTagList, splitTags, suggestSessionTags } from '~/utils/sessionTags'

/**
 * ⌘S (⌥⌘S in a browser tab): add tags to the open session. Same storage as
 * the sidebar's "Add tag" (utils/sessionTags.ts); suggestions come from the
 * tags already used on other sessions.
 */
const props = defineProps<{ sessionId: string | null; sessionName?: string }>()
const open = defineModel<boolean>('open', { default: false })

const inputEl = ref<HTMLInputElement | null>(null)
const query = ref('')
const tags = ref<string[]>([])
const known = ref<string[]>([])
const highlight = ref(-1)
const teleportTarget = ref<string | Element>('body')
let returnFocus: HTMLElement | null = null

const suggestions = computed(() => suggestSessionTags(query.value, known.value, tags.value))

watch(open, (isOpen) => {
  if (isOpen) {
    if (!props.sessionId) { open.value = false; return }
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    // In Zen full screen only the full-screen element is visible
    teleportTarget.value = document.fullscreenElement || 'body'
    tags.value = sessionTagList(props.sessionId)
    known.value = knownSessionTags()
    query.value = ''
    highlight.value = -1
    void nextTick(() => inputEl.value?.focus())
  } else {
    const el = returnFocus
    returnFocus = null
    if (el?.isConnected) void nextTick(() => el.focus())
  }
}, { immediate: true })

// The session changed under an open popover: close it rather than tag the wrong one
watch(() => props.sessionId, () => { if (open.value) close() })

function close() {
  open.value = false
}

function add(values: string[]) {
  if (!props.sessionId) return
  const fresh = splitTags(values.join(','))
  if (fresh.length) tags.value = addSessionTags(props.sessionId, fresh)
  known.value = knownSessionTags()
  query.value = ''
  highlight.value = -1
}

function remove(tag: string) {
  if (!props.sessionId) return
  tags.value = removeSessionTag(props.sessionId, tag)
  inputEl.value?.focus()
}

function onInput() {
  highlight.value = -1
  // A comma ends a tag (pasting "a, b" adds both)
  if (query.value.includes(',')) {
    const parts = query.value.split(',')
    const rest = parts.pop() || ''
    add(parts)
    query.value = rest.trimStart()
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.isComposing) return
  if (e.key === 'ArrowDown' && suggestions.value.length) {
    e.preventDefault()
    highlight.value = (highlight.value + 1) % suggestions.value.length
  } else if (e.key === 'ArrowUp' && suggestions.value.length) {
    e.preventDefault()
    highlight.value = highlight.value <= 0 ? suggestions.value.length - 1 : highlight.value - 1
  } else if (e.key === 'Tab' && !e.shiftKey && suggestions.value.length && query.value.trim()) {
    e.preventDefault()
    query.value = suggestions.value[Math.max(0, highlight.value)]!
    highlight.value = -1
  } else if (e.key === 'Enter') {
    e.preventDefault()
    if (highlight.value >= 0 && suggestions.value[highlight.value]) add([suggestions.value[highlight.value]!])
    else if (query.value.trim()) add([query.value])
    else close() // Enter on an empty field: done
  } else if (e.key === 'Backspace' && !query.value && tags.value.length) {
    remove(tags.value[tags.value.length - 1]!)
  }
}

// Escape closes (the newest open thing first); ⌘S again closes too.
useClosable(open, close, { modal: true })
useShortcut('session.tag', close, { scope: 'modal', when: () => open.value })

defineExpose({ add, remove, close })
</script>

<style scoped>
.tag-pop-overlay {
  position: fixed;
  inset: 0;
  z-index: 1004;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 18vh 16px 16px;
  background: rgba(0, 0, 0, 0.25);
}
.tag-pop {
  width: min(420px, 100%);
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: 12px;
  box-shadow: 0 16px 40px rgba(0, 0, 0, 0.3);
  padding: 14px;
}
.tag-pop__head { display: flex; align-items: baseline; gap: 8px; margin-bottom: 10px; min-width: 0; }
.tag-pop__title { font-weight: 600; font-size: 14px; }
.tag-pop__session { font-size: 12px; color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tag-pop__field {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  border: 1px solid var(--border-color);
  border-radius: 8px;
  background: var(--bg-primary);
  cursor: text;
}
.tag-pop__field:focus-within { border-color: var(--accent-primary, var(--border-color)); }
.tag-pop__chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 4px 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  background: var(--bg-tertiary, var(--bg-secondary));
  border: 1px solid var(--border-color);
}
.tag-pop__chip-x { background: none; border: none; color: inherit; cursor: pointer; font-size: 14px; line-height: 1; padding: 0 2px; opacity: 0.7; }
.tag-pop__chip-x:hover { opacity: 1; }
.tag-pop__input { flex: 1; min-width: 120px; border: none; outline: none; background: transparent; color: inherit; font: inherit; font-size: 14px; padding: 4px 0; }
.tag-pop__suggestions { list-style: none; margin: 8px 0 0; padding: 4px; border: 1px solid var(--border-color); border-radius: 8px; max-height: 200px; overflow-y: auto; }
.tag-pop__suggestion { padding: 5px 8px; border-radius: 6px; font-size: 13px; cursor: pointer; }
.tag-pop__suggestion.is-active { background: var(--bg-tertiary, var(--bg-primary)); }
.tag-pop__hint { margin-top: 8px; font-size: 11px; color: var(--text-secondary); }
.tag-pop-enter-active, .tag-pop-leave-active { transition: opacity 0.12s; }
.tag-pop-enter-from, .tag-pop-leave-to { opacity: 0; }
@media (prefers-reduced-motion: reduce) {
  .tag-pop-enter-active, .tag-pop-leave-active { transition: none; }
}
</style>
