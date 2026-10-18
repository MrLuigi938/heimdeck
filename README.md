# Heimdeck

Ein schlichtes Dashboard für deinen Home-Server: so einfach wie Heimdall, aber mit Live-Daten, einer gemeinsamen Suche und Aktionen direkt auf den Kacheln.

- **Alles in der Oberfläche**: keine YAML-Dateien. Kacheln hinzufügen, verschieben und bearbeiten geht per Klick und Drag & Drop.
- **Container automatisch finden**: Heimdeck liest deine Docker-Container aus und schlägt passende Kacheln mit Icon, Adresse und Dienst-Typ vor.
- **Live-Daten**: Download-Warteschlangen, laufende Streams, offene Anfragen, gestörte Indexer usw.
- **Aktionen**: Jellyseerr-Anfragen genehmigen oder ablehnen, SABnzbd pausieren oder fortsetzen.
- **Zuhause oder unterwegs**: Jede Kachel kann zusätzlich eine öffentliche Adresse haben. Öffnest du das Dashboard über eine lokale Adresse, werden lokale Links verwendet, sonst die öffentlichen. Umschalten geht oben rechts per Klick.
- **Verfügbarkeit**: Heimdeck prüft jede Minute alle Dienste (lokal und öffentlich) und zeigt den Verlauf über 24 Stunden oder 3 Tage mit Ausfällen und Antwortzeiten.
- **Gruppen ausblenden oder schützen**: Eine Gruppe kann ausgeblendet sein (Anzeige per 👁 für die aktuelle Sitzung) oder ein eigenes Passwort haben.
- **Gemeinsame Suche** (Taste `/`): Kacheln, Jellyfin-Bibliothek, Jellyseerr (mit Knopf „Anfragen“) und Paperless-Dokumente in einem Suchfeld.
- **Deutsch und Englisch**: Sprache bei der Einrichtung oder später in den Einstellungen wählen. Auch die Live-Daten und Fehlermeldungen werden übersetzt.
- **Login-Schutz**: Passwörter werden mit bcrypt gespeichert, API-Keys verschlüsselt (AES-256). Keys gelangen nie in den Browser.
- **Ein kleines Image** für amd64 und arm64 (Raspberry Pi, NAS).

## Installation

```yaml
services:
  heimdeck:
    image: ghcr.io/mrluigi938/heimdeck:latest
    restart: unless-stopped
    ports:
      - "3000:3000"
    volumes:
      - ./data:/data
      - /var/run/docker.sock:/var/run/docker.sock:ro   # optional, für die automatische Erkennung
    environment:
      - TZ=Europe/Berlin
```

```sh
docker compose up -d
```

Dann `http://<server>:3000` öffnen, Benutzer anlegen und auf 🐳 klicken. Fertig.

> Selbst bauen: `docker compose build` (in `docker-compose.yml` dafür `build: .` statt `image:` verwenden).

## Unterstützte Dienste

| Dienst | Live-Daten | Suche | Aktionen |
|---|---|---|---|
| Jellyfin | Streams, Bibliotheksgröße | ✓ | |
| Jellyseerr / Overseerr | offene Anfragen, Zähler | ✓ | genehmigen, ablehnen, anfragen |
| Sonarr / Radarr | Warteschlange, Demnächst, Warnungen | | |
| Prowlarr | aktive und gestörte Indexer | | |
| SABnzbd | Tempo, Warteschlange, Restzeit | | pausieren, fortsetzen |
| Paperless-ngx | Dokumente, Posteingang | ✓ | |
| RomM | Plattformen, Spiele, Spielstände | | |
| Stash | Szenen, Bilder, Größe | | |
| MeTube | aktive, fertige, fehlgeschlagene Downloads | | |
| JDownloader, FRITZ!Box, UniFi, Zoraxy, Anubis, qBittorrent, Portainer, … | Online-Status | | |

Jede Kachel kann außerdem ein „Nur Link“ sein und zeigt trotzdem an, ob der Dienst erreichbar ist. Die Statusprüfung lässt sich pro Kachel abschalten.

## Tipps

- **Was gilt als „zuhause“?** Wenn das Dashboard über eine private IP (192.168.…, 10.…), `localhost`, `*.local`, `*.lan` oder einen Namen ohne Punkt geöffnet wird. Weitere Hostnamen kannst du in den Einstellungen eintragen.
- **Interne Adresse**: Erreicht Heimdeck einen Dienst anders als dein Browser (z. B. `http://radarr:7878` im selben Docker-Netzwerk), trage das unter „Weitere Optionen“ ein.
- **Docker-Labels** überschreiben die automatische Erkennung:
  ```yaml
  labels:
    - heimdeck.name=Filme
    - heimdeck.url=https://radarr.example.de
    - heimdeck.icon=radarr
    - heimdeck.group=Medien
    - heimdeck.hide=true      # Container nicht vorschlagen
  ```
- **Icons**: Namen aus [dashboardicons.com](https://dashboardicons.com), eine Bild-URL oder ein Emoji.
- **Passwort vergessen?** `docker exec heimdeck /heimdeck reset-password <neues-passwort>`
- **Hinter HTTPS** (Reverse Proxy): `HEIMDECK_SECURE_COOKIE=true` setzen.
- **Sicherung**: Einstellungen → Exportieren. Ohne API-Keys. Alternativ den Ordner `./data` sichern.

## Entwicklung

```sh
cd web && npm install && npm run build && cd ..
HEIMDECK_DATA=./data go run .            # http://localhost:3000
# Frontend mit Hot-Reload: (cd web && npm run dev)  → http://localhost:5173
go test ./...
```

Eine neue Integration ist eine einzelne Datei in `internal/integrations/`: `Def()` beschreibt Felder und Icon, `Fetch()` liefert ein generisches `Widget` (Zahlen, Liste, Hinweise, Knöpfe). Das Frontend braucht dafür keinen eigenen Code. Optional gibt es `Search()` und `Action()`.
