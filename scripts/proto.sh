#!/usr/bin/env bash
set -euo pipefail

# Generate the Go and TypeScript bindings for Fijo's protos.
# Usage: proto.sh <module_path> [golang|typescript|all]
#
# The platform's schemas are not copied here. primitives-go ships filtering.proto
# and platform-go ships identity.proto and signin.proto inside the published
# modules, so go.mod pins which version Fijo builds against and protoc reads
# them off the module cache. Go links against the bindings those modules already
# generated, through the -M mappings, because the rules those types carry are
# server-side and a second copy of one can be wrong in a way nothing reports.
# TypeScript generates them, from the same files: there a message is a data
# class and there is nothing to restate.

MODULE_PATH="${1:?missing module path}"
TARGET="${2:-all}"

PRIMITIVES_DIR="$(go list -m -f '{{.Dir}}' github.com/primandproper/primitives-go/v2)"
PLATFORM_DIR="$(go list -m -f '{{.Dir}}' github.com/primandproper/platform-go/v14)"

FILTERING_PATH="${PRIMITIVES_DIR}/filtering/proto"
IDENTITY_PATH="${PLATFORM_DIR}/identity/proto"
SIGNIN_PATH="${PLATFORM_DIR}/authentication/signin/proto"

FILTERING_PROTO="primandproper/platform/filtering/v1/filtering.proto"
IDENTITY_PROTO="primandproper/platform/identity/v1/identity.proto"
SIGNIN_PROTO="primandproper/platform/signin/v1/signin.proto"

FIJO_PROTOS=()
while IFS= read -r file; do
  FIJO_PROTOS+=("${file}")
done < <(find proto -name '*.proto' -type f | sort)

if [ "${#FIJO_PROTOS[@]}" -eq 0 ]; then
  echo "proto.sh: no .proto files under proto/" >&2
  exit 1
fi

# protoc itself is pinned by version: its version lands in every generated
# header, so a different one is a diff in files nobody edited.
REQUIRED_PROTOC="libprotoc ${PROTOC_VERSION:-33.1}"
if [ "$(protoc --version)" != "${REQUIRED_PROTOC}" ]; then
  echo "proto.sh: need ${REQUIRED_PROTOC}, have $(protoc --version)" >&2
  exit 1
fi

# The module cache is read-only, which protoc tolerates for reading.
INCLUDES=(--proto_path proto --proto_path "${FILTERING_PATH}" --proto_path "${IDENTITY_PATH}" --proto_path "${SIGNIN_PATH}")

generate_golang() {
  local out="internal/grpc/fijopb"
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp}"' RETURN

  # The plugins are the versions go.mod pins as tools, built here rather than
  # taken from PATH, so two machines generate the same header and CI's diff
  # check compares like with like.
  go build -o "${tmp}/bin/protoc-gen-go" google.golang.org/protobuf/cmd/protoc-gen-go
  go build -o "${tmp}/bin/protoc-gen-go-grpc" google.golang.org/grpc/cmd/protoc-gen-go-grpc

  protoc \
    --plugin="protoc-gen-go=${tmp}/bin/protoc-gen-go" \
    --plugin="protoc-gen-go-grpc=${tmp}/bin/protoc-gen-go-grpc" \
    --go_out="${tmp}" --go-grpc_out="${tmp}" \
    --go_opt=module="${MODULE_PATH}" --go-grpc_opt=module="${MODULE_PATH}" \
    --go_opt="M${FILTERING_PROTO}=github.com/primandproper/primitives-go/v2/filtering/filteringpb" \
    --go-grpc_opt="M${FILTERING_PROTO}=github.com/primandproper/primitives-go/v2/filtering/filteringpb" \
    --go_opt="M${IDENTITY_PROTO}=github.com/primandproper/platform-go/v14/identity/identitypb" \
    --go-grpc_opt="M${IDENTITY_PROTO}=github.com/primandproper/platform-go/v14/identity/identitypb" \
    --go_opt="M${SIGNIN_PROTO}=github.com/primandproper/platform-go/v14/authentication/signin/signinpb" \
    --go-grpc_opt="M${SIGNIN_PROTO}=github.com/primandproper/platform-go/v14/authentication/signin/signinpb" \
    "${INCLUDES[@]}" \
    "${FIJO_PROTOS[@]}"

  rm -rf "${out}"
  mkdir -p "$(dirname "${out}")"
  mv "${tmp}/${out}" "${out}"
}

generate_typescript() {
  local out="frontend/packages/api-client/src/generated"
  local plugin="frontend/node_modules/.bin/protoc-gen-ts_proto"

  if [ ! -x "${plugin}" ]; then
    echo "proto.sh: ${plugin} is missing; run 'npm install' in frontend/ first" >&2
    exit 1
  fi

  rm -rf "${out}"
  mkdir -p "${out}"

  protoc \
    --plugin="protoc-gen-ts_proto=${plugin}" \
    --ts_proto_out="${out}" \
    --ts_proto_opt=outputServices=grpc-js \
    --ts_proto_opt=esModuleInterop=true \
    --ts_proto_opt=importSuffix=.js \
    "${INCLUDES[@]}" \
    "${FIJO_PROTOS[@]}" \
    "${FILTERING_PATH}/${FILTERING_PROTO}" \
    "${IDENTITY_PATH}/${IDENTITY_PROTO}" \
    "${SIGNIN_PATH}/${SIGNIN_PROTO}"
}

case "${TARGET}" in
  golang) generate_golang ;;
  typescript) generate_typescript ;;
  all) generate_golang; generate_typescript ;;
  *) echo "proto.sh: unknown target ${TARGET}" >&2; exit 1 ;;
esac
