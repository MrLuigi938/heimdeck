# Unterstützte Dienste

[← Zurück zur Übersicht](../../README.de.md) · [English](../services.md)

- [Alle Integrationen](#alle-integrationen)
- [Proxmox verbinden](#proxmox-verbinden)
- [Docker-Kachel](#docker-kachel)
- [Eigene API](#eigene-api)

Kachel hinzufügen, Dienst wählen und Adresse sowie API-Key (oder Benutzer und Passwort) eintragen. Schlüssel werden verschlüsselt gespeichert und gelangen nie in den Browser – Cover und Vorschaubilder, die einen Key brauchen, lädt Heimdeck selbst.

<details>
<summary><b>Beispiel: ein Dashboard mit vielen Integrationen</b> (Demo-Daten, Oberfläche auf Englisch)</summary>

![Dashboard mit Medien-, Download-, Smart-Home-, Netzwerk- und System-Kacheln](../images/dashboard-full.webp)
</details>

## Alle Integrationen

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
| PhotoPrism (App-Passwort) | Fotos, Videos, Alben, Personen, zuletzt hinzugefügt | ✓ Fotos (Ort, Person, Motiv, Datum) | |
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
| Norish (API-Schlüssel) | heute geplante Gerichte, offene Einkäufe, Anzahl Rezepte | ✓ Rezepte | |
| Nextcloud (App-Passwort) | Dateien, Benutzer, aktive Nutzer, freier Speicher (ohne Admin: eigener Speicher) | ✓ Dateien, Kalender, Kontakte, Talk, Notizen, Deck … | |
| **Netzwerk** | | | |
| AdGuard Home | DNS-Anfragen, Blockierquote, Antwortzeit, meistblockierte Domains | ✓ Abfrage-Log | Schutz pausieren/ausschalten, Domain sperren oder freigeben |
| Pi-hole (v6 mit App-Passwort, v5 mit API-Token) | DNS-Anfragen, Blockierquote, Clients | ✓ Abfrage-Log (v6) | Blockieren pausieren, Domain sperren oder freigeben |
| OPNsense (API-Key + Secret) | WAN-Datenrate, RAM, Last, Laufzeit, Gateways, Updates | | |
| UniFi (Network-API-Schlüssel) | Clients, WLAN, Geräte online, Firmware-Updates | ✓ Clients und Geräte | Gerät neu starten |
| FRITZ!Box (TR-064, Benutzer + Passwort) | wählbar: Internet (Verbindung, Datenrate, Leitung, externe IP), Geräte online, Anrufliste mit verpassten Anrufen, Smart Home (DECT-Steckdosen und Thermostate), WLAN-Netze | | DECT-Steckdosen schalten |
| UniFi Protect (API-Schlüssel) | Kameras online, aktuelle Standbilder | ✓ Kameras | |
| Speedtest Tracker | Download, Upload, Ping, Verlauf | | Test starten |
| Anubis (Metrik-Port, z. B. `http://anubis:9090`) | Challenges, gelöst, fehlgeschlagen, häufigste Regeln | | |
| **System** | | | |
| Docker (eingebundener Socket) | laufende/gestoppte Container, optional Updates | ✓ Container | optional starten, stoppen, neu starten |
| Proxmox VE (API-Token) | CPU, RAM, Speicher, Laufzeit, Gäste aktiv, am stärksten ausgelastete und gestoppte Gäste bzw. Nodes | ✓ VMs und Container | starten, herunterfahren, neu starten |
| openmediavault (Administrator) | CPU, RAM, Laufzeit, Updates, Dateisysteme, Festplatten-Zustand (S.M.A.R.T.) | | |
| **Smart Home & Energie** | | | |
| Home Assistant (Langlebiges Zugriffstoken) | gewählte Sensoren/Schalter mit eigenen Namen oder Übersicht (Lichter an, Updates, nicht verfügbare Geräte) | ✓ alle Entitäten | umschalten, Szenen und Skripte ausführen, Rollläden, Schlösser |
| ioBroker (REST-API- oder Simple-API-Adapter) | gewählte Datenpunkte mit eigenen Namen oder Adapter-Status | ✓ Datenpunkte | Schalter umschalten |
| evcc | PV, Haus, Netz, Batterie, Ladepunkte (aktualisiert alle 5 s) | | Lademodus wechseln (Aus, PV, Min+PV, Schnell, Smart) |
| WLED | An/Aus, Helligkeit, Effekt, Presets | ✓ Presets | An/Aus, Helligkeit, Preset anwenden |
| Shelly (lokal, Gen1 bis Gen4) | Leistung, Energie, Temperatur, Luftfeuchte, Akku, WLAN, Firmware-Updates | | Relais und Lichter schalten, Rollläden öffnen/stoppen/schließen |
| **Sonstige** | | | |
| Eigene API | beliebige Werte, Liste und Knöpfe aus einer JSON-Antwort | ✓ wenn eingerichtet | eigene Knöpfe |
| Webhook | Meldungen, die andere Dienste an die eigene Adresse der Kachel schicken (JSON, Formular, Text, Uptime Kuma) | | Knöpfe, die Webhooks aufrufen (Shelly, Home Assistant, Node-RED, IFTTT …) |
| JDownloader, Zoraxy, Portainer, … | Online-Status | | |

Jede Kachel kann außerdem „Nur Link“ sein und zeigt trotzdem an, ob der Dienst erreichbar ist.

Dein Dienst fehlt? Nimm die Kachel [Eigene API](#eigene-api) oder [eröffne ein Issue](https://github.com/MrLuigi938/heimdeck/issues).

## ioBroker verbinden

ioBroker hat keine eigene Daten-Schnittstelle – sie kommt von einem Adapter:

1. In ioBroker Admin → **Adapter** den Adapter **REST API** installieren (eine Instanz, Standard-Einstellungen).
2. In Heimdeck als Adresse `http://<ioBroker-IP>:8093` eintragen. Die Admin-Oberfläche auf Port 8081 liefert keine Daten. Läuft der Adapter im Web-Adapter, lautet die Adresse `http://<ioBroker-IP>:8082/rest-api`; der ältere Simple-API-Adapter (Port 8087) geht auch.
3. Neben „Datenpunkte auf der Kachel“ auf **Auswählen** klicken, Datenpunkte suchen und anhaken – IDs abtippen ist nicht nötig. Ohne Auswahl zeigt die Kachel den Status deiner Adapter.

Entitäten von Home Assistant wählst du genauso aus. Unter der Auswahl bekommt jeder Eintrag ein Feld für **deinen eigenen Namen** – praktisch, wenn Entitäten z. B. `sensor.0x00158d0001a2b3c4_temperature` heißen. Leer lassen, um den Namen aus dem Dienst zu behalten.

## Proxmox verbinden

1. In Proxmox: Rechenzentrum → Berechtigungen → **API-Token** → Token anlegen (z. B. `root@pam`, Name `heimdeck`) und **„Privilegtrennung“ abwählen** – sonst hat der Token keine eigenen Rechte und die Kachel bleibt leer. Mit Privilegtrennung dem Token selbst die Rolle **PVEAuditor** auf `/` geben (Rechenzentrum → Berechtigungen → Hinzufügen → API-Token-Berechtigung), zusätzlich **PVEVMUser** zum Starten und Stoppen von Gästen.
2. In Heimdeck: Startseite → **Proxmox verbinden** (oder Proxmox-Kachel anlegen) und das Token im Format `benutzer@pam!name=geheimnis` eintragen.

Danach findet 🔍 die Dienste in deinen VMs und Containern: am Namen bzw. an den Tags (z. B. `jellyfin`, `adguard`, `paperless-ngx` – so heißen auch die Container der Proxmox-Community-Skripte), mit der IP-Adresse des Gasts und dem üblichen Port. VMs brauchen dafür den QEMU-Gast-Agenten.

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
  Online = devices[online=true].# | zahl warn<1
Liste:          devices
Listeneintrag:  title = name
                badge = online | janein
Knöpfe:         Neu starten = POST /api/restart
```

- **Pfade**: `a.b.c`, `liste[0]`, `liste[-1]` (letztes), `liste.#` (Anzahl), `liste[feld=wert]` (filtern, auch `!=`, `>`, `<`).
- **Formate**: `text`, `zahl[:Nachkommastellen]`, `bytes`, `prozent`, `anteil` (0–1 → %), `dauer` (Sekunden), `datum`, `seit`, `janein`. Dahinter eine Einheit (`zahl:1 °C`) und Farben (`ok=up`, `warn>80`, `bad<10`). Englische Namen (`number`, `percent`, `ratio`, `duration`, `date`, `since`, `bool`) gehen ebenso.
- **Listeneintrag**: `title`, `subtitle`, `badge`, `progress`, `url`; mehrere Pfade mit `+` verbinden (`subtitle = artist + album`).
- **Suche**: optional ein Pfad mit `{q}` als Platzhalter (z. B. `/api/search?q={q}`), dann ist die API Teil der gemeinsamen Suche.
- **Header** wie `Authorization: Bearer …` werden verschlüsselt gespeichert.

Ohne eingetragene Werte zeigt die Kachel die Antwort an – praktisch, um die Pfade zu finden.

