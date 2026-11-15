<template>
  <div class="stats-page">
    <!-- Loading Overlay -->
    <div v-if="isInitialLoad" class="loading-overlay">
      <div class="loading-content">
        <div class="loading-spinner"></div>
        <p class="loading-text">Loading statistics...</p>
      </div>
    </div>

    <div class="scrollable-content">
      <div class="container">
        <header class="page-header">
          <div>
            <h1>Stats</h1>
            <p class="subtitle">Usage across your agent sessions, and how this server is set up.</p>
          </div>
          <div class="header-actions">
            <span class="status" :class="{ 'status-connected': connected }">
              <span class="status-dot"></span>
              {{ connected ? 'Live' : 'Connecting…' }}
            </span>
            <button class="refresh-btn" @click="refreshAll" :disabled="isRefreshing" :title="cacheTimestamp ? `Updated ${formatCacheAge()}` : 'Refresh'">
              <svg :class="{ spin: isRefreshing }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <polyline points="23 4 23 10 17 10"/>
                <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
              </svg>
              {{ isRefreshing ? 'Refreshing…' : 'Refresh' }}
            </button>
          </div>
        </header>

        <!-- Key metrics -->
        <section class="kpi-grid" aria-label="Key metrics">
          <div class="kpi">
            <div class="kpi-label">Sessions</div>
            <div class="kpi-value">{{ stats.totalConversations.toLocaleString() }}</div>
            <div class="kpi-sub">{{ stats.activeConversations }} active now</div>
          </div>
          <div class="kpi">
            <div class="kpi-label">Tokens</div>
            <div class="kpi-value">{{ formatNumber(stats.totalTokens) }}</div>
            <div class="kpi-sub">{{ formatNumber(stats.avgTokens) }} per session on average</div>
          </div>
          <div class="kpi">
            <div class="kpi-label">Spend</div>
            <div class="kpi-value">{{ formatCost(stats.agentTotalCost) }}</div>
            <div class="kpi-sub">Agent sessions, all time</div>
          </div>
          <div class="kpi">
            <div class="kpi-label">Claude processes</div>
            <div class="kpi-value">{{ systemInfo.process?.claude_processes ?? '—' }}</div>
            <div class="kpi-sub">Running on this machine</div>
          </div>
        </section>

        <!-- RTK (Rust Token Killer) compression stats -->
        <div class="rtk-wrap">
          <RTKStatsCard breakdown="daily" always-show />
        </div>

        <!-- Configuration -->
        <section class="section">
          <h2 class="section-title">Configuration</h2>
          <div class="config-grid">
            <dl class="config-card">
              <dt class="config-card-title">Agents</dt>
              <div class="config-row">
                <dt>Default model</dt>
                <dd>{{ systemInfo.agent?.model || '—' }}</dd>
              </div>
              <div v-if="systemInfo.provider?.default_provider" class="config-row">
                <dt>Default provider</dt>
                <dd>{{ systemInfo.provider.default_provider }}</dd>
              </div>
              <div class="config-row">
                <dt>Max concurrent sessions</dt>
                <dd>{{ systemInfo.agent?.max_sessions ?? '—' }}</dd>
              </div>
              <div class="config-row">
                <dt>Session retention</dt>
                <dd>{{ systemInfo.agent?.session_retention ? `${systemInfo.agent.session_retention} days` : '—' }}</dd>
              </div>
              <div class="config-row">
                <dt>Cleanup</dt>
                <dd>{{ systemInfo.agent?.cleanup_enabled ? `Every ${systemInfo.agent?.cleanup_interval || 0} h` : 'Off' }}</dd>
              </div>
            </dl>

            <dl class="config-card">
              <dt class="config-card-title">Server</dt>
              <div class="config-row">
                <dt>Hostname</dt>
                <dd class="mono">{{ systemInfo.system?.hostname || '—' }}</dd>
              </div>
              <div class="config-row">
                <dt>Port</dt>
                <dd class="mono">{{ systemInfo.server?.port || '—' }}</dd>
              </div>
              <div class="config-row">
                <dt>TLS</dt>
                <dd><span class="pill" :class="systemInfo.server?.tls ? 'pill-on' : 'pill-off'">{{ systemInfo.server?.tls ? 'On' : 'Off' }}</span></dd>
              </div>
              <div class="config-row">
                <dt>Authentication</dt>
                <dd><span class="pill" :class="systemInfo.server?.auth ? 'pill-on' : 'pill-off'">{{ systemInfo.server?.auth ? 'On' : 'Off' }}</span></dd>
              </div>
              <div class="config-row">
                <dt>Live clients</dt>
                <dd>{{ systemInfo.websocket?.clients ?? 0 }}</dd>
              </div>
            </dl>
          </div>
        </section>

        <!-- Danger zone -->
        <section class="section danger-zone">
          <div class="danger-text">
            <h2 class="section-title">Database</h2>
            <p>{{ dbStats.db_size_human || '…' }} on disk. Purging deletes all history, prompts, notifications and agent sessions. This cannot be undone.</p>
          </div>
          <button class="purge-btn" @click="confirmPurgeDatabase" :disabled="isPurging">
            {{ isPurging ? 'Purging…' : 'Purge database' }}
          </button>
        </section>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">

