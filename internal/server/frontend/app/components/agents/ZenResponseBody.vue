<template>
  <div
    class="zen-resp"
    :class="{ 'is-clamped': overflowing, 'is-panel': layout === 'panel' }"
    :style="{ '--zen-resp-fade': `${ZEN_RESP_FADE_PX}px` }"
    data-testid="zen-resp"
  >
    <div
      ref="box"
      class="zen-resp-box"
      :class="{ 'fade-top': fadeTop, 'fade-bottom': fadeBottom }"
      data-testid="zen-resp-box"
      @scroll.passive="onScroll"
    >
      <!-- eslint-disable-next-line vue/no-v-html -- sanitised by renderSafeMarkdown -->
      <div ref="content" class="zen-resp-md" data-testid="zen-resp-md" v-html="html"></div>
    </div>
    <div v-if="layout !== 'panel'" class="zen-resp-actions">
      <button
        v-if="overflowing"
        type="button"
        class="zen-resp-btn"
        data-testid="zen-resp-more"
        @click.stop="$emit('expand')"
      >Read more</button>
      <button
        type="button"
        class="zen-resp-btn zen-resp-copy"
        data-testid="zen-resp-copy"
        :title="copied ? 'Copied' : 'Copy response'"
        @click.stop="copy"
      >{{ copied ? 'Copied' : 'Copy' }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { copyText } from '~/utils/clipboard'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { renderSafeMarkdown } from '~/utils/safeMarkdown'
import { ZEN_RESP_FADE_PX } from '~/utils/zenLayout'

/**
 * The latest assistant reply on the Zen stage, as safe markdown.
 *
 * - `card` (default, the centred card): a clamped box with "Read more" (opens the
 *   expanded view) and Copy below it.
 * - `panel` (the full-height panel on the right of wide stages): the whole reply,
 *   scrollable on its own; Copy lives in the panel's header.
 *
 * Either way, while the reply streams in, the box follows the end of the text
 * (unless the reader scrolled up), and only the edge where more text is hidden
 * fades out, over ZEN_RESP_FADE_PX: a mask, so the text keeps its full colour
 * and fades into whatever card is behind it.
 */
const props = withDefaults(defineProps<{ text: string; layout?: 'card' | 'panel' }>(), { layout: 'card' })
defineEmits<{ (e: 'expand'): void }>()

const html = computed(() => renderSafeMarkdown(props.text || ''))
const box = ref<HTMLElement | null>(null)
const content = ref<HTMLElement | null>(null)
const overflowing = ref(false)
const fadeTop = ref(false)
const fadeBottom = ref(false)
const copied = ref(false)
let pinned = true
let ro: ResizeObserver | null = null
let copyTimer: ReturnType<typeof setTimeout> | null = null

function updateFades(b: HTMLElement) {
  const hiddenBelow = b.scrollHeight - b.clientHeight - b.scrollTop
  fadeTop.value = overflowing.value && b.scrollTop > 4
  fadeBottom.value = overflowing.value && hiddenBelow > 4
}
function measure() {
  const b = box.value
  if (!b) return
  overflowing.value = b.scrollHeight - b.clientHeight > 4
  if (pinned && overflowing.value) b.scrollTop = b.scrollHeight
  updateFades(b)
}
function onScroll() {
  const b = box.value
  if (!b) return
  pinned = b.scrollHeight - b.scrollTop - b.clientHeight < 24
  updateFades(b)
}

watch(html, () => { nextTick(measure) })
watch(() => props.layout, () => { nextTick(measure) })
onMounted(() => {
  measure()
  if (typeof ResizeObserver !== 'undefined' && box.value && content.value) {
    ro = new ResizeObserver(() => measure())
    ro.observe(content.value)
    ro.observe(box.value)
  }
})
onBeforeUnmount(() => { ro?.disconnect(); ro = null; if (copyTimer) clearTimeout(copyTimer) })

async function copy() {
  try {
    await copyText(props.text)
    copied.value = true
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => { copied.value = false }, 1500)
  } catch { /* clipboard unavailable */ }
}
</script>

<style scoped>
/* Cream ink on umber card stock (about 16:1). Every element sets its colour here:
   the app's global p / h1-h6 / a / code rules use theme colours (dark brown in
   the theatre themes), which printed paragraphs dark on the dark card. */
