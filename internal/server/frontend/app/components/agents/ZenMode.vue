<template>
  <div class="zen-mode" ref="zenContainer" :class="{ 'zen-fullscreen': isFullscreen, [`zen-viz-${activeViz}`]: true, 'zen-warp-out': warpingOut, 'zen-warp-in': warpingIn, 'zen-flying': zenFly.active, 'zen-bd-dark': activeViz === 'circus' && gpuScenes.backdrop.value.dark, 'zen-has-plate': activeViz === 'circus' && plateHeight > 0, 'zen-side-layout': responseLayout === 'panel' }" :style="[plateHeight > 0 ? { '--zen-plate-h': `${plateHeight}px` } : {}, responseLayout === 'panel' ? { '--zen-side-top': `${sidePanelTop}px` } : {}]">
    <!-- Three.js Canvas Container — singleton renderer canvas is attached here -->
    <div ref="zenCanvasContainer" class="zen-canvas-container" :class="{ 'viz-transitioning': vizTransitioning }"></div>


    <!-- Visualization mode transition overlay -->
    <Transition name="zen-viz-label">
      <div v-if="vizTransitioning" class="zen-viz-transition-label">
        <span class="zen-viz-transition-text">{{ activeVizLabel }}</span>
      </div>
    </Transition>

    <!-- Top-left: what the other sessions are doing -->
    <div v-if="(workingSessions?.length || 0) > 0 || (sessionNotices?.length || 0) > 0" class="zen-feed" aria-label="Other sessions">
      <div v-if="workingSessions?.length" class="zen-feed-working">
        <button
          v-for="w in workingSessions"
          :key="w.sessionId"
          type="button"
          class="zen-feed-dot"
          :style="{ '--c': w.projectColor || sessionHue(w.sessionId) }"
          :title="`${w.project ? w.project + ' / ' : ''}${w.name} — working. Click to open`"
          :aria-label="`Open ${w.name} (working)`"
          @click="openSession(w.sessionId)"
        ></button>
        <span class="zen-feed-working-label">{{ workingSessions.length }} working</span>
      </div>
      <TransitionGroup name="zen-notice" tag="div" class="zen-feed-notices">
        <div
          v-for="n in sessionNotices"
          :key="n.key"
          class="zen-notice"
          :class="n.kind"
          role="button"
          tabindex="0"
          :style="{ '--c': n.projectColor || sessionHue(n.sessionId) }"
          :aria-label="`${n.kind === 'done' ? 'Finished' : 'Needs you'}: ${n.name}. Open session`"
          @click="openSession(n.sessionId)"
          @keydown.enter.prevent="openSession(n.sessionId)"
        >
          <span class="zen-notice-avatar">
            <img v-if="n.avatarImage" :src="n.avatarImage" alt="" />
            <span v-else>{{ (n.name || '?').charAt(0).toUpperCase() }}</span>
          </span>
          <span class="zen-notice-body">
            <span class="zen-notice-top">
              <span class="zen-notice-kind">{{ n.kind === 'done' ? 'Finished' : 'Needs you' }}</span>
              <span class="zen-notice-time">{{ timeAgo(n.at) }}</span>
            </span>
            <span class="zen-notice-name"><span v-if="n.project" class="zen-notice-project">{{ n.project }} /</span> {{ n.name }}</span>
            <span v-if="n.snippet" class="zen-notice-snippet">{{ n.snippet }}</span>
            <span class="zen-notice-go">Open session →</span>
          </span>
          <button type="button" class="zen-notice-close" aria-label="Dismiss" title="Dismiss" @click.stop="emit('dismiss-notice', n.key)">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg>
          </button>
        </div>
      </TransitionGroup>
    </div>

    <!-- Arriving in another session -->
    <Transition name="zen-arrive">
      <div v-if="arrivingName" class="zen-arrive-label" aria-live="polite">
        <span class="zen-arrive-kicker">Now in</span>
        <span class="zen-arrive-name">{{ arrivingName }}</span>
      </div>
    </Transition>

    <!-- Top-right: Avatar + Context Usage -->
    <div class="zen-top-right">
      <div class="zen-avatar-card" :class="{ 'context-anim-active': contextAnimActive }">
        <div class="zen-avatar-ring-wrapper" :class="{ 'avatar-flip': avatarFlipping }">
          <div class="zen-avatar-ring" :style="avatarRingStyle" :class="{ 'ring-glow': contextAnimActive }">
            <img
              v-if="avatarImage"
              :src="avatarImage"
              :alt="avatarName || 'Avatar'"
              class="zen-avatar-img"
            />
            <div v-else class="zen-avatar-fallback">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
                <circle cx="12" cy="7" r="4"/>
              </svg>
            </div>
          </div>
          <!-- Radial progress arc overlay -->
          <svg
            v-if="contextAnimActive"
            class="zen-context-arc"
            width="40" height="40"
            viewBox="0 0 40 40"
          >
            <circle
              class="context-arc-track"
              cx="20" cy="20" r="17"
              fill="none"
              :stroke="contextArcColor"
              stroke-width="2.5"
              opacity="0.15"
            />
            <circle
              class="context-arc-fill"
              cx="20" cy="20" r="17"
              fill="none"
              :stroke="contextArcColor"
              stroke-width="2.5"
              stroke-linecap="round"
              :stroke-dasharray="contextArcCircumference"
              :stroke-dashoffset="contextArcOffset"
              transform="rotate(-90 20 20)"
            />
          </svg>
        </div>
        <div class="zen-avatar-info">
          <span v-if="avatarName" class="zen-avatar-name">{{ avatarName }}</span>
          <span v-if="contextPercent !== null && contextPercent !== undefined" class="zen-context-pct" :class="[contextLevel, { 'context-pct-flash': contextAnimActive }]">{{ Math.round(contextPercent) }}%</span>
        </div>
      </div>
    </div>

    <!-- Session title plate (Circus stage): name and AI summary -->
    <ZenNamePlate
      v-if="activeViz === 'circus'"
      :name="plateSessionName"
      :summary="plateDigest?.summary"
      :ghost="!!isDictating || !!composing || !!inputMessage"
      :dark="gpuScenes.backdrop.value.dark"
      @height="plateHeight = $event"
    />

    <!-- Top-center: Project & Branch indicator -->
    <Transition name="zen-msg-fade">
      <div v-if="projectName || gitBranch" class="zen-top-center">
        <div class="zen-project-indicator">
          <span v-if="projectName" class="zen-project-name" :style="projectColor ? { color: projectColor } : {}">{{ projectName }}</span>
          <span v-if="projectName && gitBranch" class="zen-project-sep">/</span>
          <span v-if="gitBranch" class="zen-branch-name">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" style="vertical-align: -1px; margin-right: 3px;">
              <line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>
            </svg>{{ gitBranch }}
          </span>
          <!-- Git stats badges with file tooltips (subtle, only when dirty) -->
          <template v-if="gitStats && !gitStats.clean">
            <span class="zen-git-stats">
              <span v-if="gitStats.staged > 0" class="zen-git-badge zen-git-staged zen-git-badge-hover">
                +{{ gitStats.staged }}
                <span v-if="gitStats.stagedFiles?.length" class="zen-git-tooltip">
                  <span class="zen-git-tooltip-title">Staged</span>
                  <span v-for="f in gitStats.stagedFiles" :key="f" class="zen-git-tooltip-file">{{ shortPath(f) }}</span>
                </span>
              </span>
              <span v-if="gitStats.modified > 0" class="zen-git-badge zen-git-modified zen-git-badge-hover">
                ~{{ gitStats.modified }}
                <span v-if="gitStats.modifiedFiles?.length" class="zen-git-tooltip">
                  <span class="zen-git-tooltip-title">Modified</span>
                  <span v-for="f in gitStats.modifiedFiles" :key="f" class="zen-git-tooltip-file">{{ shortPath(f) }}</span>
                </span>
              </span>
              <span v-if="gitStats.untracked > 0" class="zen-git-badge zen-git-untracked zen-git-badge-hover">
                ?{{ gitStats.untracked }}
                <span v-if="gitStats.untrackedFiles?.length" class="zen-git-tooltip">
                  <span class="zen-git-tooltip-title">Untracked</span>
                  <span v-for="f in gitStats.untrackedFiles" :key="f" class="zen-git-tooltip-file">{{ shortPath(f) }}</span>
                </span>
              </span>
              <span v-if="gitStats.deleted > 0" class="zen-git-badge zen-git-deleted zen-git-badge-hover">
                -{{ gitStats.deleted }}
                <span v-if="gitStats.deletedFiles?.length" class="zen-git-tooltip">
                  <span class="zen-git-tooltip-title">Deleted</span>
                  <span v-for="f in gitStats.deletedFiles" :key="f" class="zen-git-tooltip-file">{{ shortPath(f) }}</span>
                </span>
              </span>
            </span>
          </template>
          <span v-else-if="gitStats && gitStats.clean" class="zen-git-clean" title="Clean working tree">
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
              <path d="M20 6L9 17L4 12"/>
            </svg>
          </span>
        </div>
        <!-- Session tag (from localStorage) -->
        <span v-if="sessionTag" class="zen-session-tag">{{ sessionTag }}</span>
      </div>
    </Transition>

    <!-- Bottom-right: Viz picker + Sound toggle + Fullscreen -->
    <div class="zen-bottom-right-controls">
      <!-- Visualization scene picker -->
      <ZenVizPicker />

      <div class="zen-sound-control" :class="{ active: zenAudio.isEnabled.value }">
        <!-- Main sound toggle -->
        <button class="zen-sound-btn" @click="zenAudio.toggle()" data-testid="zen-sound-toggle" :aria-pressed="zenAudio.isEnabled.value" :title="zenAudio.isEnabled.value ? 'Sound on: click to mute' : 'Sound muted: click to turn on'">
          <svg v-if="zenAudio.isEnabled.value" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/>
            <path d="M19.07 4.93a10 10 0 0 1 0 14.14M15.54 8.46a5 5 0 0 1 0 7.07"/>
          </svg>
          <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/>
            <line x1="23" y1="9" x2="17" y2="15"/><line x1="17" y1="9" x2="23" y2="15"/>
          </svg>
          <span class="zen-sound-state" data-testid="zen-sound-state">{{ zenAudio.isEnabled.value ? 'sound on' : 'muted' }}</span>
        </button>

        <!-- Expand panel button (only when sound is on) -->
        <Transition name="zen-vol-fade">
          <button v-if="zenAudio.isEnabled.value" class="zen-sound-expand-btn" @click="zenAudio.togglePanel()" :title="zenAudio.showPanel.value ? 'Close mixer' : 'Open mixer'">
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path v-if="!zenAudio.showPanel.value" d="M12 5v14M5 12h14"/>
              <path v-else d="M5 12h14"/>
            </svg>
          </button>
        </Transition>

        <!-- Expanded sound panel -->
        <Transition name="zen-panel-fade">
          <div v-if="zenAudio.isEnabled.value && zenAudio.showPanel.value" class="zen-sound-panel">
            <!-- LEFT: Sound controls -->
            <div class="zen-panel-section">
              <div class="zen-panel-section-title">Sound</div>
              <div class="zen-panel-row">
                <div class="zen-volume-horizontal">
                  <span class="zen-mixer-label">vol</span>
                  <input
                    type="range" min="0" max="1" step="0.01"
                    :value="zenAudio.volume.value"
                    @input="zenAudio.setVolume(parseFloat(($event.target as HTMLInputElement).value))"
                    class="zen-volume-range-h"
                  />
                </div>
              </div>
              <div class="zen-panel-row">
                <button class="zen-mixer-toggle" :class="{ on: zenAudio.droneEnabled.value }" @click="zenAudio.toggleDrone()" title="Toggle drone">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/>
                  </svg>
                  <span class="zen-mixer-label">drone</span>
                </button>
                <button class="zen-mixer-toggle" :class="{ on: zenAudio.sfxEnabled.value }" @click="zenAudio.toggleSfx()" title="Toggle SFX">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>
                  </svg>
                  <span class="zen-mixer-label">sfx</span>
                </button>
                <button class="zen-mixer-toggle" :class="{ on: ttsEnabled }" @click="toggleTtsEnabled()" title="Toggle TTS voice output">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="23"/><line x1="8" y1="23" x2="16" y2="23"/>
                  </svg>
                  <span class="zen-mixer-label">tts</span>
                </button>
                <button class="zen-mixer-toggle" :class="{ on: zenMusic.isEnabled.value }" @click="zenMusic.toggle()" title="Toggle background music">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>
                  </svg>
                  <span class="zen-mixer-label">music</span>
                </button>
                <button
                  v-if="zenMusic.isEnabled.value"
                  class="zen-mixer-toggle"
                  :class="{ on: zenMusic.source.value === 'reel' }"
                  data-testid="zen-music-source"
                  @click="zenMusic.setSource(zenMusic.source.value === 'reel' ? 'ambient' : 'reel')"
                  :title="zenMusic.source.value === 'reel' ? 'Playing the reel music — click for ambient piano' : 'Playing ambient piano — click for the reel music'"
                >
                  <span class="zen-mixer-label">{{ zenMusic.source.value === 'reel' ? 'reel' : 'ambient' }}</span>
                </button>
              </div>
            </div>

            <div class="zen-mixer-sep-v"></div>

            <!-- CENTER: Voice -->
            <div class="zen-panel-section">
              <div class="zen-panel-section-title">Voice</div>
              <div v-if="zenTTS.error.value" class="zen-tts-error" role="status" data-testid="zen-tts-error">
                <span class="zen-mixer-label">{{ zenTTS.error.value }}</span>
                <button class="zen-mixer-toggle" data-testid="zen-tts-retry" @click="zenTTS.retry()"><span class="zen-mixer-label">Retry</span></button>
              </div>
              <div v-else-if="zenTTS.retrying.value" class="zen-tts-error" role="status"><span class="zen-mixer-label">Retrying voice…</span></div>
              <!-- Voice list -->
              <div class="zen-voice-selector">
                <template>
                  <div v-if="zenTTS.kokoro.status.value === 'loading'" class="zen-f5-loading">
                    <div class="zen-f5-progress-bar"><div class="zen-f5-progress-fill" :style="{ width: zenTTS.kokoro.loadProgress.value + '%' }"></div></div>
                    <span class="zen-mixer-label">{{ zenTTS.kokoro.loadProgress.value }}%</span>
                  </div>
                  <button v-for="voice in KOKORO_VOICES" :key="voice.id" class="zen-voice-btn"
                    :class="{ active: zenTTS.kokoro.selectedVoice.value === voice.id }"
                    @click="zenTTS.kokoro.setVoice(voice.id)"
                    :title="`${voice.label} ${voice.gender} ${voice.accent}`"
                  >
                    <span class="zen-voice-gender">{{ voice.gender }}</span>
                    <span class="zen-voice-name">{{ voice.label }}</span>
                  </button>
                </template>
              </div>
            </div>

            <div class="zen-mixer-sep-v"></div>

            <!-- RIGHT: Effects -->
            <div class="zen-panel-section">
              <div class="zen-panel-section-title">FX</div>
              <div style="display: flex; gap: 6px">
                <button class="zen-mixer-toggle" :class="{ on: zenTTS.reverbEnabled.value }" @click="zenTTS.toggleReverb()" title="Voice reverb">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M2 12C2 6.5 6.5 2 12 2a10 10 0 0 1 8 4"/>
                    <path d="M5 12a7 7 0 0 1 12-5"/>
                    <path d="M8 12a4 4 0 0 1 7-3"/>
                    <circle cx="12" cy="12" r="1"/>
                  </svg>
                  <span class="zen-mixer-label">reverb</span>
                </button>
                <button class="zen-mixer-toggle" :class="{ on: zenTTS.glitchEnabled.value }" @click="zenTTS.toggleGlitch()" title="Robotic glitch effect">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z"/>
                  </svg>
                  <span class="zen-mixer-label">glitch</span>
                </button>
              </div>
              <div v-if="zenTTS.reverbEnabled.value" class="zen-volume-horizontal" style="margin-top: 6px">
                <span class="zen-mixer-label" style="width: 20px">dry</span>
                <input
                  type="range" min="0" max="1" step="0.05"
                  :value="zenTTS.reverbAmount.value"
                  @input="zenTTS.setReverbAmount(parseFloat(($event.target as HTMLInputElement).value))"
                  class="zen-volume-range-h"
                />
                <span class="zen-mixer-label" style="width: 20px">wet</span>
              </div>
            </div>
          </div>
        </Transition>
      </div>

    <!-- Fly mode (Galaxy) -->
    <button
      v-if="zenFly.available"
      class="zen-fullscreen-btn zen-fly-btn"
      :class="{ active: zenFly.active }"
      type="button"
      :title="zenFly.active ? 'Land (Esc)' : `Fly (${zenFlyKey}) — chase the orbs`"
      :aria-pressed="zenFly.active"
      @click="toggleFly"
    >
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" aria-hidden="true">
        <path d="M21 12 3 5l3.5 7L3 19z" /><path d="M6.5 12H12" />
      </svg>
    </button>

    <button class="zen-fullscreen-btn" @click="toggleFullscreen" :title="isFullscreen ? 'Exit fullscreen (Esc)' : (fullscreenKey ? `Fullscreen (${fullscreenKey})` : 'Fullscreen')">
      <svg v-if="!isFullscreen" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3"/>
      </svg>
      <svg v-else width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M4 14h6v6m10-10h-6V4m0 6l7-7M3 21l7-7"/>
      </svg>
    </button>
    </div>

    <!-- Flight panel -->
    <Transition name="zen-fly-hud">
      <div v-if="zenFly.active" class="zen-fly-hud" role="status" aria-live="polite">
        <div class="zen-fly-row">
          <span class="zen-fly-kicker">Flying</span>
          <span class="zen-fly-speed">{{ Math.round(zenFly.speed * 3.6) }}<small> ly/h</small></span>
        </div>
        <div class="zen-fly-throttle" aria-hidden="true"><span :style="{ width: `${Math.round(zenFly.throttle * 100)}%` }" /></div>
        <div class="zen-fly-score">
          <span class="zen-fly-points">{{ zenFly.score }}<small> pts</small></span>
          <span v-if="zenFly.combo > 1" :key="`c${zenFly.pickups}`" class="zen-fly-combo">×{{ zenFly.combo }}</span>
          <span class="zen-fly-best">best {{ zenFly.best }}</span>
          <Transition name="zen-fly-pop">
            <span v-if="popVisible" :key="zenFly.pickups" class="zen-fly-pop">+{{ zenFly.lastPoints }}</span>
          </Transition>
        </div>
        <div v-if="zenFly.near" class="zen-fly-near">Near {{ zenFly.near }}</div>
        <div v-else class="zen-fly-near empty">Fly through the glowing orbs · gold = 50</div>
        <div class="zen-fly-keys">
          <span><kbd>W</kbd><kbd>S</kbd> pitch</span>
          <span><kbd>A</kbd><kbd>D</kbd> bank</span>
          <span><kbd>⇧</kbd><kbd>Q</kbd> speed</span>
          <span>drag to look</span>
        </div>
        <button type="button" class="zen-fly-land" @click="toggleFly">Land <kbd>Esc</kbd></button>
      </div>
    </Transition>

    <!-- Status Overlay -->
    <div class="zen-overlay">
      <!-- Session Status -->
      <div class="zen-status-badge" :class="[sessionStatus, { awaiting: awaitingUser }]">
        <span class="status-dot"></span>
        <span class="status-text">{{ statusLabel }}</span>
      </div>

      <!-- Subagents summary (compact) -->
      <div v-if="agents.length > 0" class="zen-agents-summary">
        <span class="agents-count">{{ runningCount }} active</span>
        <span class="agents-total">/ {{ agents.length }} subagents</span>
      </div>

      <!-- No agents - calm message -->
      <div v-else class="zen-empty">
        <p class="zen-empty-text">{{ emptyMessage }}</p>
      </div>

      <!-- Session Switcher OR Message Cards — smooth crossfade between them -->
      <Transition name="zen-panel-fade" mode="out-in" @after-enter="onPanelEnter">
        <!-- Session Switcher Panel -->
        <div v-if="showSessionSwitcher" key="session-switcher" class="zen-session-switcher">
          <div class="zen-session-switcher-header">
            <div class="zen-session-search-wrapper">
              <svg class="zen-session-search-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>
              </svg>
              <input
                ref="sessionSearchInput"
                v-model="sessionSearchQuery"
                class="zen-session-search"
                placeholder="Search sessions..."
                @keydown="handleSessionKeydown"
              />
              <kbd class="zen-session-kbd">esc</kbd>
            </div>
          </div>
          <div class="zen-session-list" v-if="filteredSessions.length > 0">
            <button
              v-for="(session, idx) in filteredSessions"
              :key="session.id"
              class="zen-session-item"
              :class="{
                'is-selected': idx === selectedSessionIndex,
                'is-current': session.id === sessionId
              }"
              @click="selectSessionFromList(session.id)"
              @mouseenter="!navigatingViaKeyboard && (selectedSessionIndex = idx)"
            >
              <div class="zen-session-item-left">
                <span
                  class="zen-session-dot"
                  :class="session.status"
                  :style="session.projectColor ? { background: session.projectColor } : {}"
                ></span>
                <div class="zen-session-item-info">
                  <span class="zen-session-item-name">
                    <span v-if="session.projectName" class="zen-session-project">{{ session.projectName }} /</span>
                    {{ sessionDisplayName(session) }}
                  </span>
                  <span class="zen-session-item-meta">
                    <span v-if="session.gitBranch" class="zen-session-branch">{{ session.gitBranch }}</span>
                    <span v-if="session.modelName" class="zen-session-model">{{ session.modelName }}</span>
                    <span v-if="session.messageCount" class="zen-session-msgs">{{ session.messageCount }} msgs</span>
                  </span>
                </div>
              </div>
              <div class="zen-session-item-right">
                <span v-if="session.id === sessionId" class="zen-session-current-badge">current</span>
                <span class="zen-session-time">{{ formatRelativeTime(session.updatedAt) }}</span>
              </div>
            </button>
          </div>
          <div v-else class="zen-session-empty">
            <span>No sessions found</span>
          </div>
        </div>

        <!-- Message History Panel -->
        <div v-else-if="showMessageHistory" key="message-history" class="zen-message-history" tabindex="-1" @keydown="handleHistoryKeydown">
          <div class="zen-history-header">
            <div class="zen-history-title-row">
              <svg class="zen-history-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/>
              </svg>
              <span class="zen-history-title">Recent responses</span>
              <kbd class="zen-session-kbd">esc</kbd>
            </div>
          </div>
          <div class="zen-history-list" v-if="historyMessages.length > 0">
            <div
              v-for="(msg, idx) in historyMessages"
              :key="msg.id"
              class="zen-history-item"
              :class="{ 'is-selected': idx === selectedHistoryIndex, 'is-latest': idx === 0 }"
              @mouseenter="!navigatingViaKeyboard && (selectedHistoryIndex = idx)"
              tabindex="0"
            >
              <div class="zen-history-item-header">
                <span class="zen-history-item-number">#{{ historyMessages.length - idx }}</span>
                <span v-if="idx === 0" class="zen-history-latest-badge">latest</span>
                <span class="zen-history-item-time">{{ formatRelativeTime(typeof msg.timestamp === 'string' ? msg.timestamp : msg.timestamp.toISOString()) }}</span>
              </div>
              <div class="zen-history-item-text">{{ truncate(msg.text, 180) }}</div>
            </div>
          </div>
          <div v-else class="zen-history-empty">
            <span>No responses yet</span>
          </div>
        </div>

        <!-- Normal message cards -->
        <div v-else key="message-cards" class="zen-messages-panel">
          <!-- Last user input — always shown with smooth crossfade -->
          <Transition name="zen-user-beam" mode="out-in">
            <div
              v-if="lastUserMessage && lastUserMessage.length > 0 && closedUserMessage !== lastUserMessage"
              :key="'user-' + lastUserMessage"
              class="zen-last-message zen-user-message"
              data-testid="zen-user-message"
            >
              <div class="zen-last-message-label">
                Your message
                <button
                  type="button"
                  class="zen-card-close"
                  data-testid="zen-user-close"
                  title="Hide until you send another message"
                  aria-label="Hide your message"
                  @click.stop="closedUserMessage = lastUserMessage ?? ''"
                >×</button>
              </div>
              <div class="zen-last-message-text zen-beam-text">{{ truncate(lastUserMessage, 200) }}</div>
            </div>
          </Transition>

          <!-- Last assistant message — smooth crossfade, dismissed on send until new response -->
          <Transition name="zen-msg-fade" mode="out-in">
            <div
              v-if="lastAssistantMessage && lastAssistantMessage.length > 0 && !assistantDismissed && !responseClosed"
              :key="'asst-' + latestResponseKey"
              class="zen-last-message zen-response-card"
              :class="[responseLayout === 'panel' ? 'zen-response-panel' : 'zen-last-message-clickable', { 'is-writing': sessionStatus === 'processing' }]"
              data-testid="zen-latest-response"
              @click="responseLayout === 'card' && (expandedResponse = true)"
            >
              <div class="zen-last-message-label">
                Latest response
                <!-- Read aloud button (respects master mute + TTS toggle) -->
                <button
                  class="zen-tts-btn"
                  :class="{ playing: zenTTS.isPlaying.value, loading: zenTTS.isLoading.value, disabled: !zenAudio.isEnabled.value || !ttsEnabled }"
                  @click.stop="toggleReadAloud"
                  :title="!ttsEnabled ? 'TTS disabled' : !zenAudio.isEnabled.value ? 'Sound is muted' : zenTTS.isPlaying.value ? 'Stop reading' : 'Read aloud'"
                >
                  <!-- Loading spinner -->
                  <svg v-if="zenTTS.isLoading.value" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="zen-tts-spinner">
                    <path d="M21 12a9 9 0 1 1-6.219-8.56"/>
                  </svg>
                  <!-- Stop icon -->
                  <svg v-else-if="zenTTS.isPlaying.value" width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                    <rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/>
                  </svg>
                  <!-- Play/speak icon -->
                  <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/>
                    <path d="M15.54 8.46a5 5 0 0 1 0 7.07"/>
                  </svg>
                </button>
                <!-- Auto-read toggle -->
                <button
                  class="zen-tts-btn zen-auto-read-btn"
                  :class="{ on: autoRead }"
                  @click.stop="toggleAutoRead()"
                  :title="autoRead ? 'Auto-read ON — click to disable' : 'Auto-read OFF — click to auto-read new responses'"
                >
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83"/>
                  </svg>
                </button>
                <!-- Panel: Copy sits in the header (the card has it under the text) -->
                <button
                  v-if="responseLayout === 'panel'"
                  type="button"
                  class="zen-resp-head-copy"
                  data-testid="zen-resp-panel-copy"
                  :title="responseCopied ? 'Copied' : 'Copy response'"
                  @click.stop="copyResponse"
                >{{ responseCopied ? 'Copied' : 'Copy' }}</button>
                <button
                  type="button"
                  class="zen-card-close"
                  data-testid="zen-resp-close"
                  title="Hide until the next reply"
                  aria-label="Hide this reply until the next one"
                  @click.stop="closeResponse"
                >×</button>
              </div>
              <ZenResponseBody :text="lastAssistantMessage" :layout="responseLayout" @expand="expandedResponse = true" />
            </div>
          </Transition>

          <!-- Closed reply: a small tab brings it back (a new reply reopens it anyway) -->
          <Transition name="zen-msg-fade">
            <button
              v-if="responseClosed && lastAssistantMessage && !assistantDismissed"
              type="button"
              class="zen-resp-reopen"
              :class="{ 'is-panel': responseLayout === 'panel' }"
              data-testid="zen-resp-reopen"
              title="Show the latest reply"
              @click.stop="reopenResponse"
            >Show reply</button>
          </Transition>

          <!-- Images the agent looked at this turn (e.g. Read on a screenshot) -->
          <Transition name="zen-msg-fade">
            <div v-if="zenImages.length > 0" class="zen-last-message zen-images-card">
              <div class="zen-last-message-label">
                {{ zenImages.length === 1 ? 'Image' : `Images · ${zenImages.length}` }}
              </div>
              <TransitionGroup name="zen-image-pop" tag="div" class="zen-images-strip">
                <button
                  v-for="(img, idx) in zenImages"
                  :key="img.dataUrl.slice(-48) + idx"
                  type="button"
                  class="zen-image-thumb"
                  :class="{ 'is-latest': idx === zenImages.length - 1 }"
                  :title="img.label || 'Open image'"
                  @click="openZenImage(idx)"
                >
                  <img :src="img.dataUrl" :alt="img.label || `Image ${idx + 1}`" />
                  <span v-if="img.label" class="zen-image-label">{{ img.label }}</span>
                </button>
              </TransitionGroup>
            </div>
          </Transition>
        </div>
      </Transition>

      <!-- Expanded Response Overlay -->
      <Transition name="zen-expand-fade">
        <div v-if="expandedResponse && lastAssistantMessage" class="zen-expanded-overlay" @click.self="expandedResponse = false">
          <div class="zen-expanded-card">
            <div class="zen-expanded-header">
              <span class="zen-expanded-label">
                Latest response
                <button
                  class="zen-tts-btn zen-tts-btn-expanded"
                  :class="{ playing: zenTTS.isPlaying.value, loading: zenTTS.isLoading.value, disabled: !zenAudio.isEnabled.value || !ttsEnabled }"
                  @click.stop="toggleReadAloud"
                  :title="!ttsEnabled ? 'TTS disabled' : !zenAudio.isEnabled.value ? 'Sound is muted' : zenTTS.isPlaying.value ? 'Stop reading' : 'Read aloud'"
                >
                  <svg v-if="zenTTS.isLoading.value" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="zen-tts-spinner">
                    <path d="M21 12a9 9 0 1 1-6.219-8.56"/>
                  </svg>
                  <svg v-else-if="zenTTS.isPlaying.value" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                    <rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/>
                  </svg>
                  <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/>
                    <path d="M15.54 8.46a5 5 0 0 1 0 7.07"/>
                  </svg>
                </button>
              </span>
              <button class="zen-expanded-close" @click="expandedResponse = false" title="Close (Esc)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M18 6 6 18"/><path d="m6 6 12 12"/>
                </svg>
              </button>
            </div>
            <div class="zen-expanded-body">
              <div class="zen-expanded-text">{{ lastAssistantMessage }}</div>
            </div>
          </div>
        </div>
      </Transition>

      <!-- Voice Transcription Overlay — visible when dictating with input hidden -->
      <Transition name="zen-dictation-fade">
        <div v-if="isDictating && inputMessage" class="zen-dictation-overlay">
          <div class="zen-dictation-card">
            <div class="zen-dictation-indicator">
              <span class="zen-dictation-dot"></span>
              <span class="zen-dictation-label">Transcribing</span>
            </div>
            <div class="zen-dictation-text">{{ inputMessage }}</div>
          </div>
        </div>
      </Transition>

      <!-- Bottom Metrics Bar -->
      <div class="zen-metrics">
        <span class="metric" v-if="(messageCount ?? 0) > 0">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/>
          </svg>
          {{ messageCount }} messages
        </span>
        <span class="metric" v-if="tokenCount">
          {{ formatTokens(tokenCount) }} tokens
        </span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { copyText } from '~/utils/clipboard'
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useBackgroundAgents, type BackgroundAgent } from '~/composables/agents/useBackgroundAgents'
import { useZenAnimation } from '~/composables/agents/useZenAnimation'
import { getZenAudio } from '~/composables/useZenAudio'
import { getZenMusic } from '~/composables/useZenMusic'
import { getZenTTS, KOKORO_VOICES } from '~/composables/useZenTTS'
import { useZenViz, DEFAULT_ZEN_VIZ } from '~/composables/agents/useZenViz'
import { useZenGpuScenes } from '~/composables/agents/zen/useZenGpuScenes'
import { useSessionDigests } from '~/composables/agents/useSessionDigests'
import { setCircusMessageContext } from '~/composables/agents/zen/circusMessages'
import { zenFlyState as zenFly, setZenFlying } from '~/composables/agents/zen/flyState'
import type { ZenSessionNotice, ZenWorkingSession } from '~/composables/agents/useZenSessionFeed'
import { getEventBus } from '~/stores/events/eventBus'
import ZenVizPicker from '~/components/ui/ZenVizPicker.vue'
import ZenNamePlate from '~/components/agents/ZenNamePlate.vue'
import ZenResponseBody from '~/components/agents/ZenResponseBody.vue'
import { zenResponseLayout, zenSidePanelTop, type ZenResponseLayout } from '~/utils/zenLayout'
import { SESSION_TAGS_STORAGE_KEY, readSessionTags } from '~/utils/sessionTags'

