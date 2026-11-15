/**
 * Plain text pools for the Circus "cut-out letters" gag. Extend freely: every
 * line is run through `sanitizeTileText` before it reaches the tiles, and the
 * tests keep each one at 28 characters or fewer.
 *
 * Voice: a Victorian paper-collage homage (an egg gentleman, tea, deadlines,
 * builds). All wording is original.
 */

export type JokePool = 'general' | 'oops' | 'done' | 'idle' | 'build'

export const JOKES: Record<JokePool, readonly string[]> = {
  general: [
    'Mind the null, old bean',
    'Egg-cellent commit!',
    'Tea is a build step',
    'Mostly harmless code',
    'Pip pip, refactor!',
    'Quite the paper trail',
    'Jolly good diff, what?',
    'One must always rebase',
    'Stiff upper lip, merge',
    'A most curious stack',
    'Do mind the gap, chap',
    'Rather fond of semicolons',
    'Branch early, tea often',
    'Spiffing variable name!',
    'Cut, paste, and carry on',
    'Terribly clever, that',
    'Never trust a lone TODO',
    'Hatch a plan, old thing',
    'Sunny side up, always',
    'Whisk first, ask later',
    'Shell games are afoot',
    'Fetch the good biscuits',
    'Scrambled, but sound',
    'A tidy commit, madam',
    'Hold the hot fix!',
    'Cogs, springs, and YAML',
    'Dash it all, a comment!',
    'Behold, an endless loop',
    'Bravo, humble function',
    'Pardon me, wrong branch',
    'Hard-boiled logic, this',
    'The plot thickens, yolkly',
    'Lint, sir, is watching',
    'Frightfully good indent',
    'One lump or two, cache?',
    'May I suggest a nap?',
    'Gold star for naming',
  ],
  oops: [
    'Sorry about the bug',
    'Oh crumbs, a crash',
    'Egg on my face, again',
    'That was not the plan',
    'Cracked, but not broken',
    'Dash it, a stack trace',
    'Frightfully sorry, old thing',
    'A slight mishap, ahem',
    'Do not look at the log',
    'Blame the intern, quietly',
  ],
  done: [
    'Splendid, all done!',
    'Tea time, I say!',
    'Jolly well done!',
    'Egg-cellent work!',
    'Hip hip, hatched!',
    'Another one for the shelf',
    'Time for a biscuit',
    'Accomplished, old bean',
    'That will do nicely',
    'Cheers, all around!',
  ],
  idle: [
    'Tea break?',
    'Anyone for a biscuit?',
    'Quiet as a sleeping egg',
    'Still here, still egg',
    'Humming to myself',
    'Counting the clouds',
    'Shall we begin, then?',
    'Waiting, most politely',
  ],
  build: [
    'Splendid, a green build!',
    'Tests pass, mostly',
    'Compiling, with tea',
    'Behold, the steam engine',
    'Shovel more coal in',
    'Do wait for the whistle',
    'Linking, most gingerly',
    'A watched build never boils',
    'Fingers crossed, chaps',
    'Red today, green tomorrow',
  ],
}

/** Local summary phrases keyed by words in the last user message. First match wins. */
export const TOPIC_PHRASES: readonly { words: readonly string[]; phrase: string }[] = [
  { words: ['test', 'spec', 'vitest', 'pytest'], phrase: 'Fixing the tests' },
  { words: ['bug', 'error', 'crash', 'broken', 'fix'], phrase: 'Squashing a bug' },
  { words: ['refactor', 'cleanup', 'clean up', 'simplify'], phrase: 'Tidying the code' },
  { words: ['doc', 'readme', 'changelog'], phrase: 'Writing it all down' },
  { words: ['deploy', 'release', 'ship'], phrase: 'Preparing to ship' },
  { words: ['commit', 'merge', 'rebase', 'branch'], phrase: 'Minding the branches' },
  { words: ['style', 'css', 'theme', 'design', 'layout'], phrase: 'Making it pretty' },
  { words: ['api', 'endpoint', 'server', 'backend'], phrase: 'Tending the server' },
  { words: ['database', 'sql', 'migration', 'schema'], phrase: 'Filing the data' },
  { words: ['build', 'compile'], phrase: 'Stoking the build' },
  { words: ['review', 'audit'], phrase: 'Reviewing the work' },
  { words: ['add', 'feature', 'implement', 'create'], phrase: 'Building something new' },
]

/** Summary phrases by what the agent last did, when nothing else is known. */
export const ACTIVITY_PHRASES = {
  read: 'Reading the code',
  edit: 'Editing the code',
  bash: 'Running a command',
  search: 'Hunting for clues',
  web: 'Consulting the web',
  agent: 'Calling in helpers',
  error: 'Mopping up a mess',
  message: 'Pondering, quietly',
  other: 'Pondering, quietly',
  idle: 'Tea break?',
} as const
