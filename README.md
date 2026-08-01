# spr-tamago-demo

[![Build and verify](https://github.com/spr-networks/spr-tamago-demo/actions/workflows/ci.yml/badge.svg)](https://github.com/spr-networks/spr-tamago-demo/actions/workflows/ci.yml)

A Hello World SPR plugin implemented as a single
[TamaGo](https://github.com/usbarmory/tamago) ARM64 kernel running under krun.
There is no Linux kernel, init process, guest userspace, or sidecar service.

The kernel itself terminates a VirtIO-vsock stream on port 4040 and serves a
single-file React UI built with
[`@spr-networks/plugin-ui`](https://github.com/spr-networks/spr-plugin-ui),
plus the `/status` endpoint. The build embeds that HTML into the Go kernel; no
frontend files or Node runtime exist in the delivered image. It also drives
VirtIO-net, obtains its IPv4
configuration from SPR DHCP, and verifies outbound DNS and TCP connectivity.
SPR and its krun runtime map the plugin's host Unix socket to the guest port:

```text
SPR API -> /state/plugins/spr-tamago-demo/socket.sock
        -> libkrun VirtIO-vsock port 4040
        -> TamaGo kernel HTTP handler
```

The UI upcall remains on vsock and does not depend on IP networking. The
separate Internet path is fully dynamic—there is no hard-coded guest address,
gateway, or DNS server:

```text
TamaGo -> usbarmory/go-net/virtio -> krun TAP -> SPR plugin bridge
       -> SPR DHCP/DNS/policy -> WAN
```

## What is in the image

The one `scratch` image contains:

- a raw ARM64 TamaGo kernel at `/tamago-kernel`;
- the corresponding ELF at `/unused`, retained for inspection; and
- no Linux filesystem or executable userspace.

The kernel includes the pinned `usbarmory/go-net` VirtIO-net driver, a small
raw-Ethernet DHCP client, go-net's userspace IP stack, and the compiled SPR
Plugin UI SDK bundle.

The Docker command is deliberately `/unused`: the trusted SPR krun policy
selects `/tamago-kernel` as the VM kernel before a container process could run.
If `/unused` is ever executed and exits with `SIGILL`/`SIGSEGV`, the
external-kernel policy was not supplied and the image was started as an
ordinary Linux process.

## SPR runtime prerequisite

SPR already supports the standard UI upcall annotations used here:

```yaml
krun.vsock_path: /state/plugins/spr-tamago-demo/socket.sock
krun.vsock_port: "4040"
```

The network path uses the same krun TAP annotations as other SPR KVM plugins:

```yaml
krun.tap_name: kruntap0
krun.net_uplink: eth0
```

`plugin.json` declares the `spr-tamago-demo` interface, a stable locally
administered device MAC, and the `wan` and `dns` policies. SPR owns addressing
and routing and supplies them to the kernel with DHCP.

Upstream crun/libkrun also support `kernel_path` and raw external kernels, but
SPR's hardened `spr-krun` runtime correctly ignores image-controlled
`/.krun_vm.json`. Manager-issued policy must authorize the kernel requested by
Compose:

```yaml
krun.kernel_path: /tamago-kernel
krun.kernel_format: "0"
```

The [`tamago` branch of `spr-networks/super`](https://github.com/spr-networks/super/tree/tamago)
adds those two trusted policy fields to `superd`. Use that branch and rebuild
the `superd` service. The runtime then supplies both the external raw kernel
and SPR-owned listening Unix socket to libkrun.

The checked-in `.krun_vm.json` is only an equivalent external-kernel hint for
testing with an unmodified upstream `krun` runtime. Hardened SPR ignores it.

## TamaGo and VirtIO

The kernel build uses the three settings required by TamaGo:

```text
GOOS=tamago
GOARCH=arm64
GOOSPKG=github.com/usbarmory/tamago
```

The kernel uses TamaGo's generic VirtIO MMIO transport and split queues to
drive both device ID 19 (VirtIO-vsock) and device ID 1 (VirtIO-net). Its small
in-tree vsock implementation handles the SPR UI stream directly in bare-metal
Go. The network interface is the official
[`usbarmory/go-net/virtio`](https://github.com/usbarmory/go-net/tree/main/virtio)
driver requested for this demo.

At boot the kernel discovers VirtIO-net, starts its queues, performs the DHCP
DISCOVER/REQUEST exchange over raw Ethernet, then hands the device and lease to
go-net's IP stack. The page and `/status` expose the device, MAC, lease,
gateway, DNS servers, and the result of a DNS plus TCP probe to
`example.com:80`.

TamaGo's current `kvm/virtio` directory also contains AMD64 PCI transport
files without architecture build constraints. The builder makes a temporary
copy of the pinned TamaGo module and replaces those two PCI files with ARM64
package stubs. The MMIO and queue implementations remain the upstream pinned
TamaGo code.

The temporary module copy also marks `0x8c000000..0x8e000000`, the queue-only
RAM excluded from the Go heap, as ARM64 Normal Memory. TamaGo otherwise maps
RAM beyond `runtime.MemRegion()` with Device attributes; the bulk memory
operations used to initialize a VirtIO queue fault on such a mapping.

The kernel's fatal-exit path uses `PSCI_0_2_FN_SYSTEM_OFF` through the HVC
conduit advertised by libkrun. It no longer uses the incorrect SMC conduit.

This krun configuration does not attach a legacy PL011 serial device. The
TamaGo `Printk` hook is therefore deliberately silent instead of touching an
unmapped MMIO address; an empty `docker logs spr-tamago-demo` is expected.
The plugin's observable interface is its host Unix socket and vsock HTTP
service.

## Build

Build or publish the single ARM64 image:

```sh
./build_docker_compose.sh
./build_docker_compose.sh --push
```

The default tag is `ghcr.io/spr-networks/spr-tamago-demo:latest`. The Node and
Go builder images, SPR Plugin UI SDK, Yarn, TamaGo module, TamaGo compiler, and
go-net module are pinned. The first build compiles the matching TamaGo Go
toolchain and can take several minutes.

The complete build environment and two-build digest check are documented in
[`REPRODUCIBLE_BUILDS.md`](REPRODUCIBLE_BUILDS.md). GitHub Actions verifies
pull requests and publishes immutable `sha-<commit>` tags plus `latest` from
`main`.

## Run in SPR

Install the standard `spr-krun-runtime`, run `superd` from the SPR `tamago`
branch above, and install this repository through **Plugins → + New Plugin**.
The supported launch path is SPR's plugin manager because it signs the trusted
runtime override and creates the SPR-owned Unix socket mapping.

`plugin.json` selects the KVM runtime, adds **spr-tamago-demo** to the sidebar,
and asks SPR for DHCP-backed WAN and DNS access. `docker-compose-kvm.yml`
contains exactly one `spr-krun` service, one TAP-backed plugin network, and no
fixed IP or Linux networking sidecar.

Open **spr-tamago-demo** in the SPR sidebar. A successful response includes
`X-TamaGo-Kernel: true`; `/status` reports `"role":"kernel"`,
`"linux":false`, `"tamago_version"`, `"ipc":"virtio-vsock"`, and a `network`
object. The SDK UI reads that endpoint through SPR's authenticated plugin API,
follows the host theme, and displays the linked TamaGo version. On an SPR host
that object should reach `"phase":"online"` and report
`"probe":"DNS + TCP example.com:80 succeeded"`.

On macOS/HVF the exact kernel can be booted to regression-test the vsock UI.
The SPR bridge, DHCP service, and TAP policy are Linux-host facilities, so the
complete Internet path is verified on an SPR host.

## Verify

Run the unit, manifest, Compose, source, and reproducible-input checks:

```sh
./test.sh
```

To build the kernel without Docker, use the matching TamaGo compiler wrapper:

```sh
yarn --cwd frontend install --frozen-lockfile
yarn --cwd frontend bundle
mkdir -p kernel/ui
cp frontend/build/index.html kernel/ui/index.html

BUILD_DIR="$(mktemp -d)"
TAMAGO_MODULE="$(go list -m -f '{{.Dir}}' github.com/usbarmory/tamago)"

go run ./tools/prepare_tamago.go \
  -tamago-dir "${TAMAGO_MODULE}" \
  -out-dir "${BUILD_DIR}/tamago-arm64"
cp go.mod "${BUILD_DIR}/kernel.mod"
cp go.sum "${BUILD_DIR}/kernel.sum"
go mod edit -modfile="${BUILD_DIR}/kernel.mod" \
  -replace=github.com/usbarmory/tamago="${BUILD_DIR}/tamago-arm64"

GOOS=tamago GOOSPKG=github.com/usbarmory/tamago GOARCH=arm64 \
  "${TAMAGO}" build -modfile="${BUILD_DIR}/kernel.mod" \
  -buildvcs=false -trimpath -tags=tamago \
  -ldflags='-T 0x80010000 -R 0x1000 -s -w -buildid=' \
  -o "${BUILD_DIR}/tamago-kernel.elf" ./kernel

go run ./tools/elf2raw.go \
  -in "${BUILD_DIR}/tamago-kernel.elf" \
  -out tamago-kernel -base 0x80000000
```

The raw image begins with a four-byte AArch64 branch to the TamaGo ELF entry
point. Loadable segments retain their linked physical addresses, preserving
TamaGo's early page-table arena below `0x80010000`.
