import { loadFont as loadFraunces } from '@remotion/google-fonts/Fraunces';
import { loadFont as loadGaramond } from '@remotion/google-fonts/EBGaramond';
import { loadFont as loadCourier } from '@remotion/google-fonts/CourierPrime';

export const display = loadFraunces('normal', { weights: ['700', '900'], subsets: ['latin'] }).fontFamily;
export const displayItalic = loadFraunces('italic', { weights: ['700', '900'], subsets: ['latin'] }).fontFamily;
export const body = loadGaramond('normal', { weights: ['400', '600'], subsets: ['latin'] }).fontFamily;
export const mono = loadCourier('normal', { weights: ['400', '700'], subsets: ['latin'] }).fontFamily;

export const c = {
  paper: '#efe4c8',
  paperLight: '#f6eedb',
  card: '#fbf5e6',
  ink: '#2b1d10',
  inkSoft: '#5c4630',
  pink: '#e8458b',
  pinkInk: '#c2185b',
  mustard: '#d4a017',
  teal: '#2a9d8f',
  sky: '#a8d8cc',
  umber: '#1c1610',
  edge: '#d9c7a0',
};

/** The pink / mustard / teal paper-strip shadow used on the wordmark. */
export const stripShadow = (em = 0.035) =>
  `${em}em ${em}em 0 ${c.pink}, ${em * 2}em ${em * 2}em 0 ${c.mustard}, ${em * 3}em ${em * 3}em 0 ${c.teal}`;
