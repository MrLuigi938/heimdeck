# Heimdeck

Ein schlichtes Dashboard für deinen Home-Server: so einfach wie Heimdall, aber mit Live-Daten, einer gemeinsamen Suche und Aktionen direkt auf den Kacheln.

- **Alles in der Oberfläche**: keine YAML-Dateien. Kacheln hinzufügen, verschieben und bearbeiten geht per Klick und Drag & Drop.
- **Dienste automatisch finden**: Heimdeck liest deine Docker-Container und – mit Proxmox verbunden – deine VMs und LXC-Container aus und schlägt passende Kacheln mit Icon, Adresse und Dienst-Typ vor.
- **Umstieg von Heimdall**: Den Heimdall-Export (Einstellungen → Exportieren) einfach importieren. Tags werden zu Gruppen, bekannte Dienste samt API-Key zu Live-Kacheln.
- **Live-Daten**: Download-Warteschlangen, laufende Streams, offene Anfragen, gestörte Indexer usw.
- **Aktionen**: Jellyseerr-Anfragen genehmigen oder ablehnen, SABnzbd pausieren, AdGuard-Schutz pausieren, Lademodus in evcc wechseln, Lichter und Schalter in Home Assistant oder ioBroker umschalten, UniFi-Geräte neu starten, Speedtest starten …
- **Zuhause oder unterwegs**: Jede Kachel kann zusätzlich eine öffentliche Adresse haben. Öffnest du das Dashboard über eine lokale Adresse, werden lokale Links verwendet, sonst die öffentlichen. Umschalten geht oben rechts per Klick.
- **Verfügbarkeit**: Heimdeck prüft jede Minute alle Dienste (lokal und öffentlich) und zeigt den Verlauf über 24 Stunden oder 3 Tage mit Ausfällen und Antwortzeiten.
- **Benachrichtigungen**: Fällt ein Dienst aus (und wenn er wieder da ist), kommt eine Nachricht per Discord, ntfy oder als Browser-Benachrichtigung.
- **Eigene API**: Jede JSON-Schnittstelle wird zur Live-Kachel – mit eigenen Werten, Liste, Knöpfen und Suche.
- **Mehrere Dashboards**: z. B. „Privat“ und „Arbeit“ – oben neben dem Titel umschalten, jedes auf Wunsch mit eigenem Hintergrundbild. Jeder Browser merkt sich das zuletzt benutzte, per `?board=<id>` lässt sich eines direkt verlinken.
- **Gruppen ausblenden oder schützen**: Eine Gruppe kann ausgeblendet sein (Anzeige per 👁 für die aktuelle Sitzung) oder ein eigenes Passwort haben.
- **Gemeinsame Suche** (Taste `/`): Kacheln, Jellyfin-Bibliothek, Jellyseerr (mit Knopf „Anfragen“), Paperless-Dokumente, RomM-Spiele, Tubesy-Videos, Immich-Fotos und -Personen, Hörbücher, Comics, Nextcloud-Dateien, Home-Assistant-Geräte, UniFi-Clients, DNS-Abfragen in AdGuard und mehr in einem Suchfeld – Cover und Vorschaubilder inklusive. Eine eingefügte Video-URL lässt sich direkt an Tubesy schicken.
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

### Ohne Docker: Debian, Ubuntu, Raspberry Pi OS

```sh
sudo sh install.sh
```

[`install/install.sh`](install/install.sh) lädt das Programm aus dem Image (Docker wird dafür nicht gebraucht), legt den Benutzer `heimdeck`, den Datenordner `/var/lib/heimdeck` und einen systemd-Dienst an. Einstellungen stehen in `/etc/default/heimdeck`. Erneut ausführen aktualisiert, `sh install.sh uninstall` entfernt Heimdeck (die Daten bleiben).

### Proxmox VE: als eigener LXC-Container

Auf dem Proxmox-Host (Shell des Nodes), mit `install.sh` im selben Ordner:

```sh
sh proxmox-lxc.sh
```

[`install/proxmox-lxc.sh`](install/proxmox-lxc.sh) legt einen kleinen unprivilegierten Debian-Container an (1 Kern, 512 MB RAM, 2 GB), installiert Heimdeck darin und zeigt die Adresse an. ID, Speicher, Netzwerk und feste IP lassen sich per Variable ändern (siehe Kopf des Skripts). Beim ersten Öffnen schlägt Heimdeck dann direkt „Proxmox verbinden“ vor.

## Umstieg von Heimdall

1. In Heimdall: Einstellungen → **Exportieren** – das lädt `HeimdallExport.json` herunter.
2. In Heimdeck: auf der Startseite **Von Heimdall übernehmen** (oder Einstellungen → Sicherung → Importieren) und die Datei wählen.

Tags werden zu Gruppen, Kacheln ohne Tag landen in der Gruppe „Heimdall“. Dienste, die Heimdeck kennt (Sonarr, Radarr, Jellyfin, Nextcloud, AdGuard, Proxmox …), werden als Live-Kachel übernommen – mit API-Key, Benutzer und Passwort aus Heimdall sowie der „Override URL“ als interne Adresse.

