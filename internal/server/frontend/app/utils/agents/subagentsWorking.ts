/**
 * How many of a session's subagents are still working. A session goes idle
 * when its own turn ends, while background subagents it launched carry on, so
 * "idle" alone hides work in progress.
 *
 * The live agents (kept current by background_agent_* events) count when the
 * client has the session's whole list; otherwise the count the server sent
 * with the session list is used.
 */
export function subagentsWorkingCount(
  liveAgents: ReadonlyArray<{ status: string }>,
  liveIsComplete: boolean,
  listedRunning?: number | null,
): number {
  if (liveIsComplete) {
    return liveAgents.filter(agent => agent.status === 'running').length
  }
  return Math.max(0, listedRunning ?? 0)
}

/** "1 subagent working", "3 subagents working". */
export function subagentsWorkingLabel(count: number): string {
  return `${count} subagent${count === 1 ? '' : 's'} working`
}
