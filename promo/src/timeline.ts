// All timings in seconds; scenes and voice-over lines are laid out from here.
export const FPS = 30;
export const TOTAL_SECONDS = 64;
export const TOTAL_FRAMES = TOTAL_SECONDS * FPS;
export const sec = (s: number) => Math.round(s * FPS);

export interface Scene { id: string; start: number; end: number }
export const scenes: Scene[] = [
  { id: 'open', start: 0, end: 5.2 },
  { id: 'intro', start: 5.2, end: 12.2 },
  { id: 'sessions', start: 12.2, end: 19.8 },
  { id: 'watch', start: 19.8, end: 27.6 },
  { id: 'models', start: 27.6, end: 34.8 },
  { id: 'zen', start: 34.8, end: 44.0 },
  { id: 'cage', start: 44.0, end: 50.8 },
  { id: 'binary', start: 50.8, end: 58.8 },
  { id: 'end', start: 58.8, end: 64 },
];
export const scene = (id: string) => scenes.find((s) => s.id === id)!;

/** Voice-over lines: file, start time, duration and the caption text. */
export const vo = [
  { file: 'audio/vo-1.wav', at: 0.9, dur: 2.65, text: 'And now… for something completely different.' },
  { file: 'audio/vo-2.wav', at: 5.9, dur: 5.1, text: 'This is Eggify. A workstation for AI coding agents. Your agents, your code, your machine.' },
  { file: 'audio/vo-3.wav', at: 12.8, dur: 5.58, text: 'Run Claude Code, Codex and friends side by side, each with its own repository, branch and terminal.' },
  { file: 'audio/vo-4.wav', at: 20.4, dur: 5.7, text: 'Watch them read, edit and test your code. Every token, every tool call, every penny, in plain sight.' },
  { file: 'audio/vo-5.wav', at: 28.3, dur: 5.09, text: 'Change models in the middle of a conversation. Claude, Codex, DeepSeek, Kimi. Nobody minds.' },
  { file: 'audio/vo-9.wav', at: 35.5, dur: 3.76, text: 'Prefer a show? Zen mode turns every session into a puppet theatre.' },
  { file: 'audio/vo-10.wav', at: 39.9, dur: 3.3, text: 'And the iPhone app keeps you in charge, even from the sofa.' },
  { file: 'audio/vo-6.wav', at: 44.8, dur: 4.25, text: 'And should an agent get ideas above its station, it lives in a very nice cage.' },
  { file: 'audio/vo-7.wav', at: 51.8, dur: 3.19, text: 'One Go binary. No dependencies. Considerable dignity.' },
  { file: 'audio/vo-8.wav', at: 60.8, dur: 2.81, text: 'Eggify. Nobody expects the autonomous agent.' },
];
