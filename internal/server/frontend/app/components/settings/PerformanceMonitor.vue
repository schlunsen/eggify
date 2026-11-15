<template>
  <div class="pm">
    <p v-if="error" class="pm-error" role="alert">{{ error }}</p>

    <div v-if="report?.findings.length" class="pm-findings" role="status">
      <h3 class="pm-h">Worth a look</h3>
      <ul>
        <li v-for="f in report.findings" :key="f.id" :data-finding="f.id">{{ f.message }}</li>
      </ul>
    </div>
    <p v-else-if="report && !tiles.server.length" class="pm-note">Collecting the first readings…</p>
    <p v-else-if="report" class="pm-ok" data-test="all-clear">Nothing unusual so far.</p>

    <template v-for="group in groups" :key="group.title">
      <h3 v-if="group.tiles.length" class="pm-h">{{ group.title }}</h3>
      <div v-if="group.tiles.length" class="pm-grid">
        <div v-for="t in group.tiles" :key="t.id" class="pm-tile" :data-tile="t.id">
          <div class="pm-tile__label">{{ t.label }}</div>
          <div class="pm-tile__value">{{ formatValue(t.current, t.unit) }}</div>
          <svg class="pm-spark" viewBox="0 0 120 28" preserveAspectRatio="none" aria-hidden="true">
            <path :d="sparkPath(t.values, 120, 28)" />
          </svg>
          <div class="pm-tile__trend">{{ formatTrend(t.perHour, t.unit) }}</div>
        </div>
      </div>
    </template>

    <div v-if="animations" class="pm-anims" data-test="animations">
      <h3 class="pm-h">Running animations right now</h3>
      <p v-if="!animations.length" class="pm-note">None.</p>
      <ul v-else>
        <li v-for="a in animations" :key="a.name"><strong>{{ a.name }}</strong> × {{ a.count }} <span class="pm-note">on {{ a.example }}</span></li>
      </ul>
    </div>

    <p class="pm-note">
      Sampled every {{ report?.interval_seconds ?? 30 }} s while Eggify runs; the history is kept in memory
      (about a day) and restarts with the app. A trend appears after ten minutes.
    </p>
    <div class="pm-actions">
      <button type="button" class="pm-btn" data-test="copy" @click="copy">{{ copied ? 'Copied' : 'Copy report' }}</button>
      <button type="button" class="pm-btn" @click="load">Refresh</button>
      <button type="button" class="pm-btn" data-test="inspect" @click="inspect">Inspect animations</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { copyText } from '~/utils/clipboard'
import { computed, onMounted, onUnmounted, ref, shallowRef } from 'vue'
import { listRunningAnimations, type RunningAnimation } from '~/utils/perfReporter'
import { buildTiles, formatTrend, formatValue, reportText, sparkPath, type PerfReport } from '~/utils/perfSummary'

const { fetchWithAuth } = useAuthenticatedFetch()
// Replaced wholesale every 15 s, never edited: no need to make 24 h of samples deeply reactive.
const report = shallowRef<PerfReport | null>(null)
const error = ref('')
const copied = ref(false)
const animations = ref<RunningAnimation[] | null>(null)

function inspect() {
  animations.value = listRunningAnimations(document)
}
const tiles = computed(() => buildTiles(report.value))
const groups = computed(() => [
  { title: 'Server', tiles: tiles.value.server },
  { title: 'This page', tiles: tiles.value.page },
])

async function load() {
  try {
    const res = await fetchWithAuth('/api/perf')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    report.value = await res.json()
    error.value = ''
  } catch (e) {
    error.value = `Could not load performance data (${e instanceof Error ? e.message : 'error'}).`
  }
}

async function copy() {
  try {
    const extra = animations.value?.length
      ? '\nRunning animations:\n' + animations.value.map(a => `  ${a.name} x${a.count} on ${a.example}`).join('\n')
      : ''
    await copyText(reportText(report.value) + extra)
    copied.value = true
    setTimeout(() => { copied.value = false }, 1500)
  } catch { /* clipboard unavailable */ }
}

let timer: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  load()
  timer = setInterval(() => { if (document.visibilityState === 'visible') load() }, 15000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<style scoped>
.pm { display: flex; flex-direction: column; gap: 12px; }
.pm-h { margin: 6px 0 0; font-size: 0.78rem; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-secondary); }
.pm-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 10px; }
.pm-tile { border: 1px solid var(--border-color); border-radius: 10px; padding: 10px 12px; background: var(--bg-primary); display: flex; flex-direction: column; gap: 2px; }
.pm-tile__label { font-size: 0.75rem; color: var(--text-secondary); }
.pm-tile__value { font-size: 1.25rem; font-weight: 700; color: var(--text-primary); }
.pm-tile__trend { font-size: 0.75rem; color: var(--text-secondary); }
.pm-spark { width: 100%; height: 28px; }
.pm-spark path { fill: none; stroke: var(--accent-purple); stroke-width: 1.5; vector-effect: non-scaling-stroke; }
.pm-findings { border: 1px solid #b45309; border-radius: 10px; padding: 10px 14px; }
.pm-findings ul { margin: 4px 0 0; padding-left: 18px; color: var(--text-primary); font-size: 0.9rem; }
.pm-anims ul { margin: 4px 0 0; padding-left: 18px; font-size: 0.85rem; color: var(--text-primary); }
.pm-ok, .pm-note { margin: 0; font-size: 0.82rem; color: var(--text-secondary); }
.pm-error { margin: 0; color: #b91c1c; font-size: 0.85rem; }
.pm-actions { display: flex; gap: 8px; }
.pm-btn { padding: 6px 12px; border-radius: 8px; border: 1px solid var(--border-color); background: var(--card-bg); color: var(--text-primary); font-size: 0.85rem; font-weight: 600; cursor: pointer; }
.pm-btn:hover { border-color: var(--accent-purple); }
</style>
