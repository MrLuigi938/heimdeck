// Phone apps of known services, to suggest an app link in the tile editor.
// android: the package name (from the Play Store address, …details?id=…);
// ios: a link the app opens itself, where it has one. Everything else on an
// iPhone goes through a Shortcut that opens the app.
export const APPS = {
  unifi: { name: 'UniFi', android: 'com.ubnt.easyunifi' },
  'unifi-protect': { name: 'UniFi Protect', android: 'com.ubnt.unifi.protect' },
  'home-assistant': { name: 'Home Assistant', android: 'io.homeassistant.companion.android', ios: 'homeassistant://' },
  jellyfin: { name: 'Jellyfin', android: 'org.jellyfin.mobile' },
  plex: { name: 'Plex', android: 'com.plexapp.android' },
  emby: { name: 'Emby', android: 'com.mb.android' },
  nextcloud: { name: 'Nextcloud', android: 'com.nextcloud.client' },
  immich: { name: 'Immich', android: 'app.alextran.immich' },
  audiobookshelf: { name: 'Audiobookshelf', android: 'com.audiobookshelf.app' },
  paperless: { name: 'Paperless Mobile', android: 'de.astubenbord.paperless_mobile' },
  vaultwarden: { name: 'Bitwarden', android: 'com.x8bit.bitwarden' },
};

// Further Android apps for the suggestion list of the package field (other
// apps for the same services).
const MORE_ANDROID = [
  { name: 'Findroid (Jellyfin)', android: 'dev.jdtech.jellyfin' },
];

// Every known Android app once, by name, for the suggestion list.
export const ANDROID_APPS = [...new Map([...Object.values(APPS), ...MORE_ANDROID].filter((a) => a.android).map((a) => [a.android, a])).values()]
  .sort((a, b) => a.name.localeCompare(b.name));

const packageRe = /^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z0-9_]+)+$/;
const playStoreRe = /^https:\/\/play\.google\.com\/store\/apps\/details\?(?:.*&)?id=([a-zA-Z][a-zA-Z0-9_.]*)/;

// What the Android field shows: just the package name for a plain app link.
export const packageOf = (link) => /^intent:\/\/#Intent;package=([^;]+);end$/.exec(link || '')?.[1] ?? link ?? '';

// The link for what was typed in the Android field – a package name, its Play
// Store address or a link of its own (the server does the same when saving).
export function androidApp(value) {
  let v = (value || '').trim();
  v = playStoreRe.exec(v)?.[1] ?? v;
  return packageRe.test(v) ? androidLink(v) : v;
}

// Opens an Android app by its package name (the website is added as the
// fallback when the tile is opened).
export const androidLink = (pkg) => `intent://#Intent;package=${pkg.trim()};end`;

// Runs the Shortcut with this name; it holds an "Open app" action.
export const shortcutLink = (name) => `shortcuts://run-shortcut?name=${encodeURIComponent(name.trim())}`;
