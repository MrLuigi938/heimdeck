#!/bin/sh
# Create Heimdeck as its own LXC container on Proxmox VE.
# Run as root on the Proxmox host (shell of the node):
#
#   sh proxmox-lxc.sh
#
# Options via environment variables (defaults in brackets):
#   CTID (next free ID)  CT_HOSTNAME (heimdeck)  CORES (1)  MEMORY (512)
#   DISK (2, GB)  STORAGE (first container storage)  BRIDGE (vmbr0)
#   IP (dhcp, or e.g. 192.168.1.50/24)  GATEWAY (static IP only)
#   HEIMDECK_PORT (3000)
#   ROOT_PASSWORD (random, shown at the end)  SSH_KEY_FILE (none, e.g. ~/.ssh/id_ed25519.pub)
#
# The container is unprivileged, starts with the host and gets Heimdeck via
# install.sh (next to this script, otherwise downloaded from INSTALL_URL).
set -eu

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() { printf '\033[31mError: %s\033[0m\n' "$*" >&2; exit 1; }

command -v pct >/dev/null 2>&1 || die "this script runs on the Proxmox host (pct not found)"
[ "$(id -u)" = 0 ] || die "please run as root"

CTID="${CTID:-$(pvesh get /cluster/nextid)}"
CT_HOSTNAME="${CT_HOSTNAME:-heimdeck}"
CORES="${CORES:-1}"
MEMORY="${MEMORY:-512}"
DISK="${DISK:-2}"
BRIDGE="${BRIDGE:-vmbr0}"
IP="${IP:-dhcp}"
PORT="${HEIMDECK_PORT:-3000}"
# root password for the container console; a random one unless set
ROOT_PASSWORD="${ROOT_PASSWORD:-$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 16)}"
[ "${#ROOT_PASSWORD}" -ge 8 ] || die "ROOT_PASSWORD needs at least 8 characters"
INSTALL_URL="${INSTALL_URL:-https://raw.githubusercontent.com/MrLuigi938/heimdeck/main/install/install.sh}"
STORAGE="${STORAGE:-$(pvesm status -content rootdir 2>/dev/null | awk 'NR > 1 && $3 == "active" { print $1; exit }')}"
TPL_STORAGE="${TPL_STORAGE:-$(pvesm status -content vztmpl 2>/dev/null | awk 'NR > 1 && $3 == "active" { print $1; exit }')}"
[ -n "$STORAGE" ] || die "no container storage found – set STORAGE=…"
[ -n "$TPL_STORAGE" ] || die "no template storage found – set TPL_STORAGE=…"

# newest Debian template (debian-12/13-standard)
say "Looking for a Debian template …"
pveam update >/dev/null 2>&1 || true
arch=$(dpkg --print-architecture)
TEMPLATE=$(pveam available --section system | awk '{print $2}' | grep -E "^debian-[0-9]+-standard_.*_${arch}\.tar\.(zst|gz|xz)$" | sort -V | tail -n 1)
[ -n "$TEMPLATE" ] || die "no Debian template found"
if ! pveam list "$TPL_STORAGE" | grep -q "$TEMPLATE"; then
  say "Downloading $TEMPLATE …"
  pveam download "$TPL_STORAGE" "$TEMPLATE" >/dev/null
fi

net="name=eth0,bridge=$BRIDGE,ip=$IP"
if [ "$IP" != dhcp ] && [ -n "${GATEWAY:-}" ]; then net="$net,gw=$GATEWAY"; fi

say "Creating container $CTID ($CT_HOSTNAME) …"
pct create "$CTID" "$TPL_STORAGE:vztmpl/$TEMPLATE" \
  --hostname "$CT_HOSTNAME" --cores "$CORES" --memory "$MEMORY" --swap 256 \
  --rootfs "$STORAGE:$DISK" --net0 "$net" \
  --unprivileged 1 --features nesting=1 --onboot 1 --tags heimdeck \
  --password "$ROOT_PASSWORD" ${SSH_KEY_FILE:+--ssh-public-keys "$SSH_KEY_FILE"} \
  --description "Heimdeck – Dashboard: http://<IP>:$PORT" >/dev/null
pct start "$CTID"

say "Waiting for network …"
i=0
until pct exec "$CTID" -- getent hosts ghcr.io >/dev/null 2>&1; do
  i=$((i + 1))
  [ "$i" -lt 60 ] || die "container $CTID has no network connection"
  sleep 1
done

# Use install.sh next to this script, otherwise download it.
dir=$(cd "$(dirname "$0")" && pwd)
if [ -f "$0" ] && [ -f "$dir/install.sh" ]; then
  pct push "$CTID" "$dir/install.sh" /root/heimdeck-install.sh
else
  pct exec "$CTID" -- sh -c "apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null && curl -fsSL '$INSTALL_URL' -o /root/heimdeck-install.sh"
fi

# Heimdeck should suggest this host's Proxmox API during setup.
host_ip=$(hostname -I | awk '{print $1}')
say "Installing Heimdeck in the container …"
pct exec "$CTID" -- env HEIMDECK_PORT="$PORT" HEIMDECK_DOCKER=0 HEIMDECK_PROXMOX_URL="https://$host_ip:8006" \
  sh /root/heimdeck-install.sh

ct_ip=$(pct exec "$CTID" -- hostname -I | awk '{print $1}')
pct set "$CTID" --description "Heimdeck – Dashboard: http://$ct_ip:$PORT" >/dev/null
say "Done! Heimdeck is running in container $CTID: http://$ct_ip:$PORT"
echo
echo "Console login for the container (Proxmox → $CTID → Console):"
echo "  user: root"
echo "  password: $ROOT_PASSWORD"
echo "Please change it after the first login with: passwd"
echo
echo "Update: pct exec $CTID -- sh /root/heimdeck-install.sh"
echo "Tip: click \"Connect Proxmox\" during setup – Heimdeck then suggests your VMs and containers as tiles."
