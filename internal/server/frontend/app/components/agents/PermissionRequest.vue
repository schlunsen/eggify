<template>
  <div class="permission-request" :class="{ 'disconnected': !connected, zen: variant === 'zen' }">
    <div v-if="!connected" class="connection-warning">
      ⚠️ Connection lost - this request may no longer be valid
    </div>
    <div v-if="variant === 'zen'" class="permission-header zen-header">
      <span class="zen-dot" aria-hidden="true"></span>
      <span class="zen-label">Needs your approval</span>
      <span v-if="permission.tool" class="zen-tool">{{ permission.tool }}</span>
      <span class="permission-time">{{ formatTime(permission.timestamp) }}</span>
    </div>
    <div v-else class="permission-header">
      <div class="permission-icon">🔐</div>
      <div class="permission-title">Permission Request</div>
      <div class="permission-time">{{ formatTime(permission.timestamp) }}</div>
    </div>
    <div class="permission-description">
      {{ permission.description }}
    </div>
    <pre v-if="variant === 'zen' && subject" class="zen-subject">{{ subject }}</pre>

    <!-- Show pattern preview for "Allow Similar" -->
    <div v-if="similarPattern" class="similar-preview" :class="{ 'security-warning': similarPattern === 'Bash(*)' }">
      <div class="preview-label">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"></circle>
          <line x1="12" y1="16" x2="12" y2="12"></line>
          <line x1="12" y1="8" x2="12.01" y2="8"></line>
        </svg>
        <strong v-if="similarPattern === 'Bash(*)'">⚠️ Security Warning:</strong>
        <strong v-else>Allow Similar will approve:</strong>
      </div>
      <code>{{ similarPattern }}</code>
      <div v-if="similarPattern === 'Bash(*)'" class="warning-message">
        Bash(*) is not allowed as a project permission (security risk). It would allow ANY command to run without approval.
      </div>
    </div>

    <div class="permission-actions">
      <button
        @click="$emit('deny', permission)"
        class="btn-deny"
        :disabled="!connected"
        title="Deny this request"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <line x1="18" y1="6" x2="6" y2="18"></line>
          <line x1="6" y1="6" x2="18" y2="18"></line>
        </svg>
        Deny
      </button>

      <button
        @click="$emit('approve-exact', permission)"
        class="btn-approve-exact"
        :disabled="!connected"
        title="Always allow this exact command/file"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 2L2 7l10 5 10-5-10-5z"></path>
          <path d="M2 17l10 5 10-5M2 12l10 5 10-5"></path>
        </svg>
        Always Exact
      </button>

      <button
        @click="$emit('approve-similar', permission)"
        class="btn-approve-similar"
        :disabled="!connected || !similarPattern || similarPattern === 'Bash(*)'"
        :title="similarPattern === 'Bash(*)' ? 'Bash(*) is not allowed as a project permission (security risk)' : similarPattern ? `Allow similar: ${similarPattern}` : 'No pattern available'"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"></path>
          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"></path>
        </svg>
        Allow Similar
      </button>

      <button
        @click="$emit('approve', permission)"
        class="btn-approve"
        :disabled="!connected"
        title="Approve this request once"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="20,6 9,17 4,12"></polyline>
        </svg>
        Approve Once
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { formatTime } from '~/utils/agents/messageFormatters'

interface Permission {
  request_id: string
  description: string
  timestamp: string | Date
  tool: string
  details: any
}

interface Props {
  permission: Permission
  connected?: boolean
  /** "zen" renders a dark glass card that matches zen mode. */
  variant?: 'default' | 'zen'
}

const props = withDefaults(defineProps<Props>(), {
  connected: true,
  variant: 'default',
})

// What the tool wants to touch (command / file / pattern), shown in the zen card.
const subject = computed(() => {
  const d = props.permission.details
  if (!d || typeof d !== 'object') return ''
  const value = d.command ?? d.file_path ?? d.path ?? d.pattern ?? d.url
  return typeof value === 'string' ? value : ''
})

defineEmits<{
  (e: 'approve', permission: Permission): void
  (e: 'approve-exact', permission: Permission): void
  (e: 'approve-similar', permission: Permission): void
  (e: 'deny', permission: Permission): void
}>()