/** Top of the name plate (px), as --zen-plate-top in the styles below */
const ZEN_PLATE_TOP = 58
import { useShortcut, useClosable, useShortcutLabels } from '~/composables/useKeyboardShortcuts'

export interface ZenSessionItem {
  id: string
  status: 'active' | 'idle' | 'ended' | 'processing'
  updatedAt: string  // ISO string
  projectName?: string
  projectColor?: string
  gitBranch?: string
  contextSummary?: string
  messageCount?: number
  modelName?: string
  avatarImage?: string | null
  avatarName?: string | null
  avatarColor?: string | null
  lastUserMessage?: string
  workingDirectory?: string
}

interface Props {
  sessionId: string
  sessionStatus: string
  contextPercent?: number | null
  messageCount?: number
  tokenCount?: number
  lastAssistantMessage?: string
  lastUserMessage?: string
  projectId?: string
  projectName?: string
  projectColor?: string
  gitBranch?: string
  gitStats?: { modified: number; untracked: number; staged: number; deleted: number; modifiedFiles?: string[]; untrackedFiles?: string[]; stagedFiles?: string[]; deletedFiles?: string[]; clean: boolean; ahead: number; behind: number } | null
  avatarImage?: string | null
  avatarName?: string | null
  avatarColor?: string | null
  recentSessions?: ZenSessionItem[]
  /** Other sessions that finished or need the user */
  sessionNotices?: ZenSessionNotice[]
  /** Other sessions currently working */
  workingSessions?: ZenWorkingSession[]
  recentAssistantMessages?: { id: string; text: string; timestamp: Date; sequence: number }[]
  isDictating?: boolean
  /** The composer text, passed only while dictating (the transcription overlay shows it) */
  inputMessage?: string
  /** The composer is not empty (fades the name plate); a boolean, so typing does not re-render Zen */
  composing?: boolean
  /** The agent is blocked on the user (permission request or question). */
  awaitingUser?: boolean
  /** Images the agent saw in the current turn (Read on image files, image results). */
  turnImages?: { dataUrl: string; mediaType: string; label?: string }[]
}

