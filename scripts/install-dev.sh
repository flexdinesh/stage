#!/bin/sh
# Installs Stage Dev (identity stage-dev) built from an exact commit
# (docs/development.md). With no SHA it fetches origin and builds the
# latest origin/main; a SHA must be a full 40-hex commit that origin has.
# The commit is built in a temporary detached git worktree (this checkout
# is never changed) with `mise run dist`, then the built stage-dev CLI runs
# `stage-dev install --from` on it, exactly as a release install does.
# --working-tree builds this checkout's working tree instead (dirty marked).
# Stage Dev has its own app, CLI, service, socket and data dir, and never
# touches the stable install. Rerun it to update.
#
#   scripts/install-dev.sh [--headless] [--cli-name NAME] [--check] [--force] [--working-tree | SHA]
set -eu

SELF=$(cd "$(dirname "$0")/.." && pwd)

fail() {
  printf 'install-dev: %s\n' "$1" >&2
  exit "${2:-1}"
}
say() { printf 'install-dev: %s\n' "$1"; }

MODE=desktop CLI_NAME='' CHECK=0 FORCE=0 WORKING_TREE=0 SHA=''
while [ $# -gt 0 ]; do
  case "$1" in
    --headless) MODE=headless ;;
    --cli-name)
      [ $# -ge 2 ] || fail "--cli-name needs a value" 2
      CLI_NAME=$2
      shift
      ;;
    --check) CHECK=1 ;;
    --force) FORCE=1 ;;
    --working-tree) WORKING_TREE=1 ;;
    -h | --help)
      sed -n '2,12p' "$0"
      exit 0
      ;;
    -*) fail "unknown option $1" 2 ;;
    *)
      [ -z "$SHA" ] || fail "only one SHA" 2
      SHA=$1
      ;;
  esac
  shift
done

if [ "$WORKING_TREE" = 1 ] && [ -n "$SHA" ]; then
  fail "--working-tree builds this checkout and takes no SHA" 2
fi
# Only a full lowercase hex SHA ever reaches git: no refs, ranges or options.
if [ -n "$SHA" ]; then
  SHA=$(printf '%s' "$SHA" | tr 'ABCDEF' 'abcdef')
  case "$SHA" in
    *[!0-9a-f]*) fail "SHA must be a full 40-character commit hash, not $SHA" 2 ;;
  esac
  [ "${#SHA}" -eq 40 ] || fail "SHA must be a full 40-character commit hash, not $SHA" 2
fi

[ "$(id -u)" != 0 ] || fail "do not run as root" 7
command -v mise >/dev/null 2>&1 || fail "mise is required (https://mise.jdx.dev)" 7
command -v git >/dev/null 2>&1 || fail "git is required" 7

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

REPO=$(git -C "$SELF" rev-parse --show-toplevel 2>/dev/null) || fail "$SELF is not a git checkout" 7
RECORD="$HOME/.local/lib/stage-dev/install.json"
WANT_CLI=${CLI_NAME:-stage-dev}

# field NAME: a string field of the install record ("" when absent).
field() {
  [ -f "$RECORD" ] || return 0
  sed -n "s/^  \"$1\": \"\(.*\)\",\{0,1\}\$/\1/p" "$RECORD" | head -n1
}

# A mode switch is refused by the installer anyway; refuse before building.
if [ -f "$RECORD" ] && [ "$(field mode)" != "$MODE" ]; then
  fail "Stage Dev is installed in $(field mode) mode; run scripts/uninstall-dev.sh first, then install in $MODE mode" 7
fi

