// The learning videos shown on /learn (made in ../../learn with Remotion).
//
// To publish an episode:
//   1. learn/scripts/deploy.sh <slug>      uploads the video and its poster to /media/learn/
//   2. below, set `published: true` on that episode and fill in `duration` (seconds),
//      `publishedOn` and, if you like, `chapters` (the scene headings and their start times)
//   3. landing/deploy.sh                   rebuilds and publishes the site
//
// Videos and posters are remote files, not Astro assets, so nothing else changes.
// landing/deploy.sh leaves /media/ alone.

export interface Chapter {
  title: string;
  /** Start of the chapter, in seconds from the start of the video. */
  at: number;
}

export interface Episode {
  number: number;
  /** Also the anchor on the page (/learn#<slug>) and the file name on the server. */
  slug: string;
  title: string;
  summary: string;
  /** Flip to true once the video is uploaded. Unpublished episodes show as "Coming soon". */
  published: boolean;
  /** Length in seconds. */
  duration?: number;
  /** Day the video went up, YYYY-MM-DD (for search engines). */
  publishedOn?: string;
  chapters?: Chapter[];
}

export const LEARN_MEDIA = 'https://eggify.xyz/media/learn';
export const videoUrl = (e: Episode) => `${LEARN_MEDIA}/${e.slug}.mp4`;
export const posterUrl = (e: Episode) => `${LEARN_MEDIA}/${e.slug}-poster.jpg`;

