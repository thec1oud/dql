#!/usr/bin/env bash
# Lets the replica stream from the primary.
set -euo pipefail
echo "host replication all all scram-sha-256" >> "$PGDATA/pg_hba.conf"
