<p align="center">
  <img src="web/public/icon.svg" width="96" alt="Heimdeck-Logo">
</p>

<h1 align="center">Heimdeck</h1>

<p align="center">
  Ein schlichtes Dashboard für deinen Home-Server – so einfach wie Heimdall, aber mit Live-Daten, einer gemeinsamen Suche über alle Dienste und Aktionen direkt auf den Kacheln.
</p>

<p align="center">
  <a href="README.md">English</a> · <b>Deutsch</b>
</p>

---

## Das kann Heimdeck

- **Alles in der Oberfläche** – keine YAML-Dateien. Kacheln hinzufügen, verschieben und bearbeiten per Klick und Drag & Drop.
- **Findet deine Dienste** – liest deine Docker-Container und (wenn verbunden) deine Proxmox-VMs und LXC-Container aus und schlägt Kacheln mit Icon, Adresse und Dienst-Typ vor.
- **Live-Daten** – Download-Warteschlangen, laufende Streams, offene Anfragen, gestörte Indexer, Energiefluss und mehr für rund 40 Dienste.
- **Aktionen** – Jellyseerr-Anfragen genehmigen, SABnzbd oder AdGuard pausieren, Lademodus in evcc wechseln, Geräte in Home Assistant oder ioBroker schalten, UniFi-Geräte neu starten, Speedtest starten …
- **Eine Suche** (Taste `/`) – Kacheln, Medienbibliotheken, Dokumente, Fotos, Spiele, Dateien, Smart-Home-Geräte, DNS-Abfragen und mehr in einem Suchfeld.
- **Verfügbarkeit und Benachrichtigungen** – jeder Dienst wird jede Minute geprüft, mit Verlauf über 24 Stunden oder 3 Tage. Ausfälle werden per Discord, ntfy oder Browser gemeldet.
- **Zuhause oder unterwegs** – jede Kachel kann eine lokale und eine öffentliche Adresse haben; Heimdeck nimmt automatisch die richtige.
- **Eigene API** – jede JSON-Schnittstelle wird zur Live-Kachel mit Werten, Liste und Knöpfen.
- **Mehrere Dashboards**, ausgeblendete oder per Passwort geschützte Gruppen, Hintergrundbild pro Dashboard.
- **Umstieg von Heimdall** – Heimdall-Export importieren; bekannte Dienste werden zu Live-Kacheln.
- **Deutsch und Englisch**, sicher von Haus aus (Passwörter mit bcrypt, API-Keys mit AES-256 verschlüsselt und nie im Browser).
- **Ein kleines Image** für amd64 und arm64 (Raspberry Pi, NAS).

## Schnellstart (Docker Compose)

`docker-compose.yml` anlegen:

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
      # optional: Container automatisch erkennen (nur lesend)
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      - TZ=Europe/Berlin
```

```sh
docker compose up -d
```

Dann `http://<server>:3000` öffnen, Benutzer anlegen und auf 🐳 klicken, um deine Container hinzuzufügen. Fertig.

**Weitere Installationsarten** – ohne Docker auf Debian, Ubuntu und Raspberry Pi OS, als Proxmox-LXC-Container oder als einzelnes Programm: siehe [Installationsanleitung](docs/de/installation.md).

## Anleitungen

| | |
|---|---|
| [Installation](docs/de/installation.md) | Docker Compose, Debian/Ubuntu, Proxmox-LXC, Programmdatei, Aktualisieren, Einstellungen, Reverse Proxy |
| [Benutzung](docs/de/benutzung.md) | Umstieg von Heimdall, zuhause/unterwegs, Benachrichtigungen, Docker-Labels, Icons, Sicherung, Tipps |
| [Unterstützte Dienste](docs/de/dienste.md) | Alle Integrationen, Proxmox verbinden, Docker-Kachel, Kachel „Eigene API“ |
| [Entwicklung](docs/de/entwicklung.md) | Selbst bauen, eigene Integration hinzufügen |

## Unterstützte Dienste (Auszug)

Jellyfin · Plex · Emby · Jellyseerr/Overseerr · Sonarr · Radarr · Lidarr · Readarr · Bazarr · Prowlarr · Immich · Audiobookshelf · Kavita · Komga · Navidrome · RomM · Tdarr · Stash · SABnzbd · NZBGet · qBittorrent · Transmission · Deluge · MeTube · Tubesy · Paperless-ngx · Nextcloud · AdGuard Home · Pi-hole · OPNsense · UniFi · Speedtest Tracker · Anubis · Docker · Proxmox VE · Home Assistant · ioBroker · evcc · beliebige JSON-API

→ [Vollständige Liste mit Live-Daten, Suche und Aktionen](docs/de/dienste.md)

## Lizenz

[MIT](LICENSE)
