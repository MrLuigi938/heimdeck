# Usage

[← Back to the README](../README.md) · [Deutsch](de/benutzung.md)

- [Moving from Heimdall](#moving-from-heimdall)
- [Dashboards and groups](#dashboards-and-groups)
- [Home or away](#home-or-away)
- [Search](#search)
- [Notifications](#notifications)
- [Docker labels](#docker-labels)
- [Icons](#icons)
- [Backup](#backup)
- [Forgot your password?](#forgot-your-password)
- [Tips](#tips)

## Moving from Heimdall

1. In Heimdall: Settings → **Export** – this downloads `HeimdallExport.json`.
2. In Heimdeck: click **Import from Heimdall** on the start page (or Settings → Backup → Import) and choose the file.

Tags become groups; tiles without a tag go to the group “Heimdall”. Services Heimdeck knows (Sonarr, Radarr, Jellyfin, Nextcloud, AdGuard, Proxmox …) are imported as live tiles – including API key, user and password from Heimdall, with the “Override URL” as the internal address.

## Dashboards and groups

- **Multiple dashboards**, e.g. “Home” and “Work”: switch next to the title at the top. Each dashboard can have its own background image. Every browser remembers the last one used; `?board=<id>` links to a specific one.
- **Groups** sort your tiles. The tile size (automatic, uniform or compact as an icon square) can be set for all groups or per group.
- **Hidden groups** are only shown after clicking 👁 (for the current session). **Protected groups** need their own password.
- **Link-only tiles**: every tile can be a plain link and still shows whether the service is reachable. The status check can be turned off per tile.

## Home or away

Each tile can have a second, public address. If you open the dashboard via a local address, Heimdeck uses the local links, otherwise the public ones. You can switch manually with one click at the top right.

“At home” means the dashboard was opened via a private IP (`192.168.…`, `10.…`, `172.16–31.…`, `100.64–127.…` e.g. Tailscale), `localhost`, `*.local`, `*.lan` or a host name without a dot. You can add more host names in the settings.

**Internal address:** if Heimdeck reaches a service differently than your browser (e.g. `http://radarr:7878` in the same Docker network), enter it under “More options” on the tile.

## Search

![Search with results from Jellyfin and Jellyseerr](images/search.webp)
<sub>Demo data</sub>

Press `/` anywhere (or click the search box). One search covers your tiles and every connected service that supports search – media libraries, requests, documents, photos and people, games, audiobooks, comics, files, smart-home devices, network clients, DNS queries and more, with covers and thumbnails. Paste a video URL to send it straight to Tubesy.

Which services can be searched is listed in [Supported services](services.md).

## Notifications

Settings → Notifications:

- **Discord**: create a webhook in Discord (Server settings → Integrations → Webhooks) and paste its URL.
- **ntfy**: subscribe to a topic in the ntfy app and enter its address (e.g. `https://ntfy.sh/my-heimdeck` or your own ntfy server), optionally with a token.
- **Browser**: can be enabled per browser. Works as long as a Heimdeck tab is open, and only via HTTPS or `localhost`.

You get a message when a service hasn't answered for the configured time (default 2 minutes) and when it is back. Only tiles with a status check are monitored.

## Docker labels

Labels on your containers override what Heimdeck detects automatically:

```yaml
labels:
  - heimdeck.name=Movies
  - heimdeck.url=https://radarr.example.com
  - heimdeck.icon=radarr
  - heimdeck.group=Media
  - heimdeck.hide=true      # don't suggest this container
```

## Icons

Use a name from [dashboardicons.com](https://dashboardicons.com) (e.g. `jellyfin`), an image URL or an emoji. Images of any aspect ratio are fitted into the icon area.

## Backup

- **Settings → Export** saves your dashboards, groups and tiles as a file – **without** API keys and passwords. Import it again under Settings → Backup.
- For a full backup including keys, back up the data folder (`./data` with Docker, `/var/lib/heimdeck` with the install script). It contains the database and the encryption key – keep it safe.

## Forgot your password?

```sh
# Docker
docker exec heimdeck /heimdeck reset-password <new-password>

# Install script / Proxmox LXC
sudo -u heimdeck HEIMDECK_DATA=/var/lib/heimdeck heimdeck reset-password <new-password>
```

The new password needs at least 8 characters.

## Tips

- **Language**: choose English or German during setup or later in the settings. Live data and error messages are translated too.
- **Behind HTTPS**: set `HEIMDECK_SECURE_COOKIE=true` – see [Installation](installation.md#behind-a-reverse-proxy-https).
- **Uptime overview**: at the bottom of the dashboard you can open the availability of all services for the last 24 hours or 3 days, with outages and response times (can be hidden in the settings).