## Proxmox verbinden

Proxmox-Kachel anlegen (Startseite → **Proxmox verbinden**) mit einem API-Token im Format `benutzer@pam!name=geheimnis` (Rechenzentrum → Berechtigungen → API-Token; Rolle *PVEAuditor* zum Anzeigen, zusätzlich *PVEVMUser* für Start/Stopp). Danach findet 🔍 die Dienste in deinen VMs und Containern: am Namen bzw. an den Tags (z. B. `jellyfin`, `adguard`, `paperless-ngx` – so heißen auch die Container der Proxmox-Community-Skripte), mit der IP-Adresse des Gasts und dem üblichen Port. VMs brauchen dafür den QEMU-Gast-Agenten.

## Unterstützte Dienste

| Dienst | Live-Daten | Suche | Aktionen |
|---|---|---|---|
| **Medien** | | | |
| Jellyfin | Streams, Bibliotheksgröße | ✓ | |
| Jellyseerr / Overseerr | offene Anfragen, Zähler | ✓ | genehmigen, ablehnen, anfragen |
| Sonarr / Radarr | Warteschlange, Demnächst, Warnungen | | |
| Lidarr | Warteschlange, neue und fehlende Alben, Warnungen | ✓ Künstler | fehlende Alben suchen |
| Bazarr | fehlende Untertitel (Folgen/Filme), gedrosselte Anbieter | ✓ Serien und Filme | fehlende Untertitel suchen |
| Prowlarr | aktive und gestörte Indexer | | |
| Immich | Fotos, Videos, Speicher pro Benutzer | ✓ Personen, Dateinamen, optional KI-Suche nach Bildinhalt | |
| Audiobookshelf | Hörbücher, Podcasts, Stunden, Weiterhören | ✓ Titel, Autoren, Serien | |
| Kavita | Serien, Bände, Dateien, Weiterlesen | ✓ mit Covern | |
| Komga | Serien, Bücher, angefangene Bücher | ✓ mit Covern | |
| Stash | Szenen, Bilder, Größe | | |
| RomM (API-Token oder Benutzer/Passwort) | Plattformen, Spiele, Spielstände | ✓ | |
| Plex | Streams, Filme/Serien/Musik | ✓ mit Covern | |
| Emby | Streams, Bibliotheksgröße | ✓ | |
| Navidrome | läuft gerade, Titelanzahl, Scan | ✓ Künstler, Alben, Titel mit Covern | |
| Readarr / Bookshelf | Warteschlange, neue und fehlende Bücher | ✓ Autoren | fehlende Bücher suchen |
| Tdarr | Dateien, Transcodes, Warteschlange, gesparter Speicher, laufende Worker | | |
| **Downloads** | | | |
| SABnzbd | Tempo, Warteschlange, Restzeit | | pausieren, fortsetzen |
| NZBGet | Tempo, Warteschlange, Restgröße, Restzeit | | pausieren, fortsetzen |
| qBittorrent, Transmission, Deluge | ↓/↑-Tempo, lädt/seedet, aktive Torrents mit Fortschritt | ✓ Torrents | alle pausieren/fortsetzen |
| MeTube | aktive, fertige, fehlgeschlagene Downloads | | |
| Tubesy (API-Schlüssel aus Tubesy → Konto) | Tempo, aktive, wartende, fehlgeschlagene Downloads, freier Speicher | ✓ Bibliothek; eingefügte URLs herunterladen | Download abbrechen, URL herunterladen |
| **Dokumente** | | | |
| Paperless-ngx | Dokumente, Posteingang | ✓ | |
| Nextcloud (App-Passwort) | Dateien, Benutzer, aktive Nutzer, freier Speicher (ohne Admin: eigener Speicher) | ✓ Dateien, Kalender, Kontakte, Talk, Notizen, Deck … | |
| **Netzwerk** | | | |
| AdGuard Home | DNS-Anfragen, Blockierquote, Antwortzeit, meistblockierte Domains | ✓ Abfrage-Log | Schutz pausieren/ausschalten, Domain sperren oder freigeben |
| Pi-hole (v6 mit App-Passwort, v5 mit API-Token) | DNS-Anfragen, Blockierquote, Clients | ✓ Abfrage-Log (v6) | Blockieren pausieren, Domain sperren oder freigeben |
| OPNsense (API-Key + Secret) | WAN-Datenrate, RAM, Last, Laufzeit, Gateways, Updates | | |
| UniFi (Network-API-Schlüssel) | Clients, WLAN, Geräte online, Firmware-Updates | ✓ Clients und Geräte | Gerät neu starten |
| Speedtest Tracker | Download, Upload, Ping, Verlauf | | Test starten |
| Anubis (Metrik-Port, z. B. `http://anubis:9090`) | Challenges, gelöst, fehlgeschlagen, häufigste Regeln | | |
| **System** | | | |
| Docker (eingebundener Socket) | laufende/gestoppte Container, optional Updates | ✓ Container | optional starten, stoppen, neu starten |
| Proxmox VE (API-Token) | CPU, RAM, Gäste aktiv, Nodes bzw. gestoppte Gäste | ✓ VMs und Container | starten, herunterfahren, neu starten |
| **Smart Home & Energie** | | | |
| Home Assistant (Langlebiges Zugriffstoken) | gewählte Sensoren/Schalter oder Übersicht (Lichter an, Updates, nicht verfügbare Geräte) | ✓ alle Entitäten | umschalten, Szenen und Skripte ausführen, Rollläden, Schlösser |
| ioBroker (REST-API- oder Simple-API-Adapter) | gewählte Datenpunkte oder Adapter-Status | ✓ Datenpunkte | Schalter umschalten |
| evcc | PV, Haus, Netz, Batterie, Ladepunkte | | Lademodus wechseln (Aus, PV, Min+PV, Schnell) |
| **Sonstige** | | | |
| Eigene API | beliebige Werte, Liste und Knöpfe aus einer JSON-Antwort | ✓ wenn eingerichtet | eigene Knöpfe |
| JDownloader, FRITZ!Box, Zoraxy, Portainer, … | Online-Status | | |

