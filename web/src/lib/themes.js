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
  { id: 'halloween', name: 'Halloween', group: 'fun', mode: 'dark', accent: '#ff7a1a', skin: 'halloween', preview: ['#140a1f', '#22112e', '#ff7a1a', '#b48cff'] },
];

export const themeById = (id) => THEMES.find((t) => t.id === id) || THEMES[0];

// The theme in effect: the dashboard's own, else the global one; in October
// the seasonal option swaps in Halloween.
export function activeTheme(settings, boardTheme = '', now = new Date()) {
  let id = boardTheme || settings?.theme || 'auto';
  if (settings?.seasonal === 'true' && now.getMonth() === 9) id = 'halloween';
  return themeById(id);
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
  // An accent of its own beats the theme's; empty = the theme's colour.
  root.style.setProperty('--accent', settings?.accent || theme.accent || '#5b8cff');
  return theme;
}
