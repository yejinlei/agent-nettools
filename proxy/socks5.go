package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
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