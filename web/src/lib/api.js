import { i18n, t } from './i18n.svelte.js';

// Small wrapper around fetch for the JSON API. The X-Lang header makes the
// backend answer (widget texts, error messages) in the selected language.
// Debug mode (setting): every API call is written to the browser console.
let debug = false;
export const setDebug = (on) => (debug = on);

export async function api(method, path, body) {
  const opts = { method, headers: { 'X-Lang': i18n.lang }, credentials: 'same-origin' };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  } else if (method !== 'GET') {
    opts.headers['Content-Type'] = 'application/json';
  }
  const start = performance.now();
  let res;
  try {
    res = await fetch('/api' + path, opts);
  } catch (err) {
    if (debug) console.error(`[Heimdeck] ${method} ${path} – keine Verbindung zum Server (${err.message})`);
    throw err;
  }
  if (debug) {
    const line = `[Heimdeck] ${method} ${path} → ${res.status} (${Math.round(performance.now() - start)} ms)`;
    res.ok ? console.debug(line) : console.warn(line);
  }
  if (res.status === 401 && path !== '/login') {
    window.dispatchEvent(new Event('heimdeck:logout'));
  }
  let data = null;
  try {
    data = await res.json();
  } catch {
    /* empty body */
  }
  if (!res.ok) throw new Error(data?.error || t('Fehler {code}', { code: res.status }));
  return data;
}

export const get = (p) => api('GET', p);
export const post = (p, b) => api('POST', p, b ?? {});
export const put = (p, b) => api('PUT', p, b ?? {});
export const del = (p) => api('DELETE', p);

// The service's own favicon, the icon fallback for tiles with an address.
export const faviconUrl = (tile) => (tile?.id && (tile.url || tile.apiUrl) ? `/api/tiles/${tile.id}/favicon` : '');
