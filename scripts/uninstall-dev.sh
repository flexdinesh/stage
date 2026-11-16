#!/bin/sh
# Uninstalls Stage Dev (identity stage-dev; docs/development.md) with the
# installed stage-dev CLI's own `uninstall`: app, CLI link, desktop entry
# and service go, data is kept. --purge also deletes Stage Dev's data,
# config (service.env), logs, database backups and desktop preferences,
# after a confirmation (--yes skips it). Nothing of the stable install, of
# `mise run desktop:dev` (.dev dirs) or of `mise run dev` (.scratch) is
# touched.
#
#   scripts/uninstall-dev.sh [--purge] [--yes]
set -eu

fail() {
  printf 'uninstall-dev: %s\n' "$1" >&2
  exit "${2:-1}"
}
say() { printf 'uninstall-dev: %s\n' "$1"; }

PURGE=0 YES=0
while [ $# -gt 0 ]; do
  case "$1" in
    --purge) PURGE=1 ;;
    --yes) YES=1 ;;
    -h | --help)
      sed -n '2,10p' "$0"
      exit 0
      ;;
    *) fail "unknown option $1" 2 ;;
  esac
  shift
done

[ "$(id -u)" != 0 ] || fail "do not run as root" 7
if [ "$PURGE" = 1 ] && [ -n "${STAGE_DATA_DIR:-}" ]; then
  fail "STAGE_DATA_DIR is set; --purge removes only the default Stage Dev dirs: unset it, or delete $STAGE_DATA_DIR yourself" 2
fi

ID=stage-dev
APPID=io.github.flexdinesh.stage-dev.desktop
case "$(uname -s)" in
  Darwin)
    SUP="$HOME/Library/Application Support"
    set -- "$SUP/$ID" "$SUP/$APPID" "$HOME/Library/Logs/$ID" "$HOME/Library/Logs/$APPID" "$HOME/Library/Caches/$APPID"
    ;;
  *)
    DATA=${XDG_DATA_HOME:-$HOME/.local/share}
    CONF=${XDG_CONFIG_HOME:-$HOME/.config}
    CACHE=${XDG_CACHE_HOME:-$HOME/.cache}
    set -- "$DATA/$ID" "$CONF/$ID" "$CONF/$APPID" "$CACHE/$APPID"
    ;;
esac

if [ "$PURGE" = 1 ] && [ "$YES" = 0 ]; then
  [ -t 0 ] || fail "--purge deletes Stage Dev's data; confirm interactively or pass --yes" 2
  printf 'Delete all Stage Dev data, config, logs and preferences?\n'
  for p in "$@"; do [ ! -e "$p" ] || printf '  %s\n' "$p"; done
  printf 'Type yes to continue: '
  read -r answer || answer=''
  [ "$answer" = yes ] || fail "cancelled; nothing was removed" 1
fi

RECORD="$HOME/.local/lib/$ID/install.json"
if [ -f "$RECORD" ]; then
  CLI=$(sed -n 's/^  "cli": "\(.*\)",$/\1/p' "$RECORD" | head -n1)
  if [ -z "$CLI" ] || [ ! -x "$CLI" ]; then
    fail "the install record names no runnable CLI ($RECORD); remove the install root by hand: rm -rf $HOME/.local/lib/$ID"
  fi
  rc=0
  "$CLI" uninstall || rc=$?
  [ "$rc" = 0 ] || exit "$rc"
else
  say "Stage Dev is not installed by the installer here ($RECORD)"
fi

if [ "$PURGE" = 1 ]; then
  for p in "$@"; do
    # Only Stage Dev's own dirs, whatever the environment says.
    case "$(basename "$p")" in
      "$ID" | "$APPID") ;;
      *) fail "refusing to delete $p" ;;
    esac
    if [ -e "$p" ]; then
      rm -rf "$p"
      say "deleted $p"
    fi
  done
fi
