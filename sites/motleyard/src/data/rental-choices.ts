export const choices = {
  machine: {
    legend: '01 / CHOOSE YOUR MACHINE',
    name: 'machine',
    type: 'radio',
    options: [
      {
        value: 'pro',
        title: 'M5 Pro',
        description:
          'The everyday workshop. Projects, tools, and room to build.',
      },
      {
        value: 'max',
        title: 'M5 Max',
        description: 'The bigger workshop. Explore a higher-tier Mac setup.',
      },
    ],
    selected: ['pro'],
  },
  tools: {
    legend: '02 / PREPARE YOUR AGENT TOOLS',
    name: 'tools',
    type: 'checkbox',
    options: [
      {
        value: 'Claude Code',
        title: 'Claude Code',
        description: 'Ready for you to connect your own account.',
      },
      {
        value: 'Codex',
        title: 'Codex',
        description: 'Another tool in your workshop. Your own provider access.',
      },
    ],
    selected: ['Claude Code', 'Codex'],
  },
  work: {
    legend: '03 / WHAT ARE YOU BUILDING?',
    name: 'work',
    type: 'radio',
    options: [
      {
        value: 'Web & apps',
        title: 'Web & apps',
        description: 'Interfaces, services, and products.',
      },
      {
        value: 'Creative tools',
        title: 'Creative tools',
        description: 'New ways to make and experiment.',
      },
      {
        value: 'A bit of everything',
        title: 'A bit of everything',
        description: 'A workshop for a motley mix.',
      },
      {
        value: 'Still exploring',
        title: 'Still exploring',
        description: 'You don’t need it all figured out.',
      },
    ],
    selected: ['Web & apps'],
  },
} as const;
