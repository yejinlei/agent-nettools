package proxy

// udp.go — SOCKS5 UDP relay framing shared by the SOCKS5 client (ConnectUDP)
// and the SOCKS5 server (listener UDP ASSOCIATE).
//
// SOCKS5 UDP datagram relay (RFC 1928 §6):
//
//	+----+------+------+----------+----------+----------+
//	|RSV | FRAG | ATYP | DST.ADDR | DST.PORT |   DATA   |
//	+----+------+------+----------+----------+----------+
//	| 2  |  1   |  1   | Variable |    2     | Variable |
//	+----+------+------+----------+----------+----------+
//
// RSV must be 0x0000; FRAG is 0 (we don't support fragmentation/relaying of
// fragmented datagrams). ATYP is 1 (IPv4), 3 (domain), or 4 (IPv6). Every
// datagram through the UDP relay carries this 10-byte-ish prefix.
//
// Two helpers live here: encodeUDPHeader builds the prefix for a destination,
// and parseUDPHeader splits an incoming relay datagram into (srcAddr, payload).

import (
	"encoding/binary"
	"fmt"
	"net"
)

// EncodeUDPHeader builds the SOCKS5 UDP relay header for dst (host:port).
// atyp: 1=IPv4, 3=domain, 4=IPv6. Returns the full prefix bytes (no payload).
// Shared by the SOCKS5 client (ConnectUDP WriteTo) and the SOCKS5 server
// (listener UDP ASSOCIATE reply framing).
func EncodeUDPHeader(dst string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(dst)
	if err != nil {
		return nil, fmt.Errorf("udp header: bad addr %s: %w", dst, err)
	}
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)

	hdr := []byte{0x00, 0x00, 0x00} // RSV(2) + FRAG(1)=0
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			hdr[2] = 0x01 // IPv4
			hdr = append(hdr, v4...)
		} else {
			hdr[2] = 0x04 // IPv6
			hdr = append(hdr, ip.To16()...)
		}
	} else {
		hdr[2] = 0x03 // domain
		hdr = append(hdr, byte(len(host)))
		hdr = append(hdr, []byte(host)...)
	}
	hdr = append(hdr, byte(port>>8), byte(port))
	return hdr, nil
}

// ParseUDPHeader splits a SOCKS5 UDP relay datagram into the source address
// (host:port, the original destination from the client's perspective) and the
// application payload. Returns the source as host:port.
func ParseUDPHeader(buf []byte) (src string, payload []byte, err error) {
	if len(buf) < 4 { // 2 RSV + 1 FRAG + 1 ATYP
		return "", nil, fmt.Errorf("udp header: short (%d bytes)", len(buf))
	}
	// buf[1] is FRAG; we ignore non-zero fragments (no reassembly).
	off := 3 // past RSV(2)+FRAG(1)
	var host string
	switch atyp := buf[2]; atyp {
	case 0x01: // IPv4
		if len(buf) < off+6 {
			return "", nil, fmt.Errorf("udp header: short ipv4")
		}
		host = net.IP(buf[off : off+4]).String()
		off += 4
	case 0x04: // IPv6
		if len(buf) < off+18 {
			return "", nil, fmt.Errorf("udp header: short ipv6")
		}
		host = net.IP(buf[off : off+16]).String()
		off += 16
	case 0x03: // domain
		if len(buf) < off+1 {
			return "", nil, fmt.Errorf("udp header: short domain len")
		}
		l := int(buf[off])
		off++
		if len(buf) < off+l+2 {
			return "", nil, fmt.Errorf("udp header: short domain body")
		}
		host = string(buf[off : off+l])
		off += l
	default:
		return "", nil, fmt.Errorf("udp header: bad atyp 0x%02x", atyp)
	}
	if len(buf) < off+2 {
		return "", nil, fmt.Errorf("udp header: short port")
	}
	port := binary.BigEndian.Uint16(buf[off : off+2])
	off += 2
	return net.JoinHostPort(host, fmt.Sprintf("%d", port)), buf[off:], nil
}
