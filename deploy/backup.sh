#!/bin/sh
# Create a consistent host-side archive of data. The service is stopped while
# the archive is created and started again on exit. Chromium profiles and
# downloads are excluded: they are rebuildable and contain session traces.
set -eu
umask 077

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
out=${1:-"$root/backups"}
stamp=$(date -u +%Y%m%dT%H%M%SZ)
archive="$out/muse2api-data-$stamp.tar.gz"

mkdir -p "$out"
chmod 700 "$out"
cd "$root"

unit=${MUSE2API_SYSTEMD_UNIT:-}
service_mode=
was_running=0
if [ -n "$unit" ]; then
    service_mode=systemd
    if systemctl is-active --quiet "$unit" 2>/dev/null; then
        systemctl stop "$unit"
        was_running=1
    fi
elif command -v docker >/dev/null 2>&1; then
    service_mode=docker
    if [ -n "$(docker compose ps --status running --quiet muse2api 2>/dev/null || true)" ]; then
        docker compose stop muse2api
        was_running=1
    fi
fi

cleanup() {
    if [ "$was_running" -eq 1 ]; then
        if [ "$service_mode" = systemd ]; then
            systemctl start "$unit"
        else
            docker compose start muse2api >/dev/null
        fi
    fi
}
trap cleanup EXIT INT TERM

if [ "$was_running" -eq 0 ]; then
    echo "warning: service was not running; archiving files as-is." >&2
fi

tar \
    --exclude='./data/chromium' \
    --exclude='./data/downloads' \
    -czf "$archive" ./data

chmod 600 "$archive"
echo "$archive"
