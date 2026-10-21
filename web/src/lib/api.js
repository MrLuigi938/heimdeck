import { i18n, t } from './i18n.svelte.js';

// Small wrapper around fetch for the JSON API. The X-Lang header makes the
// backend answer (widget texts, error messages) in the selected language.
export async function api(method, path, body) {
  const opts = { method, headers: { 'X-Lang': i18n.lang }, credentials: 'same-origin' };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  } else if (method !== 'GET') {
    opts.headers['Content-Type'] = 'application/json';
  }
  const res = await fetch('/api' + path, opts);
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
