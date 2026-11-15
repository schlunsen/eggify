/**
 * The Circus prop and costume library: original cut-outs drawn as inline SVG in
 * the paper-collage style (flat colour, ink outline, a little hatching).
 * Costumes (hats; the egg already has his own moustache and monocle) are worn by the egg gentleman and the
 * subagent walkers; props cross the stage, pop out of the hills or keep time.
 */
import type { CrossingProp, Hat } from './circusGags'

const INK = '#2b1d10'
const o = (w = 2.5) => `stroke="${INK}" stroke-width="${w}" stroke-linejoin="round" stroke-linecap="round"`

export interface PropArt { svg: string; w: number; h: number }
const art = (w: number, h: number, body: string): PropArt =>
  ({ w, h, svg: `<svg viewBox="0 0 ${w} ${h}" aria-hidden="true">${body}</svg>` })

// ── Costumes (sit on the egg's head; 100 x 70 boxes unless noted) ──────────────

export const HAT_ART: Record<Hat, PropArt> = {
  tophat: art(100, 80, `<ellipse cx="50" cy="68" rx="46" ry="9" fill="#2a2433" ${o()}/><path d="M20 68 V14 Q50 2 80 14 V68 Z" fill="#2a2433" ${o()}/><path d="M20 54 H80 V64 H20 Z" fill="#b8606e" ${o(2)}/><path d="M28 18 Q34 14 40 16" stroke="#fbf5e6" stroke-width="3" fill="none" opacity="0.6"/>`),
  bowler: art(100, 70, `<ellipse cx="50" cy="58" rx="46" ry="9" fill="#3a3040" ${o()}/><path d="M16 58 Q16 10 50 10 Q84 10 84 58 Z" fill="#3a3040" ${o()}/><path d="M16 50 H84" stroke="#c79a3e" stroke-width="5"/>`),
  crown: art(100, 70, `<path d="M12 60 L8 14 L32 36 L50 6 L68 36 L92 14 L88 60 Z" fill="#c99a45" ${o(3)}/><circle cx="50" cy="18" r="5" fill="#b8606e" ${o(1.8)}/><circle cx="14" cy="18" r="4" fill="#5f8f86" ${o(1.8)}/><circle cx="86" cy="18" r="4" fill="#5f8f86" ${o(1.8)}/><path d="M12 48 H88" stroke="${INK}" stroke-width="2"/>`),
  teacosy: art(100, 80, `<path d="M8 70 Q4 20 50 14 Q96 20 92 70 Z" fill="#b8606e" ${o(3)}/><circle cx="50" cy="10" r="8" fill="#fbf5e6" ${o(2.5)}/><path d="M18 56 H82 M22 42 H78" stroke="#fbf5e6" stroke-width="3" stroke-dasharray="6 5"/>`),
  mitre: art(100, 90, `<path d="M18 80 L34 10 Q50 -4 66 10 L82 80 Z" fill="#fbf5e6" ${o(3)}/><path d="M50 14 V78 M30 44 H70" stroke="#c79a3e" stroke-width="7"/><path d="M18 80 H82" stroke="${INK}" stroke-width="3"/>`),
  chef: art(100, 90, `<path d="M26 82 V54 C6 54 4 20 30 22 C32 4 66 2 70 22 C96 20 94 54 74 54 V82 Z" fill="#fbf5e6" ${o(3)}/><path d="M38 62 V80 M50 62 V80 M62 62 V80" stroke="${INK}" stroke-width="1.6" opacity="0.5"/>`),
  general: art(100, 70, `<path d="M6 54 Q50 6 94 54 L88 62 H12 Z" fill="#2a5d8f" ${o(3)}/><path d="M10 54 H90" stroke="#c99a45" stroke-width="6"/><path d="M40 30 l5 -8 l5 8 l5 -8 l5 8 Z" fill="#c99a45" ${o(1.6)}/><path d="M50 56 L28 66 H72 Z" fill="${INK}" opacity="0.75"/>`),
  bandsman: art(100, 100, `<rect x="30" y="50" width="40" height="30" fill="#9e2f4c" ${o(3)}/><path d="M26 82 H74 L66 90 H34 Z" fill="${INK}"/><rect x="26" y="40" width="48" height="14" fill="#9e2f4c" ${o(3)}/><path d="M40 40 C38 10 50 -2 52 -2 C54 -2 62 10 60 40" fill="#fbf5e6" ${o(2.5)}/><path d="M30 60 H70" stroke="#c99a45" stroke-width="4"/>`),
}


