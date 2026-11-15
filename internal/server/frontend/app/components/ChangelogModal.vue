<template>
  <Teleport to="body">
    <div v-if="open" class="cl-overlay" data-testid="changelog-modal" @mousedown.self="closeChangelog">
      <div ref="dialog" class="cl-dialog" role="dialog" aria-modal="true" aria-label="What's new" tabindex="-1" @keydown="onKeydown">
        <header class="cl-head">
          <h2 class="cl-title">What's new</h2>
          <span v-if="current" class="cl-current">You have v{{ current }}</span>
          <button ref="closeBtn" type="button" class="cl-close" title="Close" aria-label="Close" @click="closeChangelog">×</button>
        </header>
        <input
          v-model="query"
          class="cl-search"
          type="search"
          placeholder="Filter changes…"
          aria-label="Filter changes"
          data-testid="changelog-search"
        >
        <div class="cl-body">
          <p v-if="error" class="cl-note cl-note--error" data-testid="changelog-error">{{ error }}</p>
          <p v-else-if="loading && !releases.length" class="cl-note">Loading…</p>
          <p v-else-if="!visible.length && query.trim()" class="cl-note" data-testid="changelog-empty">Nothing matches "{{ query }}".</p>
          <p v-else-if="!visible.length" class="cl-note" data-testid="changelog-empty">No changelog entries.</p>
          <article
            v-for="r in visible"
            :key="r.version"
            class="cl-release"
            :class="{ 'cl-release--upcoming': r.unreleased }"
            data-testid="changelog-release"
          >
            <h3 class="cl-version">
              {{ r.unreleased ? 'Unreleased' : `v${r.version}` }}
              <span v-if="r.name" class="cl-name">{{ r.name }}</span>
              <span v-if="r.unreleased" class="cl-upcoming">upcoming</span>
              <time v-else-if="r.date" class="cl-date">{{ r.date }}</time>
              <span v-if="isNew(r)" class="cl-new">new</span>
            </h3>
            <p v-if="r.note" class="cl-release-note" data-testid="changelog-note">{{ r.note }}</p>
            <section v-for="(s, si) in r.sections" :key="si" class="cl-section">
              <h4 class="cl-tag" :class="`cl-tag--${sectionKind(s.title)}`">{{ s.title }}</h4>
              <ul class="cl-items">
                <!-- eslint-disable-next-line vue/no-v-html -- sanitised by renderSafeMarkdown (DOMPurify) -->
                <li v-for="(item, i) in s.items" :key="i" class="cl-item" v-html="render(item)" />
              </ul>
            </section>
          </article>
          <button
            v-if="canLoadMore"
            type="button"
            class="cl-more"
            :disabled="loading"
            data-testid="changelog-more"
            @click="loadMore"
          >{{ loading ? 'Loading…' : 'Load more' }}</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useChangelog } from '~/composables/useChangelog'
import { useClosable } from '~/composables/useKeyboardShortcuts'
import { renderSafeMarkdown } from '~/utils/safeMarkdown'
import {
  CHANGELOG_PAGE_SIZE, compareVersions, fetchChangelog, filterReleases, sectionKind,
  type ChangelogRelease,
} from '~/utils/changelog'

const { fetchWithAuth } = useAuthenticatedFetch()
const { open, seenBefore, closeChangelog } = useChangelog()

const releases = ref<ChangelogRelease[]>([])
const total = ref(0)
const current = ref('')
const limit = ref(CHANGELOG_PAGE_SIZE)
const loading = ref(false)
const error = ref('')
const query = ref('')

const visible = computed(() => filterReleases(releases.value, query.value))
const canLoadMore = computed(() => releases.value.length < total.value)

useClosable(open, closeChangelog, { modal: true })

function render(markdown: string): string {
  return renderSafeMarkdown(markdown)
}

function isNew(r: ChangelogRelease): boolean {
  return !r.unreleased && !!seenBefore.value && compareVersions(r.version, seenBefore.value) > 0
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await fetchChangelog(fetchWithAuth, { limit: limit.value })
    releases.value = data.releases
    total.value = data.total
    current.value = data.current
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load the changelog.'
  } finally {
    loading.value = false
  }
}

function loadMore() {
  limit.value += CHANGELOG_PAGE_SIZE
  void load()
}