Jede Kachel kann außerdem ein „Nur Link“ sein und zeigt trotzdem an, ob der Dienst erreichbar ist. Die Statusprüfung lässt sich pro Kachel abschalten, die Kachelgröße (einheitlich oder automatisch) pro Gruppe einstellen.

## Benachrichtigungen

Einstellungen → Benachrichtigungen:

- **Discord**: In Discord unter Servereinstellungen → Integrationen → Webhooks einen Webhook anlegen und die URL eintragen.
- **ntfy**: Ein Thema in der ntfy-App abonnieren und dessen Adresse eintragen (z. B. `https://ntfy.sh/mein-heimdeck` oder dein eigener ntfy-Server), optional mit Token.
- **Browser**: Pro Browser einschaltbar. Funktioniert, solange ein Heimdeck-Tab offen ist, und nur über HTTPS (z. B. hinter dem Reverse Proxy) oder `localhost`.

Gemeldet wird, wenn ein Dienst die eingestellte Zeit (Standard 2 Minuten) nicht antwortet, und wenn er wieder erreichbar ist. Nur Kacheln mit Online-Prüfung.

## Docker-Kachel

Zeigt laufende und gestoppte Container. In den Einstellungen unter „Docker“ lassen sich zwei Dinge einschalten (beide standardmäßig aus):

- **Nach Container-Updates suchen**: alle 6 Stunden wird das Image jedes Containers mit der Registry verglichen (nur lesen, es wird nichts aktualisiert).
- **Container starten, stoppen und neu starten**: Knöpfe auf der Kachel und in der Suche. Achtung: Jeder, der im Dashboard angemeldet ist, kann dann deine Container steuern. Mit einem Socket-Proxy braucht es dafür `POST=1` und `CONTAINERS=1`.

## Eigene API

Für alles ohne eigene Integration: Kachel „Eigene API“ wählen, Adresse eintragen und beschreiben, was angezeigt werden soll. Beispiel für eine Antwort wie `{"cpu": 0.42, "disk": {"used": 550000000000}, "devices": [{"name": "NAS", "online": true}]}`:

```
Werte:
  CPU = cpu | anteil
  Belegt = disk.used | bytes
  Online = devices[online=true].#   (warn<1)
Liste:          devices
Listeneintrag:  title = name
                badge = online | janein
Knöpfe:         Neu starten = POST /api/restart
```

Pfade: `a.b.c`, `liste[0]`, `liste[-1]` (letztes), `liste.#` (Anzahl), `liste[feld=wert]` (filtern, auch `!=`, `>`, `<`). Formate: `text`, `zahl[:Nachkommastellen]`, `bytes`, `prozent`, `anteil` (0–1 → %), `dauer` (Sekunden), `datum`, `seit`, `janein`. Dahinter eine Einheit (`zahl:1 °C`) und Farben (`ok=up`, `warn>80`, `bad<10`). Header wie `Authorization: Bearer …` werden verschlüsselt gespeichert. Ohne eingetragene Werte zeigt die Kachel die Antwort an – praktisch, um die Pfade zu finden.

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

Eine neue Integration ist eine einzelne Datei in `internal/integrations/`: `Def()` beschreibt Felder und Icon, `Fetch()` liefert ein generisches `Widget` (Zahlen, Liste, Hinweise, Knöpfe). Das Frontend braucht dafür keinen eigenen Code. Optional gibt es `Search()`, `Action()` und `ImageRequest()` (Cover und Vorschaubilder, die einen API-Key brauchen, lädt Heimdeck dann selbst, sodass der Key nie im Browser landet).

---

<p align="center"><sub>Erdacht mit ♥ von Luis Blum · gebaut mit KI ✨</sub></p>
