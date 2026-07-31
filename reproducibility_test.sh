#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

for command in docker jq; do
    if ! command -v "${command}" >/dev/null 2>&1; then
        echo "missing required command: ${command}" >&2
        exit 1
    fi
done

./verify_reproducible.sh

set -a
# shellcheck disable=SC1091
. ./reproducible.env
set +a

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/spr-tamago-repro.XXXXXX")"
created_builder=""
cleanup() {
    if [[ -n "${created_builder}" ]]; then
        docker buildx rm "${created_builder}" >/dev/null 2>&1 || true
    fi
    rm -rf -- "${work_dir}"
}
trap cleanup EXIT

builder=()
if [[ -n "${BUILDX_BUILDER:-}" ]]; then
    builder=(--builder "${BUILDX_BUILDER}")
elif [[ "$(docker buildx inspect --format '{{.Driver}}' 2>/dev/null || true)" != "docker-container" ]]; then
    created_builder="spr-tamago-repro-${RANDOM}-$$"
    docker buildx create \
        --name "${created_builder}" \
        --driver docker-container \
        --driver-opt "image=${BUILDKIT_REF}" >/dev/null
    docker buildx inspect --builder "${created_builder}" --bootstrap >/dev/null
    builder=(--builder "${created_builder}")
fi

build_once() {
    local name="$1"
    local output="${work_dir}/${name}"

    docker buildx build \
        ${builder[@]+"${builder[@]}"} \
        --platform linux/arm64 \
        --target reproducibility \
        --no-cache \
        --build-arg "GO_REF=${GO_REF}" \
        --build-arg "SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}" \
        --build-arg "TAMAGO_GO_VERSION=${TAMAGO_GO_VERSION}" \
        --build-arg "TAMAGO_GO_COMMIT=${TAMAGO_GO_COMMIT}" \
        --build-arg "GO_NET_VERSION=${GO_NET_VERSION}" \
        --provenance=false \
        --sbom=false \
        --output "type=oci,dest=${output},tar=false,rewrite-timestamp=true" \
        "${@:2}" \
        .
}

echo "Building clean ARM64 artifact set A"
build_once a "$@"
digest_a="$(jq -er '.manifests[0].digest' "${work_dir}/a/index.json")"
echo "Building clean ARM64 artifact set B"
build_once b "$@"
digest_b="$(jq -er '.manifests[0].digest' "${work_dir}/b/index.json")"

if [[ "${digest_a}" != "${digest_b}" ]]; then
    echo "non-reproducible OCI manifests: ${digest_a} != ${digest_b}" >&2
    exit 1
fi

echo "Reproducible OCI manifest: ${digest_a}"
