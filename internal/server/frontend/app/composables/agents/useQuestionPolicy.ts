import { ref } from 'vue'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import { checkResponseForUserRequired } from '~/utils/userRequired'
import { AGENT_ATTENTION_EVENT } from '~/utils/agents/needsYou'
import { DEFAULT_QUESTION_POLICY, parseQuestionPolicy, type QuestionPolicy } from '~/utils/agents/questionPolicy'

/**
 * Agent question policy over REST:
 *   GET/PUT /api/agent/question-policy               global default
 *   PUT     /api/agent/sessions/:id/question-policy  {"policy": <policy>|null}
 * Saving needs a signed-in user (403 user_required shows the sign-in toast).
 * The global policy is shared by every caller and follows the WebSocket's
 * question_policy_default_changed.
 */

const globalPolicy = ref<QuestionPolicy | null>(null)
const globalIsDefault = ref(true)
let loadPromise: Promise<QuestionPolicy | null> | null = null
let listening = false

function listen() {
  if (listening || typeof window === 'undefined') return
  listening = true
  window.addEventListener(AGENT_ATTENTION_EVENT, (event: Event) => {
    const m = (event as CustomEvent<any>).detail
    if (m?.type !== 'question_policy_default_changed') return
    const p = parseQuestionPolicy(m.policy)
    if (p) {
      globalPolicy.value = p
      globalIsDefault.value = false
    }
  })
}

export interface SaveResult {
  ok: boolean
  error?: string
  userRequired?: boolean
  data?: any
}

async function readError(res: Response, fallback: string): Promise<string> {
  try {
    const body = await res.clone().json()
    return body?.error || body?.message || fallback
  } catch {
    return fallback
  }
}

export function useQuestionPolicy() {
  const { fetchWithAuth } = useAuthenticatedFetch()
  listen()

  const loadGlobal = (force = false): Promise<QuestionPolicy | null> => {
    if (loadPromise && !force) return loadPromise
    loadPromise = (async () => {
      try {
        const res = await fetchWithAuth('/api/agent/question-policy')
        if (!res.ok) {
          loadPromise = null
          return globalPolicy.value
        }
        const body = await res.json()
        globalPolicy.value = parseQuestionPolicy(body?.policy) || { ...DEFAULT_QUESTION_POLICY }
        globalIsDefault.value = !!body?.is_default
        return globalPolicy.value
      } catch {
        loadPromise = null
        return globalPolicy.value
      }
    })()
    return loadPromise
  }

  const saveGlobal = async (policy: QuestionPolicy): Promise<SaveResult> => {
    try {
      const res = await fetchWithAuth('/api/agent/question-policy', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(policy),
      })
      if (!res.ok) {
        const userRequired = await checkResponseForUserRequired(res)
        return { ok: false, userRequired, error: await readError(res, `Failed to save (${res.status})`) }
      }
      const body = await res.json().catch(() => ({}))
      globalPolicy.value = parseQuestionPolicy(body?.policy) || policy
      globalIsDefault.value = !!body?.is_default
      return { ok: true, data: body }
    } catch (e: any) {
      return { ok: false, error: e?.message || 'Failed to save' }
    }
  }

  const saveSession = async (sessionId: string, policy: QuestionPolicy | null): Promise<SaveResult> => {
    try {
      const res = await fetchWithAuth(`/api/agent/sessions/${encodeURIComponent(sessionId)}/question-policy`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ policy }),
      })
      if (!res.ok) {
        const userRequired = await checkResponseForUserRequired(res)
        return { ok: false, userRequired, error: await readError(res, `Failed to save (${res.status})`) }
      }
      const body = await res.json().catch(() => ({}))
      return { ok: true, data: body }
    } catch (e: any) {
      return { ok: false, error: e?.message || 'Failed to save' }
    }
  }

  return { globalPolicy, globalIsDefault, loadGlobal, saveGlobal, saveSession }
}