export const episodes: Episode[] = [
  {
    number: 1,
    slug: 'quick-start',
    title: 'Quick start',
    summary: 'From download to your first working agent: create your account, connect Claude Code, Codex or another provider, open a project and start a session.',
    published: true,
    duration: 82,
    publishedOn: '2026-11-15',
    chapters: [
      { title: 'Install', at: 6 },
      { title: 'Create your account', at: 15 },
      { title: 'Connect your agents', at: 23 },
      { title: 'Other providers', at: 40 },
      { title: 'Open a project', at: 47 },
      { title: 'Start a session', at: 56 },
      { title: 'Watch it work', at: 65 },
    ],
  },
  {
    number: 2,
    slug: 'projects-sessions-tags',
    title: 'Projects, sessions and tags',
    summary: 'Add your repositories as projects, run several sessions in each and keep them findable with tags.',
    published: true,
    duration: 99,
    publishedOn: '2026-11-15',
    chapters: [
      { title: 'Projects', at: 5 },
      { title: 'Another session', at: 16 },
      { title: 'The sidebar', at: 36 },
      { title: 'Tags', at: 47 },
      { title: 'Getting around', at: 56 },
      { title: 'Agent overview', at: 75 },
    ],
  },
  {
    number: 3,
    slug: 'watching-and-steering',
    title: 'Watching and steering an agent',
    summary: 'Follow what an agent is doing, answer its questions, approve or refuse its requests, and stop it or change its model mid-task.',
    published: true,
    duration: 85,
    publishedOn: '2026-11-15',
    chapters: [
      { title: 'Watch it work', at: 5 },
      { title: 'Approve a command', at: 14 },
      { title: 'Answer a question', at: 36 },
      { title: 'Stop and steer', at: 53 },
      { title: 'Switch the model', at: 60 },
      { title: 'Read the numbers', at: 70 },
    ],
  },
  {
    number: 4,
    slug: 'subagents',
    title: 'Subagents and All agents',
    summary: 'See the helpers an agent starts, what each one is doing right now, and every agent on the machine in one overview.',
    published: true,
    duration: 94,
    publishedOn: '2026-11-15',
    chapters: [
      { title: 'Subagents', at: 6 },
      { title: 'Zoom in', at: 19 },
      { title: 'Subagents view', at: 29 },
      { title: 'Idle, but busy', at: 46 },
      { title: 'All agents', at: 53 },
      { title: 'Zen', at: 71 },
    ],
  },
  {
    number: 5,
    slug: 'teams-workers-loops',
    title: 'Teams, workers and loops',
    summary: 'Let one model plan, another build and a third review, and keep an agent trying until a check passes.',
    published: true,
    duration: 99,
    publishedOn: '2026-11-15',
    chapters: [
      { title: 'A team', at: 4 },
      { title: 'Its own worktree', at: 18 },
      { title: 'Watching a team', at: 31 },
      { title: 'Handing off a task', at: 50 },
      { title: 'Loops', at: 66 },
      { title: 'A loop on a card', at: 82 },
    ],
  },
  {
    number: 6,
    slug: 'the-cage',
    title: 'Keeping agents in the cage',
    summary: 'The sandbox, workspace trust and the review queue: what an agent may touch, and who decides.',
    published: true,
    duration: 111,
    publishedOn: '2026-11-15',
    chapters: [
      { title: 'The sandbox', at: 6 },
      { title: 'Allowed domains', at: 28 },
      { title: 'Workspace trust', at: 42 },
      { title: 'The review queue', at: 62 },
      { title: 'Permission prompts', at: 86 },
    ],
  },
  {
    number: 7,
    slug: 'git-and-worktrees',
    title: 'Git page and worktrees',
    summary: 'Read every branch, worktree and uncommitted change of a project, and give each agent a checkout of its own.',
    published: true,
    duration: 85,
    publishedOn: '2026-11-15',
    chapters: [
      { title: 'The Git page', at: 7 },
      { title: 'Worktrees', at: 13 },
      { title: 'Uncommitted', at: 29 },
      { title: 'Against main', at: 37 },
      { title: 'Branches', at: 47 },
      { title: 'New session here', at: 57 },
      { title: 'In a session', at: 66 },
    ],
  },
  {
    number: 8,
    slug: 'from-the-sofa',
    title: 'From the sofa',
    summary: 'Turn on remote access, pair your iPhone with a QR code and approve requests from wherever you are.',
    published: true,
    duration: 89,
    publishedOn: '2026-11-15',
    chapters: [
      { title: 'Away from the desk', at: 4 },
      { title: 'Eggify relay', at: 13 },
      { title: 'Or ngrok', at: 30 },
      { title: 'Pair the phone', at: 39 },
      { title: 'On the phone', at: 54 },
      { title: 'Signed-in devices', at: 63 },
    ],
  },
  {
    number: 9,
    slug: 'skills-and-connectors',
    title: 'Skills and connectors',
    summary: 'Teach your agents reusable skills and connect them to the other tools you work with.',
    published: true,
    duration: 88,
    publishedOn: '2026-11-15',
    chapters: [
      { title: 'Skills', at: 7 },
      { title: 'Review first', at: 18 },
      { title: 'Use a skill', at: 25 },
      { title: 'Skills in a session', at: 32 },
      { title: 'Connectors', at: 44 },
      { title: 'Choose per session', at: 58 },
      { title: 'Always asked', at: 67 },
    ],
  },
  {
    number: 10,
    slug: 'zen-and-themes',
    title: 'Zen and themes',
    summary: 'Watch a session as a paper circus with music, and change how the whole app looks.',
    published: true,
    duration: 79,
    publishedOn: '2026-11-15',
    chapters: [
      { title: 'Into Zen', at: 4 },
      { title: 'The stage', at: 13 },
      { title: 'Zen at work', at: 27 },
      { title: 'Backdrops and scenes', at: 39 },
      { title: 'Themes', at: 49 },
      { title: 'Next and previous', at: 62 },
    ],
  },
];

/** "1 min 32 s" */
export function formatDuration(seconds: number): string {
  const s = Math.round(seconds);
  const m = Math.floor(s / 60);
  const rest = s % 60;
  if (m === 0) return `${rest} s`;
  return rest === 0 ? `${m} min` : `${m} min ${rest} s`;
}

/** "0:42" */
export function formatTimestamp(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

/** ISO 8601 duration for structured data: "PT1M32S" */
export function isoDuration(seconds: number): string {
  const s = Math.round(seconds);
  return `PT${Math.floor(s / 60)}M${s % 60}S`;
}