.zen-resp {
  /* Colours are variables so a host can print the reply on paper (Circus) */
  --zr-ink: #fbf5e6;
  --zr-strong: #fff3d0;
  --zr-marker: #e8c98f;
  --zr-link: #f2c46d;
  --zr-link-hover: #ffd98a;
  --zr-code: #f6e7c8;
  --zr-code-bg: rgba(251, 245, 230, 0.14);
  --zr-pre-bg: rgba(0, 0, 0, 0.35);
  --zr-rule: rgba(251, 245, 230, 0.3);
  --zr-btn-border: rgba(251, 245, 230, 0.45);
  --zr-btn-hover: rgba(251, 245, 230, 0.14);
  --zr-scroll: rgba(251, 245, 230, 0.35);
  position: relative;
  color: var(--zr-ink);
}
.zen-resp-box {
  max-height: 11.5em;
  overflow: hidden;
  font: 400 1.15rem / 1.5 'EB Garamond', Georgia, serif;
  color: var(--zr-ink);
  overflow-wrap: anywhere;
}
/* Fade only the edge where text is hidden, and only about two lines of it */
.zen-resp-box.fade-bottom {
  -webkit-mask-image: linear-gradient(to bottom, #000 calc(100% - var(--zen-resp-fade)), transparent);
  mask-image: linear-gradient(to bottom, #000 calc(100% - var(--zen-resp-fade)), transparent);
}
.zen-resp-box.fade-top {
  -webkit-mask-image: linear-gradient(to top, #000 calc(100% - var(--zen-resp-fade)), transparent);
  mask-image: linear-gradient(to top, #000 calc(100% - var(--zen-resp-fade)), transparent);
}
.zen-resp-box.fade-top.fade-bottom {
  -webkit-mask-image: linear-gradient(to bottom, transparent, #000 var(--zen-resp-fade), #000 calc(100% - var(--zen-resp-fade)), transparent);
  mask-image: linear-gradient(to bottom, transparent, #000 var(--zen-resp-fade), #000 calc(100% - var(--zen-resp-fade)), transparent);
}
/* Panel: the whole reply, scrolling inside the panel */
.zen-resp.is-panel { display: flex; flex-direction: column; flex: 1; min-height: 0; }
.zen-resp.is-panel .zen-resp-box {
  max-height: none;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  scrollbar-color: var(--zr-scroll) transparent;
  padding-right: 6px;
}
.zen-resp-actions { display: flex; justify-content: flex-end; gap: 6px; margin-top: 6px; min-height: 20px; }
.zen-resp-btn {
  padding: 1px 9px;
  font: 600 0.68rem / 1.6 'Courier Prime', ui-monospace, monospace;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--zr-ink);
  background: transparent;
  border: 1px solid var(--zr-btn-border);
  border-radius: 3px;
  cursor: pointer;
}
.zen-resp-btn:hover { background: var(--zr-btn-hover); }
.zen-resp-md :deep(p),
.zen-resp-md :deep(ul),
.zen-resp-md :deep(ol),
.zen-resp-md :deep(li),
.zen-resp-md :deep(em),
.zen-resp-md :deep(blockquote),
.zen-resp-md :deep(td),
.zen-resp-md :deep(th) { color: inherit; }
.zen-resp-md :deep(p) { margin: 0 0 0.55em; }
.zen-resp-md :deep(p:last-child) { margin-bottom: 0; }
.zen-resp-md :deep(ul), .zen-resp-md :deep(ol) { margin: 0 0 0.55em; padding-left: 1.3em; }
.zen-resp-md :deep(li) { margin: 0.1em 0; }
.zen-resp-md :deep(li::marker) { color: var(--zr-marker); }
.zen-resp-md :deep(h1), .zen-resp-md :deep(h2), .zen-resp-md :deep(h3),
.zen-resp-md :deep(h4), .zen-resp-md :deep(h5), .zen-resp-md :deep(h6) {
  margin: 0 0 0.35em;
  font: 700 1.1em / 1.3 'EB Garamond', Georgia, serif;
  color: var(--zr-strong);
}
.zen-resp-md :deep(strong) { font-weight: 700; color: var(--zr-strong); }
.zen-resp-md :deep(a) { color: var(--zr-link); text-decoration: underline; }
.zen-resp-md :deep(a:hover) { color: var(--zr-link-hover); }
.zen-resp-md :deep(code) {
  font: 0.82em / 1.4 'Courier Prime', ui-monospace, monospace;
  padding: 0 4px;
  color: var(--zr-code);
  background: var(--zr-code-bg);
  border-radius: 3px;
  overflow-wrap: anywhere;
}
.zen-resp-md :deep(pre) {
  margin: 0 0 0.55em;
  padding: 6px 9px;
  color: var(--zr-code);
  background: var(--zr-pre-bg);
  border: 0;
  border-radius: 4px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.zen-resp-md :deep(pre code) { padding: 0; background: none; }
.zen-resp-md :deep(blockquote) { margin: 0 0 0.55em; padding-left: 0.8em; border-left: 2px solid var(--zr-rule); }
.zen-resp-md :deep(hr) { border: 0; border-top: 1px solid var(--zr-rule); margin: 0.6em 0; }
.zen-resp-md :deep(table) { border-collapse: collapse; font-size: 0.9em; }
.zen-resp-md :deep(th), .zen-resp-md :deep(td) { padding: 1px 6px; border: 1px solid var(--zr-rule); }
</style>