// Generate preview of what "Allow Similar" will match
const similarPattern = computed(() => {
  const { tool, details } = props.permission

  // "Allow Similar" creates a pattern based on the command/file
  switch (tool) {
    case 'Bash': {
      // Extract command name from the bash command
      if (details?.command) {
        const command = details.command.trim()
        const commandName = command.split(/\s+/)[0] // Get first word
        return `Bash(${commandName}:*)`
      }
      return `Bash(*)`
    }

    case 'Read':
      return `Read(**)`

    case 'Write':
      return `Write(**)`

    case 'Edit':
      return `Edit(**)`

    case 'Grep':
      return `Grep(*)`

    case 'Glob':
      return `Glob(*)`

    default:
      return `${tool}(*)`
  }
})
</script>

<style scoped>
.permission-request {
  background: rgba(251, 191, 36, 0.1);
  border: 2px solid var(--status-warning);
  border-radius: 0.5rem;
  padding: 1rem;
  margin-bottom: 1rem;
}

.permission-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 0.75rem;
}

.permission-icon {
  font-size: 1.5rem;
}

.permission-title {
  flex: 1;
  font-weight: 600;
  color: var(--text-primary);
}

.permission-time {
  font-size: 0.875rem;
  color: var(--text-secondary);
}

.permission-description {
  color: var(--text-primary);
  margin-bottom: 1rem;
  line-height: 1.5;
}

.similar-preview {
  background: #e8f4f8;
  border-left: 3px solid #3b82f6;
  padding: 0.75rem;
  margin: 0.75rem 0;
  border-radius: 0.375rem;
}

.preview-label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
  font-size: 0.875rem;
  color: #1e40af;
}

.preview-label svg {
  flex-shrink: 0;
}

.similar-preview code {
  display: block;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
  font-size: 0.875rem;
  color: #1e40af;
  background: rgba(59, 130, 246, 0.1);
  padding: 0.5rem;
  border-radius: 0.25rem;
}

.similar-preview.security-warning {
  background: rgba(239, 68, 68, 0.1);
  border-color: rgba(239, 68, 68, 0.3);
}

.similar-preview.security-warning .preview-label {
  color: var(--status-error, #dc2626);
}

.similar-preview.security-warning code {
  color: var(--status-error, #dc2626);
  background: rgba(239, 68, 68, 0.1);
}

.warning-message {
  margin-top: 0.5rem;
  font-size: 0.75rem;
  color: var(--status-error, #dc2626);
  font-weight: 500;
}

.permission-actions {
  display: flex;
  gap: 0.5rem;
  justify-content: flex-end;
  flex-wrap: wrap;
}

.btn-deny,
.btn-approve,
.btn-approve-exact,
.btn-approve-similar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.875rem;
  border: none;
  border-radius: 0.375rem;
  font-weight: 600;
  font-size: 0.875rem;
  cursor: pointer;
  transition: all 0.2s;
  white-space: nowrap;
}

.btn-deny {
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
}

.btn-deny:hover:not(:disabled) {
  background: var(--bg-tertiary);
  border-color: var(--text-secondary);
  transform: translateY(-1px);
}

.btn-approve-exact {
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
}

.btn-approve-exact:hover:not(:disabled) {
  background: var(--bg-tertiary);
  border-color: var(--accent-purple);
  color: var(--accent-purple);
  transform: translateY(-1px);
  box-shadow: 0 2px 8px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.2);
}

.btn-approve-similar {
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
}

.btn-approve-similar:hover:not(:disabled) {
  background: var(--bg-tertiary);
  border-color: var(--accent-purple);
  color: var(--accent-purple);
  transform: translateY(-1px);
  box-shadow: 0 2px 8px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.2);
}

.btn-approve {
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
}

.btn-approve:hover:not(:disabled) {
  background: var(--bg-tertiary);
  border-color: var(--accent-purple);
  color: var(--accent-purple);
  transform: translateY(-1px);
  box-shadow: 0 2px 8px rgba(var(--accent-purple-rgb, 139, 92, 246), 0.2);
}

.btn-deny:disabled,
.btn-approve:disabled,
.btn-approve-exact:disabled,
.btn-approve-similar:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.permission-request.disconnected {
  opacity: 0.7;
  border-color: var(--status-error);
}

.connection-warning {
  background: rgba(248, 113, 113, 0.1);
  border: 1px solid var(--status-error);
  border-radius: 0.375rem;
  padding: 0.5rem;
  margin-bottom: 0.75rem;
  font-size: 0.875rem;
  font-weight: 500;
  color: var(--status-error);
  text-align: center;
}

