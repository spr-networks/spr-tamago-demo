//go:build tamago && arm64

package main

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/spr-networks/spr-tamago-demo/kernel/sprnet"
	gnet "github.com/usbarmory/go-net"
)

const pluginMAC = "02:53:50:52:54:01"

type networkStatus struct {
	Phase   string   `json:"phase"`
	Device  string   `json:"device"`
	MAC     string   `json:"mac"`
	Address string   `json:"address"`
	Gateway string   `json:"gateway"`
	DNS     []string `json:"dns"`
	Lease   string   `json:"lease"`
	Probe   string   `json:"probe"`
	Error   string   `json:"error"`
}

var (
	networkMu     sync.RWMutex
	networkReport = networkStatus{Phase: "starting", Device: "virtio-net"}
)

func setNetworkStatus(update func(*networkStatus)) {
	networkMu.Lock()
	update(&networkReport)
	networkMu.Unlock()
}

func networkStatusSnapshot() networkStatus {
	networkMu.RLock()
	defer networkMu.RUnlock()
	copy := networkReport
	copy.DNS = append([]string(nil), networkReport.DNS...)
	return copy
}

func startInternetNetworking() {
	setNetworkStatus(func(s *networkStatus) { s.Phase = "discovering-virtio-net" })
	dev, base, mac, err := sprnet.FindVirtioNet(virtioMMIOStart, virtioMMIOEnd, virtioMMIOStep, pluginMAC)
	if err != nil {
		setNetworkStatus(func(s *networkStatus) { s.Phase, s.Error = "failed", err.Error() })
		return
	}
	setNetworkStatus(func(s *networkStatus) {
		s.Phase = "dhcp"
		s.Device = fmt.Sprintf("virtio-net MMIO %#x", base)
		s.MAC = mac.String()
	})

	lease, err := (sprnet.Client{
		Device: dev, MAC: mac, Hostname: "spr-tamago-demo", Timeout: 30 * time.Second,
	}).Acquire()
	if err != nil {
		setNetworkStatus(func(s *networkStatus) { s.Phase, s.Error = "failed", err.Error() })
		return
	}

	stack := gnet.NewGVisorStack(1)
	iface := &gnet.Interface{Stack: stack, NetworkDevice: dev}
	if err := iface.Init(lease.CIDR(), lease.MAC.String(), addrString(lease.Gateway)); err != nil {
		setNetworkStatus(func(s *networkStatus) { s.Phase, s.Error = "failed", fmt.Sprintf("configure IP stack: %v", err) })
		return
	}
	iface.HandleStackErr = func(err error, tx bool) {
		setNetworkStatus(func(s *networkStatus) { s.Error = fmt.Sprintf("network stack tx=%t: %v", tx, err) })
	}

	dns := addrStrings(lease.DNS)
	if len(dns) != 0 {
		servers := make([]string, 0, len(dns))
		for _, server := range dns {
			servers = append(servers, net.JoinHostPort(server, "53"))
		}
		net.SetDefaultNS(servers)
	}
	net.SocketFunc = stack.Socket
	go func() { _ = iface.Start(context.Background()) }()

	setNetworkStatus(func(s *networkStatus) {
		s.Phase = "online"
		s.Address = lease.CIDR()
		s.Gateway = addrString(lease.Gateway)
		s.DNS = dns
		s.Lease = lease.Duration.String()
		s.Error = ""
	})
	probeInternetUntilReady()
}

// probeInternetUntilReady handles the short interval between DHCPACK and SPR
// applying the IP-based WAN/DNS policy for the new lease. There is no guest
// event for that host-side transition, so retry the observable probe until the
// policy becomes active instead of leaving a stale degraded boot result.
func probeInternetUntilReady() {
	for !probeInternet() {
		retry := time.NewTimer(5 * time.Second)
		<-retry.C
	}
}

func probeInternet() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupHost(ctx, "example.com")
	if err != nil {
		setNetworkStatus(func(s *networkStatus) {
			s.Phase, s.Probe, s.Error = "degraded", "", fmt.Sprintf("DNS probe: %v", err)
		})
		return false
	}
	var ipv4 string
	for _, ip := range ips {
		if parsed := net.ParseIP(ip); parsed != nil && parsed.To4() != nil {
			ipv4 = parsed.To4().String()
			break
		}
	}
	if ipv4 == "" {
		setNetworkStatus(func(s *networkStatus) {
			s.Phase, s.Probe, s.Error = "degraded", "", "DNS probe returned no IPv4 address"
		})
		return false
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", net.JoinHostPort(ipv4, "80"))
	if err != nil {
		setNetworkStatus(func(s *networkStatus) {
			s.Phase, s.Probe, s.Error = "degraded", "", fmt.Sprintf("Internet TCP probe: %v", err)
		})
		return false
	}
	_ = conn.Close()
	setNetworkStatus(func(s *networkStatus) {
		s.Phase = "online"
		s.Probe = "DNS + TCP example.com:80 succeeded"
		s.Error = ""
	})
	return true
}

func addrString(addr netip.Addr) string {
	value := addr.String()
	if value == "invalid IP" {
		return ""
	}
	return value
}

func addrStrings(addrs []netip.Addr) []string {
	values := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if value := addrString(addr); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func dnsText(values []string) string {
	if len(values) == 0 {
		return "awaiting DHCP"
	}
	return strings.Join(values, ", ")
}
