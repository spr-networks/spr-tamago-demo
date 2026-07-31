package sprnet

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"
)

type fakeDHCPDevice struct {
	mac   net.HardwareAddr
	queue [][]byte
}

func (d *fakeDHCPDevice) Transmit(frame []byte) error {
	payload := frame[42:]
	xid := binary.BigEndian.Uint32(payload[4:8])
	options, _ := parseOptions(payload[240:])
	typeCode := options[53][0]
	switch typeCode {
	case 1:
		d.queue = append(d.queue, serverFrame(d.mac, xid, 2))
	case 3:
		d.queue = append(d.queue, serverFrame(d.mac, xid, 5))
	}
	return nil
}

func (d *fakeDHCPDevice) Receive(buf []byte) (int, error) {
	if len(d.queue) == 0 {
		return 0, nil
	}
	frame := d.queue[0]
	d.queue = d.queue[1:]
	return copy(buf, frame), nil
}

func TestAcquire(t *testing.T) {
	mac, _ := net.ParseMAC("02:53:50:52:54:01")
	dev := &fakeDHCPDevice{mac: mac}
	lease, err := (Client{Device: dev, MAC: mac, Hostname: "test", Timeout: time.Second}).Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := lease.CIDR(), "10.20.30.40/24"; got != want {
		t.Fatalf("CIDR=%q, want %q", got, want)
	}
	if got, want := lease.Gateway.String(), "10.20.30.1"; got != want {
		t.Fatalf("gateway=%q, want %q", got, want)
	}
	if len(lease.DNS) != 2 || lease.DNS[0].String() != "10.20.30.1" || lease.DNS[1].String() != "1.1.1.1" {
		t.Fatalf("DNS=%v", lease.DNS)
	}
}

func TestClientPacket(t *testing.T) {
	mac, _ := net.ParseMAC("02:53:50:52:54:01")
	requested := netip.MustParseAddr("10.20.30.40")
	server := netip.MustParseAddr("10.20.30.1")
	frame := buildClientFrame(mac, 0x12345678, 3, requested, server, "spr-test")
	if got := binary.BigEndian.Uint16(frame[12:14]); got != 0x0800 {
		t.Fatalf("EtherType=%#x", got)
	}
	if got := internetChecksum(frame[14:34]); got != 0 {
		t.Fatalf("IPv4 checksum residual=%#x", got)
	}
	options, err := parseOptions(frame[42+240:])
	if err != nil {
		t.Fatal(err)
	}
	if options[53][0] != 3 || addr4(options[50]) != requested || addr4(options[54]) != server {
		t.Fatalf("request options=%v", options)
	}
}

func serverFrame(mac net.HardwareAddr, xid uint32, messageType byte) []byte {
	payload := make([]byte, dhcpMinLength)
	payload[0], payload[1], payload[2] = 2, 1, 6
	binary.BigEndian.PutUint32(payload[4:8], xid)
	copy(payload[16:20], []byte{10, 20, 30, 40})
	copy(payload[20:24], []byte{10, 20, 30, 1})
	copy(payload[28:34], mac)
	copy(payload[236:240], dhcpCookie[:])
	options := payload[240:]
	pos := appendOption(options, 0, 53, []byte{messageType})
	pos = appendOption(options, pos, 54, []byte{10, 20, 30, 1})
	pos = appendOption(options, pos, 1, []byte{255, 255, 255, 0})
	pos = appendOption(options, pos, 3, []byte{10, 20, 30, 1})
	pos = appendOption(options, pos, 6, []byte{10, 20, 30, 1, 1, 1, 1, 1})
	pos = appendOption(options, pos, 51, []byte{0, 0, 0x0e, 0x10})
	options[pos] = 255

	udpLen := 8 + len(payload)
	frame := make([]byte, 14+20+udpLen)
	copy(frame[0:6], mac)
	for i := 6; i < 12; i++ {
		frame[i] = 0xff
	}
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	ip := frame[14:34]
	ip[0], ip[8], ip[9] = 0x45, 64, 17
	binary.BigEndian.PutUint16(ip[2:4], uint16(20+udpLen))
	copy(ip[12:16], []byte{10, 20, 30, 1})
	copy(ip[16:20], []byte{255, 255, 255, 255})
	binary.BigEndian.PutUint16(ip[10:12], internetChecksum(ip))
	udp := frame[34:42]
	binary.BigEndian.PutUint16(udp[0:2], dhcpServerPort)
	binary.BigEndian.PutUint16(udp[2:4], dhcpClientPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(udpLen))
	copy(frame[42:], payload)
	return frame
}
