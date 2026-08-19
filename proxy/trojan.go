package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"
)

type Trojan struct {
	cfg Config
}

func NewTrojan(cfg Config) Proxy { return &Trojan{cfg: cfg} }

func (t *Trojan) Name() string { return t.cfg.Name }

func (t *Trojan) Connect(ctx context.Context, addr string) (net.Conn, error) {
	target := fmt.Sprintf("%s:%d", t.cfg.Server, t.cfg.Port)
	rawConn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("trojan dial %s: %w", target, err)
	}

	sni := t.cfg.SNI
	if sni == "" {
		sni, _, _ = net.SplitHostPort(target)
	}
	tlsConn := tls.Client(rawConn, TLSConfig(sni, t.cfg.ALPN))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("trojan tls: %w", err)
	}

	cmd := []byte{0x04}
	host, portStr, _ := net.SplitHostPort(addr)
	var port uint16 = 80
	fmt.Sscanf(portStr, "%d", &port)

	cmd = append(cmd, 0x00, 0x03)
	cmd = append(cmd, byte(len(host)))
	cmd = append(cmd, []byte(host)...)
	cmd = append(cmd, byte(port>>8), byte(port))
	cmd = append(cmd, 0x0d, 0x0a)
	cmd = append(cmd, []byte(t.cfg.Password)...)
	cmd = append(cmd, 0x0d, 0x0a)

	if _, err := tlsConn.Write(cmd); err != nil {
		tlsConn.Close()
		return nil, err
	}

	reply := make([]byte, 1)
	if _, err := tlsConn.Read(reply); err != nil {
		return nil, err
	}
	if reply[0] != 0x00 {
		tlsConn.Close()
		return nil, fmt.Errorf("trojan rejected")
	}
	return tlsConn, nil
}

func (t *Trojan) Latency(url string) (time.Duration, error) {
	host := url
	if !strings.Contains(host, ":") { host = host + ":443" }
	start := time.Now()
	conn, err := t.Connect(context.Background(), host)
	if err != nil { return 0, err }
	conn.Close()
	return time.Since(start), nil
}

func (t *Trojan) Close() error { return nil }