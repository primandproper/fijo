#!/usr/bin/env bash
set -euo pipefail

# Render the per-domain queriers sqlc.yaml declares, after checking the queries
# against the schema.
# Usage: querier.sh <container_runner> <sql_generator_image> <sql_generator_command>
#
# compile parses every query against the DDL and is what catches a column that
# does not exist; vet applies sqlc's own rules; generate writes the Go. They run
# in that order so a broken query fails before anything is written, and the
# generated directories are cleared first so a query that was deleted does not
# leave its file behind.

CONTAINER_RUNNER="${1:-docker}"
SQL_GENERATOR_IMAGE="${2:-sqlc/sqlc:1.26.0}"
SQL_GENERATOR="${3}"

"${CONTAINER_RUNNER}" pull --quiet "${SQL_GENERATOR_IMAGE}"

# shellcheck disable=SC2086 # SQL_GENERATOR is a command line, not one word.
${SQL_GENERATOR} compile --no-remote
# shellcheck disable=SC2086
${SQL_GENERATOR} vet --no-remote

find internal/domain -type d -name generated -print0 | while IFS= read -r -d '' dir; do
  rm -f "${dir}"/*.go
done

# shellcheck disable=SC2086
${SQL_GENERATOR} generate --no-remote
