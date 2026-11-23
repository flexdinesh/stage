#!/usr/bin/env bash
# Builds Stage's assets (docs/release.md): one version and commit for the
# desktop app, its bundled CLI/server and the headless CLI/server.
#
#   scripts/release.sh dist      developer build of the working tree:
#                                identity stage-dev, version
#                                0.0.0-dev.<branch>; TARGETS=host (default)
#                                or all; out STAGE_DIST_OUT
#                                (.scratch/dist/<version>)
#   scripts/release.sh release   release build of HEAD from a clean export:
#                                version from `next`, identity stage; out
#                                STAGE_RELEASE_OUT (.scratch/release/v<version>)
#   scripts/release.sh next      print the next release version: MAJOR.MINOR
#                                from the VERSION file, patch from git tags
#                                (svu)
#   scripts/release.sh verify D  verify an asset directory (+ smoke)
#
# Nothing here uploads, tags or installs. mise run dist|release|release:next|release:verify
# call it with the pinned toolchain.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BI=github.com/flexdinesh/stage/server/internal/buildinfo
WORK=$(mktemp -d "${TMPDIR:-/tmp}/stage-release.XXXXXX")
trap 'rm -rf "$WORK"' EXIT

die() { echo "release.sh: $*" >&2; exit 1; }

# The release tool is built on first use (`next` needs only git and svu).
tool() {
  [ -x "$WORK/release" ] || go build -C "$ROOT/tools/release" -o "$WORK/release" .
  "$WORK/release" "$@"
}

# next_version prints the next release version X.Y.Z: MAJOR.MINOR from the
# VERSION file; X.Y.0 when no vX.Y.* tag exists, else svu's next patch of
# the highest one. It refuses a VERSION below the highest release tag and a
# HEAD that is already released.
next_version() {
  local mm top v num='(0|[1-9][0-9]*)'
  [ -f "$ROOT/VERSION" ] || die "VERSION file is missing"
  mm=$(tr -d '[:space:]' <"$ROOT/VERSION")
  [[ "$mm" =~ ^$num\.$num$ ]] || die "VERSION must be MAJOR.MINOR (like 0.4), not '$mm'"
  local rel="^v$num\.$num\.$num\$"
  v=$(git -C "$ROOT" tag --points-at HEAD --list 'v*' | grep -E "$rel" | head -n1 || true)
  [ -z "$v" ] || die "HEAD is already released as $v"
  top=$(git -C "$ROOT" tag --list 'v*' | grep -E "$rel" | sed 's/^v//' | sort -t. -k1,1n -k2,2n -k3,3n | tail -n1 || true)
  if [ -n "$top" ]; then
    local tmaj=${top%%.*} tmin mmaj=${mm%.*} mmin=${mm#*.}
    tmin=${top#*.} tmin=${tmin%.*}
    if [ "$tmaj" -gt "$mmaj" ] || { [ "$tmaj" -eq "$mmaj" ] && [ "$tmin" -gt "$mmin" ]; }; then
      die "VERSION $mm is below the latest release v$top"
    fi
  fi
  if git -C "$ROOT" tag --list "v$mm.*" | grep -E "^v${mm//./\\.}\.$num" >/dev/null; then
    command -v svu >/dev/null || die "svu not found (mise.toml pins it)"
    v=$(cd "$ROOT" && svu patch --tag.pattern "v$mm.*")
    v=${v#v}
  else
    v=$mm.0
  fi
  [[ "$v" =~ ^$num\.$num\.$num$ ]] || die "computed version '$v' is not X.Y.Z"
  echo "$v"
}

host_os() { case "$(uname -s)" in Linux) echo linux ;; Darwin) echo darwin ;; *) die "unsupported OS $(uname -s)" ;; esac; }
host_arch() { case "$(uname -m)" in x86_64 | amd64) echo amd64 ;; aarch64 | arm64) echo arm64 ;; *) die "unsupported CPU $(uname -m)" ;; esac; }

