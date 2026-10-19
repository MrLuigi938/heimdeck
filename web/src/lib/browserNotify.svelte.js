import { t } from './i18n.svelte.js';

// Browser notifications for outages. They work while a Heimdeck tab is open
// (also in the background) and need HTTPS or localhost. Whether they are on is
// remembered per browser.

const KEY = 'heimdeck-browser-notify';

function read() {
  try {
    return localStorage.getItem(KEY) === 'on';
  } catch {
    return false;
  }
}

export const browserNotify = $state({ on: read() && typeof Notification !== 'undefined' && Notification.permission === 'granted' });

export const notifySupported = () => typeof Notification !== 'undefined' && window.isSecureContext;

export async function setBrowserNotify(on) {
  if (on) {
    if (!notifySupported()) return false;
    const perm = Notification.permission === 'granted' ? 'granted' : await Notification.requestPermission();
    if (perm !== 'granted') return false;
  }
  browserNotify.on = on;
  try {
    localStorage.setItem(KEY, on ? 'on' : 'off');
  } catch {
    /* private mode */
  }
  if (on) show(t('Benachrichtigungen sind an'), t('Fällt ein Dienst aus, meldet sich Heimdeck hier.'));
  return true;
}

function show(title, body, tag) {
  try {
    const n = new Notification(title, { body, tag, icon: '/icon.svg' });
    n.onclick = () => {
      window.focus();
      n.close();
    };
  } catch {
    /* some mobile browsers only allow notifications from a service worker */
  }
}

// Alerts known from the previous status poll; null until the first poll so
// outages that already exist when the page opens are not announced again.
let known = null;

export function checkAlerts(status, tiles) {
  const now = new Map();
  for (const [id, st] of Object.entries(status || {})) {
    for (const kind of (st.alert || '').split(',').filter(Boolean)) now.set(id + ':' + kind, kind);
  }
  if (known && browserNotify.on) {
    const name = (id) => tiles.find((x) => String(x.id) === id)?.name || '?';
    const where = (kind) => (kind === 'ext' ? ' (' + t('öffentliche Adresse') + ')' : '');
    for (const [key, kind] of now) {
      if (!known.has(key)) show('🔴 ' + t('{name} ist nicht erreichbar', { name: name(key.split(':')[0]) }) + where(kind), '', key);
    }
    for (const [key, kind] of known) {
      if (!now.has(key)) show('✅ ' + t('{name} ist wieder erreichbar', { name: name(key.split(':')[0]) }) + where(kind), '', key);
    }
  }
  known = now;
}
