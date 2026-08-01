//go:build tamago && arm64

package sprnet

import (
	"errors"
	"fmt"
	"net"

	"github.com/usbarmory/go-net"
	vnet "github.com/usbarmory/go-net/virtio"
	"github.com/usbarmory/tamago/kvm/virtio"
)

// FindVirtioNet discovers and starts the first VirtIO-net MMIO device.
func FindVirtioNet(start, end, step uint32, fallbackMAC string) (*vnet.Net, uint32, net.HardwareAddr, error) {
	fallback, err := net.ParseMAC(fallbackMAC)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("parse fallback MAC: %w", err)
	}
	for base := start; base < end; base += step {
		transport := &virtio.MMIO{Base: base}
		if transport.DeviceID() != vnet.DeviceID {
			continue
		}
		dev := &vnet.Net{Transport: transport, MTU: gnet.MTU}
		if err := dev.Init(); err != nil {
			return nil, base, nil, err
		}
		mac := append(net.HardwareAddr(nil), fallback...)
		if transport.NegotiatedFeatures()&vnet.FeatureMAC != 0 {
			// VirtIO-net config fields are feature-dependent. Read exactly the
			// advertised MAC prefix rather than go-net's fixed 12-byte struct,
			// which crosses libkrun's config region when MTU is not offered.
			config := transport.Config(6)
			if len(config) != 6 {
				return nil, base, nil, errors.New("virtio-net MAC configuration unavailable")
			}
			mac = append(mac[:0], config...)
		}
		if invalidMAC(mac) {
			mac = append(net.HardwareAddr(nil), fallback...)
		}
		dev.Start()
		return dev, base, mac, nil
	}
	return nil, 0, nil, errVirtioNetNotFound
}

var errVirtioNetNotFound = errors.New("virtio-net device not found")

func invalidMAC(mac net.HardwareAddr) bool {
	if len(mac) != 6 || mac[0]&1 != 0 {
		return true
	}
	for _, b := range mac {
		if b != 0 {
			return false
		}
	}
	return true
}
