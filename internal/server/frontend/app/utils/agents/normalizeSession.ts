/**
 * A session from the server (session list, session_created, handovers) in the
 * shape the session store keeps. Missing values get plain defaults, which
 * prevents Vue reactivity issues with null/undefined values.
 *
 * Only the fields listed here survive, so a field the UI reads from the
 * listed session must be added. `worker`, `spawned_by_session_id`, `team` and
 * `read_only` were missing: a listed worker was an ordinary session (no row
 * under its parent, no chip in the team strip) until a session_updated for it
 * put the raw session in the store.
 */
export function normalizeSession(session: any, sessionId?: string): any {
  if (!session || typeof session !== 'object') {
    return null
  }

  return {
    id: session.id || sessionId || '',
    created_at: session.created_at || new Date().toISOString(),
    updated_at: session.updated_at || new Date().toISOString(),
    status: session.status || 'idle',
    message_count: session.message_count || 0,
    subagents_running: session.subagents_running || 0,
    scheduled_loop: session.scheduled_loop || null,
    limit_wait: session.limit_wait || null,
    cost_usd: session.cost_usd || 0,
    num_turns: session.num_turns || 0,
    duration_ms: session.duration_ms || 0,
    model_name: session.model_name || '',
    provider: session.provider || '',
    git_branch: session.git_branch || '',
    claude_session_id: session.claude_session_id || '',
    context_summary: session.context_summary || '',
    error_message: session.error_message || null,
    parent_session_id: session.parent_session_id || null,
    project_id: session.project_id || null,
    selected_avatar_id: session.selected_avatar_id || null,
    project_area: session.project_area || null,
    options: {
      system_prompt: session.options?.system_prompt || null,
      agent_name: session.options?.agent_name || null,
      tools: session.options?.tools || [],
      working_directory: session.options?.working_directory || null,
      max_tokens: session.options?.max_tokens || null,
      temperature: session.options?.temperature || null,
      permission_mode: session.options?.permission_mode || null,
      provider: session.options?.provider || null,
      model: session.options?.model || null,
      base_url: session.options?.base_url || null,
      api_key: session.options?.api_key || null,
      always_allow_rules: session.options?.always_allow_rules || [],
      project_id: session.options?.project_id || null,
      dangerously_skip_permissions: session.options?.dangerously_skip_permissions || null,
      allow_dangerously_skip_permissions: session.options?.allow_dangerously_skip_permissions || null,
      parent_session_id: session.options?.parent_session_id || null,
      // Worker sessions and teams (utils/agents/team.ts reads these).
      spawned_by_session_id: session.options?.spawned_by_session_id || null,
      worker: session.options?.worker || null,
      team: session.options?.team || null,
      read_only: session.options?.read_only || false
    }
  }
}