// ── Things that cross the stage ───────────────────────────────────────────────

export const CROSSING_ART: Record<CrossingProp, PropArt> = {
  balloon: art(140, 190, `<path d="M70 4 C16 4 6 70 40 106 L48 118 H92 L100 106 C134 70 124 4 70 4 Z" fill="#b8606e" ${o(3)}/><path d="M70 4 C40 30 40 90 56 118 M70 4 C100 30 100 90 84 118" fill="none" stroke="#fbf5e6" stroke-width="5"/><path d="M52 118 L58 150 M88 118 L82 150" stroke="${INK}" stroke-width="2.5"/><rect x="52" y="148" width="36" height="26" fill="#a5733f" ${o(2.5)}/>`),
  bicycle: art(200, 130, `<circle cx="44" cy="92" r="32" fill="none" ${o(4)}/><circle cx="156" cy="92" r="32" fill="none" ${o(4)}/><path d="M44 92 L84 92 L70 44 H112 L156 92 M84 92 L112 44 M70 44 L64 32 H80 M112 44 L120 24 H136" fill="none" ${o(4)}/><circle cx="84" cy="92" r="7" fill="#c99a45" ${o(2)}/><path d="M44 92 L84 92 M84 92 L96 112" stroke="#b5541c" stroke-width="3"/><ellipse cx="68" cy="30" rx="16" ry="6" fill="#2a2433" ${o(2)}/>`),
  train: art(340, 150, `<rect x="10" y="48" width="170" height="58" fill="#2a5d8f" ${o(3.5)}/><rect x="180" y="20" width="96" height="86" fill="#9e2f4c" ${o(3.5)}/><rect x="196" y="34" width="56" height="34" fill="#e7f4ef" ${o(2.5)}/><rect x="170" y="2" width="24" height="46" fill="#2a2433" ${o(3)}/><path d="M164 2 H200 L192 -8 H172 Z" fill="#2a2433" ${o(2)}/><path d="M10 48 L-6 106 H10" fill="#c79a3e" ${o(2.5)}/><rect x="2" y="106" width="276" height="12" fill="#2a2433" ${o(2.5)}/>${[50, 110, 168, 232].map(x => `<circle cx="${x}" cy="120" r="${x > 200 ? 26 : 20}" fill="#fbf5e6" ${o(3.5)}/><circle cx="${x}" cy="120" r="5" fill="${INK}"/>`).join('')}<path d="M50 120 H232" stroke="${INK}" stroke-width="4"/>`),
  fish: art(260, 130, `<path d="M20 66 C60 8 170 8 214 66 C170 124 60 124 20 66 Z" fill="#5f8f86" ${o(3.5)}/><path d="M214 66 L256 22 L248 66 L256 110 Z" fill="#b8606e" ${o(3.5)}/><path d="M100 14 C110 -6 140 -6 148 14" fill="#c99a45" ${o(3)}/><circle cx="62" cy="58" r="11" fill="#fbf5e6" ${o(2.5)}/><circle cx="60" cy="58" r="4" fill="${INK}"/><path d="M80 80 q60 24 130 -14 M96 40 q50 -16 110 14" fill="none" stroke="${INK}" stroke-width="1.6" opacity="0.5"/><path d="M26 78 q14 8 28 0" fill="none" ${o(2.5)}/>`),
  cat: art(220, 200, `<path d="M30 190 C0 120 30 70 70 62 L56 14 L92 40 Q110 34 128 40 L164 14 L150 62 C190 70 218 120 190 190 Z" fill="#c99a45" ${o(4)}/><path d="M60 100 q14 -12 28 0 M132 100 q14 -12 28 0" fill="none" ${o(3.5)}/><path d="M100 122 L124 122 L112 136 Z" fill="#b8606e" ${o(2.5)}/><path d="M112 136 V146 M92 152 q20 12 40 0 M60 128 L10 120 M60 138 L12 144 M164 128 L214 120 M164 138 L212 144" fill="none" stroke="${INK}" stroke-width="2.5"/><path d="M70 60 q-6 40 6 70 M150 60 q6 40 -6 70" stroke="${INK}" stroke-width="2" opacity="0.4" fill="none"/>`),
  shootingstar: art(220, 80, `<path d="M0 40 L170 28 L170 52 Z" fill="#fbf5e6" opacity="0.8"/><path d="M200 40 L186 30 L178 14 L170 30 L154 34 L168 44 L164 62 L180 52 L196 60 Z" fill="#f6d35a" ${o(3)}/>`),
  gramophone: art(150, 150, `<path d="M26 112 H124 L112 142 H38 Z" fill="#6b4226" ${o(3)}/><ellipse cx="75" cy="112" rx="50" ry="8" fill="#2a2433" ${o(2.5)}/><path d="M75 108 V56 C75 40 100 34 120 8 C140 36 134 78 96 72" fill="none" ${o(3)}/><path d="M92 8 C128 -6 150 36 128 62 C124 40 112 22 92 8 Z" fill="#c99a45" ${o(3)}/><path d="M60 100 L100 80" stroke="${INK}" stroke-width="4"/>`),
  rabbit: art(110, 170, `<path d="M30 164 C6 120 20 78 54 76 C40 60 40 14 52 6 C64 12 62 50 62 70 C72 50 86 20 98 26 C104 40 80 62 74 78 C100 92 104 134 84 164 Z" fill="#fbf5e6" ${o(3.5)}/><circle cx="44" cy="100" r="4" fill="${INK}"/><circle cx="64" cy="98" r="4" fill="${INK}"/><path d="M52 112 l6 6 l6 -6" fill="none" ${o(2.5)}/><path d="M30 118 L8 112 M30 126 L10 134 M78 118 L102 112" stroke="${INK}" stroke-width="2" fill="none"/>`),
  bird: art(150, 110, `<path d="M14 70 C20 20 80 6 112 34 C130 20 142 30 146 44 C132 44 128 54 128 64 C120 100 60 108 14 70 Z" fill="#5f8f86" ${o(3.5)}/><path d="M146 44 L150 38 L134 40" fill="#c99a45" ${o(2.5)}/><circle cx="118" cy="36" r="4" fill="${INK}"/><path d="M20 70 L0 96 L26 82 Z" fill="#b8606e" ${o(2.5)}/><path d="M50 46 C70 36 94 44 100 60 C80 66 58 62 50 46 Z" fill="#c79a3e" ${o(2.5)}/>`),
}

