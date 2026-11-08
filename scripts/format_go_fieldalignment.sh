#!/usr/bin/env bash
set -uo pipefail

# Format Go field alignment
# Usage: format_go_fieldalignment.sh
#
# fieldalignment -fix can need more than one pass: reordering one struct can
# change the size of another that embeds it. It exits 3 when it reported (and
# fixed) something, so that is the one exit worth another pass. Anything else
# is the tool failing — a toolchain it cannot read, a package that does not
# compile — and retrying it would loop forever, so it fails instead, and so
# does a run that is still finding things after MAX_PASSES.
#
# Generated packages (the sqlc queriers, the protobuf bindings) are left out:
# their layout is the generator's, and the next generation would undo a fix.

MAX_PASSES=10

# A read loop rather than mapfile: macOS ships bash 3.2.
packages=()
while IFS= read -r package; do
  packages+=("${package}")
done < <(go list ./... | grep -v -e '/generated$' -e '/fijopb$')

for ((pass = 1; pass <= MAX_PASSES; pass++)); do
  output="$(go tool fieldalignment -fix "${packages[@]}" 2>&1)"
  status=$?

  case "${status}" in
    0) exit 0 ;;
    3) continue ;;
    *)
      echo "fieldalignment failed (exit ${status}):" >&2
      echo "${output}" | head -20 >&2
      exit "${status}"
      ;;
  esac
done

echo "fieldalignment was still reordering after ${MAX_PASSES} passes:" >&2
echo "${output}" | head -20 >&2
exit 1
