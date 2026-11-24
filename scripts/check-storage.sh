#!/usr/bin/env bash
# check:storage (mise run check): storage gates that always run.
#
# - Gate tests, by fixed name, anywhere under server/internal/db/...:
#   TestMigrationVersionParity (same migration versions in both engine dirs),
#   TestStepwiseMigrations (UpTo(v-1) + seed + Up(v) per migration),
#   TestDriverImportBoundary (driver imports only in their adapters).
#   A gate not written yet is reported as "not present yet", never silently.
# - The storage adapter packages under server/internal/db, once they exist.
# - The Postgres pin: the image in examples/postgres/compose.yaml is
#   STAGE_PG_IMAGE (mise.toml [env]), the tag check:postgres runs.
#
# Run from the repository root (mise does). Postgres suites: check:postgres.
set -euo pipefail

cd "$(dirname "$0")/../server"

: "${STAGE_PG_IMAGE:?STAGE_PG_IMAGE is unset (defined in mise.toml [env]; run via mise)}"
compose=../examples/postgres/compose.yaml
images=$(sed -n -E 's/^[[:space:]]+image:[[:space:]]*["'\'']?([^"'\''[:space:]#]+).*/\1/p' "$compose")
if [ "$images" != "$STAGE_PG_IMAGE" ]; then
  echo "check:storage: $compose must use exactly one image, STAGE_PG_IMAGE ($STAGE_PG_IMAGE); found: ${images:-none}" >&2
  exit 1
fi
echo "check:storage: Postgres pin: ${compose#../} uses $STAGE_PG_IMAGE"

gates=(TestMigrationVersionParity TestStepwiseMigrations TestDriverImportBoundary)
pkgs=(store sqlite postgres storetest)

run_re="^($(IFS='|'; echo "${gates[*]}"))\$"
echo "check:storage: go test -count=1 -run '$run_re' ./internal/db/..."
out=$(mktemp)
trap 'rm -f "$out"' EXIT
status=0
go test -count=1 -v -run "$run_re" ./internal/db/... >"$out" 2>&1 || status=$?
cat "$out"
if [ "$status" -ne 0 ]; then
  echo "check:storage: gate tests failed" >&2
  exit "$status"
fi

summary=()
for g in "${gates[@]}"; do
  if grep -q "^=== RUN   $g\$" "$out"; then
    summary+=("gate $g: ran")
  else
    summary+=("gate $g: not present yet")
  fi
done

present=()
for p in "${pkgs[@]}"; do
  if [ -d "internal/db/$p" ]; then
    present+=("./internal/db/$p/...")
    summary+=("package internal/db/$p: ran")
  else
    summary+=("package internal/db/$p: not present yet")
  fi
done
if [ "${#present[@]}" -gt 0 ]; then
  echo "check:storage: go test ${present[*]}"
  go test "${present[@]}"
fi

echo "check:storage summary:"
printf '  %s\n' "${summary[@]}"
