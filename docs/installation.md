# Installation

[← Back to the README](../README.md) · [Deutsch](de/installation.md)

- [Docker Compose](#docker-compose) (recommended)
- [Debian, Ubuntu, Raspberry Pi OS (without Docker)](#debian-ubuntu-raspberry-pi-os-without-docker)
- [Proxmox VE: as its own LXC container](#proxmox-ve-as-its-own-lxc-container)
- [Single binary](#single-binary)
- [Configuration](#configuration)
- [Behind a reverse proxy (HTTPS)](#behind-a-reverse-proxy-https)
- [Updating](#updating)

Heimdeck runs on **amd64** and **arm64** Linux (PCs, servers, NAS, Raspberry Pi 3/4/5 with a 64-bit OS).
After installing, open `http://<your-server>:3000` and create your user – the first user is created in the browser.

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
      # optional: let Heimdeck discover your containers (read-only)
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      - TZ=Etc/UTC                    # your time zone, e.g. Europe/London
      - HEIMDECK_SECURE_COOKIE=false  # "true" if Heimdeck is only reachable via HTTPS
```

```sh
docker compose up -d
```

All settings, tiles and keys are stored in `./data`.

**Image tags:** `latest` follows the newest version. To pin a version, use e.g. `ghcr.io/mrluigi938/heimdeck:1.0.0` (or `:1.0` for the newest patch release of 1.0). All versions are listed under [Packages](https://github.com/MrLuigi938/heimdeck/pkgs/container/heimdeck).

### Safer: Docker socket proxy

Access to the Docker socket gives full control over Docker. If you only want Heimdeck to *see* your containers, put a socket proxy in between that only allows listing:

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
      # - POST=1   # only needed if you enable start/stop/restart on the Docker tile
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
```

### Build the image yourself

Clone the repository and use `build: .` instead of `image:` in `docker-compose.yml`, then:

```sh
docker compose up -d --build
```

## Debian, Ubuntu, Raspberry Pi OS (without Docker)

```sh
curl -fsSL https://raw.githubusercontent.com/MrLuigi938/heimdeck/main/install/install.sh -o install.sh
sudo sh install.sh
```

The [install script](../install/install.sh)

- downloads the Heimdeck program (Docker is **not** needed),
- creates a system user `heimdeck`, the data folder `/var/lib/heimdeck` and a systemd service `heimdeck`,
- writes the settings to `/etc/default/heimdeck`,
- asks whether Heimdeck may read your Docker containers, if Docker is installed.

Options can be passed as environment variables, e.g. `sudo HEIMDECK_PORT=8080 sh install.sh`:

| Variable | Default | Meaning |
|---|---|---|
| `HEIMDECK_PORT` | `3000` | Port of the web interface |
| `ROOT_PASSWORD` | random, shown at the end | root password for the container console |
| `SSH_KEY_FILE` | – | Public SSH key for root, e.g. `~/.ssh/id_ed25519.pub` |
| `HEIMDECK_TAG` | `latest` | Version to install, e.g. `1.0.0` |
| `HEIMDECK_DOCKER` | asks | `1` = discover Docker containers, `0` = don't |

Useful commands:

```sh
sudo systemctl status heimdeck     # status
sudo journalctl -u heimdeck -f     # logs
sudo sh install.sh                 # update to the newest version
sudo sh install.sh uninstall       # remove (your data in /var/lib/heimdeck stays)
```

## Proxmox VE: as its own LXC container

Run this in the shell of your Proxmox node (as root):

```sh
curl -fsSL https://raw.githubusercontent.com/MrLuigi938/heimdeck/main/install/proxmox-lxc.sh -o proxmox-lxc.sh
sh proxmox-lxc.sh
```

The [script](../install/proxmox-lxc.sh) creates a small unprivileged Debian container (1 core, 512 MB RAM, 2 GB disk) that starts with the host, installs Heimdeck in it and prints the address. When you open Heimdeck for the first time, it offers to connect to Proxmox right away – see [Connecting Proxmox](services.md#connecting-proxmox).

Change the defaults with environment variables, e.g. `CTID=150 IP=192.168.1.50/24 GATEWAY=192.168.1.1 sh proxmox-lxc.sh`:

| Variable | Default | Meaning |
|---|---|---|
| `CTID` | next free ID | Container ID |
| `CT_HOSTNAME` | `heimdeck` | Host name |
| `CORES` / `MEMORY` / `DISK` | `1` / `512` / `2` | CPU cores, RAM in MB, disk in GB |
| `STORAGE` | first container storage | Storage for the container disk |
| `TPL_STORAGE` | first template storage | Storage for the Debian template |
| `BRIDGE` | `vmbr0` | Network bridge |
| `IP` / `GATEWAY` | `dhcp` | Static address like `192.168.1.50/24` plus gateway |
| `HEIMDECK_PORT` | `3000` | Port of the web interface |

Update later from the Proxmox host with `pct exec <CTID> -- sh /root/heimdeck-install.sh`.

### Logging in to the container

The script sets a **random root password** and prints it at the end (`user: root`, `password: …`). Use it in the Proxmox web interface under *your container → Console*. Please change it after the first login:

```sh
passwd
```

Your own password or an SSH key can be set when creating the container: `ROOT_PASSWORD='…' SSH_KEY_FILE=~/.ssh/id_ed25519.pub sh proxmox-lxc.sh`. SSH logins as root only work with a key on Debian by default.

Lost the password, or created the container with an older version of the script (which set none)? Set a new one from the shell of the Proxmox host – no login needed there:

```sh
pct exec <CTID> -- passwd
```

`pct enter <CTID>` opens a root shell inside the container directly.

## Single binary

Every [release](https://github.com/MrLuigi938/heimdeck/releases) contains ready-made programs for Linux (amd64, arm64). The web interface is built in – one file is all you need:

```sh
tar -xzf heimdeck_<version>_linux_amd64.tar.gz
HEIMDECK_DATA=./data ./heimdeck
```

## Configuration

Heimdeck is configured in the web interface. A few things are set via environment variables (Docker: `environment:`, install script: `/etc/default/heimdeck`):

| Variable | Default | Meaning |
|---|---|---|
| `HEIMDECK_ADDR` | `:3000` | Address and port to listen on |
| `HEIMDECK_DATA` | `/data` | Data folder (database and encryption key) |
| `HEIMDECK_SECURE_COOKIE` | `false` | Set to `true` when Heimdeck is only reachable via HTTPS |
| `DOCKER_HOST` | `/var/run/docker.sock` | Docker connection, e.g. `tcp://socket-proxy:2375` |
| `HEIMDECK_PROXMOX_URL` | – | Proxmox address suggested during setup |
| `TZ` | `UTC` | Time zone for times and dates |

## Behind a reverse proxy (HTTPS)

Heimdeck works behind any reverse proxy (Caddy, Nginx Proxy Manager, Traefik, …). Point it to port `3000` and set `HEIMDECK_SECURE_COOKIE=true`. Browser notifications only work via HTTPS (or `localhost`).

Example for Caddy:

```
dash.example.com {
    reverse_proxy heimdeck:3000
}
```

## Updating

| Installed with | Update |
|---|---|
| Docker Compose | `docker compose pull && docker compose up -d` |
| Install script | `sudo sh install.sh` |
| Proxmox LXC | `pct exec <CTID> -- sh /root/heimdeck-install.sh` |

Your data is kept. It is a good idea to make a [backup](usage.md#backup) before updating.
