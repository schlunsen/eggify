import { ref } from 'vue'

/**
 * A request to show one subagent in its session's Subagents view (⇧⌘A), made
 * by the All agents overlay (⇧⌘S) before it opens that session. The agents
 * page takes it once the session is open (pages/agents/index.vue).
 */
export const subagentFocusRequest = ref<{ sessionId: string; agentId: string } | null>(null)

export function requestSubagentFocus(sessionId: string, agentId: string) {
  subagentFocusRequest.value = { sessionId, agentId }
}

/** Takes the request when it is for `sessionId` (and clears it), else null. */
export function takeSubagentFocus(sessionId: string | null | undefined): string | null {
  const request = subagentFocusRequest.value
  if (!request || !sessionId || request.sessionId !== sessionId) return null
  subagentFocusRequest.value = null
  return request.agentId
}
