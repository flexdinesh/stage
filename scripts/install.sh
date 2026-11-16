#!/bin/sh
# Installs the latest stable Stage release for this user (docs/install.md):
# the desktop app with its CLI/server, or with --headless the CLI/server
# only. It finds the release, downloads the manifest, checksums.txt and
# the one archive this machine needs into a temp dir, verifies its sha256,
# then runs that release's own CLI, `stage install --from DIR`, which
# verifies everything again and installs (locking, swap, links, desktop
# entry, service restart, rollback). Never run it as root.
#
#   sh install.sh [--headless] [--version X.Y.Z] [--cli-name NAME]
#
# While the repository is private, run it with an authenticated gh:
#   gh api repos/flexdinesh/stage/contents/scripts/install.sh \
#     -H 'Accept: application/vnd.github.raw' | sh
set -eu

# STAGE_INSTALL_REPO, STAGE_INSTALL_WEB and STAGE_INSTALL_NO_GH exist for
# the installer's tests (a fixture server); users never need them.
REPO=${STAGE_INSTALL_REPO:-flexdinesh/stage}
WEB=${STAGE_INSTALL_WEB:-https://github.com}
ID=stage

say() { printf '%s\n' "$*"; }
fail() {
  printf 'stage install: %s\n' "$1" >&2
  [ $# -lt 2 ] || printf 'fix: %s\n' "$2" >&2
  exit "${3:-1}"
}

usage() {
  cat <<'EOF'
Install Stage (https://github.com/flexdinesh/stage) for this user.

  sh install.sh [--headless] [--version X.Y.Z] [--cli-name NAME]

  --headless       CLI/server only (remote machines; no desktop app)
  --version X      install this version instead of the latest stable one
  --cli-name NAME  name of the CLI in ~/.local/bin (default: stage)

Updating later: stage update. Removing: stage uninstall (keeps your data).
EOF
}

MODE=desktop VERSION='' CLI_NAME=''
while [ $# -gt 0 ]; do
  case "$1" in
    --headless) MODE=headless ;;
    --version)
      [ $# -ge 2 ] || fail "--version needs a value" "" 2
      VERSION=${2#v}
      shift
      ;;
    --version=*) VERSION=${1#--version=} VERSION=${VERSION#v} ;;
    --cli-name)
      [ $# -ge 2 ] || fail "--cli-name needs a value" "" 2
      CLI_NAME=$2
      shift
      ;;
    --cli-name=*) CLI_NAME=${1#--cli-name=} ;;
    -h | --help)
      usage
      exit 0
      ;;
    *) fail "unknown option $1" "sh install.sh --help" 2 ;;
  esac
  shift
done

[ "$(id -u)" != 0 ] || fail "do not run the installer as root" "run it as the user who uses Stage; it installs into your home directory" 7
[ -n "${HOME:-}" ] || fail "HOME is not set" "" 7

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) fail "unsupported OS $(uname -s)" "Stage supports Linux and macOS" 7 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) fail "unsupported CPU $(uname -m)" "Stage supports amd64 and arm64" 7 ;;
esac

need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required" "install $1 and retry" 7; }
need tar
need mktemp
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "sha256sum or shasum is required" "install coreutils and retry" 7
fi

# gh when it is logged in (needed while the repository is private), else
# anonymous downloads through the releases/latest redirect, which has no
# API rate limit.
USE_GH=
if [ -z "${STAGE_INSTALL_NO_GH:-}" ] && command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
  USE_GH=1
elif command -v curl >/dev/null 2>&1; then
  :
elif command -v wget >/dev/null 2>&1; then
  :
else
  fail "curl, wget or gh is required" "install curl and retry" 7
fi

TMP=$(mktemp -d "${TMPDIR:-/tmp}/stage-install.XXXXXX")
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT
trap 'exit 130' INT TERM
mkdir "$TMP/assets" "$TMP/cli"

latest() {
  if [ -n "$USE_GH" ]; then
    gh release view --repo "$REPO" --json tagName --jq .tagName 2>/dev/null || return 1
    return 0
  fi
  url="$WEB/$REPO/releases/latest"
  if command -v curl >/dev/null 2>&1; then
    loc=$(curl -fsS -o /dev/null -w '%{redirect_url}' "$url" 2>/dev/null) || return 1
  else
    loc=$(wget -q -S --max-redirect=0 -O /dev/null "$url" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tr -d '\r' | tail -n1) || true
  fi
  case "$loc" in
    */releases/tag/*) printf '%s\n' "${loc##*/releases/tag/}" ;;
    *) return 1 ;;
  esac
}