import type { Stats } from '~/types/analytics'

// Cache keys
const CACHE_KEYS = {
  stats: 'cct_stats_cache',
  dbStats: 'cct_dbstats_cache',
  systemInfo: 'cct_sysinfo_cache',
  timestamp: 'cct_stats_cache_time'
}

// State
const stats = ref<Stats & { agentTotalCost?: number }>({
  totalConversations: 0,
  totalTokens: 0,
  activeConversations: 0,
  avgTokens: 0,
  timestamp: ''
})

const dbStats = ref<any>({
  db_size_human: 'Loading...'
})

const systemInfo = ref<any>({
  system: {},
  server: {},
  agent: {},
  websocket: {},
  database: {}
})

const isPurging = ref(false)
const isInitialLoad = ref(true)
const isRefreshing = ref(false)
const hasCachedData = ref(false)
const cacheTimestamp = ref<number>(0)

// WebSocket connection state
// Use the app-wide socket (provided in app.vue). Creating our own here never
// connected, so the header was stuck on "Connecting…".
const agentWs = inject<any>('agentWs', null)
const connected = computed(() => !!agentWs?.connected?.value)

// Cache helpers
function saveToCache() {
  try {
    localStorage.setItem(CACHE_KEYS.stats, JSON.stringify(stats.value))
    localStorage.setItem(CACHE_KEYS.dbStats, JSON.stringify(dbStats.value))
    localStorage.setItem(CACHE_KEYS.systemInfo, JSON.stringify(systemInfo.value))
    localStorage.setItem(CACHE_KEYS.timestamp, Date.now().toString())
  } catch {
    // localStorage might be full or unavailable
  }
}

function loadFromCache(): boolean {
  try {
    const cachedStats = localStorage.getItem(CACHE_KEYS.stats)
    const cachedDbStats = localStorage.getItem(CACHE_KEYS.dbStats)
    const cachedSystemInfo = localStorage.getItem(CACHE_KEYS.systemInfo)
    const cachedTime = localStorage.getItem(CACHE_KEYS.timestamp)

    if (cachedStats && cachedDbStats && cachedSystemInfo && cachedTime) {
      stats.value = JSON.parse(cachedStats)
      dbStats.value = JSON.parse(cachedDbStats)
      systemInfo.value = JSON.parse(cachedSystemInfo)
      cacheTimestamp.value = parseInt(cachedTime)
      return true
    }
  } catch {
    // Cache read failed
  }
  return false
}

function formatCacheAge(): string {
  if (!cacheTimestamp.value) return ''
  const age = Date.now() - cacheTimestamp.value
  const seconds = Math.floor(age / 1000)
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  return `${hours}h ago`
}

// Load initial stats
async function loadStats() {
  try {
    const data = await $fetch<Stats>('/api/stats')
    if (data) {
      stats.value = data
    }
  } catch (error) {
    // Error loading stats
  }
}