const props = defineProps<Props>()

// ─── Zen Audio ───────────────────────────────────────────────
const zenAudio = getZenAudio()
const zenTTS = getZenTTS()
const zenShortcutLabels = useShortcutLabels()
const zenFlyKey = zenShortcutLabels.label('zen.fly')
// In the desktop app ⌃⌘F is the window's native full screen (View menu)
const fullscreenKey = zenShortcutLabels.shell === 'wails' ? '' : zenShortcutLabels.label('view.fullscreen')
const zenMusic = getZenMusic()

// Zen owns the drone, SFX, music and read-aloud. The audio objects outlive
// this component, so leaving Zen (Live view, another tab, session change away
// from it) has to stop them, or they keep sounding with nothing on screen.
// The music fades out (about 1.8 s) before it is released; coming back during
// the fade brings the same track back in.
function silenceZenAudio() {
  // Fades read-aloud out, then closes its AudioContext; unloads the voice model a little later
  zenTTS.leave()
  zenMusic.leave()
  zenAudio.setPresented(false)
}
zenAudio.setPresented(true)
zenTTS.enter()
onMounted(() => {
  // Sound is on by default: the music starts now, or on the first click/key press when the
  // browser refuses to play before a gesture.
  zenMusic.arm(() => zenAudio.isEnabled.value && zenMusic.isEnabled.value)
})

// TTS enabled/disabled — master kill switch for all TTS (manual + auto)
const ttsEnabled = ref(true)
if (typeof window !== 'undefined') {
  const saved = localStorage.getItem('zenTTSEnabled')
  if (saved !== null) ttsEnabled.value = saved !== 'false'
}
function toggleTtsEnabled() {
  ttsEnabled.value = !ttsEnabled.value
  localStorage.setItem('zenTTSEnabled', String(ttsEnabled.value))
  // If disabling, stop any playing TTS
  if (!ttsEnabled.value && (zenTTS.isPlaying.value || zenTTS.isLoading.value)) {
    zenTTS.stop()
  }
}

// Auto-read: automatically speak new assistant messages
const autoRead = ref(false)
if (typeof window !== 'undefined') {
  autoRead.value = localStorage.getItem('zenAutoRead') === 'true'
}
function toggleAutoRead() {
  autoRead.value = !autoRead.value
  localStorage.setItem('zenAutoRead', String(autoRead.value))
}

// Track which message we last auto-read to avoid re-reading the same one
let lastAutoReadMsg = ''
let autoReadTimerId: ReturnType<typeof setTimeout> | null = null
let isZenUnmounted = false

// Switching session inside zen (ZenMode stays mounted): for one tick the new
// session's status/messages differ from the old one's. Those are not events —
// don't auto-read, beam or pulse for them, and start the scenes fresh.
let switchingSession = false
watch(() => props.sessionId, (id, old) => {
  if (!old || id === old) return
  switchingSession = true
  if (autoReadTimerId) { clearTimeout(autoReadTimerId); autoReadTimerId = null }
  // The old session's reply fades out (the page's session guard does the same)
  void zenTTS.fadeOut()
  // Never read the new session's existing reply aloud just because we arrived
  lastAutoReadMsg = props.lastAssistantMessage || ''
  nextTick(() => {
    lastAutoReadMsg = props.lastAssistantMessage || lastAutoReadMsg
    switchingSession = false
  })
}, { flush: 'sync' })

// Watch for session status change to trigger auto-read when response is DONE
// (not during streaming, which would cause overlapping speak calls)
watch(() => props.sessionStatus, (newStatus, oldStatus) => {
  if (switchingSession) return
  if (!autoRead.value || !ttsEnabled.value) return
  if (oldStatus !== 'processing' || (newStatus !== 'idle' && newStatus !== 'active')) return

  // Respect master mute
  if (!zenAudio.isEnabled.value) return

  // Don't auto-read while user is dictating (voice recording active)
  if (props.isDictating) return

  // Response just finished — read it.
  // Use a polling approach: props.lastAssistantMessage may not have propagated yet
  // (Vue props update asynchronously, and the message may arrive in a different tick
  // than the status change). Poll up to 8 times over ~2s to catch it.
  let attempts = 0
  const trySpeak = () => {
    if (isZenUnmounted) return
    const msg = props.lastAssistantMessage
    if (msg && msg.length >= 10 && msg !== lastAutoReadMsg) {
      lastAutoReadMsg = msg
      zenTTS.speak(msg)
    } else if (attempts < 8) {
      attempts++
      autoReadTimerId = setTimeout(trySpeak, 250)
    }
  }
  // First attempt slightly delayed to let Vue propagate the prop
  autoReadTimerId = setTimeout(trySpeak, 200)
})

// Lower the music while read-aloud is speaking over it
watch(() => zenTTS.isPlaying.value, (speaking) => zenMusic.duck(speaking))

// When master sound is muted, stop any playing TTS immediately
watch(() => zenAudio.isEnabled.value, (enabled) => {
  if (!enabled && (zenTTS.isPlaying.value || zenTTS.isLoading.value)) {
    zenTTS.stop()
  }
})

// When voice recording starts, stop any playing TTS to avoid mic feedback
watch(() => props.isDictating, (dictating) => {
  if (dictating && (zenTTS.isPlaying.value || zenTTS.isLoading.value)) {
    zenTTS.stop()
  }
})

// Sync drone to project changes
watch(() => props.projectId, (newId) => {
  zenAudio.setProject(newId || null)
}, { immediate: true })

// Fade drone out when session goes idle, fade back in when active
watch(() => props.sessionStatus, (newStatus) => {
  if (newStatus === 'ended') zenMusic.stop()
  if (newStatus === 'idle' || newStatus === 'ended') {
    zenAudio.fadeDroneOut(4.0)
  } else if (newStatus === 'active' || newStatus === 'processing') {
    zenAudio.fadeDroneIn(2.0)
    zenMusic.startIfWanted()
  }
})

// Auto-start/stop background music when master sound is toggled
watch(() => zenAudio.isEnabled.value, (enabled) => {
  if (enabled && zenMusic.isEnabled.value && !zenMusic.isPlaying.value) {
    zenMusic.start()
  } else if (!enabled && zenMusic.isPlaying.value) {
    zenMusic.stop()
  }
})

const emit = defineEmits<{
  'update:fullscreen': [value: boolean]
  'select-session': [sessionId: string]
  'dismiss-notice': [key: string]
  'open-lightbox': [data: { images: { dataUrl: string; mediaType: string }[]; startIndex: number }]
}>()

// Images from the current turn, newest last; the strip shows the latest few.
const MAX_ZEN_IMAGES = 6
const zenImages = computed(() => (props.turnImages || []).slice(-MAX_ZEN_IMAGES))
function openZenImage(index: number) {
  emit('open-lightbox', { images: zenImages.value.map(i => ({ dataUrl: i.dataUrl, mediaType: i.mediaType })), startIndex: index })
}

// Shorten file paths for tooltip display (show last 2 segments)
function shortPath(filePath: string): string {
  const parts = filePath.split('/')
  if (parts.length <= 2) return filePath
  return '…/' + parts.slice(-2).join('/')
}

const zenContainer = ref<HTMLElement | null>(null)
const zenCanvasContainer = ref<HTMLElement | null>(null)
const isFullscreen = ref(false)

// Session tag (utils/sessionTags.ts, same storage as SessionItem)
const tagVersion = ref(0)
const sessionTag = computed(() => {
  tagVersion.value // reactivity dependency
  return readSessionTags()[props.sessionId] || ''
})

// Listen for storage changes (tag edited from the sidebar or another tab)
function onStorageChange(e: StorageEvent) {
  if (e.key === SESSION_TAGS_STORAGE_KEY) tagVersion.value++
}
onMounted(() => window.addEventListener('storage', onStorageChange))
onUnmounted(() => window.removeEventListener('storage', onStorageChange))

// Context animation state
const contextAnimActive = ref(false)
let contextAnimTimer: ReturnType<typeof setTimeout> | null = null

const contextArcCircumference = 2 * Math.PI * 17 // r=17 from SVG
const contextArcSweeping = ref(false)

const contextArcOffset = computed(() => {
  if (!contextArcSweeping.value) return contextArcCircumference // fully hidden
  const pct = props.contextPercent ?? 0
  return contextArcCircumference * (1 - pct / 100)
})

const contextArcColor = computed(() => {
  const pct = props.contextPercent ?? 0
  if (pct >= 90) return '#ef4444'
  if (pct >= 75) return '#fb923c'
  if (pct >= 50) return '#fbbf24'
  return props.avatarColor || '#8B5CF6'
})

// ── Session Switcher state ──
const showSessionSwitcher = ref(false)
const sessionSearchQuery = ref('')
const selectedSessionIndex = ref(0)
const sessionSearchInput = ref<HTMLInputElement | null>(null)
const navigatingViaKeyboard = ref(false) // suppress mouseenter during keyboard nav
let keyboardNavTimer: ReturnType<typeof setTimeout> | null = null

const filteredSessions = computed(() => {
  const sessions = props.recentSessions || []
  const q = sessionSearchQuery.value.toLowerCase().trim()
  if (!q) return sessions.slice(0, 10)
  return sessions.filter(s => {
    return (
      (s.projectName && s.projectName.toLowerCase().includes(q)) ||
      (s.gitBranch && s.gitBranch.toLowerCase().includes(q)) ||
      (s.contextSummary && s.contextSummary.toLowerCase().includes(q)) ||
      (s.lastUserMessage && s.lastUserMessage.toLowerCase().includes(q)) ||
      (s.workingDirectory && s.workingDirectory.toLowerCase().includes(q)) ||
      (s.avatarName && s.avatarName.toLowerCase().includes(q))
    )
  }).slice(0, 10)
})

function toggleSessionSwitcher() {
  // Close message history if open
  if (showMessageHistory.value) closeMessageHistory()
  showSessionSwitcher.value = !showSessionSwitcher.value
  sessionSearchQuery.value = ''
  selectedSessionIndex.value = 0
  // Focus is handled by onPanelEnter (after the transition completes)
}

function onPanelEnter(el: Element) {
  // Focus the search input once the session switcher is fully in the DOM
  if (showSessionSwitcher.value) {
    sessionSearchInput.value?.focus()
  }
  // Focus the history panel so arrow keys work immediately
  if (showMessageHistory.value && el instanceof HTMLElement) {
    el.focus()
  }
}

function closeSessionSwitcher() {
  showSessionSwitcher.value = false
  sessionSearchQuery.value = ''
  selectedSessionIndex.value = 0
}

function setKeyboardNav() {
  navigatingViaKeyboard.value = true
  if (keyboardNavTimer) clearTimeout(keyboardNavTimer)
  keyboardNavTimer = setTimeout(() => { navigatingViaKeyboard.value = false }, 300)
}

function handleSessionKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    setKeyboardNav()
    selectedSessionIndex.value = Math.min(selectedSessionIndex.value + 1, filteredSessions.value.length - 1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    setKeyboardNav()
    selectedSessionIndex.value = Math.max(selectedSessionIndex.value - 1, 0)
  } else if (event.key === 'Enter') {
    event.preventDefault()
    const session = filteredSessions.value[selectedSessionIndex.value]
    if (session) {
      openSession(session.id)
      closeSessionSwitcher()
    }
  } else if (event.key === 'Escape') {
    event.preventDefault()
    closeSessionSwitcher()
  }
}

function selectSessionFromList(sessionId: string) {
  openSession(sessionId)
  closeSessionSwitcher()
}