/* ===== Zen variant: dark glass card matching zen mode (the scene is always dark) ===== */
.permission-request.zen {
  margin: 0;
  padding: 16px 18px;
  color: rgba(255, 255, 255, 0.9);
  background: rgba(10, 8, 22, 0.62);
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-left: 3px solid rgba(245, 158, 11, 0.75);
  border-radius: 16px;
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.45), 0 0 40px rgba(245, 158, 11, 0.08);
}

.permission-request.zen .zen-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}

.permission-request.zen .zen-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #f59e0b;
  box-shadow: 0 0 0 0 rgba(245, 158, 11, 0.5);
  animation: zen-perm-pulse 1.6s ease-in-out infinite;
}

.permission-request.zen .zen-label {
  font-size: 0.65rem;
  font-weight: 600;
  letter-spacing: 0.8px;
  text-transform: uppercase;
  color: #fbbf24;
}

.permission-request.zen .zen-tool {
  padding: 1px 8px;
  border-radius: 999px;
  font-size: 0.7rem;
  font-family: var(--font-mono, ui-monospace, monospace);
  color: rgba(255, 255, 255, 0.85);
  background: rgba(255, 255, 255, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.12);
}

.permission-request.zen .permission-time {
  margin-left: auto;
  font-size: 0.7rem;
  color: rgba(255, 255, 255, 0.45);
}

.permission-request.zen .permission-description {
  margin: 0 0 8px;
  font-size: 0.9rem;
  color: rgba(255, 255, 255, 0.9);
}

.permission-request.zen .zen-subject {
  margin: 0 0 12px;
  padding: 10px 12px;
  max-height: 140px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.8rem;
  line-height: 1.45;
  color: #e9e3ff;
  background: rgba(0, 0, 0, 0.35);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 10px;
}

.permission-request.zen .similar-preview {
  margin: 0 0 12px;
  padding: 8px 12px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 10px;
  color: rgba(255, 255, 255, 0.65);
}

.permission-request.zen .similar-preview .preview-label {
  color: rgba(255, 255, 255, 0.6);
  font-size: 0.72rem;
}

.permission-request.zen .similar-preview code {
  color: rgba(255, 255, 255, 0.85);
  background: transparent;
  padding: 0;
}

.permission-request.zen .similar-preview.security-warning {
  border-color: rgba(248, 113, 113, 0.3);
  background: rgba(248, 113, 113, 0.08);
}

.permission-request.zen .similar-preview.security-warning .preview-label,
.permission-request.zen .similar-preview.security-warning code,
.permission-request.zen .warning-message {
  color: #fca5a5;
}

.permission-request.zen .permission-actions {
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.permission-request.zen .btn-deny,
.permission-request.zen .btn-approve-exact,
.permission-request.zen .btn-approve-similar,
.permission-request.zen .btn-approve {
  padding: 7px 14px;
  border-radius: 999px;
  font-size: 0.78rem;
  color: rgba(255, 255, 255, 0.8);
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.14);
  box-shadow: none;
}

.permission-request.zen .btn-deny:hover:not(:disabled),
.permission-request.zen .btn-approve-exact:hover:not(:disabled),
.permission-request.zen .btn-approve-similar:hover:not(:disabled) {
  color: #fff;
  background: rgba(255, 255, 255, 0.12);
  border-color: rgba(255, 255, 255, 0.3);
  transform: none;
}

.permission-request.zen .btn-deny:hover:not(:disabled) {
  color: #fecaca;
  border-color: rgba(248, 113, 113, 0.5);
}

.permission-request.zen .btn-approve {
  color: #1a1206;
  font-weight: 600;
  background: #fbbf24;
  border-color: #fbbf24;
  box-shadow: 0 0 18px rgba(251, 191, 36, 0.35);
}

.permission-request.zen .btn-approve:hover:not(:disabled) {
  color: #1a1206;
  background: #fcd34d;
  border-color: #fcd34d;
  transform: none;
}

.permission-request.zen button:focus-visible {
  outline: 2px solid rgba(251, 191, 36, 0.8);
  outline-offset: 2px;
}

@keyframes zen-perm-pulse {
  0%, 100% { box-shadow: 0 0 0 0 rgba(245, 158, 11, 0.5); }
  50% { box-shadow: 0 0 0 6px rgba(245, 158, 11, 0); }
}

@media (prefers-reduced-motion: reduce) {
  .permission-request.zen .zen-dot { animation: none; }
}
</style>