// Load database stats
async function loadDbStats() {
  try {
    const data = await $fetch<any>('/api/db/stats')
    if (data?.stats) {
      dbStats.value = data.stats
    }
  } catch (error) {
    // Error loading database stats
  }
}

// Load system info
async function loadSystemInfo() {
  try {
    const data = await $fetch<any>('/api/system-info')
    if (data) {
      systemInfo.value = data
    }
  } catch (error) {
    // Error loading system info
  }
}

// Refresh all data
async function refreshAll() {
  isRefreshing.value = true
  try {
    await Promise.all([loadStats(), loadDbStats(), loadSystemInfo()])
    saveToCache()
    cacheTimestamp.value = Date.now()
  } finally {
    isRefreshing.value = false
  }
}

// Helper functions
function formatNumber(num: number): string {
  if (num >= 1000000) {
    return (num / 1000000).toFixed(1) + 'M'
  } else if (num >= 1000) {
    return (num / 1000).toFixed(1) + 'K'
  }
  return num.toString()
}

function formatCost(cost?: number): string {
  if (cost == null) return '—'
  return cost.toLocaleString(undefined, { style: 'currency', currency: 'USD', maximumFractionDigits: cost >= 100 ? 0 : 2 })
}

// Purge database functionality
function confirmPurgeDatabase() {
  const confirmed = confirm(
    'Are you sure you want to purge all database data?\n\n' +
    'This will permanently delete:\n' +
    '• All command history\n' +
    '• All user prompts\n' +
    '• All notifications\n' +
    '• All conversation data\n' +
    '• All agent sessions and messages\n\n' +
    'This action cannot be undone.'
  )

  if (confirmed) {
    purgeDatabase()
  }
}

async function purgeDatabase() {
  isPurging.value = true

  const { fetchWithAuth } = useAuthenticatedFetch()

  try {
    const response = await fetchWithAuth('/api/history', {
      method: 'DELETE'
    })

    if (response.ok) {
      // Reload database stats to show updated size
      await loadDbStats()
      alert('Database purged successfully!')
    } else {
      const error = await response.json()
      alert(`Failed to purge database: ${error.error || 'Unknown error'}`)
    }
  } catch (error) {
    alert(`Failed to purge database: ${error}`)
  } finally {
    isPurging.value = false
  }
}

// Load stats on mount
let systemInfoTimer: ReturnType<typeof setInterval> | undefined

onMounted(async () => {
  // Try to load cached data first for instant display
  hasCachedData.value = loadFromCache()

  if (hasCachedData.value) {
    // We have cached data, hide the full-page loader immediately
    isInitialLoad.value = false
  }

  // Fetch fresh data in parallel
  isRefreshing.value = true
  try {
    await Promise.all([loadStats(), loadDbStats(), loadSystemInfo()])
    saveToCache()
    cacheTimestamp.value = Date.now()
    hasCachedData.value = true
  } finally {
    isInitialLoad.value = false
    isRefreshing.value = false
  }

  // Refresh system info every 5 seconds
  systemInfoTimer = setInterval(loadSystemInfo, 5000)
})

onUnmounted(() => {
  if (systemInfoTimer) clearInterval(systemInfoTimer)
})
</script>

<style scoped>
.stats-page {
  background: var(--bg-primary);
  height: calc(100vh - 60px);
  display: flex;
  flex-direction: column;
  position: relative;
}

/* Loading Overlay */
.loading-overlay {
  position: absolute;
  inset: 0;
  background: var(--bg-primary);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}

.loading-content { text-align: center; }

.loading-spinner {
  width: 32px;
  height: 32px;
  border: 3px solid var(--border-color);
  border-top-color: var(--accent-purple);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  margin: 0 auto 14px;
}

@keyframes spin { to { transform: rotate(360deg); } }

.loading-text {
  color: var(--text-secondary);
  font-size: 0.95rem;
  margin: 0;
}

.scrollable-content {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 40px 32px 48px;
}

.container {
  max-width: 1100px;
  margin: 0 auto;
}

