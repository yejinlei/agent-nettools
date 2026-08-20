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

// PacketProxy is an optional capability implemented by proxies that can relay
// UDP datagrams (e.g. SOCKS5 via UDP ASSOCIATE). ConnectUDP returns a
// PacketConn whose WriteTo/ReadFrom carry application UDP payloads to/from
// arbitrary destinations through the proxy. The returned PacketConn is NOT a
// raw socket: WriteTo(addr) wraps the payload in the proxy's UDP framing and
// sends it to the proxy's relay endpoint; ReadFrom unwraps replies.
//
// Proxies that only speak TCP (HTTP, Trojan, VLESS-over-TCP, ...) do not
// implement this; callers check with AsPacketProxy before using UDP.
type PacketProxy interface {
	ConnectUDP(ctx context.Context) (net.PacketConn, error)
}

// AsPacketProxy returns p as a PacketProxy if it supports UDP, else nil+false.
// This is the seam where the TCP-only and UDP-capable proxy worlds meet: a
// caller that needs UDP (e.g. the SOCKS5 UDP listener, forward udp) probes
// once and falls back to direct UDP when the picked proxy can't.
func AsPacketProxy(p Proxy) (PacketProxy, bool) {
	pp, ok := p.(PacketProxy)
	return pp, ok
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

	// Reality transport (VLESS/XTLS over uTLS). PublicKey is the server's
	// curve25519 public key (base64url, 43 chars); ShortID is the 8-hex auth
	// tag; Fingerprint selects the uTLS ClientHello to mimic (e.g. "chrome",
	// "firefox", "random"). When Fingerprint is set, Connect uses uTLS instead
	// of crypto/tls — this is what makes the TLS handshake look like a real
	// browser's, defeating SNI/ JA3 fingerprint blocking.
	PublicKey   string
	ShortID     string
	Fingerprint string
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