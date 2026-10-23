#!/usr/bin/env bash
# Runs every workspace module's tests and fails unless hand-written code is at
# 100% statement coverage. Generated protobuf code (*.pb.go) is excluded.
#
# The pg-live tests run against a real Postgres 16 server named by
# DQL_TEST_POSTGRES_URL, a role that may create roles and databases (such as
# postgres://postgres@localhost:5432/postgres). Each test package creates and
# drops its own databases there.
set -euo pipefail
cd "$(dirname "$0")/.."

: "${DQL_TEST_POSTGRES_URL:?set DQL_TEST_POSTGRES_URL to a Postgres 16 server URL; the pg-live tests need one}"

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

echo "mode: set" > "$out/all.out"
for mod in $(go work edit -json | sed -n 's/.*"DiskPath": "\(.*\)".*/\1/p'); do
  (cd "$mod" && go test -count=1 -coverprofile="$out/mod.out" ./...)
  if [ -f "$out/mod.out" ]; then
    tail -n +2 "$out/mod.out" | grep -v '\.pb\.go:' >> "$out/all.out" || true
    rm "$out/mod.out"
  fi
done

if [ "$(wc -l < "$out/all.out")" -eq 1 ]; then
  echo "coverage: no hand-written statements"
  exit 0
fi

total=$(go tool cover -func="$out/all.out" | awk '/^total:/ {print $3}')
go tool cover -func="$out/all.out" | awk '$3 != "100.0%"'
echo "coverage: $total"
[ "$total" = "100.0%" ] || { echo "coverage below 100%" >&2; exit 1; }
