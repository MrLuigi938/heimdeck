import { get } from './api.js';

// A background is one entry per line: a picture address (uploaded or from
// the web) or a photo album – "album:<tile>:<album id>:<name>". A single
// address, as stored before 1.5, is a list of one. Heimdeck's own
// backgrounds are "builtin:<id>".

// Each is drawn twice – for the dark and for the light look – and the one
// that fits the theme in effect is shown.
export const BUILTIN_BACKGROUNDS = [
  { id: 'bifroest', name: 'Bifröst', text: 'Nordlicht als Bogen über der Seite' },
  { id: 'himinbjoerg', name: 'Himinbjörg', text: 'Sternenhimmel über den Bergen' },
  { id: 'deck', name: 'Deck', text: 'Ein ruhiges Raster aus leuchtenden Kacheln' },
];
const builtinIds = new Set(BUILTIN_BACKGROUNDS.map((b) => b.id));
/** Dark or light, as the theme in effect right now. */
export const backgroundMode = () => (document.documentElement.dataset.theme === 'light' ? 'light' : 'dark');
export const builtinUrl = (id, mode = backgroundMode()) => `/backgrounds/${id}-${mode}.svg`;

export function parseBackgrounds(value) {
  return String(value || '')
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .map((l) => {
      const b = /^builtin:([a-z]+)$/.exec(l);
      if (b && builtinIds.has(b[1])) return { kind: 'builtin', id: b[1], name: BUILTIN_BACKGROUNDS.find((x) => x.id === b[1]).name, url: builtinUrl(b[1]) };
      const m = /^album:(\d+):([^:]+):?(.*)$/.exec(l);
      return m ? { kind: 'album', tile: Number(m[1]), album: m[2], name: m[3] || m[2] } : { kind: 'url', url: l };
    });
}

export const joinBackgrounds = (list) =>
  list.map((e) => (e.kind === 'album' ? `album:${e.tile}:${e.album}:${e.name.replace(/\n/g, ' ')}` : e.kind === 'builtin' ? `builtin:${e.id}` : e.url)).join('\n');

// Album pictures, loaded once per page view (the server keeps them 10 minutes).
const albumCache = new Map();
async function albumImages(e) {
  const key = `${e.tile}:${e.album}`;
  if (!albumCache.has(key)) {
    albumCache.set(key, get(`/tiles/${e.tile}/albums/${encodeURIComponent(e.album)}`).then((r) => r.images || []).catch(() => []));
  }
  return albumCache.get(key);
}

/** All picture addresses of a background, albums expanded. */
export async function backgroundImages(value) {
  const out = [];
  for (const e of parseBackgrounds(value)) {
    if (e.kind === 'album') out.push(...(await albumImages(e)));
    else if (e.kind === 'builtin') out.push(builtinUrl(e.id));
    else out.push(e.url);
  }
  return out;
}

// How often the picture changes, in seconds; 0 = a new one on every page load.
export const ROTATE_CHOICES = [0, 60, 300, 900, 3600, 86400];

/**
 * The picture to show now: with an interval, every device shows the same
 * picture at the same time; without one, a random one per page load.
 */
export function pickBackground(images, every, seed) {
  if (!images.length) return '';
  if (images.length === 1) return images[0];
  if (!every) return images[seed % images.length];
  // A fixed shuffle so albums do not run in upload order.
  const slot = Math.floor(Date.now() / 1000 / every);
  return images[(slot * 7919) % images.length];
}