# build SRC OUT: builds into OUT from the tree at SRC, using IDENTITY,
# VERSION, COMMIT, DATE, CHANNEL, CLI_TARGETS, DESKTOP_TARGETS, OMIT and
# DESKTOP_FLAGS.
build() {
  local src=$1 out=$2 name appid t os arch platforms=()
  case "$IDENTITY" in
    stage) name="Stage" appid=io.github.flexdinesh.stage.desktop ;;
    stage-dev) name="Stage Dev" appid=io.github.flexdinesh.stage-dev.desktop ;;
    *) die "identity $IDENTITY" ;;
  esac
  # The build stamp (docs/release.md#build-stamp) is how verification reads
  # binaries it cannot run.
  local stamp="stage-build/1 identity=$IDENTITY version=$VERSION commit=$COMMIT;"
  echo "==> $name $VERSION ($IDENTITY, ${COMMIT}) from $src"
  echo "    cli: ${CLI_TARGETS[*]}; desktop: ${DESKTOP_TARGETS[*]:-none}"

  # Web assets: built once, embedded in every CLI/server binary. Product
  # packages only (the browser host and the desktop UI MyGo builds below);
  # apps/website is never installed or built here.
  (cd "$src" && pnpm install --frozen-lockfile --filter '@stage/web...' --filter '@stage/desktop...' && pnpm --filter @stage/web build)
  find "$src/server/internal/webui/dist" -mindepth 1 ! -name .gitkeep -delete
  cp -R "$src/apps/web/dist/." "$src/server/internal/webui/dist/"

  # One CLI/server per target: the headless artifact and the desktop's
  # bundled CLI are the same file.
  for t in "${CLI_TARGETS[@]}"; do
    os=${t%-*} arch=${t#*-}
    mkdir -p "$WORK/cli/$t"
    (cd "$src/server" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
      -ldflags "-s -w -X $BI.Identity=$IDENTITY -X $BI.Version=$VERSION -X $BI.Commit=$COMMIT -X '$BI.Stamp=$stamp'" \
      -o "$WORK/cli/$t/stage" ./cmd/stage)
  done

  if [ "${#DESKTOP_TARGETS[@]}" -gt 0 ]; then
    local app="$src/desktop/app"
    rm -rf "$app/resources" "$app/build"
    mkdir -p "$app/resources"
    cp "$src/LICENSE" "$app/resources/LICENSE"
    for t in "${DESKTOP_TARGETS[@]}"; do
      case "$t" in
        linux-*)
          mkdir -p "$app/resources/$t/bin"
          cp "$WORK/cli/$t/stage" "$app/resources/$t/bin/stage"
          platforms+=("linux/${t#linux-}")
          ;;
        darwin-universal)
          # MyGo merges the darwin pair into one universal binary.
          for a in arm64 amd64; do
            mkdir -p "$app/resources/darwin-$a/bin"
            cp "$WORK/cli/darwin-$a/stage" "$app/resources/darwin-$a/bin/stage"
          done
          platforms+=("darwin/universal")
          ;;
      esac
    done
    local list
    list=$(IFS=,; echo "${platforms[*]}")
    local ldflags="-X 'main.appName=$name' -X main.appIdentifier=$appid -X main.appIdentity=$IDENTITY -X 'main.appBuild=$stamp'"
    (cd "$app" && export IDENTITY STAGE_VERSION="$VERSION" MYGO_LDFLAGS="$ldflags" &&
      go tool mygo build -platform "$list" ${DESKTOP_FLAGS[@]+"${DESKTOP_FLAGS[@]}"})
  fi

  local omit_args=()
  for o in ${OMIT[@]+"${OMIT[@]}"}; do omit_args+=(-omit "$o"); done
  tool assemble -out "$out" -cli "$WORK/cli" -desktop "$src/desktop/app/build" \
    -license "$src/LICENSE" -readme "$src/README.md" \
    -version "$VERSION" -commit "$COMMIT" -identity "$IDENTITY" -channel "$CHANNEL" -date "$DATE" \
    -cli-targets "$(IFS=,; echo "${CLI_TARGETS[*]}")" \
    -desktop-targets "$(IFS=,; echo "${DESKTOP_TARGETS[*]:-}")" \
    ${omit_args[@]+"${omit_args[@]}"}
  tool verify -smoke "$out"
}

