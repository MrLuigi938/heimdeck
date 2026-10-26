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
};

// Opens an Android app by its package name (the website is added as the
// fallback when the tile is opened).
export const androidLink = (pkg) => `intent://#Intent;package=${pkg.trim()};end`;

// Runs the Shortcut with this name; it holds an "Open app" action.
export const shortcutLink = (name) => `shortcuts://run-shortcut?name=${encodeURIComponent(name.trim())}`;
