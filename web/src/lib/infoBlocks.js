// Info blocks on a freely arranged page: clock, weather, status … They sit in
// the page grid next to the title and the groups, keep the size they are
// given (their content scales with it and never reaches past it) and show
// more or less depending on that size.
// Stored with the layout: L.blocks = { <id>: { type, tile, opt, style } }.

export const BLOCK_TYPES = {
  clock: { icon: '🕒', name: 'Uhr', text: 'Große Uhr – digital, analog oder als Wortuhr, mit Datum und zweiter Zeitzone', size: { w: 4, h: 4 }, min: { w: 1, h: 1 },
    opt: { face: 'digital', font: 'thin', seconds: false, date: true, greet: false, tz: '', tzName: '' } },
  weather: { icon: '🌤️', name: 'Wetter', text: 'Live-Wetter mit Stundenverlauf, Vorschau und Stimmung – aus einer Wetter-Kachel', size: { w: 5, h: 6 }, min: { w: 2, h: 1 }, source: 'weather',
    opt: { mood: true, hours: 24, days: true } },
  status: { icon: '🟢', name: 'Status', text: 'Alles in Ordnung – oder welche Dienste gerade gestört sind', size: { w: 3, h: 2 }, min: { w: 1, h: 1 }, opt: {} },
  agenda: { icon: '📅', name: 'Termine', text: 'Die nächsten Termine aus einer Kalender-Kachel', size: { w: 4, h: 5 }, min: { w: 2, h: 2 }, source: 'calendar', opt: {} },
  text: { icon: '📝', name: 'Text & Notiz', text: 'Überschrift, Notiz oder Spruch – mit einfachem Markdown', size: { w: 3, h: 3 }, min: { w: 1, h: 1 },
    opt: { text: '', look: 'plain' } },
  countdown: { icon: '⏳', name: 'Countdown', text: 'Tage bis zu einem Datum – Urlaub, Geburtstag, Feiertag', size: { w: 3, h: 3 }, min: { w: 1, h: 1 },
    opt: { label: '', date: '', yearly: false, start: '' } },
};

// align '': as the kind of block does it (clock and countdown centred, text and lists left).
export const BLOCK_STYLE = { bg: 'card', scale: 1, align: '', accent: '' };

export function newBlockId(taken) {
  let id;
  do id = 'i' + Math.random().toString(36).slice(2, 9);
  while (taken?.[id]);
  return id;
}

export function blockOf(b) {
  const def = BLOCK_TYPES[b?.type];
  return def ? { ...b, opt: { ...def.opt, ...(b.opt || {}) }, style: { ...BLOCK_STYLE, ...(b.style || {}) } } : null;
}

// The parts of the page that can be hidden (not the settings: the way back).
export const HIDEABLE = ['title', 'boards', 'greet', 'search', 'tools'];

// How a block of this size shows: by its grid cells, like the search.
// mini: one line; small: the main value and one more line; band: wide and
// flat, everything side by side; medium and large show more.
export function shapeOf(it) {
  if (!it) return 'medium';
  if (it.h <= 1 || it.w <= 1 || (it.w <= 2 && it.h <= 2)) return 'mini';
  if (it.w >= 8 && it.h <= 3) return 'band';
  if (it.h <= 2 || it.w <= 3) return 'small';
  if (it.h <= 5 || it.w <= 4) return 'medium';
  return 'large';
}

// The rows a text block needs at a width (in grid columns) so its text stays
// readable: at least 12 px (headings 16 px) times the chosen size. The block
// cannot be made smaller than that; its text shrinks down to this size at most.
export const TEXT_MIN_FONT = 12;
export const HEADING_MIN_FONT = 16;
export function textRows(opt, style, w, colPx) {
  const text = (opt?.text || '').trim();
  if (!text) return 1;
  const scale = Number(style?.scale) || 1;
  const note = opt.look === 'note';
  const pad = 2 * 10 + (note ? 12 : 0);
  const width = Math.max(20, w * colPx + (w - 1) * 12 - pad);
  let height;
  if (opt.look === 'heading') {
    const font = HEADING_MIN_FONT * scale;
    const lines = Math.ceil((text.length * font * 0.55) / width);
    height = lines * font * 1.1;
  } else {
    const font = TEXT_MIN_FONT * scale * (opt.look === 'quote' ? 1.12 : 1);
    const perLine = Math.max(4, Math.floor(width / (font * 0.5)));
    let lines = opt.look === 'quote' ? 1 : 0;
    for (const raw of text.split('\n')) {
      const line = raw.replace(/\*\*|\*|`|\[|\]\([^)]*\)/g, '');
      const list = /^\s*([-*•]|\d+[.)])\s+/.test(line);
      lines += Math.max(1, Math.ceil(line.length / (list ? perLine - 3 : perLine)));
      if (!list && line.trim()) lines += 0.15; // space after a paragraph
    }
    height = lines * font * 1.4;
  }
  return Math.min(40, Math.ceil((height + pad + 12) / 41));
}