cd "$ROOT"
mode=${1:-}
DESKTOP_FLAGS=()
OMIT=()

case "$mode" in
  dist)
    IDENTITY=stage-dev CHANNEL=dev
    COMMIT=$(git rev-parse HEAD)
    if [ -n "$(git status --porcelain)" ]; then COMMIT="$COMMIT-dirty"; fi
    VERSION=$(tool version -mode dev -branch "$(git rev-parse --abbrev-ref HEAD)" -commit "$COMMIT")
    DATE=$(git show -s --format=%cI HEAD)
    case "${TARGETS:-host}" in
      host)
        if [ "$(host_os)" = darwin ]; then
          CLI_TARGETS=(darwin-arm64 darwin-amd64) DESKTOP_TARGETS=(darwin-universal)
        else
          CLI_TARGETS=("linux-$(host_arch)") DESKTOP_TARGETS=("linux-$(host_arch)")
        fi
        ;;
      all)
        CLI_TARGETS=(linux-amd64 linux-arm64 darwin-arm64 darwin-amd64)
        DESKTOP_TARGETS=(linux-amd64 linux-arm64 darwin-universal)
        ;;
      *) die "TARGETS must be host or all, not $TARGETS" ;;
    esac
    # Developer builds skip the DMG; on Linux a macOS app is cross-built and
    # unsigned (never native-validated here).
    DESKTOP_FLAGS=(-skip-dmg)
    OUT=${STAGE_DIST_OUT:-$ROOT/.scratch/dist/$VERSION}
    # Only a previous dist (it has a manifest) is replaced.
    if [ -d "$OUT" ] && [ -n "$(ls -A "$OUT")" ]; then
      [ -f "$OUT/stage-release.json" ] || die "$OUT is not empty and holds no earlier build"
      rm -rf "$OUT"
    fi
    build "$ROOT" "$OUT"
    echo "version $VERSION"
    echo "out $OUT"
    ;;
  release)
    VERSION=$(next_version)
    VERSION=$(tool version -mode release -version "$VERSION")
    IDENTITY=stage CHANNEL=stable
    git diff --quiet HEAD -- || die "the working tree has uncommitted changes; release builds HEAD exactly"
    COMMIT=$(git rev-parse HEAD)
    if tag=$(git rev-parse -q --verify "refs/tags/v$VERSION^{commit}"); then
      [ "$tag" = "$COMMIT" ] || die "tag v$VERSION exists on $tag, not HEAD ($COMMIT)"
    fi
    DATE=$(git show -s --format=%cI HEAD)
    CLI_TARGETS=(linux-amd64 linux-arm64 darwin-arm64 darwin-amd64)
    DESKTOP_TARGETS=(linux-amd64 linux-arm64)
    if [ "$(host_os)" = darwin ] && [ -n "${MACOS_SIGNING_IDENTITY:-}" ] && [ -n "${MACOS_NOTARY_PROFILE:-}" ]; then
      DESKTOP_TARGETS+=(darwin-universal)
    else
      OMIT=("desktop/darwin/universal=needs a Mac with Developer ID signing and notarization (MACOS_SIGNING_IDENTITY, MACOS_NOTARY_PROFILE)")
    fi
    OUT=${STAGE_RELEASE_OUT:-$ROOT/.scratch/release/v$VERSION}
    if [ -e "$OUT" ] && [ -n "$(ls -A "$OUT")" ]; then die "$OUT is not empty"; fi
    # Only tracked files of HEAD: no .env, .scratch, databases or keys.
    mkdir -p "$WORK/src"
    git archive --format=tar HEAD | tar -x -C "$WORK/src"
    build "$WORK/src" "$OUT"
    echo "version $VERSION"
    echo "out $OUT"
    ;;
  next)
    next_version
    ;;
  verify)
    [ -n "${2:-}" ] || die "usage: release.sh verify DIR"
    tool verify -smoke "$2"
    ;;
  *)
    die "usage: release.sh dist|release|next|verify DIR"
    ;;
esac
