#!/bin/sh
# Heimdeck als eigenen LXC-Container auf Proxmox VE anlegen.
# Auf dem Proxmox-Host (Shell des Nodes) als root ausführen:
#
#   sh proxmox-lxc.sh
#
# Anpassen per Umgebungsvariable (Standardwerte in Klammern):
#   CTID (nächste freie ID)  CT_HOSTNAME (heimdeck)  CORES (1)  MEMORY (512)
#   DISK (2, GB)  STORAGE (erster Speicher für Container)  BRIDGE (vmbr0)
#   IP (dhcp, oder z. B. 192.168.1.50/24)  GATEWAY (nur bei fester IP)
#   HEIMDECK_PORT (3000)
#
# Der Container ist unprivilegiert, startet mit dem Host und bekommt Heimdeck
# über install.sh (liegt im selben Ordner oder wird von INSTALL_URL geladen).
set -eu

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() { printf '\033[31mFehler: %s\033[0m\n' "$*" >&2; exit 1; }

command -v pct >/dev/null 2>&1 || die "dieses Skript läuft auf dem Proxmox-Host (pct fehlt)"
[ "$(id -u)" = 0 ] || die "bitte als root ausführen"

CTID="${CTID:-$(pvesh get /cluster/nextid)}"
CT_HOSTNAME="${CT_HOSTNAME:-heimdeck}"
CORES="${CORES:-1}"
MEMORY="${MEMORY:-512}"
DISK="${DISK:-2}"
BRIDGE="${BRIDGE:-vmbr0}"
IP="${IP:-dhcp}"
PORT="${HEIMDECK_PORT:-3000}"
STORAGE="${STORAGE:-$(pvesm status -content rootdir 2>/dev/null | awk 'NR > 1 && $3 == "active" { print $1; exit }')}"
TPL_STORAGE="${TPL_STORAGE:-$(pvesm status -content vztmpl 2>/dev/null | awk 'NR > 1 && $3 == "active" { print $1; exit }')}"
[ -n "$STORAGE" ] || die "kein Speicher für Container gefunden – STORAGE=… angeben"
[ -n "$TPL_STORAGE" ] || die "kein Speicher für Vorlagen gefunden – TPL_STORAGE=… angeben"

# Neueste Debian-Vorlage (debian-12/13-standard)
say "Suche Debian-Vorlage …"
pveam update >/dev/null 2>&1 || true
arch=$(dpkg --print-architecture)
TEMPLATE=$(pveam available --section system | awk '{print $2}' | grep -E "^debian-[0-9]+-standard_.*_${arch}\.tar\.(zst|gz|xz)$" | sort -V | tail -n 1)
[ -n "$TEMPLATE" ] || die "keine Debian-Vorlage gefunden"
if ! pveam list "$TPL_STORAGE" | grep -q "$TEMPLATE"; then
  say "Lade $TEMPLATE …"
  pveam download "$TPL_STORAGE" "$TEMPLATE" >/dev/null
fi

net="name=eth0,bridge=$BRIDGE,ip=$IP"
if [ "$IP" != dhcp ] && [ -n "${GATEWAY:-}" ]; then net="$net,gw=$GATEWAY"; fi

say "Lege Container $CTID ($CT_HOSTNAME) an …"
pct create "$CTID" "$TPL_STORAGE:vztmpl/$TEMPLATE" \
  --hostname "$CT_HOSTNAME" --cores "$CORES" --memory "$MEMORY" --swap 256 \
  --rootfs "$STORAGE:$DISK" --net0 "$net" \
  --unprivileged 1 --features nesting=1 --onboot 1 --tags heimdeck \
  --description "Heimdeck – Dashboard: http://<IP>:$PORT" >/dev/null
pct start "$CTID"

say "Warte auf Netzwerk …"
i=0
until pct exec "$CTID" -- getent hosts ghcr.io >/dev/null 2>&1; do
  i=$((i + 1))
  [ "$i" -lt 60 ] || die "Container $CTID bekommt keine Netzwerkverbindung"
  sleep 1
done

# install.sh neben diesem Skript verwenden, sonst herunterladen.
dir=$(cd "$(dirname "$0")" && pwd)
if [ -f "$dir/install.sh" ]; then
  pct push "$CTID" "$dir/install.sh" /root/heimdeck-install.sh
else
  [ -n "${INSTALL_URL:-}" ] || die "install.sh nicht gefunden – neben dieses Skript legen oder INSTALL_URL angeben"
  pct exec "$CTID" -- sh -c "apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null && curl -fsSL '$INSTALL_URL' -o /root/heimdeck-install.sh"
fi

# Heimdeck soll beim Einrichten die Proxmox-API dieses Hosts vorschlagen.
host_ip=$(hostname -I | awk '{print $1}')
say "Installiere Heimdeck im Container …"
pct exec "$CTID" -- env HEIMDECK_PORT="$PORT" HEIMDECK_DOCKER=0 HEIMDECK_PROXMOX_URL="https://$host_ip:8006" \
  sh /root/heimdeck-install.sh

ct_ip=$(pct exec "$CTID" -- hostname -I | awk '{print $1}')
pct set "$CTID" --description "Heimdeck – Dashboard: http://$ct_ip:$PORT" >/dev/null
say "Fertig! Heimdeck läuft im Container $CTID: http://$ct_ip:$PORT"
echo "Aktualisieren: pct exec $CTID -- sh /root/heimdeck-install.sh"
echo "Tipp: Beim Einrichten auf „Proxmox verbinden“ klicken – dann schlägt Heimdeck deine VMs und Container als Kacheln vor."
