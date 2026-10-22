# Installation

[← Zurück zur Übersicht](../../README.de.md) · [English](../installation.md)

- [Docker Compose](#docker-compose) (empfohlen)
- [Debian, Ubuntu, Raspberry Pi OS (ohne Docker)](#debian-ubuntu-raspberry-pi-os-ohne-docker)
- [Proxmox VE: als eigener LXC-Container](#proxmox-ve-als-eigener-lxc-container)
- [Einzelnes Programm](#einzelnes-programm)
- [Einstellungen](#einstellungen)
- [Hinter einem Reverse Proxy (HTTPS)](#hinter-einem-reverse-proxy-https)
- [Aktualisieren](#aktualisieren)

Heimdeck läuft auf Linux mit **amd64** und **arm64** (PCs, Server, NAS, Raspberry Pi 3/4/5 mit 64-Bit-System).
Nach der Installation `http://<server>:3000` öffnen und deinen Benutzer anlegen – der erste Benutzer wird im Browser erstellt.

## Docker Compose

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
      - HEIMDECK_SECURE_COOKIE=false  # "true", wenn Heimdeck nur über HTTPS erreichbar ist
```

```sh
docker compose up -d
```

Alle Einstellungen, Kacheln und Schlüssel liegen in `./data`.

**Image-Tags:** `latest` ist immer die neueste Version. Für eine feste Version z. B. `ghcr.io/mrluigi938/heimdeck:1.0.0` verwenden (oder `:1.0` für die neueste Fehlerbehebung von 1.0). Alle Versionen stehen unter [Packages](https://github.com/MrLuigi938/heimdeck/pkgs/container/heimdeck).

### Noch sicherer: Docker-Socket-Proxy

Zugriff auf den Docker-Socket bedeutet volle Kontrolle über Docker. Soll Heimdeck deine Container nur *sehen*, gib den Socket über einen Proxy frei, der nur das Auflisten erlaubt:

```yaml
services:
  heimdeck:
    image: ghcr.io/mrluigi938/heimdeck:latest
    restart: unless-stopped
    ports:
      - "3000:3000"
    volumes:
      - ./data:/data
    environment:
      - DOCKER_HOST=tcp://socket-proxy:2375

  socket-proxy:
    image: tecnativa/docker-socket-proxy:latest
    restart: unless-stopped
    environment:
      - CONTAINERS=1
      # - POST=1   # nur nötig, wenn du Start/Stopp auf der Docker-Kachel einschaltest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
```

### Image selbst bauen

Repository klonen, in `docker-compose.yml` `build: .` statt `image:` verwenden und dann:

```sh
docker compose up -d --build
```

## Debian, Ubuntu, Raspberry Pi OS (ohne Docker)

```sh
curl -fsSL https://raw.githubusercontent.com/MrLuigi938/heimdeck/main/install/install.sh -o install.sh
sudo sh install.sh
```

Das [Installationsskript](../../install/install.sh)

- lädt das Heimdeck-Programm herunter (Docker wird dafür **nicht** gebraucht),
- legt den Systembenutzer `heimdeck`, den Datenordner `/var/lib/heimdeck` und den systemd-Dienst `heimdeck` an,
- speichert die Einstellungen in `/etc/default/heimdeck`,
- fragt, ob Heimdeck deine Docker-Container auslesen darf, falls Docker installiert ist.

Optionen per Umgebungsvariable, z. B. `sudo HEIMDECK_PORT=8080 sh install.sh`:

| Variable | Standard | Bedeutung |
|---|---|---|
| `HEIMDECK_PORT` | `3000` | Port der Weboberfläche |
| `ROOT_PASSWORD` | zufällig, wird am Ende angezeigt | root-Passwort für die Container-Konsole |
| `SSH_KEY_FILE` | – | Öffentlicher SSH-Schlüssel für root, z. B. `~/.ssh/id_ed25519.pub` |
| `HEIMDECK_TAG` | `latest` | Zu installierende Version, z. B. `1.0.0` |
| `HEIMDECK_DOCKER` | fragt nach | `1` = Docker-Container erkennen, `0` = nicht |

Nützliche Befehle:

```sh
sudo systemctl status heimdeck     # Status
sudo journalctl -u heimdeck -f     # Protokoll
sudo sh install.sh                 # auf die neueste Version aktualisieren
sudo sh install.sh uninstall       # entfernen (die Daten in /var/lib/heimdeck bleiben)
```

## Proxmox VE: als eigener LXC-Container

In der Shell deines Proxmox-Nodes (als root):

```sh
curl -fsSL https://raw.githubusercontent.com/MrLuigi938/heimdeck/main/install/proxmox-lxc.sh -o proxmox-lxc.sh
sh proxmox-lxc.sh
```

Das [Skript](../../install/proxmox-lxc.sh) legt einen kleinen unprivilegierten Debian-Container an (1 Kern, 512 MB RAM, 2 GB Speicher), der mit dem Host startet, installiert Heimdeck darin und zeigt die Adresse an. Beim ersten Öffnen schlägt Heimdeck direkt vor, Proxmox zu verbinden – siehe [Proxmox verbinden](dienste.md#proxmox-verbinden).

Standardwerte lassen sich per Umgebungsvariable ändern, z. B. `CTID=150 IP=192.168.1.50/24 GATEWAY=192.168.1.1 sh proxmox-lxc.sh`:

| Variable | Standard | Bedeutung |
|---|---|---|
| `CTID` | nächste freie ID | Container-ID |
| `CT_HOSTNAME` | `heimdeck` | Name des Containers in Proxmox (Hostname); wird im Terminal abgefragt |
| `CORES` / `MEMORY` / `DISK` | `1` / `512` / `2` | CPU-Kerne, RAM in MB, Speicher in GB |
| `STORAGE` | erster Container-Speicher | Speicher für die Container-Disk |
| `TPL_STORAGE` | erster Vorlagen-Speicher | Speicher für die Debian-Vorlage |
| `BRIDGE` | `vmbr0` | Netzwerk-Bridge |
| `IP` / `GATEWAY` | `dhcp` | Feste Adresse wie `192.168.1.50/24` plus Gateway |
| `HEIMDECK_PORT` | `3000` | Port der Weboberfläche |

Später aktualisieren auf dem Proxmox-Host mit `pct exec <CTID> -- sh /root/heimdeck-install.sh`.

### Anmelden im Container

Das Skript setzt ein **zufälliges root-Passwort** und zeigt es am Ende an (`user: root`, `password: …`). Damit meldest du dich in der Proxmox-Oberfläche unter *dein Container → Konsole* an. Bitte ändere es nach der ersten Anmeldung:

```sh
passwd
```

Ein eigenes Passwort oder einen SSH-Schlüssel kannst du schon beim Anlegen festlegen: `ROOT_PASSWORD='…' SSH_KEY_FILE=~/.ssh/id_ed25519.pub sh proxmox-lxc.sh`. Per SSH kann sich root bei Debian standardmäßig nur mit Schlüssel anmelden.

Passwort vergessen oder den Container mit einer älteren Version des Skripts angelegt (die keins gesetzt hat)? Dann setzt du in der Shell des Proxmox-Hosts ein neues – dort ist keine Anmeldung im Container nötig:

```sh
pct exec <CTID> -- passwd
```

`pct enter <CTID>` öffnet direkt eine root-Shell im Container.

## Einzelnes Programm

Jedes [Release](https://github.com/MrLuigi938/heimdeck/releases) enthält fertige Programme für Linux (amd64, arm64). Die Weboberfläche ist eingebaut – eine Datei reicht:

```sh
tar -xzf heimdeck_<version>_linux_amd64.tar.gz
HEIMDECK_DATA=./data ./heimdeck
```

## Einstellungen

Heimdeck wird in der Weboberfläche eingerichtet. Ein paar Dinge werden per Umgebungsvariable gesetzt (Docker: `environment:`, Installationsskript: `/etc/default/heimdeck`):

| Variable | Standard | Bedeutung |
|---|---|---|
| `HEIMDECK_ADDR` | `:3000` | Adresse und Port |
| `HEIMDECK_DATA` | `/data` | Datenordner (Datenbank und Schlüssel) |
| `HEIMDECK_SECURE_COOKIE` | `false` | Auf `true` setzen, wenn Heimdeck nur über HTTPS erreichbar ist |
| `DOCKER_HOST` | `/var/run/docker.sock` | Docker-Verbindung, z. B. `tcp://socket-proxy:2375` |
| `HEIMDECK_PROXMOX_URL` | – | Proxmox-Adresse, die beim Einrichten vorgeschlagen wird |
| `HEIMDECK_DEBUG` | `false` | `true` = Debug-Modus von Anfang an, unabhängig von der Einstellung (siehe [Fehlersuche](benutzung.md#fehlersuche)) |
| `TZ` | `UTC` | Zeitzone für Uhrzeiten und Daten |

## Hinter einem Reverse Proxy (HTTPS)

Heimdeck funktioniert hinter jedem Reverse Proxy (Caddy, Nginx Proxy Manager, Traefik, …). Auf Port `3000` weiterleiten und `HEIMDECK_SECURE_COOKIE=true` setzen. Browser-Benachrichtigungen funktionieren nur über HTTPS (oder `localhost`).

Beispiel für Caddy:

```
dash.example.com {
    reverse_proxy heimdeck:3000
}
```

## Aktualisieren

| Installiert mit | Aktualisieren |
|---|---|
| Docker Compose | `docker compose pull && docker compose up -d` |
| Installationsskript | `sudo sh install.sh` |
| Proxmox-LXC | `pct exec <CTID> -- sh /root/heimdeck-install.sh` |

Deine Daten bleiben erhalten. Vorher eine [Sicherung](benutzung.md#sicherung) zu machen schadet nicht.
