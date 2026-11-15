import type { PageContent } from './types';

export const pageContent = {
  index: {
    features: [
      {
        title: 'A crew, on your terms.',
        description:
          'Run agent sessions side by side. Choose the right tool for each job and keep every conversation close to its project.',
      },
      {
        title: 'Stay in the picture.',
        description:
          'Follow tool activity, usage, costs, and Git changes. See what’s happening while the work is happening.',
      },
      {
        title: 'Take the long way home.',
        description:
          'Check in from your iPhone through remote access to your licensed Mac. Your workspace is still right where you left it.',
      },
    ],
    steps: [],
    faqs: [
      {
        question: 'Does Motleyard include Claude or Codex?',
        answer:
          'Motleyard is the workspace around your agent tools. The Motleyard license does not promise bundled model subscriptions or usage credits. Bring the provider access your chosen tools require.',
      },
      {
        question: 'Can I use it without an internet connection?',
        answer:
          'The planned license works for up to 30 days without reconnecting to the license server. Cloud-based agents still need their own internet connection.',
      },
      {
        question: 'How does renting a machine work?',
        answer:
          'Choose a Mac tier and the tools you want set up. The rental configurator lets you explore the proposed offer and save a setup brief. Rates, terms, and availability are still to be decided.',
      },
      {
        question: 'Can I download Motleyard today?',
        answer:
          'The first licensed release is in preparation, and downloads are not currently available. This site previews a proposed Motleyard identity for the existing Eggify app.',
      },
    ],
  },
  licensing: {
    features: [
      {
        title: 'The workspace is paid.',
        description:
          'There is no free tier or trial in the current licensing plan. Pricing and purchase details are still to be announced.',
      },
      {
        title: 'Your iPhone follows your Mac.',
        description:
          'The iPhone app connects to your licensed Mac. It does not need a separate license of its own.',
      },
      {
        title: 'Models are separate.',
        description:
          'The Motleyard license covers the workspace. It does not include a promise of Claude or Codex subscriptions, API credits, or unlimited model usage.',
      },
    ],
    steps: [
      {
        title: 'Activate your computer.',
        description:
          'Enter your serial number to activate an available computer slot.',
      },
      {
        title: 'Move a slot when you need to.',
        description:
          'Deactivate an old install to free one of your three slots for another computer.',
      },
      {
        title: 'Reconnect within 30 days.',
        description:
          'License validation allows up to 30 days offline. Your agent provider’s connectivity requirements still apply.',
      },
    ],
    faqs: [
      {
        question: 'Is this a subscription or a one-time purchase?',
        answer:
          'The merged licensing decision defines activation and access, but does not yet settle pricing or billing cadence. Neither is promised here.',
      },
      {
        question: 'Can I download a free version?',
        answer:
          'Current free downloads have been withdrawn. The first licensed release is being prepared; there is no download or checkout on this concept site.',
      },
      {
        question: 'Is the rental the same as buying a software license?',
        answer:
          'They are separate offers. The rental concept includes a prepared Motleyard workspace, but bundle pricing, activation terms, and what happens when a rental ends still need to be decided.',
      },
    ],
  },
  about: {
    features: [
      {
        title: 'The builder stays in charge.',
        description:
          'AI adds capability. You bring judgment, taste, context, and the reason for doing the work.',
      },
      {
        title: 'The tools can change.',
        description:
          'A workspace should have room for different agents and new approaches. The project matters more than allegiance to a single model.',
      },
      {
        title: 'Starting should feel good.',
        description:
          'Whether you bring your computer or rent a prepared setup, the experience should make you want to get to work.',
      },
    ],
    steps: [],
    faqs: [],
  },
  login: {
    features: [],
    steps: [
      {
        title: 'Your license, in one place.',
        description:
          'Connect a serial number to your customer account. View its status without using the serial as your password.',
      },
      {
        title: 'Keep track of your computers.',
        description:
          'See which installs occupy your three slots and free a slot when you move to a new computer.',
      },
      {
        title: 'Your whole setup.',
        description:
          'Find release information and keep your proposed Mac rental alongside your workspace license.',
      },
    ],
    faqs: [],
  },
  account: {
    features: [],
    steps: [],
    faqs: [],
  },
  configure: {
    features: [],
    steps: [],
    faqs: [],
  },
  machines: {
    features: [
      {
        title: 'A Mac for your work.',
        description:
          'A dedicated machine for your projects. Pick a proposed Pro or Max tier, then describe the work it needs to handle.',
      },
      {
        title: 'A prepared workspace.',
        description:
          'The rental concept pairs the machine with Motleyard. Open your projects and get your sessions organized from the start.',
      },
      {
        title: 'Your chosen tools.',
        description:
          'Select Claude Code, Codex, or both for setup. You connect your own accounts; provider subscriptions and usage remain separate.',
      },
    ],
    steps: [
      {
        title: 'Make it your setup.',
        description:
          'Choose a machine tier and the agent tools you want prepared.',
      },
      {
        title: 'Make it your workspace.',
        description:
          'Connect your own provider accounts and add your projects. Your credentials stay out of this demo.',
      },
      {
        title: 'Make something happen.',
        description:
          'Start your sessions, follow the work, and decide what deserves to ship.',
      },
    ],
    faqs: [
      {
        question: 'Is this a physical Mac or a cloud server?',
        answer:
          'The idea is a physical Mac for your workspace. This concept does not offer hosted Mac access or a cloud virtual machine.',
      },
      {
        question: 'Does more powerful hardware make cloud agents faster?',
        answer:
          'Not automatically. Cloud-model response speed depends on the provider. Choose your computer for your local tools, builds, and workloads rather than assuming it speeds up remote inference.',
      },
      {
        question: 'What are the rental terms?',
        answer:
          'Rental length, deposits, shipping, support, returns, and end-of-rental data handling still need to be defined. The configurator is a product demo and cannot create an order.',
      },
      {
        question: 'Can I purchase instead of renting?',
        answer:
          'This version explores rental. An outright purchase option has not been defined.',
      },
    ],
  },
  workspace: {
    features: [
      {
        title: 'Give each idea a home.',
        description:
          'Keep sessions, working directories, and Git context close to the project they belong to. Switch projects without turning your desktop into an archaeological dig.',
      },
      {
        title: 'See the work unfold.',
        description:
          'Follow tool calls, changes, and usage as sessions run. Know where an agent is spending its time before you decide what comes next.',
      },
      {
        title: 'Stay involved.',
        description:
          'Set tool permissions, review the work, and choose your next move. The point is to give you more capability while keeping you in charge.',
      },
    ],
    steps: [
      {
        title: 'Your Mac is the home base.',
        description:
          'Your project and sessions stay on the Mac running Motleyard.',
      },
      {
        title: 'The relay connects the dots.',
        description:
          'Licensed remote access connects your iPhone to that Mac. Both need the connectivity required for remote access.',
      },
      {
        title: 'No separate iPhone license.',
        description:
          'The iPhone app follows the licensed Mac. It does not consume a separate desktop activation.',
      },
    ],
    faqs: [
      {
        question: 'Do my agents run entirely offline?',
        answer:
          'Not necessarily. Motleyard’s offline license allowance is separate from model access. Cloud-based agents still need an internet connection and the appropriate provider account.',
      },
      {
        question: 'Can I bring my own computer?',
        answer:
          'Yes. The standalone workspace is the software path. The proposed rental is an additional way to get a Mac and tools together.',
      },
      {
        question: 'Are provider subscriptions included?',
        answer:
          'No bundled provider subscriptions or model credits are promised in this concept. Sign in with your own eligible provider accounts.',
      },
    ],
  },
} satisfies Record<string, PageContent>;