// ── Pieces for the dedicated gags ─────────────────────────────────────────────

export const TEAPOT_ART = art(140, 110, `<path d="M26 40 C20 100 40 104 70 104 C100 104 118 100 112 40 Z" fill="#fbf5e6" ${o(3.5)}/><path d="M26 40 H112 C108 22 40 22 26 40 Z" fill="#b8606e" ${o(3)}/><circle cx="69" cy="20" r="6" fill="#b8606e" ${o(2.5)}/><path d="M112 54 Q140 46 132 74 Q128 84 112 84" fill="none" ${o(4)}/><path d="M28 56 L6 40 L4 52 L26 66" fill="#fbf5e6" ${o(3)}/><path d="M44 62 q24 16 50 0" fill="none" stroke="#5f8f86" stroke-width="3.5"/>`)
export const TEACUP_ART = art(90, 70, `<path d="M10 14 H72 C72 52 60 62 41 62 C22 62 10 52 10 14 Z" fill="#fbf5e6" ${o(3)}/><path d="M72 20 Q92 18 86 38 Q80 48 68 46" fill="none" ${o(3.5)}/><ellipse cx="41" cy="66" rx="34" ry="5" fill="#b8606e" ${o(2.5)}/><path d="M22 28 H60" stroke="#5f8f86" stroke-width="3.5"/>`)
/** A bureaucrat: head on a long stalk (the stalk is stretched by the gag) */
export const STALK_HEAD_ART = art(120, 150, `<path d="M52 150 V96 M68 150 V96" stroke="${INK}" stroke-width="3" fill="#e8c9a0"/><rect x="52" y="90" width="16" height="60" fill="#e8c9a0"/><path d="M52 90 V150 M68 90 V150" stroke="${INK}" stroke-width="3"/><ellipse cx="60" cy="58" rx="40" ry="38" fill="#f0d5ae" ${o(3.5)}/><path d="M20 38 H100" stroke="${INK}" stroke-width="3"/><path d="M28 38 Q28 4 60 4 Q92 4 92 38 Z" fill="#2a2433" ${o(3)}/><circle cx="44" cy="58" r="5" fill="${INK}"/><circle cx="76" cy="58" r="5" fill="${INK}"/><path d="M38 76 C44 68 52 72 60 76 C68 72 76 68 82 76 C76 84 68 80 60 82 C52 80 44 84 38 76 Z" fill="${INK}"/><path d="M50 94 H70" stroke="${INK}" stroke-width="3"/>`)
export const COCONUT_ART = art(70, 44, `<path d="M4 40 C4 4 66 4 66 40 Z" fill="#7a5230" ${o(3)}/><path d="M14 30 q6 -10 12 0 M32 24 q6 -10 12 0 M46 32 q6 -8 12 0" fill="none" stroke="#3b2814" stroke-width="2"/><circle cx="24" cy="34" r="3" fill="${INK}"/><circle cx="35" cy="34" r="3" fill="${INK}"/>`)
export const SIGNPOST_ART = art(120, 170, `<rect x="54" y="20" width="12" height="150" fill="#a5733f" ${o(3)}/><path d="M10 28 H88 L104 44 L88 60 H10 Z" fill="#fbf5e6" ${o(3)}/><path d="M110 76 H32 L16 92 L32 108 H110 Z" fill="#c99a45" ${o(3)}/><path d="M26 44 H72 M40 92 H92" stroke="${INK}" stroke-width="3" stroke-dasharray="8 6"/>`)
export const LANTERN_ART = art(70, 120, `<path d="M35 0 V18" stroke="${INK}" stroke-width="3"/><path d="M14 22 H56 L62 42 V82 L56 98 H14 L8 82 V42 Z" fill="#f6d35a" ${o(3)}/><path d="M22 22 V98 M48 22 V98 M8 60 H62" stroke="${INK}" stroke-width="2"/><path d="M10 22 H60 L35 6 Z" fill="#9e2f4c" ${o(3)}/>`)
export const POINTING_HAND_ART = art(150, 70, `<path d="M0 22 H70 L90 16 C110 14 128 20 148 30 C128 40 110 46 90 46 L70 44 H0 Z" fill="#fbf5e6" ${o(3)}/><path d="M0 14 H40 V52 H0 Z" fill="#9e2f4c" ${o(3)}/><path d="M76 24 C96 24 120 24 142 30" fill="none" stroke="${INK}" stroke-width="2" opacity="0.5"/>`)
export const MOONFACE_ART = art(120, 120, `<path d="M72 6 A54 54 0 1 0 72 114 A40 40 0 1 1 72 6 Z" fill="#fbf0c8" ${o(3.5)}/><circle cx="44" cy="52" r="4" fill="${INK}"/><path d="M36 76 q10 10 22 2" fill="none" ${o(2.5)}/><path d="M34 40 q10 -8 18 0" fill="none" stroke="${INK}" stroke-width="2.5"/>`)

/** Everything with a name, so tests and docs can count the library. */
export const PROP_LIBRARY: Record<string, PropArt> = {
  ...Object.fromEntries(Object.entries(HAT_ART).map(([k, v]) => [`hat:${k}`, v])),
  ...Object.fromEntries(Object.entries(CROSSING_ART).map(([k, v]) => [`prop:${k}`, v])),
  teapot: TEAPOT_ART,
  teacup: TEACUP_ART,
  stalkhead: STALK_HEAD_ART,
  coconut: COCONUT_ART,
  signpost: SIGNPOST_ART,
  lantern: LANTERN_ART,
  hand: POINTING_HAND_ART,
  moonface: MOONFACE_ART,
}
