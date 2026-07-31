# syntax=docker/dockerfile:1@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89
ARG GO_REF=docker.io/library/golang:1.26.4-bookworm@sha256:b305420a68d0f229d91eb3b3ed9e519fcf2cf5461da4bef997bf927e8c0bfd2b

FROM ${GO_REF} AS builder
ARG TARGETARCH
ARG SOURCE_DATE_EPOCH=0
ARG TAMAGO_GO_VERSION=tamago-go1.26.4
ARG TAMAGO_GO_COMMIT=c6c7dc072c5248c9b668d4ad0af1d7653eb3cfa5
ARG GO_NET_VERSION=v0.0.0-20260714134120-c2c964e7084c
ENV SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY kernel/ ./kernel/
COPY overlays/ ./overlays/
COPY tools/ ./tools/

RUN --mount=type=cache,target=/root/.cache/tamago-go \
    set -eux; \
    root="/root/.cache/tamago-go/${TAMAGO_GO_VERSION}"; \
    marker="${root}/.spr-commit"; \
    if ! test -x "${root}/bin/go" || \
       ! test -f "${marker}" || \
       ! grep -Fqx "${TAMAGO_GO_COMMIT}" "${marker}"; then \
      rm -rf "${root}"; \
      git init "${root}"; \
      git -C "${root}" remote add origin https://github.com/usbarmory/tamago-go.git; \
      git -C "${root}" fetch --depth=1 origin "${TAMAGO_GO_COMMIT}"; \
      git -C "${root}" checkout --detach FETCH_HEAD; \
      test "$(git -C "${root}" rev-parse HEAD)" = "${TAMAGO_GO_COMMIT}"; \
      cd "${root}/src"; \
      ./make.bash; \
      printf '%s\n' "${TAMAGO_GO_COMMIT}" >"${marker}"; \
    fi; \
    test "$(git -C "${root}" rev-parse HEAD)" = "${TAMAGO_GO_COMMIT}"; \
    test "$("${root}/bin/go" env GOVERSION)" = "go1.26.4"

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/tamago-go \
    set -eux; \
    test "${TARGETARCH}" = arm64; \
    go test ./...; \
    test "$(go list -m -f '{{.Version}}' github.com/usbarmory/go-net)" = "${GO_NET_VERSION}"; \
    TAMAGO_DIR="$(go list -m -f '{{.Dir}}' github.com/usbarmory/tamago)"; \
    go run ./tools/prepare_tamago.go \
      -tamago-dir "${TAMAGO_DIR}" \
      -out-dir /tmp/tamago-arm64; \
    cp go.mod /tmp/kernel.mod; \
    cp go.sum /tmp/kernel.sum; \
    go mod edit -modfile=/tmp/kernel.mod \
      -replace=github.com/usbarmory/tamago=/tmp/tamago-arm64; \
    TAMAGO="$(go tool -n github.com/usbarmory/tamago/cmd/tamago)"; \
    GOOS=tamago GOOSPKG=github.com/usbarmory/tamago GOARCH=arm64 \
      "${TAMAGO}" build \
        -modfile=/tmp/kernel.mod \
        -buildvcs=false \
        -trimpath \
        -tags=tamago \
        -ldflags="-T 0x80010000 -R 0x1000 -s -w -buildid=" \
        -o /tamago-kernel.elf \
        ./kernel; \
    go run ./tools/elf2raw.go \
      -in /tamago-kernel.elf \
      -out /tamago-kernel \
      -base 0x80000000

FROM scratch AS reproducibility
COPY --from=builder /tamago-kernel /tamago-kernel.elf /artifacts/
COPY .krun_vm.json /artifacts/.krun_vm.json

FROM scratch AS kernel
LABEL org.opencontainers.image.source="https://github.com/spr-networks/spr-tamago-demo"
COPY --from=builder /tamago-kernel /tamago-kernel
COPY --from=builder /tamago-kernel.elf /unused
COPY .krun_vm.json /.krun_vm.json
CMD ["/unused"]
