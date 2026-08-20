package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SOCKS5Proxy struct {
	cfg Config
}

func NewSOCKS5(cfg Config) Proxy { return &SOCKS5Proxy{cfg: cfg} }

func (s *SOCKS5Proxy) Name() string { return s.cfg.Name }

func (s *SOCKS5Proxy) Connect(ctx context.Context, addr string) (net.Conn, error) {
	target := net.JoinHostPort(s.cfg.Server, strconv.Itoa(s.cfg.Port))
	conn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("socks5 dial %s: %w", target, err)
	}
	if err := s.handshake(conn); err != nil {
		conn.Close()
		return nil, err
	}
	if err := s.request(conn, addr); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (s *SOCKS5Proxy) handshake(conn net.Conn) error {
	buf := []byte{0x05, 0x01, 0x00}
	if s.cfg.Username != "" {
		buf = []byte{0x05, 0x02, 0x00, 0x02}
	}
	if _, err := conn.Write(buf); err != nil {
		return err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	if resp[1] == 0x02 {
		return s.auth(conn)
	}
	if resp[1] != 0x00 {
		return fmt.Errorf("socks5 handshake rejected")
	}
	return nil
}

func (s *SOCKS5Proxy) auth(conn net.Conn) error {
	buf := make([]byte, 0, 2+len(s.cfg.Username)+1+len(s.cfg.Password))
	buf = append(buf, 0x01, byte(len(s.cfg.Username)))
	buf = append(buf, []byte(s.cfg.Username)...)
	buf = append(buf, byte(len(s.cfg.Password)))
	buf = append(buf, []byte(s.cfg.Password)...)
	if _, err := conn.Write(buf); err != nil {
		return err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	if resp[1] != 0x00 {
		return fmt.Errorf("socks5 auth failed")
	}
	return nil
}

func (s *SOCKS5Proxy) request(conn net.Conn, addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("socks5 bad addr %s: %w", addr, err)
	}
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)

	ips, err := net.LookupIP(host)
	atype := byte(0x01)
	var ipBytes []byte
	if err == nil && len(ips) > 0 {
		ipBytes = ips[0].To4()
		if ipBytes == nil {
			ipBytes = ips[0].To16()
			atype = 0x04
		}
	} else {
		atype = 0x03
	}

	buf := []byte{0x05, 0x01, 0x00, atype}
	if atype == 0x03 {
		buf = append(buf, byte(len(host)))
		buf = append(buf, []byte(host)...)
	} else {
		buf = append(buf, ipBytes...)
	}
	buf = append(buf, byte(port>>8), byte(port))

	if _, err := conn.Write(buf); err != nil {
		return err
	}
	resp := make([]byte, 5)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	if resp[1] != 0x00 {
		return fmt.Errorf("socks5 request failed: 0x%02x", resp[1])
	}
	boundAt := resp[3]
	switch boundAt {
	case 0x01:
		io.ReadFull(conn, make([]byte, 6))
	case 0x04:
		io.ReadFull(conn, make([]byte, 18))
	case 0x03:
		lenBuf := make([]byte, 1)
		io.ReadFull(conn, lenBuf)
		io.ReadFull(conn, make([]byte, int(lenBuf[0])+2))
	}
	return nil
}

func (s *SOCKS5Proxy) Latency(url string) (time.Duration, error) {
	host := url
	if !strings.Contains(host, ":") {
		host = host + ":443"
	}
	start := time.Now()
	conn, err := s.Connect(context.Background(), host)
	if err != nil {
		return 0, err
	}
	conn.Close()
	return time.Since(start), nil
}

func (s *SOCKS5Proxy) Close() error { return nil }

// ConnectUDP implements PacketProxy via SOCKS5 UDP ASSOCIATE (CMD 0x03). It
// performs the TCP greeting + a UDP ASSOCIATE request; the server replies with
// the BND.ADDR:PORT of its UDP relay socket. We then open a local UDP socket
// and relay datagrams there, wrapping each payload in the SOCKS5 UDP header
// (encodeUDPHeader) and unwrapping replies (parseUDPHeader). The TCP control
// connection stays open for the life of the relay — closing it tears down the
// association. The returned PacketConn's WriteTo/ReadFrom speak *application*
// payloads (DNS, QUIC, etc.), not raw SOCKS5 frames.
//
// We bind the relay to 127.0.0.1 (the server told us its relay address, but in
// practice most servers return 0.0.0.0 and expect the client to send to the
// same IP the TCP connection came from). So we dial UDP to the proxy server's
// host:port using the server-reported port when it is non-zero, else the TCP
// port — covering both the "same port" and "reported port" deployments.
func (s *SOCKS5Proxy) ConnectUDP(ctx context.Context) (net.PacketConn, error) {
	target := net.JoinHostPort(s.cfg.Server, strconv.Itoa(s.cfg.Port))
	ctrl, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("socks5 udp dial %s: %w", target, err)
	}
	if err := s.handshake(ctrl); err != nil {
		ctrl.Close()
		return nil, fmt.Errorf("socks5 udp handshake: %w", err)
	}
	relayAddr, err := s.udpAssociate(ctrl)
	if err != nil {
		ctrl.Close()
		return nil, fmt.Errorf("socks5 udp associate: %w", err)
	}

	// Local UDP socket bound to an ephemeral port; this is what callers
	// WriteTo/ReadFrom on. Outgoing datagrams go to relayAddr.
	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		ctrl.Close()
		return nil, fmt.Errorf("socks5 udp listen: %w", err)
	}

	uc := &socks5UDPConn{
		ctrl:   ctrl,
		relay:  relayAddr,
		udp:    udpConn,
		readCh: make(chan udpReply, 64),
	}
	// Pump: read SOCKS5-framed replies off the UDP socket, unwrap them, push
	// (src, payload) onto readCh for ReadFrom to surface to the caller.
	uc.wg.Add(1)
	go uc.readLoop()
	return uc, nil
}

