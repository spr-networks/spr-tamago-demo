# Reproducible builds

The kernel is built from content-addressed container inputs and
checksum-verified Go modules. The canonical pins live in
[`reproducible.env`](reproducible.env), and
[`verify_reproducible.sh`](verify_reproducible.sh) rejects drift between that
file, `go.mod`, the Dockerfile, and Compose.

## Required environment

- Docker with the Buildx plugin
- the Buildx release pinned by `BUILDX_VERSION`
- the BuildKit image pinned by `BUILDKIT_REF`
- ARM64 execution through native ARM64 or the binfmt image pinned by
  `BINFMT_REF`
- `bash`, `git`, `jq`, and network access to Docker Hub, GitHub, the Yarn
  registry, and the Go module proxy for the first cold build

The Dockerfile pins the Node and Go builder images by digest. `frontend/yarn.lock`
pins the SPR Plugin UI SDK and every frontend package; the SDK git dependency is
also fixed to `PLUGIN_UI_COMMIT`. `go.mod` and `go.sum` pin
and authenticate TamaGo, `usbarmory/go-net`, and all transitive Go modules.
`GO_NET_VERSION` and `GO_NET_COMMIT` record the exact official VirtIO-network
source selected for the kernel. The
TamaGo compiler wrapper normally shallow-clones a mutable tag; this build
pre-installs its toolchain from the exact `TAMAGO_GO_COMMIT` instead and checks
the checkout before compiling the kernel.

The frontend stage turns the React application into one inline `index.html`,
which is embedded into the kernel with `go:embed`. Builds set
`SOURCE_DATE_EPOCH=0`, omit Go build IDs and VCS paths, disable
BuildKit's time-varying provenance envelope, and ask the OCI exporter to
rewrite layer timestamps. The delivered programs are static ARM64 binaries;
the kernel image is `scratch` and contains no Linux userspace.

## Verify locally

Validate all pins and source-level tests:

```sh
./test.sh
```

Build the complete artifact set twice with clean Dockerfile layers and compare
the resulting OCI manifest digests:

```sh
./reproducibility_test.sh
```

Cache mounts are permitted for downloaded modules, the pinned TamaGo
toolchain, and compiler objects. Every Dockerfile instruction still executes
twice because the test uses `--no-cache`; whether a cache mount is reused or
rebuilt, it cannot change the exported artifact bytes without causing the
manifest comparison to fail.

To create locally loaded release images:

```sh
./build_docker_compose.sh
```

The `main` GitHub Actions workflow performs the same checks and publishes
an immutable `sha-<commit>` tag to GHCR. The `latest` tag is an alias of that
immutable manifest.
