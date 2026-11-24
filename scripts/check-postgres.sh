#!/usr/bin/env bash
# check:postgres: the db and api suites with -tags postgres against a
# throwaway Postgres container (name stage-pgtest-<random>, label
# stage-pgtest=1, random 127.0.0.1 port, tmpfs data), always removed.
# Postgres is Docker-only: no Docker is a failure, never a skip.
#
# --if-changed (used by `mise run check`): run only when the branch changes
# db code vs the merge-base with origin/main, committed or uncommitted
# (including untracked files). The image is STAGE_PG_IMAGE (mise.toml [env]).
#
# Run from the repository root (mise does).
set -euo pipefail

cd "$(dirname "$0")/.."

watched=(server/internal/db docs/storage.md server/go.mod Dockerfile)

if [ "${1:-}" = "--if-changed" ]; then
  if base=$(git merge-base HEAD origin/main 2>/dev/null); then
    changed=$( {
      git diff --name-only "$base" -- "${watched[@]}"
      git ls-files --others --exclude-standard -- "${watched[@]}"
    } | sort -u)
    if [ -z "$changed" ]; then
      echo "check:postgres: skipped: no db changes vs merge-base ${base:0:12} with origin/main (watched: ${watched[*]})"
      exit 0
    fi
    echo "check:postgres: db changes vs merge-base ${base:0:12} with origin/main:"
    printf '%s\n' "$changed" | sed 's/^/  /'
  else
    echo "check:postgres: no merge-base with origin/main (missing ref?): running anyway"
  fi
elif [ $# -gt 0 ]; then
  echo "usage: $0 [--if-changed]" >&2
  exit 2
fi

: "${STAGE_PG_IMAGE:?STAGE_PG_IMAGE is unset (defined in mise.toml [env]; run via mise)}"

if ! command -v docker >/dev/null 2>&1; then
  echo "check:postgres: docker not found. Postgres checks need Docker (Postgres is Docker-only); install Docker and rerun." >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "check:postgres: Docker daemon not reachable (docker info failed). Start Docker and rerun." >&2
  exit 1
fi

name="stage-pgtest-$(LC_ALL=C tr -dc 'a-z0-9' </dev/urandom | head -c 10 || true)"
cleanup() { docker rm -f -v "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "check:postgres: starting $name ($STAGE_PG_IMAGE)"
docker run -d --rm --name "$name" --label stage-pgtest=1 \
  -p 127.0.0.1::5432 \
  --tmpfs /var/lib/postgresql \
  -e POSTGRES_PASSWORD=stage \
  "$STAGE_PG_IMAGE" \
  -c fsync=off -c synchronous_commit=off -c full_page_writes=off >/dev/null

ready=
for _ in $(seq 1 120); do
  if docker exec "$name" pg_isready -q -h 127.0.0.1 -U postgres >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.5
done
if [ -z "$ready" ]; then
  echo "check:postgres: $name not ready after 60s; logs:" >&2
  docker logs "$name" >&2 || true
  exit 1
fi

port=$(docker port "$name" 5432/tcp | head -n1)
port=${port##*:}
echo "check:postgres: ready on 127.0.0.1:$port: $(docker exec "$name" psql -U postgres -tAc 'select version()')"

export STAGE_TEST_DATABASE_URL="postgres://postgres:stage@127.0.0.1:$port/postgres?sslmode=disable"
echo "check:postgres: STAGE_TEST_DATABASE_URL=$STAGE_TEST_DATABASE_URL go test -tags postgres -count=1 ./internal/db/... ./internal/api/..."
(cd server && go test -tags postgres -count=1 ./internal/db/... ./internal/api/...)
echo "check:postgres: done; removing $name"
