#!/bin/sh
# Heimdeck ohne Docker installieren oder aktualisieren – für Debian, Ubuntu,
# Raspberry Pi OS und Proxmox-LXC-Container (systemd).
#
#   sh install.sh             installieren bzw. aktualisieren
#   sh install.sh uninstall   Dienst und Programm entfernen (Daten bleiben)
#
# Einstellungen per Umgebungsvariable:
#   HEIMDECK_PORT=3000        Port der Weboberfläche
#   HEIMDECK_TAG=latest       Version (Tag des Images)
#   HEIMDECK_DOCKER=1|0       Docker-Container erkennen (fragt sonst nach)
#   HEIMDECK_PROXMOX_URL=…    Proxmox-Adresse, die beim Einrichten vorgeschlagen wird
#
# Das Programm wird aus dem öffentlichen Image ghcr.io/mrluigi938/heimdeck
# geladen; Docker ist dafür nicht nötig.
set -eu

IMAGE="mrluigi938/heimdeck"
TAG="${HEIMDECK_TAG:-latest}"
BIN=/usr/local/bin/heimdeck
DATA=/var/lib/heimdeck
ENVFILE=/etc/default/heimdeck
UNIT=/etc/systemd/system/heimdeck.service

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() { printf '\033[31mFehler: %s\033[0m\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "bitte als root ausführen (z. B. mit sudo)"
command -v systemctl >/dev/null 2>&1 || die "systemd wird benötigt"

if [ "${1:-}" = uninstall ]; then
  systemctl disable --now heimdeck 2>/dev/null || true
  rm -f "$UNIT" "$BIN"
  systemctl daemon-reload
  say "Heimdeck wurde entfernt. Deine Daten liegen weiter in $DATA."
  exit 0
fi

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "nicht unterstützte Architektur: $(uname -m) (nur amd64 und arm64)" ;;
esac

if ! command -v curl >/dev/null 2>&1 || ! command -v tar >/dev/null 2>&1; then
  say "Installiere curl und tar …"
  apt-get update -qq && apt-get install -y -qq curl tar ca-certificates >/dev/null
fi

# ---- Programm aus der Registry laden (ohne Docker) ----

download() {
  dest=$1
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  token=$(curl -fsSL "https://ghcr.io/token?scope=repository:$IMAGE:pull" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
  [ -n "$token" ] || die "keine Verbindung zu ghcr.io"
  accept="application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"
  get() { curl -fsSL -H "Authorization: Bearer $token" -H "Accept: $accept" "https://ghcr.io/v2/$IMAGE/$1"; }

  # Image-Index → Manifest für diese Architektur → Schichten
  index=$(get "manifests/$TAG" | tr -d ' \n\t\r')
  digest=$(printf '%s' "$index" | sed 's/{"mediaType"/\n{"mediaType"/g' | grep "\"architecture\":\"$ARCH\"" |
    grep -v attestation | sed -n 's/.*"digest":"\(sha256:[0-9a-f]*\)".*/\1/p' | head -n 1)
  [ -n "$digest" ] || die "kein Image für $ARCH gefunden (Version $TAG)"
  layers=$(get "manifests/$digest" | tr -d ' \n\t\r' | sed 's/.*"layers":\[//' |
    grep -o '"digest":"sha256:[0-9a-f]*"' | cut -d'"' -f4)
  # Das Programm steckt in einer der letzten Schichten; von hinten suchen.
  for layer in $(printf '%s\n' $layers | sed '1!G;h;$!d'); do
    get "blobs/$layer" >"$tmp/layer.tgz"
    if tar -xzf "$tmp/layer.tgz" -C "$tmp" heimdeck 2>/dev/null; then
      chmod 755 "$tmp/heimdeck"
      mv -f "$tmp/heimdeck" "$dest"
      return 0
    fi
  done
  die "Programm im Image nicht gefunden"
}

if [ -n "${HEIMDECK_DOWNLOAD_ONLY:-}" ]; then
  download "$HEIMDECK_DOWNLOAD_ONLY"
  exit 0
fi

update=0
[ -f "$BIN" ] && [ -f "$UNIT" ] && update=1

say "Lade Heimdeck ($TAG, $ARCH) …"
download "$BIN.new"
mv -f "$BIN.new" "$BIN"

if [ "$update" = 1 ]; then
  systemctl restart heimdeck
  say "Heimdeck wurde aktualisiert."
  exit 0
fi

# ---- Benutzer, Daten, Dienst ----

id heimdeck >/dev/null 2>&1 || useradd --system --home-dir "$DATA" --shell /usr/sbin/nologin heimdeck
mkdir -p "$DATA"
chown heimdeck:heimdeck "$DATA"
chmod 700 "$DATA"

port="${HEIMDECK_PORT:-3000}"
if [ ! -f "$ENVFILE" ]; then
  {
    echo "# Einstellungen für Heimdeck – nach Änderungen: systemctl restart heimdeck"
    echo "HEIMDECK_DATA=$DATA"
    echo "HEIMDECK_ADDR=:$port"
    echo "TZ=$(cat /etc/timezone 2>/dev/null || echo Europe/Berlin)"
    echo "# Hinter einem Reverse Proxy mit HTTPS:"
    echo "#HEIMDECK_SECURE_COOKIE=true"
    if [ -n "${HEIMDECK_PROXMOX_URL:-}" ]; then echo "HEIMDECK_PROXMOX_URL=$HEIMDECK_PROXMOX_URL"; fi
  } >"$ENVFILE"
fi

# Docker-Erkennung: Zugriff auf den Docker-Socket bedeutet volle Kontrolle über
# Docker, deshalb nur auf Nachfrage.
groups_line=""
if [ -S /var/run/docker.sock ] && getent group docker >/dev/null 2>&1; then
  answer="${HEIMDECK_DOCKER:-}"
  if [ -z "$answer" ] && [ -t 0 ]; then
    printf 'Docker gefunden. Soll Heimdeck deine Container automatisch erkennen? [J/n] '
    read -r reply
    case "$reply" in n* | N*) answer=0 ;; *) answer=1 ;; esac
  fi
  if [ "$answer" = 1 ]; then groups_line="SupplementaryGroups=docker"; fi
fi

cat >"$UNIT" <<EOF
[Unit]
Description=Heimdeck – Dashboard für den Home-Server
After=network-online.target
Wants=network-online.target

[Service]
User=heimdeck
Group=heimdeck
$groups_line
EnvironmentFile=$ENVFILE
ExecStart=$BIN
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=$DATA

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now heimdeck >/dev/null 2>&1

for _ in 1 2 3 4 5 6 7 8 9 10; do
  HEIMDECK_ADDR=":$port" "$BIN" healthcheck >/dev/null 2>&1 && break
  sleep 1
done

ip=$(hostname -I 2>/dev/null | awk '{print $1}')
say "Heimdeck läuft: http://${ip:-localhost}:$port"
echo "Erdacht mit ♥ von Luis Blum · gebaut mit KI ✨"
echo "Aktualisieren: dieses Skript erneut ausführen. Entfernen: sh install.sh uninstall"
echo "Passwort vergessen: sudo -u heimdeck HEIMDECK_DATA=$DATA heimdeck reset-password <neues-passwort>"
