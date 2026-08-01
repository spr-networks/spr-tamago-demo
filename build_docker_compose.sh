#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

set -a
# shellcheck disable=SC1091
. ./reproducible.env
set +a

output=(--load)
extra=()
for arg in "$@"; do
    case "${arg}" in
        --load)
            output=(--load)
            ;;
        --push)
            output=(--output "type=registry,rewrite-timestamp=true")
            ;;
        *)
            extra+=("${arg}")
            ;;
    esac
done

docker buildx build \
    "${output[@]}" \
    --platform linux/arm64 \
    --target kernel \
    --tag "${SPR_TAMAGO_IMAGE:-ghcr.io/spr-networks/spr-tamago-demo:latest}" \
    --build-arg "NODE_REF=${NODE_REF}" \
    --build-arg "GO_REF=${GO_REF}" \
    --build-arg "SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}" \
    --build-arg "YARN_VERSION=${YARN_VERSION}" \
    --build-arg "PLUGIN_UI_COMMIT=${PLUGIN_UI_COMMIT}" \
    --build-arg "TAMAGO_VERSION=${TAMAGO_VERSION}" \
    --build-arg "TAMAGO_GO_VERSION=${TAMAGO_GO_VERSION}" \
    --build-arg "TAMAGO_GO_COMMIT=${TAMAGO_GO_COMMIT}" \
    --build-arg "GO_NET_VERSION=${GO_NET_VERSION}" \
    --provenance=false \
    --sbom=false \
    ${extra[@]+"${extra[@]}"} \
    .
