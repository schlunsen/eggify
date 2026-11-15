// The 60-second promo film (rendered from promo/ with Remotion). The video lives
// on the server under /media/ (uploaded by promo/deploy.sh) so the repo doesn't
// carry large binaries; landing/deploy.sh leaves /media/ alone. The poster is
// a compressed copy of /media/eggify-promo-poster.jpg in src/assets.
export const FILM_SRC = '/media/eggify-promo.mp4';
