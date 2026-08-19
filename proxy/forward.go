package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type Forward struct { cfg Config }

func NewForward(cfg Config) Proxy { return &Forward{cfg: cfg} }
func (f *Forward) Name() string { return f.cfg.Name }

func (f *Forward) Connect(ctx context.Context, addr string) (net.Conn, error) {
	target := net.JoinHostPort(f.cfg.Server, strconv.Itoa(f.cfg.Port))
	rawConn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil { return nil, err }

	// Use SNI field: empty SNI = plain HTTP; non-empty = HTTPS to destination
	sni := strings.TrimSpace(f.cfg.SNI)
	if sni == "" {
		return rawConn, nil
	}

	tlsConn := tls.Client(rawConn, &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("forward tls %s: %w", sni, err)
	}
	return tlsConn, nil
}

func (f *Forward) Latency(url string) (time.Duration, error) {
	target := net.JoinHostPort(f.cfg.Server, strconv.Itoa(f.cfg.Port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil { return 0, err }
	conn.Close()
	return time.Since(start), nil
}

func (f *Forward) Close() error { return nil }
