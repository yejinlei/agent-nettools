package proxy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

type Direct struct{}

func NewDirect() Proxy { return &Direct{} }

func (d *Direct) Name() string { return "DIRECT" }

func (d *Direct) Connect(ctx context.Context, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("dns lookup %s: %w", host, err)
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no IP for %s", host)
	}
	return nil, fmt.Errorf("direct dial %s: %w", addr, lastErr)
}

func (d *Direct) Latency(url string) (time.Duration, error) {
	host := url
	if !strings.Contains(host, ":") {
		host = host + ":443"
	}
	start := time.Now()
	conn, err := d.Connect(context.Background(), host)
	if err != nil {
		return 0, err
	}
	conn.Close()
	return time.Since(start), nil
}

func (d *Direct) Close() error { return nil }