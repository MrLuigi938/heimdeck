#!/bin/sh
# Install or update Heimdeck without Docker – for Debian, Ubuntu,
# Raspberry Pi OS and Proxmox LXC containers (systemd).
#
#   sh install.sh             install or update
#   sh install.sh uninstall   remove service and program (data is kept)
#
# Options via environment variables:
#   HEIMDECK_PORT=3000        port of the web interface
#   HEIMDECK_TAG=latest       version (image tag)
#   HEIMDECK_DOCKER=1|0       discover Docker containers (asks otherwise)
#   HEIMDECK_PROXMOX_URL=…    Proxmox address suggested during setup
#
# The program is taken from the public image ghcr.io/mrluigi938/heimdeck;
# Docker is not needed for this.
set -eu

IMAGE="mrluigi938/heimdeck"
TAG="${HEIMDECK_TAG:-latest}"
BIN=/usr/local/bin/heimdeck
DATA=/var/lib/heimdeck
ENVFILE=/etc/default/heimdeck
UNIT=/etc/systemd/system/heimdeck.service

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() { printf '\033[31mError: %s\033[0m\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "please run as root (e.g. with sudo)"
command -v systemctl >/dev/null 2>&1 || die "systemd is required"

if [ "${1:-}" = uninstall ]; then
  systemctl disable --now heimdeck 2>/dev/null || true
  rm -f "$UNIT" "$BIN"
  systemctl daemon-reload
  say "Heimdeck has been removed. Your data is still in $DATA."
  exit 0
fi

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m) (only amd64 and arm64)" ;;
esac

if ! command -v curl >/dev/null 2>&1 || ! command -v tar >/dev/null 2>&1; then
  say "Installing curl and tar …"
  apt-get update -qq && apt-get install -y -qq curl tar ca-certificates >/dev/null
fi

# ---- Download the program from the registry (without Docker) ----

download() {
  dest=$1
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  token=$(curl -fsSL "https://ghcr.io/token?scope=repository:$IMAGE:pull" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
  [ -n "$token" ] || die "cannot connect to ghcr.io"
  accept="application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"
  get() { curl -fsSL -H "Authorization: Bearer $token" -H "Accept: $accept" "https://ghcr.io/v2/$IMAGE/$1"; }

  # image index → manifest for this architecture → layers
  index=$(get "manifests/$TAG" | tr -d ' \n\t\r')
  digest=$(printf '%s' "$index" | sed 's/{"mediaType"/\n{"mediaType"/g' | grep "\"architecture\":\"$ARCH\"" |
    grep -v attestation | sed -n 's/.*"digest":"\(sha256:[0-9a-f]*\)".*/\1/p' | head -n 1)
  [ -n "$digest" ] || die "no image found for $ARCH (version $TAG)"
  layers=$(get "manifests/$digest" | tr -d ' \n\t\r' | sed 's/.*"layers":\[//' |
    grep -o '"digest":"sha256:[0-9a-f]*"' | cut -d'"' -f4)
  # The program is in one of the last layers; search from the end.
  for layer in $(printf '%s\n' $layers | sed '1!G;h;$!d'); do
    get "blobs/$layer" >"$tmp/layer.tgz"
    if tar -xzf "$tmp/layer.tgz" -C "$tmp" heimdeck 2>/dev/null; then
      chmod 755 "$tmp/heimdeck"
      mv -f "$tmp/heimdeck" "$dest"
      return 0
    fi
  done
  die "program not found in the image"
}

if [ -n "${HEIMDECK_DOWNLOAD_ONLY:-}" ]; then
  download "$HEIMDECK_DOWNLOAD_ONLY"
  exit 0
fi

update=0
[ -f "$BIN" ] && [ -f "$UNIT" ] && update=1

say "Downloading Heimdeck ($TAG, $ARCH) …"
download "$BIN.new"
mv -f "$BIN.new" "$BIN"

if [ "$update" = 1 ]; then
  systemctl restart heimdeck
  say "Heimdeck has been updated."
  exit 0
fi

# ---- User, data, service ----

id heimdeck >/dev/null 2>&1 || useradd --system --home-dir "$DATA" --shell /usr/sbin/nologin heimdeck
mkdir -p "$DATA"
chown heimdeck:heimdeck "$DATA"
chmod 700 "$DATA"

port="${HEIMDECK_PORT:-3000}"
if [ ! -f "$ENVFILE" ]; then
  {
    echo "# Heimdeck settings – after changes: systemctl restart heimdeck"
    echo "HEIMDECK_DATA=$DATA"
    echo "HEIMDECK_ADDR=:$port"
    echo "TZ=$(cat /etc/timezone 2>/dev/null || echo UTC)"
    echo "# Behind a reverse proxy with HTTPS:"
    echo "#HEIMDECK_SECURE_COOKIE=true"
    if [ -n "${HEIMDECK_PROXMOX_URL:-}" ]; then echo "HEIMDECK_PROXMOX_URL=$HEIMDECK_PROXMOX_URL"; fi
  } >"$ENVFILE"
fi

# Docker discovery: access to the Docker socket means full control over
# Docker, so only after asking.
groups_line=""
if [ -S /var/run/docker.sock ] && getent group docker >/dev/null 2>&1; then
  answer="${HEIMDECK_DOCKER:-}"
  if [ -z "$answer" ] && [ -t 0 ]; then
    printf 'Docker found. Should Heimdeck discover your containers automatically? [Y/n] '
    read -r reply
    case "$reply" in n* | N*) answer=0 ;; *) answer=1 ;; esac
  fi
  if [ "$answer" = 1 ]; then groups_line="SupplementaryGroups=docker"; fi
fi

cat >"$UNIT" <<EOF
[Unit]
Description=Heimdeck – dashboard for your home server
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
say "Heimdeck is running: http://${ip:-localhost}:$port"
echo "Update: run this script again. Remove: sh install.sh uninstall"
echo "Forgot your password: sudo -u heimdeck HEIMDECK_DATA=$DATA heimdeck reset-password <new-password>"
