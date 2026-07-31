package sprnet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"time"
)

const (
	bootpFixedLength = 236
	dhcpMinLength    = 300
	dhcpClientPort   = 68
	dhcpServerPort   = 67
)

var dhcpCookie = [4]byte{99, 130, 83, 99}

// FrameDevice is the raw Ethernet interface implemented by go-net/virtio.
type FrameDevice interface {
	Receive([]byte) (int, error)
	Transmit([]byte) error
}

// Lease is the dynamic IPv4 configuration returned by an SPR DHCP server.
type Lease struct {
	Address   netip.Addr
	PrefixLen int
	Gateway   netip.Addr
	DNS       []netip.Addr
	Server    netip.Addr
	Duration  time.Duration
	MAC       net.HardwareAddr
}

func (l Lease) CIDR() string {
	return netip.PrefixFrom(l.Address, l.PrefixLen).String()
}

// Client performs DHCP directly over a raw Ethernet device. It runs before
// the IP stack owns the VirtIO receive queue, so no Linux interface or fixed
// guest address is required.
type Client struct {
	Device   FrameDevice
	MAC      net.HardwareAddr
	Hostname string
	Timeout  time.Duration
}

type dhcpPacket struct {
	messageType byte
	address     netip.Addr
	server      netip.Addr
	mask        [4]byte
	router      netip.Addr
	dns         []netip.Addr
	lease       time.Duration
}

// Acquire broadcasts DHCPDISCOVER and DHCPREQUEST and returns the ACK lease.
func (c Client) Acquire() (Lease, error) {
	if c.Device == nil {
		return Lease{}, errors.New("nil DHCP frame device")
	}
	if len(c.MAC) != 6 {
		return Lease{}, fmt.Errorf("invalid DHCP MAC length %d", len(c.MAC))
	}
	if c.Timeout <= 0 {
		c.Timeout = 20 * time.Second
	}

	deadline := time.Now().Add(c.Timeout)
	xid := binary.BigEndian.Uint32(c.MAC[2:6]) ^ uint32(time.Now().UnixNano())
	var lastErr error
	for attempt := 0; attempt < 4 && time.Now().Before(deadline); attempt++ {
		if err := c.Device.Transmit(buildClientFrame(c.MAC, xid, 1, netip.Addr{}, netip.Addr{}, c.Hostname)); err != nil {
			lastErr = fmt.Errorf("transmit DHCP discover: %w", err)
			continue
		}
		offer, err := c.waitFor(xid, 2, boundedDeadline(deadline, time.Duration(2<<attempt)*time.Second))
		if err != nil {
			lastErr = err
			continue
		}
		if !offer.server.IsValid() {
			lastErr = errors.New("DHCP offer omitted server identifier")
			continue
		}

		if err := c.Device.Transmit(buildClientFrame(c.MAC, xid, 3, offer.address, offer.server, c.Hostname)); err != nil {
			lastErr = fmt.Errorf("transmit DHCP request: %w", err)
			continue
		}
		ack, err := c.waitFor(xid, 5, boundedDeadline(deadline, 4*time.Second))
		if err != nil {
			lastErr = err
			continue
		}
		lease, err := mergeLease(c.MAC, offer, ack)
		if err != nil {
			lastErr = err
			continue
		}
		return lease, nil
	}
	if lastErr == nil {
		lastErr = errors.New("DHCP timed out")
	}
	return Lease{}, lastErr
}

func boundedDeadline(overall time.Time, wait time.Duration) time.Time {
	next := time.Now().Add(wait)
	if next.After(overall) {
		return overall
	}
	return next
}

