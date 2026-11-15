export const machines = ['pro', 'max'] as const;
export const agentTools = ['Claude Code', 'Codex'] as const;
export const workOptions = [
  'Web & apps',
  'Creative tools',
  'A bit of everything',
  'Still exploring',
] as const;
export type Machine = (typeof machines)[number];
export type AgentTool = (typeof agentTools)[number];
export type WorkFocus = (typeof workOptions)[number];
export interface RentalSetup {
  machine: Machine;
  tools: AgentTool[];
  work: WorkFocus;
}

export function parseSetup(params: URLSearchParams): RentalSetup {
  const requestedTools = params.has('tools')
    ? (params.get('tools') ?? '').split(',')
    : agentTools;
  const work =
    workOptions.find((option) => option === params.get('work')) ??
    workOptions[0];
  return {
    machine: params.get('machine') === 'max' ? 'max' : 'pro',
    tools: agentTools.filter((tool) => requestedTools.includes(tool)),
    work,
  };
}

export function machineName(machine: Machine): string {
  return machine === 'max' ? 'Mac M5 Max' : 'Mac M5 Pro';
}

export function setupLink(setup: RentalSetup, origin: string): URL {
  const url = new URL('/configure.html', origin);
  url.searchParams.set('machine', setup.machine);
  url.searchParams.set('tools', setup.tools.join(','));
  url.searchParams.set('work', setup.work);
  return url;
}

export function setupBrief(setup: RentalSetup): string {
  return [
    'MOTLEYARD / YOUR SETUP BRIEF',
    '',
    machineName(setup.machine),
    'Workspace: Motleyard',
    `Agent tools: ${setup.tools.join(', ') || 'Bring your own'}`,
    `Building: ${setup.work}`,
    '',
    'Illustrative rental concept. No order has been placed.',
    'Hardware specifications, availability, rates, and rental terms are not final.',
    'Provider accounts, subscriptions, and usage are separate.',
  ].join('\n');
}
