<template>
  <transition name="modal-fade">
    <!-- In zen, recording shows as ZenVoiceBar above the input instead -->
    <div v-if="show && !compact" class="recording-modal-overlay" :class="{ zen: variant === 'zen' }" @click="$emit('cancel')">
      <div class="recording-modal" @click.stop>
        <div class="modal-header">
          <h3>Voice Recording</h3>
          <button @click="$emit('cancel')" class="close-btn" title="Cancel">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="18" y1="6" x2="6" y2="18"></line>
              <line x1="6" y1="6" x2="18" y2="18"></line>
            </svg>
          </button>
        </div>

        <div class="modal-body">
          <!-- Recording Visualization -->
          <div class="recording-visualization" v-if="isRecording && !isTranscribing">
            <div class="pulse-ring"></div>
            <div class="pulse-ring-2"></div>
            <div class="microphone-icon">
              <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="1">
                <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"></path>
                <path d="M19 10v2a7 7 0 0 1-14 0v-2"></path>
                <line x1="12" y1="19" x2="12" y2="23"></line>
                <line x1="8" y1="23" x2="16" y2="23"></line>
              </svg>
            </div>
          </div>

          <!-- Transcribing State -->
          <div v-if="isTranscribing" class="transcribing-state">
            <div class="spinner"></div>
            <p class="status-text">Transcribing...</p>
          </div>

          <!-- Model Loading State -->
          <div v-if="isModelLoading" class="loading-state">
            <div class="loading-icon">
              <svg width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path>
                <polyline points="7 10 12 15 17 10"></polyline>
                <line x1="12" y1="15" x2="12" y2="3"></line>
              </svg>
            </div>
            <h3 class="loading-title">{{ modelCached ? 'Loading voice model' : 'Preparing AI Model' }}</h3>
            <p class="status-text">{{ loadingText }}</p>
            <p class="status-subtext">{{ loadingSubtext }}</p>
            <div class="progress-bar-container">
              <div class="progress-bar">
                <div class="progress-fill" :style="{ width: progress + '%' }"></div>
              </div>
              <p class="progress-text">{{ progress }}%</p>
            </div>
          </div>

          <!-- Recording Status -->
          <div v-if="!isTranscribing && !isModelLoading" class="recording-status">
            <p class="status-text">
              {{ isRecording ? 'Recording...' : 'Ready to record' }}
            </p>
            <p class="duration">{{ formattedDuration }}</p>
            <p v-if="isRecording" class="status-hint">Press Space to stop recording</p>
          </div>

          <!-- Error Display -->
          <div v-if="error" class="error-message">
            {{ error }}
          </div>
        </div>

        <div class="modal-footer">
          <button
            v-if="isRecording"
            @click="$emit('finish')"
            class="btn-modal btn-stop"
            :disabled="isTranscribing"
          >
            Stop & Transcribe (Space)
          </button>
          <button
            @click="$emit('cancel')"
            class="btn-modal btn-cancel"
            :disabled="isModelLoading"
          >
            Cancel (Esc)
          </button>
        </div>
      </div>
    </div>
  </transition>
</template>

<script setup lang="ts">
import { computed } from 'vue'

interface Props {
  show: boolean
  isRecording: boolean
  isTranscribing: boolean
  isModelLoading: boolean
  progress: number
  duration: number
  error: string | null
  modelName?: string
  /** "zen" renders dark glass to match zen mode. */
  variant?: 'default' | 'zen'
  /** Model files come from this machine (true), are downloading (false), or unknown (null). */
  modelCached?: boolean | null
}

const props = withDefaults(defineProps<Props>(), { variant: 'default', modelName: undefined, modelCached: null })

const loadingText = computed(() => {
  const name = props.modelName || 'Whisper'
  if (props.progress >= 99) return 'Warming up model...'
  if (props.modelCached) return `Loading ${name} from this computer...`
  if (props.modelCached === false) return `Downloading ${name} speech recognition model...`
  return `Preparing ${name}...`
})

const loadingSubtext = computed(() => {
  if (props.progress >= 99) return 'Almost ready'
  if (props.modelCached) return 'Already downloaded. It loads into the browser once per page load.'
  return "This only happens once, then it's cached for instant use"
})

defineEmits<{
  'finish': []
  'cancel': []
}>()

/** Zen, recording or transcribing, nothing to report: ZenVoiceBar shows it instead. */
const compact = computed(() =>
  props.variant === 'zen' && !props.isModelLoading && !props.error && (props.isRecording || props.isTranscribing)
)