func (c Client) waitFor(xid uint32, want byte, deadline time.Time) (dhcpPacket, error) {
	buf := make([]byte, 1600)
	for time.Now().Before(deadline) {
		n, err := c.Device.Receive(buf)
		if err != nil {
			return dhcpPacket{}, fmt.Errorf("receive DHCP: %w", err)
		}
		if n == 0 {
			runtime.Gosched()
			time.Sleep(2 * time.Millisecond)
			continue
		}
		packet, packetXID, packetMAC, err := parseServerFrame(buf[:n])
		if err != nil || packetXID != xid || !equalMAC(packetMAC, c.MAC) {
			continue
		}
		if packet.messageType == 6 {
			return dhcpPacket{}, errors.New("DHCP server rejected request")
		}
		if packet.messageType == want {
			return packet, nil
		}
	}
	return dhcpPacket{}, fmt.Errorf("DHCP message type %d timed out", want)
}

func mergeLease(mac net.HardwareAddr, offer, ack dhcpPacket) (Lease, error) {
	if !ack.address.IsValid() || ack.address.IsUnspecified() {
		ack.address = offer.address
	}
	if ack.mask == [4]byte{} {
		ack.mask = offer.mask
	}
	if !ack.router.IsValid() {
		ack.router = offer.router
	}
	if len(ack.dns) == 0 {
		ack.dns = offer.dns
	}
	if !ack.server.IsValid() {
		ack.server = offer.server
	}
	if ack.lease == 0 {
		ack.lease = offer.lease
	}
	ones, bits := net.IPMask(ack.mask[:]).Size()
	if !ack.address.Is4() || bits != 32 || ones < 0 {
		return Lease{}, errors.New("DHCP lease has no valid IPv4 address or subnet mask")
	}
	return Lease{
		Address:   ack.address,
		PrefixLen: ones,
		Gateway:   ack.router,
		DNS:       append([]netip.Addr(nil), ack.dns...),
		Server:    ack.server,
		Duration:  ack.lease,
		MAC:       append(net.HardwareAddr(nil), mac...),
	}, nil
}

func buildClientFrame(mac net.HardwareAddr, xid uint32, messageType byte, requested, server netip.Addr, hostname string) []byte {
	payload := make([]byte, dhcpMinLength)
	payload[0], payload[1], payload[2] = 1, 1, 6 // BOOTREQUEST, Ethernet, MAC length.
	binary.BigEndian.PutUint32(payload[4:8], xid)
	binary.BigEndian.PutUint16(payload[10:12], 0x8000) // Broadcast replies.
	copy(payload[28:34], mac)
	copy(payload[bootpFixedLength:bootpFixedLength+4], dhcpCookie[:])

	options := payload[bootpFixedLength+4:]
	pos := 0
	pos = appendOption(options, pos, 53, []byte{messageType})
	pos = appendOption(options, pos, 61, append([]byte{1}, mac...))
	if messageType == 3 && requested.Is4() {
		v := requested.As4()
		pos = appendOption(options, pos, 50, v[:])
	}
	if messageType == 3 && server.Is4() {
		v := server.As4()
		pos = appendOption(options, pos, 54, v[:])
	}
	if hostname != "" {
		name := []byte(hostname)
		if len(name) > 63 {
			name = name[:63]
		}
		pos = appendOption(options, pos, 12, name)
	}
	pos = appendOption(options, pos, 55, []byte{1, 3, 6, 15, 51, 54})
	pos = appendOption(options, pos, 57, []byte{0x05, 0xdc}) // 1500-byte maximum message.
	options[pos] = 255

	udpLen := 8 + len(payload)
	ipLen := 20 + udpLen
	frame := make([]byte, 14+ipLen)
	for i := 0; i < 6; i++ {
		frame[i] = 0xff
	}
	copy(frame[6:12], mac)
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	ip := frame[14:34]
	ip[0], ip[8], ip[9] = 0x45, 64, 17
	binary.BigEndian.PutUint16(ip[2:4], uint16(ipLen))
	binary.BigEndian.PutUint16(ip[4:6], uint16(xid))
	copy(ip[16:20], []byte{255, 255, 255, 255})
	binary.BigEndian.PutUint16(ip[10:12], internetChecksum(ip))
	udp := frame[34:42]
	binary.BigEndian.PutUint16(udp[0:2], dhcpClientPort)
	binary.BigEndian.PutUint16(udp[2:4], dhcpServerPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(udpLen))
	copy(frame[42:], payload)
	return frame
}

