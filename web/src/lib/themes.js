// Themes: a palette (colours in themes.css) and for the playful ones a skin
// that also changes shapes and decoration. Neither changes the layout.
// The setting "theme" holds the id; "auto" follows the system (Heimdeck
// dark or light). A dashboard can have its own theme.

export const THEMES = [
  { id: 'auto', name: 'Automatisch (wie System)', group: 'classic', preview: ['#0f1115', '#eef1f6', '#5b8cff', '#e8eaf0'] },
  { id: 'dark', name: 'Heimdeck Dunkel', group: 'classic', mode: 'dark', accent: '#5b8cff', preview: ['#0f1115', '#1c1f26', '#5b8cff', '#e8eaf0'] },
  { id: 'light', name: 'Heimdeck Hell', group: 'classic', mode: 'light', accent: '#5b8cff', preview: ['#eef1f6', '#ffffff', '#5b8cff', '#1b2130'] },
  { id: 'nord', name: 'Nord', group: 'classic', mode: 'dark', accent: '#88c0d0', preview: ['#2e3440', '#3b4252', '#88c0d0', '#eceff4'] },
  { id: 'mocha', name: 'Mokka', group: 'classic', mode: 'dark', accent: '#cba6f7', preview: ['#1e1e2e', '#313244', '#cba6f7', '#cdd6f4'] },
  { id: 'retro', name: 'Retro-Warm', group: 'classic', mode: 'dark', accent: '#fe8019', preview: ['#282828', '#3c3836', '#fe8019', '#ebdbb2'] },
  { id: 'vampire', name: 'Vampir', group: 'classic', mode: 'dark', accent: '#bd93f9', preview: ['#282a36', '#44475a', '#bd93f9', '#f8f8f2'] },
  { id: 'sand', name: 'Sand', group: 'classic', mode: 'light', accent: '#268bd2', preview: ['#eee8d5', '#fdf6e3', '#268bd2', '#073642'] },
  { id: 'terminal', name: 'Terminal', group: 'fun', mode: 'dark', accent: '#3cff6e', skin: 'terminal', preview: ['#050a05', '#0a140a', '#3cff6e', '#9dff9d'] },
  { id: 'neon', name: 'Neon City', group: 'fun', mode: 'dark', accent: '#fcee0a', skin: 'neon', preview: ['#0b0b14', '#12121f', '#fcee0a', '#00f0ff'] },
  { id: 'xp', name: 'Luna 2001', group: 'fun', mode: 'light', accent: '#316ac5', skin: 'xp', preview: ['#3a6ea5', '#ece9d8', '#0a5fe8', '#000000'] },
  { id: 'aero', name: 'Aero-Glas', group: 'fun', mode: 'dark', accent: '#4aa3e8', skin: 'aero', preview: ['#123f66', '#7fb0d8', '#f5f8fb', '#ffffff'] },
  { id: 'hexer', name: 'Hexer', group: 'fun', mode: 'dark', accent: '#c9a45c', skin: 'hexer', preview: ['#14110d', '#24201a', '#c9a45c', '#e8dcc0'] },
  { id: 'mmo', name: 'Gildenhalle', group: 'fun', mode: 'dark', accent: '#a335ee', skin: 'mmo', preview: ['#0d1620', '#14222e', '#c8a24a', '#ffd36b'] },
  { id: 'studio', name: 'Studio', group: 'fun', mode: 'light', accent: '#0a84ff', skin: 'studio', preview: ['#8fa8e8', '#f6f6f8', '#0a84ff', '#1d1d1f'] },
  { id: 'glass', name: 'Flüssigglas', group: 'fun', mode: 'dark', accent: '#64d2ff', skin: 'glass', preview: ['#3b2a7a', '#8f86c9', '#64d2ff', '#ffffff'] },
  // Festive themes: like any other theme, and they also switch themselves on
  // for their days (see SEASONS). They bring decoration and effects along.
  { id: 'newyear', name: 'Silvester', group: 'festive', mode: 'dark', accent: '#f5c84c', skin: 'newyear', preview: ['#0b1026', '#1e2650', '#f5c84c', '#f4f1ff'] },
  { id: 'easter', name: 'Ostern', group: 'festive', mode: 'light', accent: '#9b6fd6', skin: 'easter', preview: ['#f6f3ff', '#ffffff', '#9b6fd6', '#2c2540'] },
  { id: 'summer', name: 'Sommeranfang', group: 'festive', mode: 'light', accent: '#f08a24', skin: 'summer', preview: ['#ffeccf', '#ffffff', '#f08a24', '#3a2a14'] },
  { id: 'halloween', name: 'Halloween', group: 'festive', mode: 'dark', accent: '#ff7a1a', skin: 'halloween', preview: ['#140a1f', '#22112e', '#ff7a1a', '#b48cff'] },
  { id: 'advent', name: 'Advent', group: 'festive', mode: 'dark', accent: '#e8b04b', skin: 'advent', preview: ['#0e1a14', '#1c3226', '#e8b04b', '#f3ecd8'] },
  { id: 'christmas', name: 'Weihnachten', group: 'festive', mode: 'dark', accent: '#e5484d', skin: 'christmas', preview: ['#1a0d10', '#3a1a1e', '#e5484d', '#fbf1e6'] },
];

export const themeById = (id) => THEMES.find((t) => t.id === id) || THEMES[0];

