#!/bin/sh
# Prepare a Linux bind-mounted data directory for the non-root container user.
# It never deletes data; it only creates missing directories and fixes owner.
set -eu
umask 077

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
data=${MUSE2API_DATA_DIR:-"$root/data"}

# Read a simple KEY=VALUE from .env without executing it. sudo usually drops
# the caller's environment, so this keeps the script aligned with Compose.
read_env() {
    [ -f "$root/.env" ] || return 0
    line=$(grep -E "^$1=" "$root/.env" | tail -n 1 || true)
    [ -n "$line" ] || return 0
    value=${line#*=}
    value=$(printf '%s' "$value" | tr -d '\r')
    case $value in
        \"*\") value=${value#\"}; value=${value%\"} ;;
        \'*\') value=${value#\'}; value=${value%\'} ;;
    esac
    printf '%s' "$value"
}

uid=${MUSE2API_UID:-$(read_env MUSE2API_UID)}
gid=${MUSE2API_GID:-$(read_env MUSE2API_GID)}
uid=${uid:-10001}
gid=${gid:-10001}

for dir in "$data" "$data/media" "$data/backups" "$data/chromium/generate" "$data/downloads"; do
    mkdir -p "$dir"
    chmod 700 "$dir"
done

current_uid=$(id -u)
if [ "$current_uid" -eq 0 ]; then
    chown -R "$uid:$gid" "$data"
    echo "data directory ready: $data (owner $uid:$gid, mode 700)"
elif [ "$current_uid" -eq "$uid" ]; then
    echo "data directory ready: $data (owner current UID $uid, mode 700)"
else
    echo "error: data is owned by host UID $current_uid but the container runs as $uid." >&2
    echo "  run: sudo sh deploy/init-data.sh" >&2
    echo "  or set MUSE2API_UID=$current_uid and MUSE2API_GID=$(id -g) in .env" >&2
    exit 1
fi