func appendOption(dst []byte, pos int, code byte, value []byte) int {
	if len(value) > 255 || pos+2+len(value) >= len(dst) {
		return pos
	}
	dst[pos], dst[pos+1] = code, byte(len(value))
	copy(dst[pos+2:], value)
	return pos + 2 + len(value)
}

func parseServerFrame(frame []byte) (packet dhcpPacket, xid uint32, mac net.HardwareAddr, err error) {
	if len(frame) < 14+20+8+bootpFixedLength+4 || binary.BigEndian.Uint16(frame[12:14]) != 0x0800 {
		return packet, 0, nil, errors.New("not an IPv4 DHCP frame")
	}
	ip := frame[14:]
	ihl := int(ip[0]&0x0f) * 4
	if ip[0]>>4 != 4 || ihl < 20 || len(ip) < ihl+8 || ip[9] != 17 {
		return packet, 0, nil, errors.New("invalid DHCP IPv4 header")
	}
	udp := ip[ihl:]
	if binary.BigEndian.Uint16(udp[0:2]) != dhcpServerPort || binary.BigEndian.Uint16(udp[2:4]) != dhcpClientPort {
		return packet, 0, nil, errors.New("not a DHCP server response")
	}
	udpLen := int(binary.BigEndian.Uint16(udp[4:6]))
	if udpLen < 8+bootpFixedLength+4 || udpLen > len(udp) {
		return packet, 0, nil, errors.New("invalid DHCP UDP length")
	}
	payload := udp[8:udpLen]
	if payload[0] != 2 || payload[1] != 1 || payload[2] != 6 || string(payload[bootpFixedLength:bootpFixedLength+4]) != string(dhcpCookie[:]) {
		return packet, 0, nil, errors.New("invalid DHCP BOOTP response")
	}
	xid = binary.BigEndian.Uint32(payload[4:8])
	mac = append(net.HardwareAddr(nil), payload[28:34]...)
	packet.address = addr4(payload[16:20])
	packet.server = addr4(payload[20:24])
	options, err := parseOptions(payload[bootpFixedLength+4:])
	if err != nil {
		return packet, 0, nil, err
	}
	if v := options[53]; len(v) == 1 {
		packet.messageType = v[0]
	}
	if v := options[54]; len(v) == 4 {
		packet.server = addr4(v)
	}
	if v := options[1]; len(v) == 4 {
		copy(packet.mask[:], v)
	}
	if v := options[3]; len(v) >= 4 {
		packet.router = addr4(v[:4])
	}
	if v := options[6]; len(v) >= 4 {
		for len(v) >= 4 {
			packet.dns = append(packet.dns, addr4(v[:4]))
			v = v[4:]
		}
	}
	if v := options[51]; len(v) == 4 {
		packet.lease = time.Duration(binary.BigEndian.Uint32(v)) * time.Second
	}
	return packet, xid, mac, nil
}

func parseOptions(data []byte) (map[byte][]byte, error) {
	options := make(map[byte][]byte)
	for pos := 0; pos < len(data); {
		code := data[pos]
		pos++
		switch code {
		case 0:
			continue
		case 255:
			return options, nil
		}
		if pos >= len(data) {
			return nil, errors.New("truncated DHCP option length")
		}
		length := int(data[pos])
		pos++
		if pos+length > len(data) {
			return nil, errors.New("truncated DHCP option value")
		}
		options[code] = append([]byte(nil), data[pos:pos+length]...)
		pos += length
	}
	return options, nil
}

func internetChecksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data[:2]))
		data = data[2:]
	}
	if len(data) == 1 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func addr4(data []byte) netip.Addr {
	if len(data) < 4 {
		return netip.Addr{}
	}
	return netip.AddrFrom4([4]byte{data[0], data[1], data[2], data[3]})
}

func equalMAC(a, b net.HardwareAddr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
