package proxy

import (
	"context"
	"crypto/tls"
	"net"
	"time"
)

type Proxy interface {
	Name() string
	Connect(ctx context.Context, addr string) (net.Conn, error)
	Latency(url string) (time.Duration, error)
	Close() error
}

type Config struct {
	Name     string
	Type     string
	Server   string
	Port     int
	Username string
	Password string
	Cipher   string
	SNI      string
	ALPN     []string
	UUID     string
	AlterID  int
	Network  string
	Method   string
	Proxies  []string
	URL      string
	Interval int
	Default  string
}

func TLSConfig(sni string, alpn []string) *tls.Config {
	cfg := &tls.Config{InsecureSkipVerify: true}
	if sni != "" {
		cfg.ServerName = sni
	}
	if len(alpn) > 0 {
		cfg.NextProtos = alpn
	}
	return cfg
}