function formatRelativeTime(isoString: string): string {
  const now = Date.now()
  const then = new Date(isoString).getTime()
  const diffMs = now - then
  const diffMin = Math.floor(diffMs / 60000)
  if (diffMin < 1) return 'now'
  if (diffMin < 60) return `${diffMin}m`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h`
  const diffDay = Math.floor(diffHr / 24)
  return `${diffDay}d`
}

function sessionDisplayName(session: ZenSessionItem): string {
  if (session.contextSummary) return truncate(session.contextSummary, 50)
  if (session.lastUserMessage) return truncate(session.lastUserMessage, 50)
  if (session.gitBranch) return session.gitBranch
  if (session.workingDirectory) {
    const parts = session.workingDirectory.split('/')
    return parts[parts.length - 1] || session.workingDirectory
  }
  return session.id.slice(0, 8)
}

// Reset selected index when search query changes
watch(sessionSearchQuery, () => {
  selectedSessionIndex.value = 0
})

// ── Message History state ──
const showMessageHistory = ref(false)
const selectedHistoryIndex = ref(0)

const historyMessages = computed(() => {
  return props.recentAssistantMessages || []
})

function toggleMessageHistory() {
  // Close session switcher if open
  if (showSessionSwitcher.value) closeSessionSwitcher()
  showMessageHistory.value = !showMessageHistory.value
  selectedHistoryIndex.value = 0 // start on latest (index 0 = most recent)
}

function closeMessageHistory() {
  showMessageHistory.value = false
  selectedHistoryIndex.value = 0
}

function handleHistoryKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    setKeyboardNav()
    selectedHistoryIndex.value = Math.min(selectedHistoryIndex.value + 1, historyMessages.value.length - 1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    setKeyboardNav()
    selectedHistoryIndex.value = Math.max(selectedHistoryIndex.value - 1, 0)
  } else if (event.key === 'Escape') {
    event.preventDefault()
    closeMessageHistory()
  }
}

/** Read-aloud button: stop while reading (or loading), otherwise read the latest reply. */
function toggleReadAloud() {
  if (zenTTS.isPlaying.value || zenTTS.isLoading.value) {
    zenTTS.stop()
    return
  }
  if (zenAudio.isEnabled.value && ttsEnabled.value && props.lastAssistantMessage) {
    zenTTS.speak(props.lastAssistantMessage)
  }
}

// ── Expanded Response state ──
const expandedResponse = ref(false)

// ⌥F: fly / land (Galaxy). A registry shortcut that never fires while typing,
// so Option+F still types "ƒ" in the composer.
useShortcut('zen.fly', () => toggleFly(), { scope: 'zen', when: () => zenFly.available })

// Escape (shortcut registry's Escape stack, newest first): stop read-aloud,
// close the expanded reply, close the session switcher / message history.
// Escape never falls through to the agent.
useClosable(() => zenTTS.isPlaying.value || zenTTS.isLoading.value, () => zenTTS.stop())
useClosable(expandedResponse, () => { expandedResponse.value = false })
useClosable(showSessionSwitcher, () => closeSessionSwitcher())
useClosable(showMessageHistory, () => closeMessageHistory())

// Assistant message dismissed state — hides old response on send, re-shows when new response arrives
const assistantDismissed = ref(false)
let dismissResetWatch: (() => void) | null = null

function dismissMessages() {
  assistantDismissed.value = true
  // Auto-restore when a new assistant message arrives (i.e. the response comes back)
  if (dismissResetWatch) dismissResetWatch()
  dismissResetWatch = watch(() => props.lastAssistantMessage, () => {
    assistantDismissed.value = false
    if (dismissResetWatch) {
      dismissResetWatch()
      dismissResetWatch = null
    }
  })
}

function triggerContextAnimation() {
  // Clear any existing timer
  if (contextAnimTimer) clearTimeout(contextAnimTimer)

  // Reset arc to hidden, then sweep after a frame
  contextArcSweeping.value = false
  contextAnimActive.value = true

  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      contextArcSweeping.value = true
    })
  })

  // Hold for 2.5s then fade out
  contextAnimTimer = setTimeout(() => {
    contextAnimActive.value = false
    contextArcSweeping.value = false
    contextAnimTimer = null
  }, 2500)
}

function toggleFullscreen() {
  isFullscreen.value = !isFullscreen.value
  emit('update:fullscreen', isFullscreen.value)
  // Resize the canvas after layout change
  nextTick(() => {
    setTimeout(() => handleResize(), 50)
  })
}

const { getAgentsForSession, getRunningAgentsForSession } = useBackgroundAgents()

const agents = computed(() => getAgentsForSession(props.sessionId))
const runningCount = computed(() => getRunningAgentsForSession(props.sessionId).length)

const sessionStatusRef = computed(() => props.sessionStatus)
const runningAgentCountRef = computed(() => runningCount.value)
const projectColorRef = computed(() => props.projectColor || '')

// Zen visualization mode
const { activeViz, isTransitioning: vizTransitioning, getVizInfo } = useZenViz()
const activeVizLabel = computed(() => getVizInfo().label)

// Initialize Three.js animation (waves — the default)
const { init: initAnimation, handleResize, detach: detachAnimation, triggerPulse, triggerBeamDrop, triggerToolBeam, triggerSendPulse } = useZenAnimation({
  container: zenCanvasContainer,
  sessionStatus: sessionStatusRef,
  runningAgentCount: runningAgentCountRef,
  projectColor: projectColorRef
})

// ── Circus (DOM), Galaxy / Ink (WebGPURenderer); Waves is the WebGL scene above ──
const { setViz } = useZenViz()
let wavesShown = false
const sessionIdRef = computed(() => props.sessionId)
/** Every session known right now, so the ten backdrops are spread over them */
const knownSessionIds = () => [
  props.sessionId,
  ...(props.recentSessions || []).map(s => s.id),
  ...(props.workingSessions || []).map(s => s.sessionId),
  ...(props.sessionNotices || []).map(s => s.sessionId),
]
const gpuScenes = useZenGpuScenes({
  container: zenCanvasContainer,
  sessionId: sessionIdRef,
  openSessionIds: knownSessionIds,
  activeViz,
  sessionStatus: sessionStatusRef,
  projectColor: projectColorRef,
  showWaves: async () => {
    await initAnimation()
    wavesShown = true
  },
  hideWaves: () => {
    if (wavesShown) detachAnimation()
    wavesShown = false
  },
  runningAgentIds: () => agents.value.filter(a => a.status === 'running').map(a => a.agent_id),
  // The default (Circus) needs no GPU, so it is the safe fallback
  onUnavailable: () => setViz(DEFAULT_ZEN_VIZ, true),
})

// Circus letters sometimes spell a short summary: use the cached digest (never requests one)
const { getDigest: getSessionDigest } = useSessionDigests()
watch(
  () => [props.sessionId, getSessionDigest(props.sessionId), props.lastUserMessage] as const,
  ([id, digest, lastUser]) => setCircusMessageContext(id ? { summary: digest?.summary, tags: digest?.tags, lastUserMessage: lastUser } : null),
  { immediate: true }
)

/** Forward a session event to the Circus/Galaxy/Ink scene (no-op while Waves is shown). */
function dispatchVizEvent(toolName: string, detail: string, isError = false, filePath = '') {
  gpuScenes.toolEvent(toolName, filePath, detail, isError)
}

// ── Title plate: the session's name, and its AI summary when one is already written ──
// Only reads what exists (generate: false); Zen never queues a new paid summary.
const { getDigest, loadCached } = useSessionDigests()
const plateSessionName = computed(() => props.avatarName || `Session ${props.sessionId.slice(0, 8)}`)
const plateDigest = computed(() => getDigest(props.sessionId))
/** Measured height of the name plate (0 when not shown); drives --zen-plate-h */
const plateHeight = ref(0)

// ── Latest response: a full-height panel on the right of a wide stage, else the centred card ──
const zenWidth = ref(0)
const responseLayout = computed<ZenResponseLayout>(() => zenResponseLayout(zenWidth.value))
const sidePanelTop = computed(() => zenSidePanelTop(zenWidth.value, ZEN_PLATE_TOP, activeViz.value === 'circus' ? plateHeight.value : 0))
/** Re-mount (and fade) the card for a new reply, not for every streamed chunk of the same one */
const latestResponseKey = computed(() => props.recentAssistantMessages?.[0]?.id ?? '')

// Closing a card hides it until something new arrives: the reply until the next
// reply (a new message id, not another streamed chunk of this one), your message
// until you send another. Kept per Zen visit, not saved.
const closedResponseKey = ref<string | null>(null)
/** The reply's id; without one, its opening words (stable while it streams) */
const responseIdentity = computed(() => latestResponseKey.value || `text:${(props.lastAssistantMessage ?? '').slice(0, 80)}`)
const responseClosed = computed(() => closedResponseKey.value !== null && closedResponseKey.value === responseIdentity.value)
function closeResponse() {
  closedResponseKey.value = responseIdentity.value
  if (expandedResponse.value) expandedResponse.value = false
}
function reopenResponse() { closedResponseKey.value = null }
const closedUserMessage = ref<string | null>(null)
const responseCopied = ref(false)
let responseCopyTimer: ReturnType<typeof setTimeout> | null = null
async function copyResponse() {
  try {
    await copyText(props.lastAssistantMessage || '')
    responseCopied.value = true
    if (responseCopyTimer) clearTimeout(responseCopyTimer)
    responseCopyTimer = setTimeout(() => { responseCopied.value = false }, 1500)
  } catch { /* clipboard unavailable */ }
}
watch(() => [props.sessionId, props.messageCount], () => {
  if (props.sessionId) void loadCached({ id: props.sessionId, status: props.sessionStatus, message_count: props.messageCount })
}, { immediate: true })

// ── Moving to another session without leaving zen ──
const warpingOut = ref(false)
const warpingIn = ref(false)
const arrivingName = ref('')
let warpTimer: ReturnType<typeof setTimeout> | null = null
let arriveTimer: ReturnType<typeof setTimeout> | null = null
const prefersReducedMotion = typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

/** Warp the HUD out, then ask the page to switch session (it stays in zen). */
function openSession(sessionId: string) {
  if (!sessionId || sessionId === props.sessionId || warpingOut.value) return
  if (prefersReducedMotion) {
    emit('select-session', sessionId)
    return
  }
  warpingOut.value = true
  sendPulse()
  warpTimer = setTimeout(() => {
    warpTimer = null
    emit('select-session', sessionId)
    // If the switch did not happen (e.g. failed), bring the HUD back
    setTimeout(() => { warpingOut.value = false }, 1200)
  }, 340)
}

watch(() => props.sessionId, (id, old) => {
  if (!old || id === old) return
  gpuScenes.resetScenes(agents.value.filter(a => a.status === 'running').map(a => a.agent_id))
  warpingOut.value = false
  const s = props.recentSessions?.find(x => x.id === id)
  arrivingName.value = s ? `${s.projectName ? s.projectName + ' / ' : ''}${sessionDisplayName(s)}` : ''
  warpingIn.value = !prefersReducedMotion
  if (arriveTimer) clearTimeout(arriveTimer)
  arriveTimer = setTimeout(() => {
    arriveTimer = null
    arrivingName.value = ''
    warpingIn.value = false
  }, 1900)
})

/** Stable colour for a session without a project colour. */
function sessionHue(id: string) {
  let h = 0
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0
  return `hsl(${h % 360} 80% 68%)`
}

function timeAgo(at: number) {
  const s = Math.max(0, Math.round((Date.now() - at) / 1000))
  if (s < 45) return 'just now'
  const m = Math.round(s / 60)
  return m < 60 ? `${m}m ago` : `${Math.round(m / 60)}h ago`
}

// "+points" pop on every pickup
const popVisible = ref(false)
let popTimer: ReturnType<typeof setTimeout> | null = null
watch(() => zenFly.pickups, () => {
  popVisible.value = true
  if (popTimer) clearTimeout(popTimer)
  if (responseCopyTimer) clearTimeout(responseCopyTimer)
  popTimer = setTimeout(() => { popVisible.value = false }, 900)
})

/** Fly mode (Waves, Galaxy): take off / land. */
function toggleFly() {
  if (!zenFly.available) return
  if (!zenFly.active) {
    // Flight keys must reach the ship, not a focused text box
    const el = document.activeElement as HTMLElement | null
    if (el && (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT' || el.isContentEditable)) el.blur()
    closeSessionSwitcher()
    closeMessageHistory()
  }
  setZenFlying(!zenFly.active)
}

/** Send pulse on whichever scene is visible. */
function sendPulse() {
  triggerSendPulse()
  gpuScenes.sendPulse()
}

const statusLabel = computed(() => {
  if (props.awaitingUser) return 'Needs you'
  switch (props.sessionStatus) {
    case 'processing': return 'Processing'
    case 'idle': return 'Idle'
    case 'active': return 'Active'
    case 'ended': return 'Ended'
    default: return props.sessionStatus
  }
})

const emptyMessage = computed(() => {
  if (props.awaitingUser) return 'Waiting for your answer'
  if (props.sessionStatus === 'processing') return 'Working...'
  if (props.sessionStatus === 'idle') return 'Waiting for input'
  if (props.sessionStatus === 'ended') return 'Session complete'
  return 'Ready'
})

// Avatar display
const avatarImage = computed(() => props.avatarImage || null)
const avatarName = computed(() => props.avatarName || null)
const avatarRingStyle = computed(() => {
  const color = props.avatarColor || '#8B5CF6'
  return {
    borderColor: color,
    boxShadow: `0 0 12px ${color}55`
  }
})

// Context usage level for color coding
const contextLevel = computed(() => {
  const pct = props.contextPercent ?? 0
  if (pct >= 90) return 'critical'
  if (pct >= 75) return 'warning'
  if (pct >= 50) return 'high'
  return 'normal'
})

const truncate = (text: string, maxLen: number): string => {
  if (text.length <= maxLen) return text
  return text.substring(0, maxLen) + '...'
}

const formatTokens = (count: number): string => {
  if (count >= 1000) return `${(count / 1000).toFixed(1)}k`
  return String(count)
}

// ── Avatar flip animation on session change ──
const avatarFlipping = ref(false)
let avatarFlipTimer: ReturnType<typeof setTimeout> | null = null
watch(() => props.avatarImage, () => {
  avatarFlipping.value = true
  if (avatarFlipTimer) clearTimeout(avatarFlipTimer)
  avatarFlipTimer = setTimeout(() => { avatarFlipping.value = false }, 600)
})

// ── Per-agent color palette ──
// A set of distinct, calming hues that get assigned to each subagent
const AGENT_COLORS: [number, number, number][] = [
  [0.545, 0.361, 0.965],   // Purple  #8B5CF6
  [0.133, 0.827, 0.933],   // Cyan    #22d3ee
  [0.259, 0.816, 0.588],   // Emerald #42D096
  [0.961, 0.620, 0.263],   // Amber   #F59E43
  [0.925, 0.376, 0.604],   // Rose    #EC609A
  [0.392, 0.580, 0.969],   // Blue    #6494F7
  [0.784, 0.490, 0.925],   // Violet  #C87DEC
  [0.306, 0.765, 0.769],   // Teal    #4EC3C4
]

// Map agent_id → color, assigned in order of appearance
const agentColorMap = new Map<string, [number, number, number]>()
let nextColorIndex = 0

function getAgentColor(agentId: string): [number, number, number] {
  if (!agentColorMap.has(agentId)) {
    agentColorMap.set(agentId, AGENT_COLORS[nextColorIndex % AGENT_COLORS.length]!)
    nextColorIndex++
  }
  return agentColorMap.get(agentId)!
}

// Watch for agent completion to trigger pulse in agent's color
// Subagents: spawn / output / finish, diffed per agent. On a session switch
// the new session's agents are taken as the baseline — they are not "new".
watch(
  () => ({
    session: props.sessionId,
    list: agents.value.map(a => ({ id: a.agent_id, lines: a.output_lines, status: a.status })),
  }),
  (next, prev) => {
    if (!prev || next.session !== prev.session) return
    const oldMap = new Map(prev.list.map(a => [a.id, a]))
    for (const agent of next.list) {
      const was = oldMap.get(agent.id)
      if (!was) {
        triggerBeamDrop('message', getAgentColor(agent.id))
        if (agent.status === 'running') gpuScenes.agentSpawn(agent.id)
        continue
      }
      const finished = (agent.status === 'completed' || agent.status === 'failed') && was.status !== agent.status
      if (finished) {
        triggerBeamDrop('message', getAgentColor(agent.id))
        triggerPulse(0.5)
        gpuScenes.agentComplete(agent.id)
      } else if (agent.lines > was.lines) {
        triggerBeamDrop('message', getAgentColor(agent.id))
        const agentData = agents.value.find(a => a.agent_id === agent.id)
        const cleanOutput = (agentData?.last_output || 'Agent output').replace(/\n/g, ' ').trim()
        dispatchVizEvent('Message', cleanOutput.length > 60 ? cleanOutput.substring(0, 57) + '...' : cleanOutput)
      }
    }
  },
  // Not deep: the getter builds a fresh snapshot of just the fields it needs,
  // so it already fires on every relevant change.
)

// A list reload (re-subscribe, reconnect) replaces the agents in one tick, which the
// diff above cannot see: keep the stage in step with who is running now.
watch(
  () => agents.value.filter(a => a.status === 'running').map(a => a.agent_id).sort().join('|'),
  () => gpuScenes.syncAgents(agents.value.filter(a => a.status === 'running').map(a => a.agent_id)),
)

// Watch for new messages (main session only, skip when subagents are active
// to avoid duplicating ripples already triggered by the agent output watcher)
watch(() => props.messageCount, (newCount, oldCount) => {
  if (switchingSession) return
  if (newCount && oldCount && newCount > oldCount) {
    // Skip if subagents are active — their output watcher already handles ripples
    if (agents.value.some(a => a.status === 'running')) return

    triggerBeamDrop('message')
    const msg = props.lastAssistantMessage || 'New response'
    const cleanMsg = msg.replace(/\n/g, ' ').trim()
    const displayMsg = cleanMsg.length > 60 ? cleanMsg.substring(0, 57) + '...' : cleanMsg
    dispatchVizEvent('Message', displayMsg)
  }
})

// Watch for session status to trigger a ripple when processing starts
// (Only trigger the beam drop — real tool events now come via event bus)
watch(() => props.sessionStatus, (newStatus, oldStatus) => {
  if (switchingSession) return
  if (newStatus === 'processing' && oldStatus !== 'processing') {
    triggerBeamDrop('tool')
  }
})

let resizeObserver: ResizeObserver | null = null

// ── Listen for real tool:use events from the event bus ──
const eventBus = getEventBus()
let unsubToolUse: (() => void) | null = null
let unsubToolError: (() => void) | null = null
let unsubInterrupted: (() => void) | null = null
let unsubAgentError: (() => void) | null = null

function extractToolDetail(tool: string, parameters: any): string {
  const params = parameters
    ? (typeof parameters === 'string' ? (() => { try { return JSON.parse(parameters) } catch { return {} } })() : parameters)
    : {}

  if (tool === 'Read' || tool === 'Write' || tool === 'Edit') {
    const fp = params.file_path || ''
    // Show just the filename + parent dir for brevity
    const parts = fp.split('/')
    return parts.length > 2 ? parts.slice(-2).join('/') : fp
  }
  if (tool === 'Bash') {
    const cmd = params.command || ''
    // Truncate long commands
    return cmd.length > 60 ? cmd.substring(0, 57) + '...' : cmd
  }
  if (tool === 'Glob') return params.pattern || ''
  if (tool === 'Grep') return params.pattern || ''
  if (tool === 'Agent') return params.description || params.prompt?.substring(0, 40) || ''
  if (tool === 'WebSearch' || tool === 'WebFetch') return params.query || params.url || ''
  return ''
}

/** Full file path from tool parameters (Galaxy places stars by directory). */
function extractFilePath(parameters: any): string {
  if (!parameters) return ''
  const params = typeof parameters === 'string' ? (() => { try { return JSON.parse(parameters) } catch { return {} } })() : parameters
  return params.file_path || params.notebook_path || params.path || ''
}

onMounted(async () => {
  // Subscribe and observe synchronously, before any await: if zen unmounts
  // while the scene is still loading, onUnmounted can then clean these up
  // (subscribing after the await leaked them).

  // Use ResizeObserver to handle container resizing
  if (zenContainer.value) {
    zenWidth.value = zenContainer.value.clientWidth
    resizeObserver = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width
      if (w) zenWidth.value = Math.round(w)
      handleResize()
    })
    resizeObserver.observe(zenContainer.value)
  }

  // Subscribe to real tool use events
  unsubToolUse = eventBus.on('tool:use', (data: any) => {
    if (data?.sessionId !== props.sessionId) return
    const toolName = data.tool || 'Tool'
    const detail = extractToolDetail(toolName, data.parameters)
    // Light pillar + ripple coloured by tool category
    triggerToolBeam(toolName)
    dispatchVizEvent(toolName, detail || toolName, !!data.isError, extractFilePath(data.parameters))
  })

  // Failed tool calls: red flare (Galaxy) / hanko seal (Ink)
  unsubToolError = eventBus.on('tool:error', (data: any) => {
    if (data?.sessionId !== props.sessionId) return
    dispatchVizEvent('Error', 'Tool failed', true)
  })

  // Interrupts and agent errors: the foot comes down (Circus), a flare elsewhere
  unsubInterrupted = eventBus.on('session:interrupted', (data: any) => {
    if (data?.sessionId !== props.sessionId) return
    dispatchVizEvent('Interrupt', 'Interrupted', true)
  })
  unsubAgentError = eventBus.on('agent:error', (data: any) => {
    if (data?.sessionId !== props.sessionId) return
    dispatchVizEvent('Error', 'Agent error', true)
  })

  await nextTick()
  if (isZenUnmounted) return
  await gpuScenes.start()
})

onUnmounted(() => {
  isZenUnmounted = true
  silenceZenAudio()
  if (warpTimer) clearTimeout(warpTimer)
  if (arriveTimer) clearTimeout(arriveTimer)
  if (popTimer) clearTimeout(popTimer)

  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }

  // Clear auto-read polling timer (Bug 1)
  if (autoReadTimerId) {
    clearTimeout(autoReadTimerId)
    autoReadTimerId = null
  }

  // Clear context animation timer (Bug 2)
  if (contextAnimTimer) {
    clearTimeout(contextAnimTimer)
    contextAnimTimer = null
  }

  // Clear keyboard nav timer (Bug 3)
  if (keyboardNavTimer) {
    clearTimeout(keyboardNavTimer)
    keyboardNavTimer = null
  }

  // Clear avatar flip timer (Bug 4)
  if (avatarFlipTimer) {
    clearTimeout(avatarFlipTimer)
    avatarFlipTimer = null
  }

  // Stop dynamic dismissResetWatch watcher (Bug 5)
  if (dismissResetWatch) {
    dismissResetWatch()
    dismissResetWatch = null
  }

  // Unsubscribe from event bus
  if (unsubToolUse) {
    unsubToolUse()
    unsubToolUse = null
  }
  unsubToolError?.()
  unsubToolError = null
  unsubInterrupted?.()
  unsubInterrupted = null
  unsubAgentError?.()
  unsubAgentError = null

  detachAnimation()
})

// Expose send pulse trigger and fullscreen state for parent components
defineExpose({
  triggerSendPulse: sendPulse,
  triggerContextAnimation,
  dismissMessages,
  toggleSessionSwitcher,
  closeSessionSwitcher,
  toggleMessageHistory,
  closeMessageHistory,
  isFullscreen,
  playSendChime: zenAudio.playSendChime,
  playToolBip: zenAudio.playToolBip,
})
</script>

<style scoped>
.zen-mode {
  /* The zen scene is always dark, whatever the app theme: pin the overlay
     tokens to their light-on-dark values so text stays readable in light themes. */
  --overlay-text: rgba(255, 255, 255, 0.6);
  --overlay-text-hover: rgba(255, 255, 255, 0.8);
  --overlay-text-active: #ffffff;
  --overlay-bg: rgba(255, 255, 255, 0.05);
  --overlay-bg-hover: rgba(255, 255, 255, 0.08);
  --overlay-bg-active: rgba(255, 255, 255, 0.1);
  --overlay-border: rgba(255, 255, 255, 0.1);
  --overlay-border-hover: rgba(255, 255, 255, 0.2);
  --overlay-divider: rgba(255, 255, 255, 0.15);
  /* Top of the session name plate; the HUD column starts below the plate */
  --zen-plate-top: 58px;
  flex: 1;
  position: relative;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
  background: #07040f;
  animation: zen-view-in 0.5s cubic-bezier(0.22, 1, 0.36, 1) both;
}

/* ── Circus scene: a light paper collage, not a dark sky ──
   The HUD was designed for dark scenes (white text on faint black glass),
   which washes out over cream paper. Over the circus the glass becomes
   umber ink card stock and loose text is printed in ink, so everything
   stays legible. */
.zen-mode.zen-viz-circus {
  background: #efe4c8;
  --overlay-border: rgba(251, 245, 230, 0.16);
  --overlay-border-hover: rgba(251, 245, 230, 0.3);
}
/* No backdrop blur over the Circus stage: it redraws 12 times a second, so every
   blurred HUD card made the GPU process re-blur the area behind it each frame
   (the cards are already near-opaque, so nothing visible is lost). */
.zen-mode.zen-viz-circus :where(*) {
  backdrop-filter: none !important;
  -webkit-backdrop-filter: none !important;
}
.zen-mode.zen-viz-circus .zen-project-indicator,
.zen-mode.zen-viz-circus .zen-avatar-card,
.zen-mode.zen-viz-circus .zen-status-badge,
.zen-mode.zen-viz-circus .zen-agents-summary,
.zen-mode.zen-viz-circus .zen-last-message,
.zen-mode.zen-viz-circus .zen-metrics,
.zen-mode.zen-viz-circus .zen-feed-working,
.zen-mode.zen-viz-circus .zen-notice,
.zen-mode.zen-viz-circus .zen-sound-btn,
.zen-mode.zen-viz-circus .zen-sound-expand-btn,
.zen-mode.zen-viz-circus .zen-mixer-toggle,
.zen-mode.zen-viz-circus .zen-fullscreen-btn {
  background: rgba(28, 22, 16, 0.84);
  box-shadow: 2px 3px 0 rgba(43, 29, 16, 0.28);
}
.zen-mode.zen-viz-circus :deep(.zen-viz-active-btn) {
  background: rgba(28, 22, 16, 0.84);
}
.zen-mode.zen-viz-circus .zen-session-switcher,
.zen-mode.zen-viz-circus .zen-message-history,
.zen-mode.zen-viz-circus .zen-expanded-card {
  background: rgba(28, 22, 16, 0.93);
}
.zen-mode.zen-viz-circus .zen-last-message {
  border-left-color: #e8458b;
}
.zen-mode.zen-viz-circus .zen-empty-text {
  color: #2b1d10;
  font-family: 'EB Garamond', Georgia, serif;
  font-style: italic;
  font-size: 1.05rem;
  text-shadow: 0 0 8px rgba(239, 228, 200, 0.9), 0 0 2px rgba(239, 228, 200, 0.9);
}
.zen-mode.zen-viz-circus .zen-viz-transition-text,
.zen-mode.zen-viz-circus .zen-arrive-label,
.zen-mode.zen-viz-circus .zen-arrive-kicker,
.zen-mode.zen-viz-circus .zen-arrive-name {
  color: #2b1d10;
  text-shadow: 0 0 14px rgba(239, 228, 200, 0.95), 0 0 3px rgba(239, 228, 200, 0.95);
}

/* Dark backdrops (sunset, storm, tea room, greenhouse, graveyard, moon): loose text is cream, not ink */
.zen-mode.zen-viz-circus.zen-bd-dark .zen-empty-text,
.zen-mode.zen-viz-circus.zen-bd-dark .zen-viz-transition-text,
.zen-mode.zen-viz-circus.zen-bd-dark .zen-arrive-label,
.zen-mode.zen-viz-circus.zen-bd-dark .zen-arrive-kicker,
.zen-mode.zen-viz-circus.zen-bd-dark .zen-arrive-name {
  color: #fbf5e6;
  text-shadow: 0 0 12px rgba(16, 10, 24, 0.95), 0 0 3px rgba(16, 10, 24, 0.95);
}

/* Live → Zen: fade/settle in; the HUD rises in after the scene */
@keyframes zen-view-in {
  from { opacity: 0; scale: 1.015; }
  to { opacity: 1; scale: 1; }
}

.zen-mode .zen-top-right,
.zen-mode .zen-top-center,
.zen-mode .zen-status-badge,
.zen-mode .zen-agents-summary,
.zen-mode .zen-bottom-right-controls {
  animation: zen-hud-rise 0.7s cubic-bezier(0.22, 1, 0.36, 1) 0.25s both;
}
.zen-mode .zen-status-badge { animation-delay: 0.35s; }
.zen-mode .zen-agents-summary { animation-delay: 0.42s; }
.zen-mode .zen-bottom-right-controls { animation-delay: 0.5s; }

@keyframes zen-hud-rise {
  from { opacity: 0; translate: 0 8px; }
  to { opacity: 1; translate: 0 0; }
}

/* Zen → Live: fade out before the chat view takes over */
.zen-mode.zen-leaving {
  animation: zen-view-out 0.2s ease-in forwards;
  pointer-events: none;
}

@keyframes zen-view-out {
  from { opacity: 1; scale: 1; filter: none; }
  to { opacity: 0; scale: 0.99; filter: blur(2px); }
}

@media (prefers-reduced-motion: reduce) {
  .zen-mode,
  .zen-mode .zen-top-right,
  .zen-mode .zen-top-center,
  .zen-mode .zen-status-badge,
  .zen-mode .zen-agents-summary,
  .zen-mode .zen-bottom-right-controls {
    animation: none;
  }
}

/* Fullscreen mode — fixed overlay with smooth expand animation */
.zen-mode.zen-fullscreen {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 9999;
  animation: zen-expand 0.5s cubic-bezier(0.4, 0, 0.2, 1) forwards;
}

@keyframes zen-expand {
  0% {
    opacity: 0.8;
    clip-path: inset(5% 5% 5% 5% round 16px);
  }
  100% {
    opacity: 1;
    clip-path: inset(0 0 0 0 round 0px);
  }
}

.zen-canvas-container {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  z-index: 0;
  isolation: isolate;
}

/* Stage below HUD: the canvas container (z 0, isolated) holds every prop, gag, cloud and
   letter tile; the HUD layers (z 1+) always paint above it. Over the Circus stage the cards
   and chips get a solid umber base, so shapes passing behind them never show through. */
.zen-mode.zen-viz-circus .zen-project-indicator,
.zen-mode.zen-viz-circus .zen-avatar-card,
.zen-mode.zen-viz-circus .zen-status-badge,
.zen-mode.zen-viz-circus .zen-agents-summary,
.zen-mode.zen-viz-circus .zen-last-message,
.zen-mode.zen-viz-circus .zen-metrics,
.zen-mode.zen-viz-circus .zen-feed-working,
.zen-mode.zen-viz-circus .zen-notice,
.zen-mode.zen-viz-circus .zen-session-switcher,
.zen-mode.zen-viz-circus .zen-message-history,
.zen-mode.zen-viz-circus .zen-expanded-card {
  background-color: rgb(28, 22, 16);
}

/* Top-right avatar + context card */
/* ── Top-center: Project & Branch ── */
.zen-top-center {
  position: absolute;
  top: 14px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 8px;
}

.zen-project-indicator {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 14px;
  background: rgba(0, 0, 0, 0.3);
  backdrop-filter: blur(12px);
  border-radius: 20px;
  border: 1px solid var(--overlay-border);
  font-size: 0.86rem;
  letter-spacing: 0.2px;
  white-space: nowrap;
}

.zen-project-name {
  font-weight: 600;
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.7);
}

.zen-project-sep {
  color: var(--overlay-text);
  font-weight: 300;
}

.zen-branch-name {
  color: var(--overlay-text);
  font-family: 'Monaco', 'Menlo', monospace;
  font-size: 0.82rem;
}

.zen-session-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.65rem;
  font-weight: 600;
  padding: 4px 10px;
  border-radius: 10px;
  background: linear-gradient(135deg, rgba(var(--accent-purple-rgb, 139, 92, 246), 0.3), rgba(var(--accent-purple-rgb, 139, 92, 246), 0.25));
  border: 1px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
  color: rgba(196, 181, 253, 0.9);
  letter-spacing: 0.02em;
  white-space: nowrap;
}

.zen-session-tag::before {
  content: '🏷';
  font-size: 0.55rem;
}

.zen-git-stats {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-left: 6px;
}

.zen-git-badge {
  font-family: 'Monaco', 'Menlo', monospace;
  font-size: 0.58rem;
  font-weight: 600;
  padding: 1px 4px;
  border-radius: 4px;
  letter-spacing: 0.3px;
}

.zen-git-staged {
  color: rgba(34, 197, 94, 0.7);
  background: rgba(34, 197, 94, 0.1);
}

.zen-git-modified {
  color: rgba(245, 158, 11, 0.7);
  background: rgba(245, 158, 11, 0.1);
}

.zen-git-untracked {
  color: rgba(148, 163, 184, 0.6);
  background: rgba(148, 163, 184, 0.08);
}

.zen-git-deleted {
  color: rgba(239, 68, 68, 0.6);
  background: rgba(239, 68, 68, 0.1);
}

.zen-git-badge-hover {
  position: relative;
  cursor: default;
}

.zen-git-tooltip {
  display: none;
  position: absolute;
  top: calc(100% + 8px);
  left: 50%;
  transform: translateX(-50%);
  min-width: 180px;
  max-width: 300px;
  max-height: 240px;
  overflow-y: auto;
  background: rgba(15, 15, 25, 0.95);
  backdrop-filter: blur(16px);
  border: 1px solid var(--overlay-border);
  border-radius: 8px;
  padding: 8px 0;
  z-index: 100;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
  flex-direction: column;
}

.zen-git-badge-hover:hover .zen-git-tooltip {
  display: flex;
}

.zen-git-tooltip-title {
  font-family: -apple-system, BlinkMacSystemFont, sans-serif;
  font-size: 0.6rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.8px;
  color: var(--overlay-text);
  padding: 2px 10px 6px;
  border-bottom: 1px solid var(--overlay-border);
  margin-bottom: 2px;
}

.zen-git-tooltip-file {
  font-family: 'Monaco', 'Menlo', monospace;
  font-size: 0.65rem;
  color: var(--overlay-text-hover);
  padding: 3px 10px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  line-height: 1.5;
}

.zen-git-tooltip-file:hover {
  color: var(--overlay-text-active);
  background: var(--overlay-bg);
}

/* Tooltip scrollbar */
.zen-git-tooltip::-webkit-scrollbar {
  width: 4px;
}
.zen-git-tooltip::-webkit-scrollbar-track {
  background: transparent;
}
.zen-git-tooltip::-webkit-scrollbar-thumb {
  background: var(--overlay-bg-active);
  border-radius: 2px;
}

/* Tooltip arrow */
.zen-git-tooltip::before {
  content: '';
  position: absolute;
  top: -4px;
  left: 50%;
  transform: translateX(-50%) rotate(45deg);
  width: 8px;
  height: 8px;
  background: rgba(15, 15, 25, 0.95);
  border-left: 1px solid var(--overlay-border);
  border-top: 1px solid var(--overlay-border);
}

.zen-git-clean {
  margin-left: 5px;
  color: rgba(34, 197, 94, 0.4);
  vertical-align: middle;
}

.zen-top-right {
  position: absolute;
  top: 12px;
  right: 12px;
  z-index: 2;
}

.zen-avatar-card {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 14px 6px 6px;
  background: rgba(0, 0, 0, 0.3);
  backdrop-filter: blur(12px);
  border-radius: 24px;
  border: 1px solid var(--overlay-border);
}

.zen-avatar-ring-wrapper {
  position: relative;
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.zen-avatar-ring {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  border: 2px solid var(--accent-purple, #8B5CF6);
  overflow: hidden;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.2);
  transition: border-color 0.4s, box-shadow 0.4s;
}

/* Glow pulse on context animation */
.zen-avatar-ring.ring-glow {
  animation: ring-glow-pulse 1.2s ease-in-out;
}

@keyframes ring-glow-pulse {
  0% { box-shadow: 0 0 6px currentColor; }
  40% { box-shadow: 0 0 18px currentColor, 0 0 30px currentColor; }
  100% { box-shadow: 0 0 6px currentColor; }
}

/* Radial progress arc — overlaid on top of avatar ring */
.zen-context-arc {
  position: absolute;
  top: -2px;
  left: -2px;
  pointer-events: none;
}

.context-arc-fill {
  transition: stroke-dashoffset 1.4s cubic-bezier(0.25, 0.46, 0.45, 0.94);
}

/* Flash on the percentage text */
.context-pct-flash {
  animation: pct-flash 1.5s ease-out;
}

@keyframes pct-flash {
  0% { color: rgba(255, 255, 255, 0.95); text-shadow: 0 0 8px currentColor; }
  50% { color: rgba(255, 255, 255, 0.8); text-shadow: 0 0 4px currentColor; }
  100% { text-shadow: none; }
}

/* Subtle card highlight during context animation */
.zen-avatar-card.context-anim-active {
  border-color: var(--overlay-border);
  background: rgba(0, 0, 0, 0.4);
  transition: background 0.3s, border-color 0.3s;
}

/* Avatar flip animation */
.zen-avatar-ring-wrapper {
  perspective: 400px;
}
.avatar-flip .zen-avatar-ring {
  animation: avatar-flip-anim 0.6s ease-in-out;
}
@keyframes avatar-flip-anim {
  0% { transform: rotateY(0deg); }
  50% { transform: rotateY(90deg); }
  100% { transform: rotateY(0deg); }
}

.zen-avatar-img {
  width: 100%;
  height: 100%;
  object-fit: contain;
  display: block;
}

.zen-avatar-fallback {
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--overlay-text);
}

.zen-avatar-info {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.zen-avatar-name {
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--overlay-text);
  letter-spacing: 0.2px;
  line-height: 1.2;
}

.zen-context-pct {
  font-size: 0.8rem;
  font-weight: 600;
  font-family: 'Monaco', 'Menlo', monospace;
  color: var(--overlay-text);
  line-height: 1.2;
}

.zen-context-pct.high {
  color: rgba(251, 191, 36, 0.7);
}

.zen-context-pct.warning {
  color: rgba(251, 146, 60, 0.8);
}

.zen-context-pct.critical {
  color: rgba(239, 68, 68, 0.85);
}

/* Fullscreen toggle button — bottom right, raised in fullscreen to avoid input overlap */
/* Bottom-right controls container */
.zen-bottom-right-controls {
  position: absolute;
  bottom: 12px;
  right: 12px;
  z-index: 3;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
}

.zen-mode.zen-fullscreen .zen-bottom-right-controls {
  bottom: 80px;
}

/* Sound control */
.zen-sound-control {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 3px;
  position: relative;
}

.zen-sound-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 32px;
  height: 32px;
  padding: 0 8px;
  border-radius: 8px;
  border: 1px solid var(--overlay-border);
  background: rgba(0, 0, 0, 0.3);
  backdrop-filter: blur(10px);
  color: var(--overlay-text);
  cursor: pointer;
  transition: color 0.2s, background 0.2s, border-color 0.2s, box-shadow 0.3s;
}

.zen-sound-btn:hover {
  color: var(--overlay-text);
  background: rgba(0, 0, 0, 0.4);
  border-color: var(--overlay-border);
}

.zen-sound-control.active .zen-sound-btn {
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.8);
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.25);
  box-shadow: 0 0 12px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.15);
}

/* Volume slider */
.zen-sound-expand-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  border: 1px solid var(--overlay-border);
  background: rgba(0, 0, 0, 0.25);
  color: var(--overlay-text);
  cursor: pointer;
  transition: all 0.2s ease;
}
.zen-sound-expand-btn:hover {
  color: var(--overlay-text);
  background: rgba(0, 0, 0, 0.4);
}

/* ─── Mixer panel ─── */
.zen-sound-state {
  margin-left: 6px;
  white-space: nowrap;
  font-size: 10px;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.zen-sound-panel {
  position: absolute;
  right: 40px;
  bottom: 0;
  display: flex;
  flex-direction: row;
  align-items: stretch;
  gap: 0;
  padding: 14px 16px;
  background: rgba(10, 8, 20, 0.9);
  backdrop-filter: blur(20px);
  border: 1px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.15);
  border-radius: 14px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5), 0 0 20px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.08);
}

.zen-panel-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 0 14px;
  min-width: 80px;
}
.zen-panel-section:first-child { padding-left: 0; }
.zen-panel-section:last-child { padding-right: 0; }

.zen-panel-section-title {
  font-size: 7px;
  text-transform: uppercase;
  letter-spacing: 1.5px;
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
  font-weight: 600;
  margin-bottom: 2px;
}

.zen-panel-row {
  display: flex;
  gap: 6px;
}

.zen-mixer-sep-v {
  width: 1px;
  background: var(--overlay-border);
  align-self: stretch;
}

/* Horizontal volume slider */
.zen-volume-horizontal {
  display: flex;
  align-items: center;
  gap: 6px;
}

.zen-volume-range-h {
  -webkit-appearance: none;
  appearance: none;
  width: 80px;
  height: 3px;
  background: var(--overlay-bg-active);
  border-radius: 2px;
  outline: none;
  cursor: pointer;
}

.zen-volume-range-h::-webkit-slider-thumb {
  -webkit-appearance: none;
  appearance: none;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.7);
  border: 1px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
  cursor: pointer;
  transition: background 0.2s;
}
.zen-volume-range-h::-webkit-slider-thumb:hover {
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.9);
}
.zen-volume-range-h::-moz-range-thumb {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.7);
  border: 1px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
  cursor: pointer;
}

/* ─── Mixer panel elements ─── */
.zen-mixer-label {
  font-size: 8px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--overlay-text);
  font-weight: 500;
}

.zen-mixer-sep {
  width: 1px;
  height: 60px;
  background: var(--overlay-border);
  align-self: center;
}

.zen-mixer-toggle {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 8px 6px;
  border-radius: 8px;
  border: 1px solid var(--overlay-border);
  background: rgba(0, 0, 0, 0.2);
  color: var(--overlay-text);
  cursor: pointer;
  transition: all 0.25s ease;
}
.zen-mixer-toggle:hover {
  color: var(--overlay-text);
  background: rgba(0, 0, 0, 0.35);
}
.zen-mixer-toggle.on {
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.8);
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.2);
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.08);
}
.zen-mixer-toggle.on .zen-mixer-label {
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.5);
}

/* Volume & panel fade transitions */
.zen-vol-fade-enter-active,
.zen-vol-fade-leave-active {
  transition: opacity 0.3s ease, transform 0.3s ease;
}
.zen-vol-fade-enter-from,
.zen-vol-fade-leave-to {
  opacity: 0;
  transform: scale(0.9);
}
.zen-vol-fade-enter-to,
.zen-vol-fade-leave-from {
  opacity: 1;
  transform: scale(1);
}

.zen-fullscreen-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 8px;
  border: 1px solid var(--overlay-border);
  background: rgba(0, 0, 0, 0.3);
  backdrop-filter: blur(10px);
  color: var(--overlay-text);
  cursor: pointer;
  transition: color 0.2s, background 0.2s, border-color 0.2s;
}

.zen-fullscreen-btn:hover {
  color: var(--overlay-text);
  background: rgba(0, 0, 0, 0.4);
  border-color: var(--overlay-border);
}

.zen-overlay {
  position: relative;
  z-index: 1;
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 24px;
  padding: 32px;
  pointer-events: none;
}

.zen-overlay > * {
  pointer-events: auto;
}

/* The name plate (absolute, top: --zen-plate-top) has a variable height
   (name + up to 3 summary lines); ZenNamePlate measures it into --zen-plate-h,
   and the centred chip/card column never starts above its bottom edge. */
.zen-mode.zen-has-plate .zen-overlay {
  padding-top: max(32px, calc(var(--zen-plate-top) + var(--zen-plate-h, 0px) + 18px));
}
@media (max-width: 900px) {
  .zen-mode { --zen-plate-top: 54px; }
}

/* Wide stage (utils/zenLayout.ts): the latest response is a full-height panel on
   the right, from below the avatar card (or the name plate, when the plate reaches
   that far; --zen-side-top) to above the composer (--zen-composer-h, measured by
   ChatArea), clear of the sound/fullscreen controls on the right edge. The status
   chips and "Your message" stay in the centre column, which moves left of it. */
.zen-mode.zen-side-layout {
  --zen-side-w: clamp(300px, 30%, 460px);
}
.zen-mode.zen-side-layout .zen-overlay {
  padding-right: calc(var(--zen-side-w) + 80px);
}
.zen-last-message.zen-response-panel {
  position: absolute;
  top: var(--zen-side-top, 64px);
  right: 56px;
  /* above the composer and the sound/fullscreen controls (about 130px tall at the bottom edge) */
  bottom: max(calc(var(--zen-composer-h, 110px) + 30px), 160px);
  width: var(--zen-side-w);
  max-width: none;
  min-height: 160px;
  display: flex;
  flex-direction: column;
  z-index: 2;
}
/* Fullscreen: the controls sit higher (above the fixed composer) */
.zen-mode.zen-fullscreen .zen-last-message.zen-response-panel {
  bottom: max(calc(var(--zen-composer-h, 110px) + 38px), 260px);
}
.zen-response-panel .zen-last-message-label { flex-shrink: 0; }
.zen-resp-head-copy {
  margin-left: auto;
  padding: 1px 9px;
  font: 600 0.68rem / 1.6 'Courier Prime', ui-monospace, monospace;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: #fbf5e6;
  background: transparent;
  border: 1px solid rgba(251, 245, 230, 0.45);
  border-radius: 3px;
  cursor: pointer;
}
.zen-resp-head-copy:hover { background: rgba(251, 245, 230, 0.14); }

/* Status Badge */
.zen-status-badge {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 20px;
  border-radius: 20px;
  background: rgba(0, 0, 0, 0.4);
  backdrop-filter: blur(12px);
  border: 1px solid var(--overlay-border);
  font-size: 1.05rem;
  font-weight: 500;
  color: var(--overlay-text-hover);
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--overlay-text);
}

.zen-status-badge.processing .status-dot {
  background: var(--accent-purple, #8B5CF6);
  animation: zen-pulse 2s ease-in-out infinite;
}

.zen-status-badge.idle .status-dot,
.zen-status-badge.active .status-dot {
  background: var(--accent-green, #22c55e);
}

.zen-status-badge.ended .status-dot {
  background: var(--overlay-text);
}

.zen-status-badge.awaiting {
  border-color: rgba(245, 158, 11, 0.55);
  color: #fbbf24;
}

.zen-status-badge.awaiting .status-dot {
  background: #f59e0b;
  animation: zen-pulse 1.6s ease-in-out infinite;
}

/* ── Images seen this turn ── */
.zen-last-message.zen-images-card {
  width: auto;
  min-width: 0;
  align-self: center;
}

.zen-images-strip {
  display: flex;
  gap: 8px;
  overflow-x: auto;
  scrollbar-width: none;
}

.zen-image-thumb {
  position: relative;
  flex: 0 0 auto;
  width: 72px;
  height: 72px;
  padding: 0;
  border-radius: 10px;
  overflow: hidden;
  border: 1px solid var(--overlay-border);
  background: rgba(0, 0, 0, 0.35);
  cursor: zoom-in;
  transition: transform 0.2s ease, border-color 0.2s ease;
}

.zen-image-thumb:hover,
.zen-image-thumb:focus-visible {
  transform: translateY(-2px);
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.7);
  outline: none;
}

.zen-image-thumb.is-latest {
  width: 112px;
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.55);
}

.zen-image-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.zen-image-label {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  padding: 2px 6px;
  font-size: 0.6rem;
  color: #fff;
  background: linear-gradient(transparent, rgba(0, 0, 0, 0.7));
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  text-align: left;
}

.zen-image-pop-enter-active {
  transition: opacity 0.35s ease, transform 0.35s cubic-bezier(0.2, 0.9, 0.3, 1.3);
}

.zen-image-pop-enter-from {
  opacity: 0;
  transform: scale(0.6) translateY(8px);
}

@media (prefers-reduced-motion: reduce) {
  .zen-status-badge.awaiting .status-dot { animation: none; }
  .zen-image-pop-enter-active { transition: none; }
}

@keyframes zen-pulse {
  0%, 100% { opacity: 1; box-shadow: 0 0 0 0 rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4); }
  50% { opacity: 0.6; box-shadow: 0 0 0 6px rgba(var(--accent-purple-rgb, 139, 92, 246), 0); }
}

/* Subagents Summary (compact) */
.zen-agents-summary {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding: 8px 18px;
  background: rgba(0, 0, 0, 0.35);
  backdrop-filter: blur(12px);
  border-radius: 16px;
  border: 1px solid var(--overlay-border);
}

.agents-count {
  font-size: 1rem;
  font-weight: 600;
  color: var(--accent-purple, #a78bfa);
}

.agents-total {
  font-size: 0.9rem;
  color: var(--overlay-text);
}

/* Last assistant message */
.zen-last-message {
  max-width: 480px;
  width: 100%;
  padding: 14px 18px;
  background: rgba(0, 0, 0, 0.3);
  backdrop-filter: blur(14px);
  border-radius: 14px;
  border: 1px solid var(--overlay-border);
  border-left: 3px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
}

.zen-last-message-label {
  font-size: 0.8rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.8px;
  color: var(--overlay-text);
  margin-bottom: 8px;
  display: flex;
  align-items: center;
  gap: 8px;
}

/* ─── TTS Read Aloud Button ─── */
.zen-tts-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  border: 1px solid var(--overlay-border);
  background: rgba(0, 0, 0, 0.2);
  color: var(--overlay-text);
  cursor: pointer;
  transition: all 0.2s ease;
  flex-shrink: 0;
}
.zen-tts-btn:hover {
  color: var(--overlay-text-hover);
  background: rgba(0, 0, 0, 0.4);
  border-color: var(--overlay-border-hover);
}
.zen-tts-btn.playing {
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.9);
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.1);
  box-shadow: 0 0 10px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.2);
}
.zen-tts-btn.loading {
  color: rgba(255, 200, 50, 0.8);
  border-color: rgba(255, 200, 50, 0.3);
}
.zen-tts-btn.disabled {
  opacity: 0.25;
  cursor: not-allowed;
  pointer-events: none;
}
.zen-auto-read-btn.on {
  color: rgba(100, 220, 150, 0.9);
  border-color: rgba(100, 220, 150, 0.4);
  background: rgba(100, 220, 150, 0.1);
  box-shadow: 0 0 8px rgba(100, 220, 150, 0.2);
}

/* ─── Voice Selector ─── */
/* ─── Engine Toggle ─── */
.zen-engine-toggle {
  display: flex;
  gap: 3px;
  margin-bottom: 4px;
}
.zen-engine-btn {
  flex: 1;
  padding: 4px 6px;
  border: 1px solid var(--overlay-border);
  border-radius: 4px;
  background: transparent;
  color: var(--overlay-text);
  cursor: pointer;
  transition: all 0.2s ease;
}
.zen-engine-btn:hover {
  color: var(--overlay-text);
  border-color: var(--overlay-border-hover);
}
.zen-engine-btn.active {
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.9);
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.1);
}

.zen-voice-selector {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 2px 0;
}
.zen-voice-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px;
  border: 1px solid transparent;
  border-radius: 4px;
  background: transparent;
  color: var(--overlay-text);
  cursor: pointer;
  font-size: 9px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  transition: all 0.2s ease;
  white-space: nowrap;
}
.zen-voice-btn:hover {
  color: var(--overlay-text);
  background: var(--overlay-bg);
}
.zen-voice-btn.active {
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.9);
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.3);
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.1);
}
.zen-voice-gender {
  font-size: 10px;
}
.zen-voice-name {
  font-family: var(--font-mono, monospace);
}

/* ─── F5-TTS Status ─── */
.zen-f5-status {
  display: flex;
  align-items: center;
  gap: 6px;
}
.zen-f5-load-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  border: 1px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.3);
  border-radius: 5px;
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.08);
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.8);
  cursor: pointer;
  font-size: 9px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  transition: all 0.2s ease;
}
.zen-f5-load-btn:hover {
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.15);
  border-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.5);
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 1);
}
.zen-f5-loading {
  display: flex;
  align-items: center;
  gap: 6px;
}
.zen-f5-progress-bar {
  width: 60px;
  height: 4px;
  background: var(--overlay-bg-hover);
  border-radius: 2px;
  overflow: hidden;
}
.zen-f5-progress-fill {
  height: 100%;
  background: linear-gradient(90deg, rgba(var(--accent-purple-rgb, 139, 92, 246), 0.6), rgba(var(--accent-purple-rgb, 139, 92, 246), 0.9));
  border-radius: 2px;
  transition: width 0.3s ease;
}
.zen-f5-ready {
  display: flex;
  align-items: center;
  gap: 4px;
}

.zen-tts-btn-expanded {
  width: 26px;
  height: 26px;
}
@keyframes zen-tts-spin {
  to { transform: rotate(360deg); }
}
.zen-tts-spinner {
  animation: zen-tts-spin 0.8s linear infinite;
}

.zen-last-message-text {
  font-size: 1rem;
  line-height: 1.5;
  color: var(--overlay-text-active);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 120px;
  overflow-y: auto;
  scrollbar-width: thin;
}

.zen-last-message.zen-user-message {
  border-left-color: rgba(56, 232, 255, 0.35);
  opacity: 1;
}

.zen-user-message .zen-last-message-label {
  color: rgba(56, 232, 255, 0.4);
}

/* Clickable response card */
.zen-last-message-clickable {
  cursor: pointer;
  transition: border-color 0.2s, background 0.2s;
}

.zen-last-message-clickable:hover {
  background: rgba(0, 0, 0, 0.4);
  border-left-color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.6);
}

/* ── Expanded Response Overlay ── */
.zen-expanded-overlay {
  position: absolute;
  inset: 0;
  z-index: 50;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.6);
  padding: 32px;
}

.zen-expanded-card {
  max-width: 600px;
  width: 100%;
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  background: rgba(20, 16, 12, 0.96);
  border-radius: 16px;
  border: 1px solid var(--overlay-border);
  border-left: 3px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
  overflow: hidden;
}

.zen-expanded-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px;
  border-bottom: 1px solid var(--overlay-border);
  flex-shrink: 0;
}

.zen-expanded-label {
  font-size: 0.65rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.8px;
  color: var(--overlay-text);
}

.zen-expanded-close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  border: none;
  background: var(--overlay-bg-hover);
  color: var(--overlay-text);
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.zen-expanded-close:hover {
  background: var(--overlay-bg-active);
  color: var(--overlay-text-hover);
}

.zen-expanded-body {
  flex: 1;
  overflow-y: auto;
  scrollbar-width: thin;
  padding: 18px;
}

.zen-expanded-text {
  font-size: 1.05rem;
  line-height: 1.6;
  color: var(--overlay-text);
  white-space: pre-wrap;
  word-break: break-word;
}

/* Expanded overlay transitions */
.zen-expand-fade-enter-active {
  transition: opacity 0.3s ease-out;
}
.zen-expand-fade-enter-active .zen-expanded-card {
  transition: transform 0.3s ease-out, opacity 0.3s ease-out;
}
.zen-expand-fade-leave-active {
  transition: opacity 0.25s ease-in;
}
.zen-expand-fade-leave-active .zen-expanded-card {
  transition: transform 0.25s ease-in, opacity 0.25s ease-in;
}
.zen-expand-fade-enter-from {
  opacity: 0;
}
.zen-expand-fade-enter-from .zen-expanded-card {
  opacity: 0;
  transform: scale(0.95) translateY(10px);
}
.zen-expand-fade-leave-to {
  opacity: 0;
}
.zen-expand-fade-leave-to .zen-expanded-card {
  opacity: 0;
  transform: scale(0.97) translateY(5px);
}

/* Empty state */
.zen-empty {
  text-align: center;
}

.zen-empty-text {
  font-size: 1rem;
  color: var(--overlay-text);
  font-weight: 300;
  letter-spacing: 0.5px;
}

/* ── Voice Transcription Overlay ── */
.zen-dictation-overlay {
  position: absolute;
  bottom: 80px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 25;
  width: min(600px, 85vw);
}

.zen-dictation-card {
  background: rgba(0, 0, 0, 0.55);
  backdrop-filter: blur(20px);
  border: 1px solid rgba(56, 232, 255, 0.25);
  border-radius: 14px;
  padding: 16px 20px;
  box-shadow:
    0 0 20px rgba(56, 232, 255, 0.08),
    inset 0 1px 0 rgba(56, 232, 255, 0.1);
}

.zen-dictation-indicator {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}

.zen-dictation-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: rgba(255, 80, 80, 0.9);
  box-shadow: 0 0 8px rgba(255, 80, 80, 0.6);
  animation: zen-dictation-pulse 1.2s ease-in-out infinite;
}

@keyframes zen-dictation-pulse {
  0%, 100% { opacity: 1; box-shadow: 0 0 8px rgba(255, 80, 80, 0.6); }
  50% { opacity: 0.4; box-shadow: 0 0 3px rgba(255, 80, 80, 0.3); }
}

.zen-dictation-label {
  font-family: 'Share Tech Mono', 'JetBrains Mono', monospace;
  font-size: 0.65rem;
  text-transform: uppercase;
  letter-spacing: 0.12em;
  color: rgba(255, 80, 80, 0.7);
}

.zen-dictation-text {
  font-size: 0.9rem;
  line-height: 1.6;
  color: var(--overlay-text-active);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 160px;
  overflow-y: auto;
  scrollbar-width: thin;
  animation: zen-dictation-text-glow 2s ease-in-out infinite alternate;
}

@keyframes zen-dictation-text-glow {
  0% { text-shadow: 0 0 4px rgba(56, 232, 255, 0.15); }
  100% { text-shadow: 0 0 8px rgba(56, 232, 255, 0.3); }
}

/* Dictation overlay transition */
.zen-dictation-fade-enter-active {
  transition: opacity 0.4s ease-out, transform 0.4s ease-out;
}
.zen-dictation-fade-leave-active {
  transition: opacity 0.3s ease-in, transform 0.3s ease-in;
}
.zen-dictation-fade-enter-from {
  opacity: 0;
  transform: translateX(-50%) translateY(12px) scale(0.96);
}
.zen-dictation-fade-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(-8px) scale(0.97);
}

/* Bottom Metrics */
.zen-metrics {
  display: flex;
  align-items: center;
  gap: 20px;
  padding: 10px 20px;
  background: rgba(0, 0, 0, 0.3);
  backdrop-filter: blur(12px);
  border-radius: 12px;
  border: 1px solid var(--overlay-border);
}

.metric {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 0.9rem;
  color: var(--overlay-text);
  font-family: 'Monaco', 'Menlo', monospace;
}

.metric svg {
  opacity: 0.5;
}

/* ── Panel crossfade (session switcher ↔ message cards) ── */
.zen-panel-fade-enter-active {
  transition: opacity 0.4s ease-out, transform 0.4s ease-out;
}
.zen-panel-fade-leave-active {
  transition: opacity 0.35s ease-in, transform 0.35s ease-in;
}
.zen-panel-fade-enter-from {
  opacity: 0;
  transform: translateY(10px) scale(0.97);
}
.zen-panel-fade-leave-to {
  opacity: 0;
  transform: translateY(-8px) scale(0.97);
}

/* Messages panel wrapper for transition */
.zen-messages-panel {
  display: contents;
}

/* ── Session Switcher ── */
.zen-session-switcher {
  max-width: 520px;
  width: 100%;
  background: rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(20px);
  border-radius: 16px;
  border: 1px solid var(--overlay-border);
  overflow: hidden;
}

.zen-session-switcher-header {
  padding: 12px 14px;
  border-bottom: 1px solid var(--overlay-border);
}

.zen-session-search-wrapper {
  display: flex;
  align-items: center;
  gap: 8px;
  background: var(--overlay-bg);
  border-radius: 10px;
  padding: 8px 12px;
}

.zen-session-search-icon {
  color: var(--overlay-text);
  flex-shrink: 0;
}

.zen-session-search {
  flex: 1;
  background: transparent;
  border: none;
  outline: none;
  color: var(--overlay-text-hover);
  font-size: 0.85rem;
  font-family: inherit;
  letter-spacing: 0.2px;
}

.zen-session-search::placeholder {
  color: var(--overlay-text);
}

.zen-session-kbd {
  font-size: 0.6rem;
  font-family: 'Monaco', 'Menlo', monospace;
  color: var(--overlay-text);
  background: var(--overlay-bg-hover);
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px solid var(--overlay-border);
  flex-shrink: 0;
}

.zen-session-list {
  max-height: 340px;
  overflow-y: auto;
  scrollbar-width: thin;
  padding: 4px;
}

.zen-session-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 10px 12px;
  background: transparent;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  transition: background 0.15s;
  text-align: left;
  gap: 12px;
}

.zen-session-item:hover,
.zen-session-item.is-selected {
  background: var(--overlay-bg-hover);
}

.zen-session-item.is-current {
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.08);
  border: 1px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.15);
}

.zen-session-item.is-current.is-selected {
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.12);
}

.zen-session-item-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex: 1;
}

.zen-session-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--overlay-text);
}

.zen-session-dot.active,
.zen-session-dot.processing {
  background: var(--accent-green, #22c55e);
}

.zen-session-dot.idle {
  background: var(--accent-purple, #8B5CF6);
}

.zen-session-dot.ended {
  background: var(--overlay-text);
}

.zen-session-item-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.zen-session-item-name {
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--overlay-text-hover);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.zen-session-project {
  color: var(--overlay-text);
  font-weight: 400;
  margin-right: 2px;
}

.zen-session-item-meta {
  display: flex;
  gap: 8px;
  font-size: 0.65rem;
  color: var(--overlay-text);
  font-family: 'Monaco', 'Menlo', monospace;
}

.zen-session-branch {
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.5);
}

.zen-session-item-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.zen-session-current-badge {
  font-size: 0.55rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.6);
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.1);
  padding: 2px 6px;
  border-radius: 4px;
}

.zen-session-time {
  font-size: 0.7rem;
  font-family: 'Monaco', 'Menlo', monospace;
  color: var(--overlay-text);
  min-width: 24px;
  text-align: right;
}

.zen-session-empty {
  padding: 24px;
  text-align: center;
  font-size: 0.8rem;
  color: var(--overlay-text);
}

/* ── Message History Panel ── */
.zen-message-history {
  max-width: 520px;
  width: 100%;
  background: rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(20px);
  border-radius: 16px;
  border: 1px solid var(--overlay-border);
  overflow: hidden;
  outline: none;
}

.zen-history-header {
  padding: 12px 14px;
  border-bottom: 1px solid var(--overlay-border);
}

.zen-history-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.zen-history-icon {
  color: var(--overlay-text);
  flex-shrink: 0;
}

.zen-history-title {
  flex: 1;
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--overlay-text);
  letter-spacing: 0.3px;
}

.zen-history-list {
  max-height: 380px;
  overflow-y: auto;
  scrollbar-width: thin;
  padding: 4px;
}

.zen-history-item {
  padding: 10px 12px;
  border-radius: 10px;
  cursor: default;
  transition: background 0.15s;
  outline: none;
}

.zen-history-item:hover,
.zen-history-item.is-selected {
  background: var(--overlay-bg-hover);
}

.zen-history-item.is-latest {
  border-left: 2px solid rgba(var(--accent-purple-rgb, 139, 92, 246), 0.4);
}

.zen-history-item.is-latest.is-selected {
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.08);
}

.zen-history-item-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.zen-history-item-number {
  font-size: 0.6rem;
  font-weight: 600;
  font-family: 'Monaco', 'Menlo', monospace;
  color: var(--overlay-text);
}

.zen-history-latest-badge {
  font-size: 0.55rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.6);
  background: rgba(var(--accent-purple-rgb, 139, 92, 246), 0.1);
  padding: 2px 6px;
  border-radius: 4px;
}

.zen-history-item-time {
  margin-left: auto;
  font-size: 0.65rem;
  font-family: 'Monaco', 'Menlo', monospace;
  color: var(--overlay-text);
}

.zen-history-item-text {
  font-size: 0.78rem;
  line-height: 1.45;
  color: var(--overlay-text);
  white-space: pre-wrap;
  word-break: break-word;
}

.zen-history-item.is-selected .zen-history-item-text {
  color: var(--overlay-text-hover);
}

.zen-history-empty {
  padding: 24px;
  text-align: center;
  font-size: 0.8rem;
  color: var(--overlay-text);
}

/* Smooth crossfade transition for last message updates */
.zen-msg-fade-enter-active {
  transition: opacity 0.6s ease-in, transform 0.6s ease-out;
}
.zen-msg-fade-leave-active {
  transition: opacity 0.8s cubic-bezier(0.4, 0, 0.2, 1), transform 0.8s cubic-bezier(0.4, 0, 0.2, 1);
}
.zen-msg-fade-enter-from {
  opacity: 0;
  transform: translateY(8px);
}
.zen-msg-fade-leave-to {
  opacity: 0;
  transform: translateY(-6px) scale(0.98);
}

/* ── User message beam glow transition ── */
.zen-user-beam-enter-active {
  transition: opacity 0.5s ease-out, transform 0.5s ease-out;
}
.zen-user-beam-leave-active {
  transition: opacity 0.4s ease-in, transform 0.4s ease-in;
}
.zen-user-beam-enter-from {
  opacity: 0;
  transform: translateY(10px) scale(0.97);
}
.zen-user-beam-leave-to {
  opacity: 0;
  transform: translateY(-6px) scale(0.98);
}

/* Beam sweep on text — glowing highlight sweeps left-to-right */
.zen-user-beam-enter-active .zen-beam-text {
  animation: zen-beam-sweep 1.2s ease-out 0.1s both;
}

@keyframes zen-beam-sweep {
  0% {
    background-size: 200% 100%;
    background-position: -100% 0;
    -webkit-background-clip: text;
    background-clip: text;
    color: transparent;
    background-image: linear-gradient(
      90deg,
      rgba(255, 255, 255, 1) 0%,
      rgba(255, 255, 255, 1) 30%,
      rgba(56, 232, 255, 1) 45%,
      rgba(var(--accent-purple-rgb, 139, 92, 246), 0.9) 50%,
      rgba(56, 232, 255, 1) 55%,
      rgba(255, 255, 255, 1) 70%,
      rgba(255, 255, 255, 1) 100%
    );
    text-shadow: none;
  }
  60% {
    background-size: 200% 100%;
    background-position: 100% 0;
    -webkit-background-clip: text;
    background-clip: text;
    color: transparent;
    background-image: linear-gradient(
      90deg,
      rgba(255, 255, 255, 1) 0%,
      rgba(255, 255, 255, 1) 30%,
      rgba(56, 232, 255, 1) 45%,
      rgba(var(--accent-purple-rgb, 139, 92, 246), 0.9) 50%,
      rgba(56, 232, 255, 1) 55%,
      rgba(255, 255, 255, 1) 70%,
      rgba(255, 255, 255, 1) 100%
    );
    text-shadow: none;
  }
  80% {
    background-size: 200% 100%;
    background-position: 100% 0;
    color: rgba(255, 255, 255, 1);
    text-shadow: 0 0 12px rgba(56, 232, 255, 0.5), 0 0 30px rgba(56, 232, 255, 0.2);
  }
  100% {
    color: rgba(255, 255, 255, 1);
    text-shadow: none;
  }
}

/* Glow pulse on the card border when entering */
.zen-user-beam-enter-active.zen-user-message {
  animation: zen-card-glow 1.0s ease-out both;
}

@keyframes zen-card-glow {
  0% {
    border-left-color: rgba(56, 232, 255, 0.8);
    box-shadow: -4px 0 20px rgba(56, 232, 255, 0.3), inset 0 0 30px rgba(56, 232, 255, 0.05);
  }
  50% {
    border-left-color: rgba(56, 232, 255, 0.6);
    box-shadow: -2px 0 12px rgba(56, 232, 255, 0.15), inset 0 0 15px rgba(56, 232, 255, 0.02);
  }
  100% {
    border-left-color: rgba(56, 232, 255, 0.35);
    box-shadow: none;
  }
}

/* ============================================ */
/* Galaxy file labels (DOM layer added by the scene) */
/* ============================================ */

.zen-canvas-container :deep(.zen-galaxy-labels) {
  position: absolute;
  inset: 0;
  z-index: 1;
  pointer-events: none;
  overflow: hidden;
}

.zen-canvas-container :deep(.zen-galaxy-label) {
  --c: #dfe8ff;
  position: absolute;
  left: 0;
  top: 0;
  margin: -0.55em 0 0 14px;
  padding: 2px 8px 2px 16px;
  border-radius: 999px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  letter-spacing: 0.02em;
  color: rgba(255, 255, 255, 0.88);
  background: rgba(6, 4, 14, 0.45);
  border: 1px solid color-mix(in srgb, var(--c) 35%, transparent);
  white-space: nowrap;
  opacity: 0;
  will-change: transform, opacity;
  text-shadow: 0 0 8px color-mix(in srgb, var(--c) 60%, transparent);
}

.zen-canvas-container :deep(.zen-galaxy-label)::before {
  content: '';
  position: absolute;
  left: 6px;
  top: 50%;
  width: 5px;
  height: 5px;
  margin-top: -2.5px;
  border-radius: 50%;
  background: var(--c);
  box-shadow: 0 0 6px var(--c);
}

/* ============================================ */
/* Visualization Mode Picker & Transitions      */
/* ============================================ */

/* Canvas transition when switching viz modes */
.zen-canvas-container.viz-transitioning {
  animation: viz-canvas-fade 0.6s ease-in-out;
}

@keyframes viz-canvas-fade {
  0% { opacity: 1; }
  40% { opacity: 0.3; filter: brightness(1.5) saturate(0.3); }
  60% { opacity: 0.3; filter: brightness(1.5) saturate(0.3); }
  100% { opacity: 1; filter: none; }
}

/* Visualization mode name overlay during transition */
.zen-viz-transition-label {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  z-index: 10;
  pointer-events: none;
}

.zen-viz-transition-text {
  font-size: 1.4rem;
  font-weight: 600;
  letter-spacing: 4px;
  text-transform: uppercase;
  color: var(--overlay-text-hover);
  text-shadow: 0 0 20px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.5), 0 0 60px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.2);
}

.zen-viz-label-enter-active {
  transition: opacity 0.25s ease, transform 0.3s ease;
}

.zen-viz-label-leave-active {
  transition: opacity 0.25s ease, transform 0.3s ease;
}

.zen-viz-label-enter-from {
  opacity: 0;
  transform: translate(-50%, -50%) scale(0.85);
}

.zen-viz-label-leave-to {
  opacity: 0;
  transform: translate(-50%, -50%) scale(1.1);
}

/* ============================================ */
/* Other sessions: working dots + notice cards   */
/* ============================================ */

.zen-feed {
  position: absolute;
  top: 14px;
  left: 14px;
  z-index: 6;
  width: min(330px, calc(50% - 120px));
  min-width: 240px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  pointer-events: none;
}

.zen-feed > * { pointer-events: auto; }

.zen-feed-working {
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px 5px 8px;
  border-radius: 999px;
  background: rgba(10, 6, 22, 0.5);
  border: 1px solid rgba(255, 255, 255, 0.08);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
}

.zen-feed-dot {
  --c: #9d8bff;
  width: 9px;
  height: 9px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: var(--c);
  box-shadow: 0 0 8px var(--c);
  cursor: pointer;
  animation: zen-feed-breathe 1.8s ease-in-out infinite;
}

.zen-feed-dot:nth-child(2n) { animation-delay: -0.6s; }
.zen-feed-dot:nth-child(3n) { animation-delay: -1.2s; }
.zen-feed-dot:hover { transform: scale(1.35); }
.zen-feed-dot:focus-visible { outline: 2px solid rgba(255, 255, 255, 0.7); outline-offset: 2px; }

@keyframes zen-feed-breathe {
  0%, 100% { opacity: 0.55; }
  50% { opacity: 1; }
}

.zen-feed-working-label {
  margin-left: 2px;
  font-size: 0.88rem;
  letter-spacing: 0.03em;
  color: rgba(255, 255, 255, 0.6);
}

.zen-feed-notices {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.zen-notice {
  --c: #9d8bff;
  --k: #5fe3c0;
  position: relative;
  display: flex;
  gap: 10px;
  padding: 10px 30px 10px 10px;
  border-radius: 14px;
  background: rgba(12, 7, 26, 0.6);
  border: 1px solid rgba(255, 255, 255, 0.09);
  border-left: 2px solid var(--k);
  box-shadow: 0 10px 30px rgba(0, 0, 0, 0.35);
  backdrop-filter: blur(16px) saturate(1.2);
  -webkit-backdrop-filter: blur(16px) saturate(1.2);
  color: rgba(255, 255, 255, 0.9);
  text-align: left;
  cursor: pointer;
  transition: background 0.2s ease, border-color 0.2s ease, transform 0.2s ease;
}

.zen-notice.needs-you {
  --k: #ffb84d;
  animation: zen-notice-call 2.6s ease-in-out infinite;
}

@keyframes zen-notice-call {
  0%, 100% { box-shadow: 0 10px 30px rgba(0, 0, 0, 0.35), 0 0 0 rgba(255, 184, 77, 0); }
  50% { box-shadow: 0 10px 30px rgba(0, 0, 0, 0.35), 0 0 22px rgba(255, 184, 77, 0.22); }
}

.zen-notice:hover,
.zen-notice:focus-visible {
  background: rgba(22, 13, 44, 0.72);
  border-color: rgba(255, 255, 255, 0.16);
  border-left-color: var(--k);
  transform: translateX(2px);
  outline: none;
}

.zen-notice-avatar {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: 50%;
  overflow: hidden;
  font-size: 0.8rem;
  font-weight: 600;
  color: #fff;
  background: color-mix(in srgb, var(--c) 45%, #1a1030);
  box-shadow: 0 0 0 1px color-mix(in srgb, var(--c) 60%, transparent), 0 0 12px color-mix(in srgb, var(--c) 35%, transparent);
}

.zen-notice-avatar img { width: 100%; height: 100%; object-fit: cover; }

.zen-notice-body {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.zen-notice-top {
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.zen-notice-kind {
  font-size: 0.66rem;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--k);
}

.zen-notice-time {
  font-size: 0.68rem;
  color: rgba(255, 255, 255, 0.45);
}

.zen-notice-name {
  font-size: 0.84rem;
  font-weight: 600;
  line-height: 1.3;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.zen-notice-project {
  font-weight: 500;
  color: color-mix(in srgb, var(--c) 70%, white);
}

.zen-notice-snippet {
  font-size: 0.76rem;
  line-height: 1.4;
  color: rgba(255, 255, 255, 0.62);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.zen-notice-go {
  max-height: 0;
  opacity: 0;
  overflow: hidden;
  font-size: 0.72rem;
  color: var(--k);
  transition: max-height 0.2s ease, opacity 0.2s ease, margin 0.2s ease;
}

.zen-notice:hover .zen-notice-go,
.zen-notice:focus-visible .zen-notice-go {
  max-height: 20px;
  opacity: 1;
  margin-top: 3px;
}

.zen-notice-close {
  position: absolute;
  top: 7px;
  right: 7px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: rgba(255, 255, 255, 0.4);
  cursor: pointer;
}

.zen-notice-close:hover { background: rgba(255, 255, 255, 0.1); color: #fff; }
.zen-notice-close:focus-visible { outline: 2px solid rgba(255, 255, 255, 0.6); }

.zen-notice-enter-active { transition: opacity 0.5s ease, transform 0.5s cubic-bezier(0.2, 0.8, 0.2, 1), filter 0.5s ease; }
.zen-notice-leave-active { transition: opacity 0.3s ease, transform 0.3s ease; position: absolute; width: 100%; }
.zen-notice-move { transition: transform 0.4s ease; }
.zen-notice-enter-from { opacity: 0; transform: translateX(-16px); filter: blur(4px); }
.zen-notice-leave-to { opacity: 0; transform: translateX(-10px); }

/* ============================================ */
/* Warp to another session (stays in zen)       */
/* ============================================ */

.zen-mode.zen-warp-out > :not(.zen-canvas-container):not(.zen-arrive-label) {
  transition: opacity 0.34s ease, filter 0.34s ease;
  opacity: 0 !important;
  filter: blur(8px);
}

.zen-mode.zen-warp-out .zen-canvas-container {
  transition: filter 0.34s ease, transform 0.6s ease;
  filter: brightness(1.35) saturate(1.2);
  transform: scale(1.03);
}

.zen-mode.zen-warp-in > :not(.zen-canvas-container):not(.zen-arrive-label) {
  animation: zen-warp-in 0.7s cubic-bezier(0.2, 0.8, 0.2, 1) both;
}

@keyframes zen-warp-in {
  from { opacity: 0; filter: blur(8px); }
  to { opacity: 1; filter: blur(0); }
}

.zen-arrive-label {
  position: absolute;
  top: 38%;
  left: 50%;
  transform: translate(-50%, -50%);
  z-index: 8;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  pointer-events: none;
  text-align: center;
  text-shadow: 0 2px 24px rgba(0, 0, 0, 0.6);
}

.zen-arrive-kicker {
  font-size: 0.7rem;
  letter-spacing: 0.3em;
  text-transform: uppercase;
  color: rgba(255, 255, 255, 0.55);
}

.zen-arrive-name {
  font-size: 1.35rem;
  font-weight: 600;
  color: rgba(255, 255, 255, 0.95);
  max-width: 70vw;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.zen-arrive-enter-active { transition: opacity 0.5s ease, letter-spacing 0.9s ease, filter 0.5s ease; }
.zen-arrive-leave-active { transition: opacity 0.6s ease, filter 0.6s ease; }
.zen-arrive-enter-from { opacity: 0; filter: blur(6px); }
.zen-arrive-leave-to { opacity: 0; filter: blur(4px); }

@media (max-width: 720px) {
  .zen-feed { width: calc(100% - 28px); min-width: 0; }
  .zen-feed-notices .zen-notice:nth-child(n + 3) { display: none; }
}

@media (prefers-reduced-motion: reduce) {
  .zen-feed-dot, .zen-notice.needs-you { animation: none; }
  .zen-mode.zen-warp-in > :not(.zen-canvas-container) { animation: none; }
}

/* ============================================ */
/* Fly mode (Galaxy)                            */
/* ============================================ */

.zen-fly-btn.active {
  color: #bff4ff;
  border-color: rgba(127, 232, 255, 0.6);
  box-shadow: 0 0 16px rgba(127, 232, 255, 0.35);
}

/* Flying: the scene is the point — fade the cards away and let the canvas
   receive drags */
.zen-mode.zen-flying .zen-overlay,
.zen-mode.zen-flying .zen-top-center,
.zen-mode.zen-flying .zen-feed {
  opacity: 0 !important;
  pointer-events: none !important;
  transition: opacity 0.6s ease;
}

.zen-fly-hud {
  position: absolute;
  left: 16px;
  bottom: 16px;
  z-index: 7;
  width: 250px;
  padding: 12px 14px;
  border-radius: 14px;
  background: rgba(8, 5, 20, 0.55);
  border: 1px solid rgba(127, 232, 255, 0.22);
  box-shadow: 0 10px 30px rgba(0, 0, 0, 0.4), 0 0 24px rgba(127, 232, 255, 0.08);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  color: rgba(255, 255, 255, 0.9);
  font-size: 0.78rem;
}

.zen-fly-row { display: flex; align-items: baseline; justify-content: space-between; }
.zen-fly-kicker { font-size: 0.64rem; letter-spacing: 0.24em; text-transform: uppercase; color: #7fe8ff; }
.zen-fly-speed { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 1.15rem; font-variant-numeric: tabular-nums; }
.zen-fly-speed small { font-size: 0.66rem; color: rgba(255, 255, 255, 0.5); }

.zen-fly-throttle { height: 3px; margin: 8px 0 10px; border-radius: 2px; background: rgba(255, 255, 255, 0.1); overflow: hidden; }
.zen-fly-throttle span { display: block; height: 100%; background: linear-gradient(90deg, #ff7ad9, #7fe8ff); transition: width 0.15s linear; }

.zen-fly-near { margin-bottom: 10px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: #ffe2a8; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.zen-fly-near.empty { color: rgba(255, 255, 255, 0.4); }

.zen-fly-keys { display: flex; flex-wrap: wrap; gap: 4px 12px; font-size: 0.7rem; color: rgba(255, 255, 255, 0.55); }
.zen-fly-keys kbd, .zen-fly-land kbd {
  display: inline-block;
  min-width: 16px;
  margin-right: 2px;
  padding: 0 4px;
  border-radius: 4px;
  text-align: center;
  font-family: inherit;
  font-size: 0.66rem;
  color: rgba(255, 255, 255, 0.85);
  background: rgba(255, 255, 255, 0.1);
}

.zen-fly-land {
  margin-top: 12px;
  width: 100%;
  padding: 7px 10px;
  border-radius: 999px;
  border: 1px solid rgba(127, 232, 255, 0.35);
  background: rgba(127, 232, 255, 0.1);
  color: #dffaff;
  font: inherit;
  cursor: pointer;
}
.zen-fly-land:hover { background: rgba(127, 232, 255, 0.2); }
.zen-fly-land:focus-visible, .zen-fly-btn:focus-visible { outline: 2px solid rgba(127, 232, 255, 0.8); outline-offset: 2px; }

.zen-fly-hud-enter-active, .zen-fly-hud-leave-active { transition: opacity 0.4s ease, transform 0.4s ease; }
.zen-fly-hud-enter-from, .zen-fly-hud-leave-to { opacity: 0; transform: translateY(8px); }

@media (max-width: 600px) {
  .zen-fly-keys { display: none; }
  .zen-fly-hud { width: 180px; }
}

.zen-fly-score { position: relative; display: flex; align-items: baseline; gap: 10px; margin-bottom: 8px; }
.zen-fly-points { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 1.05rem; font-weight: 600; color: #ffe08a; font-variant-numeric: tabular-nums; }
.zen-fly-points small { font-size: 0.64rem; font-weight: 400; color: rgba(255, 255, 255, 0.5); }
.zen-fly-combo { padding: 0 6px; border-radius: 999px; font-size: 0.72rem; font-weight: 700; color: #1a0f00; background: linear-gradient(90deg, #ffcf5a, #ff8fd8); animation: zen-fly-combo-in 0.35s ease; }
@keyframes zen-fly-combo-in { from { transform: scale(1.6); } to { transform: scale(1); } }
.zen-fly-best { margin-left: auto; font-size: 0.68rem; color: rgba(255, 255, 255, 0.45); }
.zen-fly-pop { position: absolute; left: 0; top: -22px; font-weight: 700; color: #ffe08a; text-shadow: 0 0 12px rgba(255, 207, 90, 0.7); pointer-events: none; }
.zen-fly-pop-enter-active { transition: opacity 0.2s ease, transform 0.9s ease; }
.zen-fly-pop-leave-active { transition: opacity 0.4s ease, transform 0.4s ease; }
.zen-fly-pop-enter-from { opacity: 0; transform: translateY(6px); }
.zen-fly-pop-leave-to { opacity: 0; transform: translateY(-10px); }

/* ── Close a card until something new arrives (reply: next reply; yours: next send) ── */
.zen-card-close {
  margin-left: auto;
  width: 22px;
  height: 22px;
  display: inline-grid;
  place-items: center;
  padding: 0;
  font: 400 1.05rem / 1 system-ui, sans-serif;
  color: var(--overlay-text);
  background: transparent;
  border: 1px solid transparent;
  border-radius: 4px;
  cursor: pointer;
  flex-shrink: 0;
}
.zen-card-close:hover,
.zen-card-close:focus-visible {
  color: var(--overlay-text-hover);
  border-color: var(--overlay-border-hover);
  outline: none;
}
/* Copy already takes the free space in the panel header */
.zen-resp-head-copy + .zen-card-close { margin-left: 0; }
.zen-resp-reopen {
  align-self: center;
  padding: 6px 14px;
  font: 600 0.7rem / 1 'Courier Prime', ui-monospace, monospace;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--overlay-text-hover);
  background: rgba(0, 0, 0, 0.4);
  border: 1px solid var(--overlay-border);
  border-radius: 999px;
  cursor: pointer;
}
/* Wide stage: the tab sits where the panel was, on the right */
.zen-resp-reopen.is-panel {
  position: absolute;
  top: var(--zen-side-top, 64px);
  right: 56px;
  z-index: 2;
}

/* ══ Circus: the HUD's text cards are paper props on the stage ══
   Ink on aged paper, a thin ruled inner border and a drop shadow, matching the
   engraved collage. The reply panel is as tall as its text (capped above the
   composer), "Your message" is a pinned note tilted a little, and a caret shows
   while the reply is still being written. Other scenes keep the dark glass. */
.zen-mode.zen-viz-circus {
  --zen-paper: #f4e9d0;
  --zen-paper-note: #efdfb9;
  --zen-paper-edge: #dcc79a;
  --zen-ink: #2b1d10;
  --zen-ink-soft: #6b5638;
  --zen-rule: #9a7b48;
  --zen-rose: #a8505e;
  --zen-verdigris: #3f7a70;
}
.zen-mode.zen-viz-circus .zen-last-message.zen-response-card,
.zen-mode.zen-viz-circus .zen-last-message.zen-user-message,
.zen-mode.zen-viz-circus .zen-expanded-card {
  position: relative;
  color: var(--zen-ink);
  background: var(--zen-paper);
  border: 1px solid var(--zen-paper-edge);
  border-radius: 2px;
  box-shadow: 0 1px 0 rgba(255, 255, 255, 0.45) inset, 0 10px 22px rgba(10, 8, 20, 0.35), 0 2px 0 rgba(0, 0, 0, 0.22);
}
.zen-mode.zen-viz-circus .zen-last-message.zen-response-card::before,
.zen-mode.zen-viz-circus .zen-last-message.zen-user-message::before {
  content: "";
  position: absolute;
  inset: 5px;
  border: 1px solid var(--zen-rule);
  opacity: 0.4;
  pointer-events: none;
}
.zen-mode.zen-viz-circus .zen-last-message.zen-response-card .zen-last-message-label,
.zen-mode.zen-viz-circus .zen-last-message.zen-user-message .zen-last-message-label {
  font: 700 0.66rem / 1 'Courier Prime', ui-monospace, monospace;
  letter-spacing: 0.14em;
  color: var(--zen-ink-soft);
}
.zen-mode.zen-viz-circus .zen-response-card .zen-last-message-label {
  padding-bottom: 9px;
  margin-bottom: 12px;
  border-bottom: 1px solid var(--zen-rule);
}
.zen-mode.zen-viz-circus .zen-response-card .zen-tts-btn,
.zen-mode.zen-viz-circus .zen-response-card .zen-card-close,
.zen-mode.zen-viz-circus .zen-user-message .zen-card-close,
.zen-mode.zen-viz-circus .zen-resp-head-copy {
  color: var(--zen-ink);
  background: transparent;
  border-color: transparent;
  box-shadow: none;
}
.zen-mode.zen-viz-circus .zen-response-card .zen-tts-btn:hover,
.zen-mode.zen-viz-circus .zen-card-close:hover,
.zen-mode.zen-viz-circus .zen-card-close:focus-visible,
.zen-mode.zen-viz-circus .zen-resp-head-copy:hover {
  background: rgba(43, 29, 16, 0.08);
  border-color: var(--zen-rule);
}
.zen-mode.zen-viz-circus .zen-response-card .zen-tts-btn.playing,
.zen-mode.zen-viz-circus .zen-response-card .zen-auto-read-btn.on {
  color: var(--zen-verdigris);
  border-color: var(--zen-verdigris);
}
.zen-mode.zen-viz-circus .zen-resp-head-copy {
  border-color: var(--zen-rule);
}
/* The reply: ink on paper, a drop cap, sized to its text */
.zen-mode.zen-viz-circus .zen-response-card :deep(.zen-resp) {
  --zr-ink: #2b1d10;
  --zr-strong: #1c1209;
  --zr-marker: #a8505e;
  --zr-link: #2f6b61;
  --zr-link-hover: #8f3f4d;
  --zr-code: #3a2814;
  --zr-code-bg: rgba(154, 123, 72, 0.2);
  --zr-pre-bg: rgba(154, 123, 72, 0.14);
  --zr-rule: rgba(154, 123, 72, 0.6);
  --zr-btn-border: #9a7b48;
  --zr-btn-hover: rgba(43, 29, 16, 0.08);
  --zr-scroll: rgba(107, 86, 56, 0.45);
}
.zen-mode.zen-viz-circus .zen-response-card :deep(.zen-resp-md > p:first-child::first-letter) {
  float: left;
  font-size: 2.9em;
  line-height: 0.82;
  padding: 0.07em 0.08em 0 0;
  font-weight: 700;
  color: var(--zen-rose);
}
.zen-mode.zen-viz-circus .zen-response-card.is-writing :deep(.zen-resp-md > :last-child::after) {
  content: "";
  display: inline-block;
  width: 2px;
  height: 1.05em;
  margin-left: 3px;
  vertical-align: -0.15em;
  background: var(--zen-rose);
  animation: zen-ink-caret 1s steps(2) infinite;
}
@keyframes zen-ink-caret { 50% { opacity: 0; } }
.zen-mode.zen-viz-circus .zen-last-message.zen-response-panel {
  bottom: auto;
  min-height: 0;
  max-height: calc(100% - var(--zen-side-top, 64px) - max(calc(var(--zen-composer-h, 110px) + 30px), 160px));
  padding: 16px 20px 20px;
  rotate: 0.4deg;
}
.zen-mode.zen-viz-circus.zen-fullscreen .zen-last-message.zen-response-panel {
  max-height: calc(100% - var(--zen-side-top, 64px) - max(calc(var(--zen-composer-h, 110px) + 38px), 260px));
}
.zen-mode.zen-viz-circus .zen-last-message-clickable.zen-response-card:hover {
  background: #f8efdb;
}
/* Your message: a pinned note */
.zen-mode.zen-viz-circus .zen-last-message.zen-user-message {
  background: var(--zen-paper-note);
  rotate: -1deg;
  padding: 12px 16px 14px;
}
.zen-mode.zen-viz-circus .zen-user-message .zen-last-message-label { margin-bottom: 6px; }
.zen-mode.zen-viz-circus .zen-user-message .zen-last-message-text {
  font: italic 400 1.1rem / 1.4 'EB Garamond', Georgia, serif;
  color: var(--zen-ink);
  scrollbar-color: rgba(107, 86, 56, 0.45) transparent;
}
/* The expanded reply */
.zen-mode.zen-viz-circus .zen-expanded-card :deep(*) { color: var(--zen-ink); }
.zen-mode.zen-viz-circus .zen-expanded-text {
  font: 400 1.12rem / 1.55 'EB Garamond', Georgia, serif;
}
.zen-mode.zen-viz-circus .zen-resp-reopen {
  color: var(--zen-ink);
  background: var(--zen-paper);
  border: 1px solid var(--zen-rule);
  border-radius: 2px;
  box-shadow: 0 6px 14px rgba(10, 8, 20, 0.3);
}
@media (prefers-reduced-motion: reduce) {
  .zen-mode.zen-viz-circus .zen-last-message.zen-response-panel,
  .zen-mode.zen-viz-circus .zen-last-message.zen-user-message { rotate: none; }
  .zen-mode.zen-viz-circus .zen-response-card.is-writing :deep(.zen-resp-md > :last-child::after) { animation: none; }
}
</style>
