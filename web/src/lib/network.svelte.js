// Decides whether the dashboard is opened from the home network or from
// outside, so tiles can open their public address when you're away.
//
// "Local" means the dashboard itself was opened via a private address
// (192.168.x.x, 10.x.x.x, localhost, *.local, *.lan, a host without dots …)
// or via one of the hostnames listed in the settings. It can be overridden
// per browser session with the switch in the header.

const KEY = 'heimdeck-network';

function readOverride() {
  try {
    return sessionStorage.getItem(KEY) || '';
  } catch {
    return '';
  }
}

export const network = $state({ override: readOverride(), localHosts: '' });

export function setOverride(mode) {
  network.override = mode;
  try {
    if (mode) sessionStorage.setItem(KEY, mode);
    else sessionStorage.removeItem(KEY);
  } catch {
    /* private mode */
  }
}

export function isPrivateHost(host) {
  host = host.toLowerCase().replace(/^\[|\]$/g, '');
  if (host === 'localhost' || !host.includes('.') && !host.includes(':')) return true;
  if (/\.(local|lan|home|internal|localdomain|home\.arpa)$/.test(host)) return true;
  const v4 = host.match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)$/);
  if (v4) {
    const [a, b] = [+v4[1], +v4[2]];
    return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 100 && b >= 64 && b <= 127);
  }
  return host === '::1' || /^f[cd]/.test(host) || host.startsWith('fe80');
}

export function detectLocal(localHosts) {
  const host = location.hostname.toLowerCase();
  const extra = (localHosts || '').split(/[\s,]+/).map((h) => h.trim().toLowerCase()).filter(Boolean);
  return extra.includes(host) || isPrivateHost(host);
}

export function isLocal() {
  if (network.override === 'local') return true;
  if (network.override === 'remote') return false;
  return detectLocal(network.localHosts);
}

// The address a tile should open right now.
export function tileHref(tile, local) {
  if (!local && tile.externalUrl) return tile.externalUrl;
  return tile.url || tile.externalUrl || '';
}

// Widget and search links are built from the tile's local address by the
// backend; when away, point them at the public address instead.
export function rewrite(url, tile, local) {
  if (!url || local || !tile?.externalUrl || !tile.url) return url;
  const base = tile.url.replace(/\/+$/, '');
  if (url === base || url.startsWith(base + '/') || url.startsWith(base + '?') || url.startsWith(base + '#')) {
    return tile.externalUrl.replace(/\/+$/, '') + url.slice(base.length);
  }
  return url;
}