// ---- Seasonal decoration ----
// Each season knows when it is on. Easter and the first Sunday of Advent move
// every year and are calculated.
function easterSunday(year) {
  // Anonymous Gregorian algorithm
  const a = year % 19, b = Math.floor(year / 100), c = year % 100;
  const d = Math.floor(b / 4), e = b % 4, f = Math.floor((b + 8) / 25), g = Math.floor((b - f + 1) / 3);
  const h = (19 * a + b - d - g + 15) % 30, i = Math.floor(c / 4), k = c % 4;
  const l = (32 + 2 * e + 2 * i - h - k) % 7, m = Math.floor((a + 11 * h + 22 * l) / 451);
  const month = Math.floor((h + l - 7 * m + 114) / 31), day = ((h + l - 7 * m + 114) % 31) + 1;
  return new Date(year, month - 1, day);
}
function firstAdvent(year) {
  const christmas = new Date(year, 11, 25);
  const fourth = new Date(year, 11, 25 - (christmas.getDay() || 7));
  return new Date(year, 11, fourth.getDate() - 21); // Date handles the step back into November
}
const day = (d) => new Date(d.getFullYear(), d.getMonth(), d.getDate());
const between = (now, from, to) => day(now) >= day(from) && day(now) <= day(to);

export const SEASONS = [
  { id: 'newyear', name: 'Silvester', when: '28. Dez. – 2. Jan.', on: (n) => (n.getMonth() === 11 && n.getDate() >= 28) || (n.getMonth() === 0 && n.getDate() <= 2) },
  { id: 'easter', name: 'Ostern', when: 'Gründonnerstag – Ostermontag', on: (n) => { const e = easterSunday(n.getFullYear()); return between(n, new Date(e.getFullYear(), e.getMonth(), e.getDate() - 3), new Date(e.getFullYear(), e.getMonth(), e.getDate() + 1)); } },
  { id: 'summer', name: 'Sommeranfang', when: '20. – 23. Juni', on: (n) => n.getMonth() === 5 && n.getDate() >= 20 && n.getDate() <= 23 },
  { id: 'halloween', name: 'Halloween', when: 'ganzer Oktober', on: (n) => n.getMonth() === 9 },
  { id: 'advent', name: 'Advent', when: '1. Advent – 23. Dez.', on: (n) => between(n, firstAdvent(n.getFullYear()), new Date(n.getFullYear(), 11, 23)) },
  { id: 'christmas', name: 'Weihnachten', when: '24. – 27. Dez.', on: (n) => n.getMonth() === 11 && n.getDate() >= 24 && n.getDate() <= 27 },
];

// Which Advent Sunday it is (1–4), for the candles of the wreath.
export function adventCandles(now = new Date()) {
  const first = firstAdvent(now.getFullYear());
  return Math.min(4, Math.max(1, Math.floor((day(now) - day(first)) / (7 * 86400e3)) + 1));
}

// The season in effect – its decoration and effects:
// - a festive theme picked for good (globally or for this dashboard), or else
// - with "festive days automatically" on, the one whose days these are,
//   unless its own "automatically" switch is off.
export function activeSeason(settings, boardTheme = '', now = new Date()) {
  const chosen = themeById(boardTheme || settings?.theme || 'auto');
  if (chosen.group === 'festive') return SEASONS.find((s) => s.id === chosen.id) || null;
  if (settings?.seasonal !== 'true') return null;
  const off = (settings.seasonalOff || '').split(',');
  return SEASONS.find((s) => !off.includes(s.id) && s.on(now)) || null;
}

// The theme in effect: the dashboard's own, else the global one; on festive
// days with "whole theme", the festive theme for those days.
export function activeTheme(settings, boardTheme = '', now = new Date()) {
  const chosen = themeById(boardTheme || settings?.theme || 'auto');
  const season = activeSeason(settings, boardTheme, now);
  if (season && chosen.group !== 'festive' && settings?.seasonalMode === 'theme') return themeById(season.id);
  return chosen;
}
// applyTheme sets everything on <html>: data-theme (dark/light base),
// data-palette and data-skin, the style options and the accent colour.
export function applyTheme(settings, boardTheme = '') {
  const root = document.documentElement;
  const theme = activeTheme(settings, boardTheme);
  const mode = theme.mode || (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
  root.dataset.theme = mode;
  const set = (key, value) => (value ? (root.dataset[key] = value) : delete root.dataset[key]);
  set('palette', theme.mode ? theme.id : '');
  set('skin', theme.skin || '');
  set('corners', settings?.corners || '');
  set('tiles', settings?.tileStyle || '');
  set('font', settings?.font || '');
  set('density', settings?.density || '');
  // The season adds decoration on top of any theme (status symbols, effects).
  set('season', activeSeason(settings, boardTheme)?.id || '');
  // An accent of its own beats the theme's; empty = the theme's colour.
  const accent = settings?.accent || theme.accent || '#5b8cff';
  root.style.setProperty('--accent', accent);
  root.style.setProperty('--on-accent', onColor(accent));
  return theme;
}

// Text colour for a surface in the given colour: white or near-black,
// whichever has the higher contrast (WCAG). Light accents such as neon
// yellow get dark text, dark ones white.
export function onColor(hex) {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex || '');
  if (!m) return '#fff';
  const lin = (c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  const [r, g, b] = [0, 2, 4].map((i) => lin(parseInt(m[1].slice(i, i + 2), 16) / 255));
  const l = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  const dark = 0.0056; // luminance of #111
  return (1.05 / (l + 0.05)) >= ((l + 0.05) / (dark + 0.05)) ? '#fff' : '#111';
}