// Focus: into the dialog on open, Tab stays inside, back to the opener on close.
const dialog = ref<HTMLElement | null>(null)
const closeBtn = ref<HTMLElement | null>(null)
let opener: HTMLElement | null = null
const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'Tab' || event.isComposing) return
  const root = dialog.value
  if (!root) return
  const nodes = [...root.querySelectorAll<HTMLElement>(FOCUSABLE)]
  if (!nodes.length) { event.preventDefault(); root.focus(); return }
  const first = nodes[0]!
  const last = nodes[nodes.length - 1]!
  const active = document.activeElement
  if (event.shiftKey && (active === first || active === root || !root.contains(active))) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && (active === last || !root.contains(active))) { event.preventDefault(); first.focus() }
}

watch(open, (isOpen) => {
  if (isOpen) {
    opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
    query.value = ''
    limit.value = CHANGELOG_PAGE_SIZE
    void load()
    // After the dialog is in the document (Teleport mounts it a tick later).
    void nextTick().then(nextTick).then(() => (closeBtn.value || dialog.value)?.focus({ preventScroll: true }))
  } else {
    if (opener?.isConnected) opener.focus({ preventScroll: true })
    opener = null
  }
}, { immediate: true })
</script>

<style scoped>
.cl-overlay { position: fixed; inset: 0; z-index: 2000; display: flex; align-items: center; justify-content: center; padding: 16px; background: rgba(0, 0, 0, 0.45); }
.cl-dialog { display: flex; flex-direction: column; width: min(720px, 100%); max-height: min(86vh, 900px); background: var(--bg-primary); color: var(--text-primary); border: 1px solid var(--border-color); border-radius: 12px; overflow: hidden; }
.cl-head { display: flex; align-items: baseline; gap: 10px; padding: 14px 16px 8px; }
.cl-title { margin: 0; font-size: 1.1rem; }
.cl-current { color: var(--text-secondary); font-size: 0.75rem; font-family: var(--font-mono); }
.cl-close { margin-left: auto; background: none; border: 0; color: var(--text-secondary); font-size: 1.4rem; line-height: 1; cursor: pointer; }
.cl-search { margin: 0 16px 8px; padding: 8px 10px; border: 1px solid var(--border-color); border-radius: 8px; background: var(--overlay-bg); color: var(--text-primary); font: inherit; }
.cl-body { overflow-y: auto; padding: 4px 16px 16px; }
.cl-note { color: var(--text-secondary); }
.cl-note--error { color: var(--status-error); }
.cl-release--upcoming { opacity: 0.8; border-style: dashed; }
.cl-release-note { margin: 0 0 6px; color: var(--text-secondary); font-style: italic; }
.cl-upcoming { font-size: 0.7rem; font-weight: 400; color: var(--text-secondary); font-style: italic; }
.cl-dialog:focus { outline: none; }
.cl-release { padding: 12px 0; border-top: 1px solid var(--border-color); }
.cl-version { display: flex; flex-wrap: wrap; align-items: baseline; gap: 8px; margin: 0 0 6px; font-size: 1rem; font-family: var(--font-mono); }
.cl-name, .cl-date { color: var(--text-secondary); font-size: 0.75rem; font-weight: 400; }
.cl-new { font-size: 0.65rem; text-transform: uppercase; padding: 1px 6px; border-radius: 999px; border: 1px solid var(--accent-purple); color: var(--accent-purple); }
.cl-tag { display: inline-block; margin: 8px 0 4px; padding: 1px 8px; border-radius: 999px; font-size: 0.7rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.04em; color: var(--text-primary); border: 1px solid var(--border-color); background: var(--overlay-bg); }
.cl-tag--added { border-color: var(--accent-green); }
.cl-tag--fixed { border-color: var(--accent-orange); }
.cl-tag--changed, .cl-tag--improved, .cl-tag--performance { border-color: var(--accent-cyan); }
.cl-tag--security { border-color: var(--status-error); }
.cl-tag--removed { border-color: var(--accent-purple); }
.cl-items { margin: 0; padding-left: 18px; }
.cl-item :deep(ul), .cl-item :deep(ol) { margin: 2px 0; padding-left: 18px; }
.cl-item { margin: 4px 0; line-height: 1.45; overflow-wrap: anywhere; }
.cl-item :deep(p) { margin: 0; }
.cl-item :deep(code) { font-family: var(--font-mono); font-size: 0.85em; }
.cl-more { display: block; margin: 12px auto 0; padding: 6px 16px; border: 1px solid var(--border-color); border-radius: 8px; background: var(--overlay-bg); color: var(--text-primary); cursor: pointer; }
@media (max-width: 640px) { .cl-overlay { padding: 0; } .cl-dialog { width: 100%; max-height: 100vh; height: 100%; border-radius: 0; } }
</style>
