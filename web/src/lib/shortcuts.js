// Keyboard shortcuts of the dashboard. The keys are a setting ("shortcuts",
// JSON {action: key}); actions without an entry keep their default.
// "shortcutsOff" = "true" turns them all off.

export const ACTIONS = [
  { id: 'search', label: 'Suche öffnen', key: '/' },
  { id: 'edit', label: 'Bearbeitungsmodus an/aus', key: 'e' },
  { id: 'settings', label: 'Einstellungen öffnen', key: ',' },
  { id: 'hidden', label: 'Ausgeblendete Gruppen zeigen/verbergen', key: 'h' },
  { id: 'network', label: 'Zuhause/unterwegs umschalten', key: 'n' },
  { id: 'prevBoard', label: 'Vorheriges Dashboard', key: '[' },
  { id: 'nextBoard', label: 'Nächstes Dashboard', key: ']' },
  { id: 'help', label: 'Tastenkürzel anzeigen', key: '?' },
];

// Keys that cannot be used: they belong to the browser or to text fields.
const RESERVED = new Set(['Escape', 'Tab', 'Enter', ' ', 'Shift', 'Control', 'Alt', 'Meta', 'CapsLock', 'Backspace', 'Delete']);

export function keymap(settings) {
  let own = {};
  try {
    own = JSON.parse(settings?.shortcuts || '{}') || {};
  } catch {
    own = {};
  }
  return Object.fromEntries(ACTIONS.map((a) => [a.id, typeof own[a.id] === 'string' ? own[a.id] : a.key]));
}

// Digits 1–9 always open the dashboard with that number.
export const isBoardKey = (key) => /^[1-9]$/.test(key);

export function usableKey(key) {
  return key.length === 1 && !RESERVED.has(key) && !isBoardKey(key);
}

// Shortcuts never fire while typing or while a dialog is open.
export function blocked(e) {
  if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return true;
  const el = document.activeElement;
  if (el?.matches?.('input, textarea, select, [contenteditable=""], [contenteditable="true"]')) return true;
  return !!document.querySelector('.backdrop');
}

export const keyLabel = (key) => (key ? key.toUpperCase() : '–');