TMP='' WT=''
cleanup() {
  if [ -n "$WT" ] && [ -d "$WT" ]; then
    if ! git -C "$REPO" worktree remove --force "$WT" >/dev/null 2>&1; then
      rm -rf "$WT"
      git -C "$REPO" worktree prune >/dev/null 2>&1 || true
    fi
  fi
  if [ -n "$TMP" ]; then rm -rf "$TMP"; fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

BASE="${XDG_CACHE_HOME:-$HOME/.cache}/stage-install-dev"
mkdir -p "$BASE"
TMP=$(mktemp -d "$BASE/build.XXXXXX")

if [ "$WORKING_TREE" = 0 ]; then
  git -C "$REPO" remote get-url origin >/dev/null 2>&1 || fail "this checkout has no origin remote" 7
  git -C "$REPO" fetch --quiet origin ||
    fail "git fetch origin failed; nothing was built (local refs are never used instead)" 10
  if [ -z "$SHA" ]; then
    SHA=$(git -C "$REPO" rev-parse --verify --quiet 'refs/remotes/origin/main^{commit}') ||
      fail "origin has no main branch" 10
  else
    # Origin must have it: an origin branch contains it, or origin serves it
    # to an empty repo (a fetch into this one is a no-op for objects it
    # already has, so it cannot tell a local-only commit apart).
    if ! git -C "$REPO" cat-file -e "$SHA^{commit}" 2>/dev/null ||
      [ -z "$(git -C "$REPO" for-each-ref --contains "$SHA" refs/remotes/origin 2>/dev/null)" ]; then
      git init --quiet "$TMP/probe" || fail "could not create $TMP/probe"
      git -C "$TMP/probe" fetch --quiet --depth=1 "$(git -C "$REPO" remote get-url origin)" "$SHA" 2>/dev/null ||
        fail "origin has no commit $SHA (push it first, or check the hash)" 10
      rm -rf "$TMP/probe"
      git -C "$REPO" fetch --quiet origin "$SHA" 2>/dev/null || fail "could not fetch $SHA from origin" 10
    fi
    git -C "$REPO" cat-file -e "$SHA^{commit}" 2>/dev/null || fail "$SHA is not a commit" 10
  fi
  say "commit $SHA"

  if [ "$FORCE" = 0 ] && [ "$(field commit)" = "$SHA" ] && [ "$(field cliName)" = "$WANT_CLI" ]; then
    say "Stage Dev $(field version) ($SHA) is already installed ($MODE mode); --force rebuilds it"
    exit 0
  fi

  # The build interface this script drives: mise run dist with
  # STAGE_DIST_OUT, and stage install --from (45792be and later).
  if ! git -C "$REPO" cat-file -e "$SHA:server/internal/install/install.go" 2>/dev/null ||
    ! git -C "$REPO" grep -q -F '[tasks.dist]' "$SHA" -- mise.toml 2>/dev/null ||
    ! git -C "$REPO" grep -q -F 'STAGE_DIST_OUT' "$SHA" -- scripts/release.sh 2>/dev/null; then
    fail "commit $SHA predates the dev-install build interface (mise run dist + stage install --from, from 45792be); pick a newer commit" 7
  fi
fi

OUT="$TMP/dist"
mkdir "$OUT" "$TMP/cli"

if [ "$WORKING_TREE" = 1 ]; then
  SRC=$REPO
else
  WT="$TMP/src"
  # No hooks: a checkout hook must not run in (or change) the build tree.
  git -C "$REPO" -c core.hooksPath=/dev/null worktree add --quiet --detach "$WT" "$SHA" ||
    fail "could not create a worktree at $SHA"
  SRC=$WT
fi

# Pinned toolchain, optional checks, then the build. The temp worktree is
# trusted for these commands only (no persistent mise trust). Chained with
# && because set -e does not apply inside a subshell tested with ||.
(
  cd "$SRC" && export MISE_TRUSTED_CONFIG_PATHS="$SRC" &&
    mise install &&
    { [ "$CHECK" = 0 ] || mise run check; } &&
    STAGE_DIST_OUT="$OUT" mise run dist
) || fail "build failed; Stage Dev was not changed"

VERSION=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$OUT/stage-release.json" | head -n1)
COMMIT=$(sed -n 's/^  "commit": "\(.*\)",$/\1/p' "$OUT/stage-release.json" | head -n1)
[ -n "$VERSION" ] || fail "no version in $OUT/stage-release.json"
if [ "$WORKING_TREE" = 0 ] && [ "$COMMIT" != "$SHA" ]; then
  fail "the build is of $COMMIT, not $SHA (did the build change tracked files?); Stage Dev was not changed"
fi
TARBALL="$OUT/stage-dev-$VERSION-$OS-$ARCH.tar.gz"
MEMBER="stage-dev-$VERSION-$OS-$ARCH/stage"
[ -f "$TARBALL" ] || fail "dist built no $TARBALL"
tar -xzf "$TARBALL" -C "$TMP/cli" "$MEMBER" || fail "could not unpack $TARBALL"

set -- install --from "$OUT"
if [ "$MODE" = headless ]; then set -- "$@" --headless; else set -- "$@" --desktop; fi
if [ -n "$CLI_NAME" ]; then set -- "$@" --cli-name "$CLI_NAME"; fi
rc=0
"$TMP/cli/$MEMBER" "$@" || rc=$?
[ "$rc" = 0 ] || exit "$rc"

if [ "$WORKING_TREE" = 0 ]; then
  [ "$(field commit)" = "$SHA" ] || fail "installed, but the install record names commit $(field commit), not $SHA"
  say "Stage Dev $VERSION ($SHA) installed"
fi
