<p align="center">
  <img src="web/public/icon.svg" width="96" alt="Heimdeck logo">
</p>

<h1 align="center">Heimdeck</h1>

<p align="center">
  A simple dashboard for your home server – as easy as Heimdall, but with live data, one search across all your services and actions right on the tiles.
</p>

<p align="center">
  <b>English</b> · <a href="README.de.md">Deutsch</a>
</p>

---

## Highlights

- **Everything in the UI** – no YAML. Add, move and edit tiles by click and drag & drop.
- **Finds your services** – reads your Docker containers and (once connected) your Proxmox VMs and LXC containers and suggests tiles with icon, address and service type.
- **Live data** – download queues, active streams, pending requests, broken indexers, energy flow and more for around 40 services.
- **Actions** – approve Jellyseerr requests, pause SABnzbd or AdGuard, switch the evcc charge mode, toggle Home Assistant or ioBroker devices, restart UniFi devices, start a speed test …
- **One search** (press `/`) – tiles, media libraries, documents, photos, games, files, smart-home devices, DNS queries and more in a single search box.
- **Uptime and alerts** – every service is checked once a minute, with a 24 h / 3 day history. Outages are reported via Discord, ntfy or browser notifications.
- **Home or away** – each tile can have a local and a public address; Heimdeck picks the right one automatically.
- **Custom API tile** – turn any JSON endpoint into a live tile with values, a list and buttons.
- **Multiple dashboards**, hidden or password-protected groups, background images per dashboard.
- **Easy switch from Heimdall** – import the Heimdall export file; known services become live tiles.
- **English and German**, secure by default (bcrypt passwords, AES-256 encrypted API keys that never reach the browser).
- **One small image** for amd64 and arm64 (Raspberry Pi, NAS).

## Quick start (Docker Compose)

Create a `docker-compose.yml`:

```yaml
services:
  heimdeck:
    image: ghcr.io/mrluigi938/heimdeck:latest
    container_name: heimdeck
    restart: unless-stopped
    ports:
      - "3000:3000"
    volumes:
      - ./data:/data
      # optional: let Heimdeck discover your containers (read-only)
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      - TZ=Etc/UTC   # your time zone, e.g. America/New_York
```

```sh
docker compose up -d
```

Open `http://<your-server>:3000`, create your user and click 🐳 to add your containers. Done.

**Other ways to install** – without Docker on Debian, Ubuntu and Raspberry Pi OS, as a Proxmox LXC container, or as a single binary: see the [installation guide](docs/installation.md).

## Documentation

| | |
|---|---|
| [Installation](docs/installation.md) | Docker Compose, Debian/Ubuntu, Proxmox LXC, binary, updating, configuration, reverse proxy |
| [Usage](docs/usage.md) | Moving from Heimdall, home/away, notifications, Docker labels, icons, backup, tips |
| [Supported services](docs/services.md) | All integrations, connecting Proxmox, the Docker tile, the custom API tile |
| [Development](docs/development.md) | Building from source, adding an integration |

## Supported services (excerpt)

Jellyfin · Plex · Emby · Jellyseerr/Overseerr · Sonarr · Radarr · Lidarr · Readarr · Bazarr · Prowlarr · Immich · Audiobookshelf · Kavita · Komga · Navidrome · RomM · Tdarr · Stash · SABnzbd · NZBGet · qBittorrent · Transmission · Deluge · MeTube · Tubesy · Paperless-ngx · Nextcloud · AdGuard Home · Pi-hole · OPNsense · UniFi · Speedtest Tracker · Anubis · Docker · Proxmox VE · Home Assistant · ioBroker · evcc · any JSON API

→ [Full list with live data, search and actions](docs/services.md)

## License

Copyright © 2026 Luis Blum

Heimdeck is free software under the [GNU Affero General Public License v3.0](LICENSE). You may use, change and share it – also commercially – as long as changed versions, including ones offered as a web service, are published under the same license with their source code.