/* Page header */
.page-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 28px;
}

.page-header h1 {
  font-size: 2rem;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0 0 6px;
  letter-spacing: -0.02em;
}

.subtitle {
  font-size: 0.95rem;
  color: var(--text-secondary);
  margin: 0;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.status {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 0.85rem;
  color: var(--text-secondary);
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--status-warning);
  animation: pulse 2s ease-in-out infinite;
}

.status-connected .status-dot {
  background: var(--status-success);
  animation: none;
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}

.refresh-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 34px;
  padding: 0 14px;
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--text-primary);
  cursor: pointer;
  transition: border-color 0.15s ease, background-color 0.15s ease;
}

.refresh-btn:hover:not(:disabled) {
  border-color: var(--primary-border-hover, var(--accent-purple));
  background: var(--card-hover);
}

.refresh-btn:disabled { opacity: 0.6; cursor: default; }
.refresh-btn .spin { animation: spin 0.8s linear infinite; }

/* KPI tiles */
.kpi-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
  margin-bottom: 24px;
}

.kpi {
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 12px;
  padding: 20px 22px;
  min-width: 0;
}

.kpi-label {
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--text-secondary);
  margin-bottom: 10px;
}

.kpi-value {
  font-family: var(--font-primary);
  font-size: 2.1rem;
  font-weight: 600;
  line-height: 1.1;
  letter-spacing: -0.02em;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.kpi-sub {
  margin-top: 8px;
  font-size: 0.8rem;
  color: var(--text-muted);
}

/* Sections */
.rtk-wrap {
  margin-bottom: 24px;
}

.section {
  background: var(--card-bg);
  border: 1px solid var(--border-color);
  border-radius: 12px;
  padding: 24px;
  margin-bottom: 24px;
}

.section-title {
  font-size: 1.1rem;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0 0 16px;
}

.config-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 16px;
}

.config-card {
  margin: 0;
  padding: 6px 18px;
  background: var(--bg-secondary);
  border: 1px solid var(--border-color);
  border-radius: 10px;
}

.config-card-title {
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-muted);
  padding: 12px 0 6px;
}

.config-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 10px 0;
  border-top: 1px solid var(--border-color);
  font-size: 0.9rem;
}

.config-row dt { color: var(--text-secondary); }

.config-row dd {
  margin: 0;
  color: var(--text-primary);
  font-weight: 500;
  text-align: right;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.config-row dd.mono {
  font-family: var(--font-mono);
  font-size: 0.85rem;
}

.pill {
  display: inline-block;
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 0.78rem;
  font-weight: 600;
}

.pill-on {
  color: var(--status-success);
  background: color-mix(in srgb, var(--status-success) 14%, transparent);
}

.pill-off {
  color: var(--text-muted);
  background: var(--badge-bg, var(--bg-tertiary));
}

/* Danger zone */
.danger-zone {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  border-color: color-mix(in srgb, var(--status-error) 35%, var(--border-color));
}

.danger-zone .section-title { margin-bottom: 4px; }

.danger-text p {
  margin: 0;
  font-size: 0.9rem;
  color: var(--text-secondary);
}

.purge-btn {
  flex-shrink: 0;
  height: 36px;
  padding: 0 16px;
  background: transparent;
  color: var(--status-error);
  border: 1px solid color-mix(in srgb, var(--status-error) 50%, transparent);
  border-radius: 8px;
  font-size: 0.875rem;
  font-weight: 600;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;
}

.purge-btn:hover:not(:disabled) {
  background: var(--status-error);
  color: #fff;
}

.purge-btn:disabled { opacity: 0.6; cursor: not-allowed; }

@media (max-width: 1000px) {
  .kpi-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}

@media (max-width: 768px) {
  .scrollable-content { padding: 24px 16px 32px; }
  .page-header { flex-direction: column; align-items: flex-start; }
  .page-header h1 { font-size: 1.6rem; }
  .danger-zone { flex-direction: column; align-items: flex-start; }
}

@media (max-width: 480px) {
  .kpi-grid { grid-template-columns: 1fr; }
}
</style>
