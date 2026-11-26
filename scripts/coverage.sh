#!/usr/bin/env bash
# Runs every workspace module's tests and fails unless hand-written code is at
# 100% statement coverage. Generated code is excluded: protobuf's (*.pb.go)
# and the SQLite and T-SQL grammars ANTLR generates (querysources/sqlite/grammar
# and querysources/sqlserver/grammar).
#
# The pg-live tests run against a real Postgres 16 server named by
# DQL_TEST_POSTGRES_URL, a role that may create roles and databases (such as
# postgres://postgres@localhost:5432/postgres). Each test package creates and
# drops its own databases there.
#
# The routing tests also need a streaming replica of that server, named by
# DQL_TEST_POSTGRES_REPLICA_URL; conformance/replica/compose.yaml starts
# both. Without it they are skipped, and coverage falls short.
#
# The mysql-live tests run against a MySQL 8.4 server named by
# DQL_TEST_MYSQL_URL, a go-sql-driver/mysql DSN without a database for a user
# that may create and drop databases (such as root:root@tcp(localhost:3306)/);
# conformance/mysql/compose.yaml starts one, with a GTID replica of it named
# by DQL_TEST_MYSQL_REPLICA_URL (root:root@tcp(localhost:3308)/), which the
# routing scenario needs on MySQL. They also run against a MariaDB
# server named by DQL_TEST_MARIADB_URL, in the same form (such as
# root:root@tcp(localhost:3307)/), which the same compose file starts.
set -euo pipefail
cd "$(dirname "$0")/.."

: "${DQL_TEST_POSTGRES_URL:?set DQL_TEST_POSTGRES_URL to a Postgres 16 server URL; the pg-live tests need one}"
: "${DQL_TEST_MYSQL_URL:?set DQL_TEST_MYSQL_URL to a MySQL 8.4 server DSN; the mysql-live tests need one}"
: "${DQL_TEST_MYSQL_REPLICA_URL:?set DQL_TEST_MYSQL_REPLICA_URL to a replica of DQL_TEST_MYSQL_URL; the MySQL routing scenario needs one}"
: "${DQL_TEST_MARIADB_URL:?set DQL_TEST_MARIADB_URL to a MariaDB 10.11 or newer server DSN; the mysql-live tests need one}"

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

echo "mode: set" > "$out/all.out"
for mod in $(go work edit -json | sed -n 's/.*"DiskPath": "\(.*\)".*/\1/p'); do
  (cd "$mod" && go test -count=1 -coverprofile="$out/mod.out" ./...)
  if [ -f "$out/mod.out" ]; then
    tail -n +2 "$out/mod.out" | grep -v -e '\.pb\.go:' -e '/querysources/sqlite/grammar/' -e '/querysources/sqlserver/grammar/' >> "$out/all.out" || true
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
