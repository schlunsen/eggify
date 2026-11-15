import type { ImageMetadata } from 'astro';
import sessions from '../assets/sessions.webp';
import icon from '../assets/icon.png';
import cherub from '../assets/cherub.webp';
import handover from '../assets/handover.webp';
import foot from '../assets/foot.webp';
import egg from '../assets/egg.webp';
import providers from '../assets/providers.webp';
import sandbox from '../assets/sandbox.webp';
import metrics from '../assets/metrics.webp';

export interface Sketch {
  /** What the feature is, in plain words. */
  label: string;
  title: string;
  body: string;
  image: ImageMetadata;
  alt: string;
}

/** The feature cards, in programme order. Keep the claims true to the app. */
export const sketches: Sketch[] = [
  {
    label: 'The Mac app',
    title: 'The Self-Winding Machine',
    body: 'A proper macOS app with the server inside. Signed and notarized, updates itself in one click, and keeps a tray icon in your menu bar for remote access and updates.',
    image: icon,
    alt: 'The Eggify app icon: an egg gentleman about to be stepped on by a giant foot',
  },
  {
    label: 'Parallel sessions & projects',
    title: 'The Ministry of Many Sessions',
    body: 'Run several agents side by side, each with its own folder, branch, terminal and permissions. Open a folder, clone a repository or start an empty project, then start a session there.',
    image: sessions,
    alt: 'Five identical clerks in bowler hats typing on typewriters',
  },
  {
    label: 'First-run agent setup',
    title: 'The Cherub of Good Beginnings',
    body: 'On first start Eggify checks that Claude Code and Codex are installed and logged in, and walks you through the login or an API key if they are not.',
    image: cherub,
    alt: 'A cherub blowing a trumpet',
  },
  {
    label: 'Remote access & iPhone pairing',
    title: 'The Long Arm',
    body: 'Switch on remote access over ngrok from Settings and get a public address with a QR code. Point your iPhone camera at the pairing code and the iOS app signs straight in.',
    image: handover,
    alt: 'One hand passing an egg to another',
  },
  {
    label: '⌘K palette & shortcuts',
    title: 'The Foot of Authority',
    body: 'One ⌘K palette for sessions, projects, actions and settings, one set of native shortcuts and Mac menus. And ⌘. stops a runaway agent, firmly but politely.',
    image: foot,
    alt: 'A giant foot descending from above',
  },
  {
    label: 'Zen mode & Puppet Theatre',
    title: 'The Circus Stage',
    body: 'Zen turns a session into a paper puppet show: tool calls become gags, subagents walk on as cut-out characters, errors bring down the foot. The Puppet Theatre theme dresses the whole app to match.',
    image: egg,
    alt: 'An egg gentleman in a bowler hat with a monocle and cane',
  },
  {
    label: 'Any model, any provider',
    title: 'The Switchboard',
    body: 'Claude, OpenAI Codex, DeepSeek, GLM, Kimi or any Anthropic-compatible endpoint. Move a live session to another model and the conversation comes along.',
    image: providers,
    alt: 'An Edwardian switchboard operator plugging in cables',
  },
  {
    label: 'Sandbox & permissions',
    title: 'The Gilded Cage',
    body: 'Sandboxed sessions with an egress allowlist, tool-by-tool approvals, protected secrets and workspace trust. And YOLO mode, for when you simply want it done.',
    image: sandbox,
    alt: 'An egg in a bowler hat sitting inside a gilded birdcage',
  },
  {
    label: 'Live costs & metrics',
    title: 'The Gauges of Inevitable Truth',
    body: 'Live tokens, costs, tool calls, git status and running processes, streamed the moment they happen. Every cent accounted for.',
    image: metrics,
    alt: 'A steam-powered contraption covered in gauges, printing ticker tape',
  },
];

export const tickerLines = [
  'Nobody expects the autonomous agent',
  'Now showing: one egg, several agents',
  'Press ⌘. to stop the agent (the foot is separate)',
  "It's just a flesh wound (exit code 1)",
  'Signed, notarized and slightly silly',
  'This dashboard is no more. It has been refreshed',
];
