import { ref } from 'vue'
import { useAuthenticatedFetch } from '~/composables/useAuthenticatedFetch'
import type { PendingCounts, PendingMemoryItem, PendingSkillItem, ReviewEdits, ReviewKind } from '~/types/review'
import { approveBody } from '~/types/review'

// Shared across the app so the sidebar badge and the review queues agree.
const counts = ref<PendingCounts>({ memories: 0, skills: 0, total: 0, memories_by_project: {} })

/**
 * Review queue for memories and skills that agents wrote. Approve/reject
 * need a logged-in user on the server (the API key alone is refused).
 */
export function usePendingReview() {
  const { fetchWithAuth } = useAuthenticatedFetch()

  const loadCounts = async () => {
    try {
      const res = await fetchWithAuth('/api/review/pending/counts')
      if (res.ok) counts.value = await res.json()
    } catch (err) {
      console.warn('Failed to load pending review counts:', err)
    }
  }

  const loadPendingMemories = async (projectId?: string): Promise<PendingMemoryItem[]> => {
    const q = projectId ? `&project_id=${encodeURIComponent(projectId)}` : ''
    const res = await fetchWithAuth(`/api/review/pending?kind=memory${q}`)
    if (!res.ok) throw new Error('Failed to load pending memories')
    const data = await res.json()
    return data.memories || []
  }

  const loadPendingSkills = async (): Promise<PendingSkillItem[]> => {
    const res = await fetchWithAuth('/api/review/pending?kind=skill')
    if (!res.ok) throw new Error('Failed to load pending skills')
    const data = await res.json()
    return data.skills || []
  }

  const decide = async (kind: ReviewKind, id: string, action: 'approve' | 'reject', edits?: ReviewEdits) => {
    const path = kind === 'memory' ? 'memories' : 'skills'
    const body = action === 'approve' ? approveBody(kind, edits) : undefined
    const res = await fetchWithAuth(`/api/review/${path}/${encodeURIComponent(id)}/${action}`, {
      method: 'POST',
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined
    })
    if (!res.ok) {
      let message = `Failed to ${action}`
      try {
        const data = await res.json()
        if (data?.error) message = data.error
      } catch { /* keep default */ }
      throw new Error(message)
    }
    await loadCounts()
  }

  return { counts, loadCounts, loadPendingMemories, loadPendingSkills, decide }
}
