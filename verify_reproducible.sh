#!/usr/bin/env bash
# shellcheck disable=SC2153 # Uppercase pins are assigned by reproducible.env.
set -euo pipefail
cd "$(dirname "$0")"

if invalid_line="$(grep -Env '^[[:space:]]*(#.*)?$|^[A-Z][A-Z0-9_]*=[A-Za-z0-9_./:@+-]+$' reproducible.env || true)" && \
   [[ -n "${invalid_line}" ]]; then
    echo "reproducible.env contains an invalid line:" >&2
    echo "${invalid_line}" >&2
    exit 1
fi

duplicates="$(awk -F= '/^[A-Z][A-Z0-9_]*=/{print $1}' reproducible.env | sort | uniq -d)"
if [[ -n "${duplicates}" ]]; then
    echo "reproducible.env contains duplicate keys: ${duplicates}" >&2
    exit 1
fi

set -a
# shellcheck disable=SC1091
. ./reproducible.env
set +a

required=(
    DOCKERFILE_SYNTAX
    BUILDKIT_REF
    BUILDX_VERSION
    BINFMT_REF
    GO_REF
    GO_VERSION
    TAMAGO_VERSION
    TAMAGO_COMMIT
    TAMAGO_GO_VERSION
    TAMAGO_GO_COMMIT
    GO_NET_VERSION
    GO_NET_COMMIT
    SOURCE_DATE_EPOCH
)
for name in "${required[@]}"; do
    if [[ -z "${!name:-}" ]]; then
        echo "missing reproducible input: ${name}" >&2
        exit 1
    fi
done

for name in DOCKERFILE_SYNTAX BUILDKIT_REF BINFMT_REF GO_REF; do
    value="${!name}"
    if [[ ! "${value}" =~ @sha256:[0-9a-f]{64}$ ]]; then
        echo "${name} is not pinned by sha256 digest" >&2
        exit 1
    fi
done

go_version="$(awk '$1 == "go" { print $2 }' go.mod)"
tamago_version="$(awk '
  $1 == "github.com/usbarmory/tamago" { print $2; exit }
  $1 == "require" && $2 == "github.com/usbarmory/tamago" { print $3; exit }
' go.mod)"
go_net_version="$(awk '$1 == "github.com/usbarmory/go-net" { print $2; exit }' go.mod)"

[[ "${go_version}" == "${GO_VERSION}" ]]
[[ "${tamago_version}" == "${TAMAGO_VERSION}" ]]
[[ "${go_net_version}" == "${GO_NET_VERSION}" ]]
[[ "${GO_NET_VERSION}" == *"-${GO_NET_COMMIT:0:12}" ]]
[[ "${TAMAGO_VERSION}" == *"-${TAMAGO_COMMIT}" ]]
[[ "${TAMAGO_GO_VERSION}" == "tamago-go${GO_VERSION}" ]]
[[ "${BUILDX_VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "${SOURCE_DATE_EPOCH}" == "0" ]]

grep -Fqx "# syntax=${DOCKERFILE_SYNTAX}" Dockerfile
grep -Fqx "ARG GO_REF=${GO_REF}" Dockerfile
grep -Fqx "ARG TAMAGO_GO_VERSION=${TAMAGO_GO_VERSION}" Dockerfile
grep -Fqx "ARG TAMAGO_GO_COMMIT=${TAMAGO_GO_COMMIT}" Dockerfile
grep -Fqx "ARG GO_NET_VERSION=${GO_NET_VERSION}" Dockerfile
grep -Fq "GO_REF: \${GO_REF:-${GO_REF}}" docker-compose.yml
grep -Fq "TAMAGO_GO_COMMIT: \${TAMAGO_GO_COMMIT:-${TAMAGO_GO_COMMIT}}" docker-compose.yml
grep -Fq "GO_NET_VERSION: \${GO_NET_VERSION:-${GO_NET_VERSION}}" docker-compose.yml

echo "Reproducible input pins are internally consistent."