// udpAssociate sends CMD 0x03 (UDP ASSOCIATE) with dst=0.0.0.0:0 (the client
// doesn't know its own egress IP ahead of time; 0.0.0.0:0 is the conventional
// request). Returns the server's relay UDP address (host:port).
func (s *SOCKS5Proxy) udpAssociate(ctrl net.Conn) (string, error) {
	// DST.ADDR = 0.0.0.0:0 → atyp IPv4, 4 zero bytes, 2 zero port bytes.
	req := []byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if _, err := ctrl.Write(req); err != nil {
		return "", err
	}
	resp := make([]byte, 5) // ver, rep, rsv, atyp, + addr...
	if _, err := io.ReadFull(ctrl, resp); err != nil {
		return "", err
	}
	if resp[1] != 0x00 {
		return "", fmt.Errorf("udp associate rejected: 0x%02x", resp[1])
	}
	host, port, ok := readSOCKS5Bound(ctrl, resp[3])
	if !ok {
		return "", fmt.Errorf("udp associate: bad bind addr")
	}
	// Most servers report 0.0.0.0; use the TCP peer's IP in that case.
	if host == "0.0.0.0" || host == "::" {
		host = s.cfg.Server
	}
	if port == 0 {
		port = uint16(s.cfg.Port) // fall back to the TCP port
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

// readSOCKS5Bound reads the BND.ADDR+PORT tail of a SOCKS5 reply given atyp.
// Mirrors readSOCKS5Addr in forward.go but reads from a bare conn (no offset).
func readSOCKS5Bound(ctrl net.Conn, atyp byte) (host string, port uint16, ok bool) {
	switch atyp {
	case 0x01:
		b := make([]byte, 6)
		if _, err := io.ReadFull(ctrl, b); err != nil {
			return "", 0, false
		}
		host = net.IPv4(b[0], b[1], b[2], b[3]).String()
		port = uint16(b[4])<<8 | uint16(b[5])
	case 0x04:
		b := make([]byte, 18)
		if _, err := io.ReadFull(ctrl, b); err != nil {
			return "", 0, false
		}
		host = net.IP(b[0:16]).String()
		port = uint16(b[16])<<8 | uint16(b[17])
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(ctrl, l); err != nil {
			return "", 0, false
		}
		b := make([]byte, int(l[0])+2)
		if _, err := io.ReadFull(ctrl, b); err != nil {
			return "", 0, false
		}
		host = string(b[:int(l[0])])
		port = uint16(b[int(l[0])])<<8 | uint16(b[int(l[0])+1])
	default:
		return "", 0, false
	}
	return host, port, true
}

// socks5UDPConn is the PacketConn returned by ConnectUDP. It wraps a local UDP
// socket that talks SOCKS5-framed datagrams to the server's relay endpoint,
// and a TCP control connection whose lifetime governs the association.
type socks5UDPConn struct {
	ctrl   net.Conn
	relay  string
	udp    net.PacketConn
	readCh chan udpReply
	wg     sync.WaitGroup
	closed sync.Once
}

type udpReply struct {
	from    net.Addr
	payload []byte
}

func (u *socks5UDPConn) readLoop() {
	defer u.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, from, err := u.udp.ReadFrom(buf)
		if err != nil {
			return
		}
		_ = from
		src, payload, err := ParseUDPHeader(buf[:n])
		if err != nil {
			continue // drop malformed relay datagrams
		}
		addr, _ := net.ResolveUDPAddr("udp", src)
		select {
		case u.readCh <- udpReply{from: addr, payload: append([]byte(nil), payload...)}:
		default:
			// backpressure: drop if the caller isn't draining fast enough.
		}
	}
}

func (u *socks5UDPConn) ReadFrom(p []byte) (int, net.Addr, error) {
	r, ok := <-u.readCh
	if !ok {
		return 0, nil, fmt.Errorf("socks5 udp relay closed")
	}
	n := copy(p, r.payload)
	return n, r.from, nil
}

func (u *socks5UDPConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	hdr, err := EncodeUDPHeader(addr.String())
	if err != nil {
		return 0, err
	}
	udpAddr, err := net.ResolveUDPAddr("udp", u.relay)
	if err != nil {
		return 0, err
	}
	frame := append(hdr, p...)
	return u.udp.WriteTo(frame, udpAddr)
}

func (u *socks5UDPConn) LocalAddr() net.Addr                { return u.udp.LocalAddr() }
func (u *socks5UDPConn) SetDeadline(t time.Time) error      { return u.udp.SetDeadline(t) }
func (u *socks5UDPConn) SetReadDeadline(t time.Time) error { return u.udp.SetReadDeadline(t) }
func (u *socks5UDPConn) SetWriteDeadline(t time.Time) error {
	return u.udp.SetWriteDeadline(t)
}

func (u *socks5UDPConn) Close() error {
	var err error
	u.closed.Do(func() {
		close(u.readCh)
		err = u.udp.Close()
		u.ctrl.Close()
		u.wg.Wait()
	})
	return err
}