// duration is in whole seconds (useVoiceRecording ticks once per second)
const formattedDuration = computed(() => {
  const totalSeconds = Math.floor(props.duration)
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`
})
</script>

<style scoped>
.recording-modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 10050; /* above fullscreen zen mode (9999) */
  backdrop-filter: blur(4px);
}

.recording-modal {
  background: var(--card-bg);
  border-radius: 16px;
  padding: 24px;
  min-width: 400px;
  max-width: 500px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.4);
  border: 1px solid var(--border-color);
}

.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 24px;
}

.modal-header h3 {
  margin: 0;
  font-size: 1.25rem;
  color: var(--text-primary);
}

.close-btn {
  background: none;
  border: none;
  color: var(--text-secondary);
  cursor: pointer;
  padding: 4px;
  border-radius: 4px;
  transition: all 0.2s;
}

.close-btn:hover {
  background: var(--bg-secondary);
  color: var(--text-primary);
}

.modal-body {
  min-height: 200px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  padding: 32px 16px;
}

.recording-visualization {
  position: relative;
  width: 120px;
  height: 120px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.pulse-ring,
.pulse-ring-2 {
  position: absolute;
  width: 100%;
  height: 100%;
  border: 3px solid #dc3545;
  border-radius: 50%;
  animation: pulse 2s ease-out infinite;
  opacity: 0;
}

.pulse-ring-2 {
  animation-delay: 1s;
}

@keyframes pulse {
  0% {
    transform: scale(0.5);
    opacity: 0.8;
  }
  50% {
    opacity: 0.4;
  }
  100% {
    transform: scale(1.2);
    opacity: 0;
  }
}

.microphone-icon {
  width: 64px;
  height: 64px;
  background: linear-gradient(135deg, #dc3545, #c82333);
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  z-index: 1;
  box-shadow: 0 4px 20px rgba(220, 53, 69, 0.4);
}

.transcribing-state,
.loading-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  padding: 20px;
  text-align: center;
}

.loading-icon {
  color: var(--accent-purple);
  animation: bounce 2s ease-in-out infinite;
}

@keyframes bounce {
  0%, 100% {
    transform: translateY(0);
  }
  50% {
    transform: translateY(-10px);
  }
}

.loading-title {
  font-size: 1.3rem;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0;
}

.status-subtext {
  font-size: 0.85rem;
  color: var(--text-secondary);
  margin: -8px 0 0 0;
  opacity: 0.8;
}

.progress-bar-container {
  width: 100%;
  max-width: 300px;
}

.spinner {
  width: 48px;
  height: 48px;
  border: 4px solid var(--border-color);
  border-top-color: var(--accent-purple);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.recording-status {
  text-align: center;
}

.status-text {
  font-size: 1.1rem;
  color: var(--text-primary);
  margin: 0 0 8px 0;
  font-weight: 500;
}

.duration {
  font-size: 2rem;
  font-weight: 600;
  color: var(--accent-purple);
  margin: 0;
  font-variant-numeric: tabular-nums;
}

.status-hint {
  font-size: 0.85rem;
  color: var(--text-secondary);
  margin: 8px 0 0 0;
  opacity: 0.8;
  font-style: italic;
}

.progress-bar {
  width: 100%;
  height: 8px;
  background: var(--bg-secondary);
  border-radius: 4px;
  overflow: hidden;
  margin-top: 8px;
}

.progress-fill {
  height: 100%;
  background: linear-gradient(90deg, var(--accent-purple), var(--accent-purple-hover));
  transition: width 0.3s ease;
}

.progress-text {
  font-size: 0.9rem;
  color: var(--text-secondary);
  margin: 4px 0 0 0;
}

.error-message {
  color: #dc3545;
  background: rgba(220, 53, 69, 0.1);
  padding: 12px 16px;
  border-radius: 8px;
  font-size: 0.9rem;
  text-align: center;
}

.modal-footer {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-top: 24px;
  align-items: stretch;
}

.btn-modal {
  width: 100%;
  padding: 14px 20px;
  border: none;
  border-radius: 8px;
  font-size: 0.95rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-stop {
  background: linear-gradient(135deg, var(--accent-purple), var(--accent-purple-hover));
  color: white;
}

.btn-stop:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.3);
}

.btn-cancel {
  background: var(--bg-secondary);
  color: var(--text-secondary);
}

.btn-cancel:hover:not(:disabled) {
  background: var(--border-color);
  color: var(--text-primary);
}

.btn-modal:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.modal-fade-enter-active,
.modal-fade-leave-active {
  transition: opacity 0.3s ease;
}

.modal-fade-enter-from,
.modal-fade-leave-to {
  opacity: 0;
}

.modal-fade-enter-active .recording-modal,
.modal-fade-leave-active .recording-modal {
  transition: transform 0.3s ease;
}

.modal-fade-enter-from .recording-modal,
.modal-fade-leave-to .recording-modal {
  transform: scale(0.9) translateY(-20px);
}

/* ===== Zen variant ===== */
.recording-modal-overlay.zen {
  background: rgba(4, 2, 12, 0.35);
  backdrop-filter: blur(3px);
}
.recording-modal-overlay.zen .recording-modal {
  background: rgba(10, 8, 22, 0.82);
  backdrop-filter: blur(22px);
  -webkit-backdrop-filter: blur(22px);
  border: 1px solid rgba(255, 255, 255, 0.1);
  box-shadow: 0 -12px 50px rgba(0, 0, 0, 0.5);
  border-radius: 18px;
  color: rgba(255, 255, 255, 0.9);
}
.recording-modal-overlay.zen .modal-header,
.recording-modal-overlay.zen .modal-footer {
  background: transparent;
  border-color: rgba(255, 255, 255, 0.08);
}
.recording-modal-overlay.zen .modal-header h3,
.recording-modal-overlay.zen .status-text,
.recording-modal-overlay.zen .duration,
.recording-modal-overlay.zen .loading-title {
  color: #fff;
}
.recording-modal-overlay.zen .status-hint,
.recording-modal-overlay.zen .status-subtext,
.recording-modal-overlay.zen .progress-text,
.recording-modal-overlay.zen .close-btn {
  color: rgba(255, 255, 255, 0.55);
}
.recording-modal-overlay.zen .progress-bar {
  background: rgba(255, 255, 255, 0.1);
}
.recording-modal-overlay.zen .btn-modal {
  border-radius: 999px;
}
.recording-modal-overlay.zen .btn-cancel {
  color: rgba(255, 255, 255, 0.75);
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.14);
}
.recording-modal-overlay.zen .btn-cancel:hover:not(:disabled) {
  color: #fff;
  background: rgba(255, 255, 255, 0.12);
}
.recording-modal-overlay.zen .btn-stop {
  box-shadow: 0 0 20px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.35);
}
</style>
