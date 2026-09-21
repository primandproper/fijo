#!/usr/bin/env bash
set -euo pipefail

# Generate the Go constants for every environment variable that can override the
# application's configuration.
# Usage: envvars.sh <module_path>
#   e.g. envvars.sh github.com/primandproper/template-go
#
# The generator prints an advisory line to stderr naming the dependency modules
# it discovered and did not parse. That is expected: only primitives-go supplies
# configuration structs here, so everything else is correctly walked past.

MODULE_PATH="${1:?missing module path}"

go run "${MODULE_PATH}/cmd/tools/codegen/envvars"
