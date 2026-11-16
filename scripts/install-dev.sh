#!/bin/sh
# Installs Stage Dev (identity stage-dev) from this checkout's working tree
# (docs/development.md): `mise run dist` builds this machine's assets into
# a temp dir, then the built stage-dev CLI runs `stage-dev install --from`
# on them, exactly as a release install does. Stage Dev has its own app,
# CLI (stage-dev), service, socket and data dir, and never touches the
# stable install. Rerun it to update.
#
#   scripts/install-dev.sh [--headless] [--cli-name NAME]
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)

fail() {
  printf 'install-dev: %s\n' "$1" >&2
  exit "${2:-1}"
}

MODE=desktop CLI_NAME=''
while [ $# -gt 0 ]; do
  case "$1" in
    --headless) MODE=headless ;;
    --cli-name)
      [ $# -ge 2 ] || fail "--cli-name needs a value" 2
      CLI_NAME=$2
      shift
      ;;
    -h | --help)
      sed -n '2,10p' "$0"
      exit 0
      ;;
    *) fail "unknown option $1" 2 ;;
  esac
  shift
done

[ "$(id -u)" != 0 ] || fail "do not run as root" 7
command -v mise >/dev/null 2>&1 || fail "mise is required (https://mise.jdx.dev)" 7

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) fail "unsupported OS $(uname -s)" 7 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) fail "unsupported CPU $(uname -m)" 7 ;;
esac

TMP=$(mktemp -d "${TMPDIR:-/tmp}/stage-install-dev.XXXXXX")
trap 'rm -rf "$TMP"' EXIT
trap 'exit 130' INT TERM
OUT="$TMP/dist"
mkdir "$OUT" "$TMP/cli"

(cd "$ROOT" && STAGE_DIST_OUT="$OUT" mise run dist) || fail "mise run dist failed"

VERSION=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$OUT/stage-release.json" | head -n1)
[ -n "$VERSION" ] || fail "no version in $OUT/stage-release.json"
TARBALL="$OUT/stage-dev-$VERSION-$OS-$ARCH.tar.gz"
MEMBER="stage-dev-$VERSION-$OS-$ARCH/stage"
[ -f "$TARBALL" ] || fail "dist built no $TARBALL"
tar -xzf "$TARBALL" -C "$TMP/cli" "$MEMBER" || fail "could not unpack $TARBALL"

set -- install --from "$OUT"
if [ "$MODE" = headless ]; then set -- "$@" --headless; else set -- "$@" --desktop; fi
if [ -n "$CLI_NAME" ]; then set -- "$@" --cli-name "$CLI_NAME"; fi
"$TMP/cli/$MEMBER" "$@"