fetch() { # fetch NAME
  if [ -n "$USE_GH" ]; then
    gh release download "v$VERSION" --repo "$REPO" --dir "$TMP/assets" --pattern "$1" >/dev/null 2>&1 && return 0
    return 1
  fi
  url="$WEB/$REPO/releases/download/v$VERSION/$1"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 2 --connect-timeout 15 -o "$TMP/assets/$1" "$url"
  else
    wget -q -O "$TMP/assets/$1" "$url"
  fi
}

PRIVATE_FIX="check your connection; if the repository is private, log in with \`gh auth login\` and retry"
if [ -z "$VERSION" ]; then
  TAG=$(latest) || fail "could not find the latest Stage release at $WEB/$REPO" "$PRIVATE_FIX" 10
  case "$TAG" in
    v[0-9]*) VERSION=${TAG#v} ;;
    *) fail "the latest release tag '$TAG' is not a Stage version" "" 10 ;;
  esac
  case "$VERSION" in
    *-*) fail "the latest release $VERSION is a pre-release" "" 10 ;;
  esac
fi
case "$VERSION" in
  *[!0-9a-z.-]* | '') fail "invalid version '$VERSION'" "use X.Y.Z" 2 ;;
esac

if [ "$MODE" = headless ]; then
  ASSET="$ID-$VERSION-$OS-$ARCH.tar.gz"
  MEMBER="$ID-$VERSION-$OS-$ARCH/stage"
elif [ "$OS" = linux ]; then
  ASSET="$ID-desktop-$VERSION-linux-$ARCH.tar.gz"
  MEMBER="bin/stage"
else
  ASSET="$ID-desktop-$VERSION-darwin-universal.tar.gz"
  MEMBER="Stage.app/Contents/Resources/bin/stage"
fi

say "Stage $VERSION ($MODE, $OS-$ARCH)"
for f in stage-release.json checksums.txt; do
  fetch "$f" || fail "could not download $f of Stage $VERSION" "$PRIVATE_FIX" 10
done
SUM=$(awk -v n="$ASSET" '$2 == n || $2 == "*" n { print $1 }' "$TMP/assets/checksums.txt")
if [ -z "$SUM" ]; then
  if [ "$MODE" = desktop ]; then
    fail "Stage $VERSION has no desktop app for $OS-$ARCH" "install the CLI/server only: sh install.sh --headless" 7
  fi
  fail "Stage $VERSION has no build for $OS-$ARCH" "" 7
fi
case "$SUM" in
  *[!0-9a-f]*) fail "checksums.txt is malformed" "retry; report it if it persists" 11 ;;
esac
[ "${#SUM}" -eq 64 ] || fail "checksums.txt is malformed" "retry; report it if it persists" 11
fetch "$ASSET" || fail "could not download $ASSET" "$PRIVATE_FIX" 10
GOT=$(sha256 "$TMP/assets/$ASSET")
[ "$GOT" = "$SUM" ] || fail "$ASSET failed verification (sha256 $GOT, expected $SUM); nothing was installed" "retry; report it if it persists" 11

# Refuse unsafe members before extracting the CLI.
tar -tzf "$TMP/assets/$ASSET" >"$TMP/members" 2>/dev/null || fail "$ASSET is not a valid archive; nothing was installed" "retry" 11
if grep -Eq '(^/|(^|/)\.\.(/|$))' "$TMP/members"; then
  fail "$ASSET contains unsafe paths; nothing was installed" "report it" 11
fi
grep -qx "$MEMBER" "$TMP/members" || fail "$ASSET has no $MEMBER" "report it" 11
tar -xzf "$TMP/assets/$ASSET" -C "$TMP/cli" "$MEMBER" || fail "could not unpack $ASSET" "check free disk space in ${TMPDIR:-/tmp}" 1
CLI="$TMP/cli/$MEMBER"
[ -f "$CLI" ] && [ ! -L "$CLI" ] || fail "$MEMBER is not a regular file" "report it" 11
chmod 755 "$CLI"

set -- install --from "$TMP/assets"
if [ "$MODE" = headless ]; then set -- "$@" --headless; else set -- "$@" --desktop; fi
if [ -n "$CLI_NAME" ]; then set -- "$@" --cli-name "$CLI_NAME"; fi
"$CLI" "$@"
