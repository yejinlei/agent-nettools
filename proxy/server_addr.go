package proxy

import (
	"net"
	"strconv"
)

// ServerAddr returns the "host:port" a proxy dials to reach its server, in a
// form safe for IPv6 (net.JoinHostPort). It is consumed by Chain so a hop can
// be told to reach the *next* hop's server. Proxies without a server
// (DIRECT/REJECT/groups) don't implement this, and serverAddrOf returns "".
//
// Implementing these as small one-liners (rather than exposing cfg on the
// interface) keeps the Proxy interface stable — the optional-method pattern
// (serverAddrOf type-asserts to serverer) means adding ServerAddr to a new
// protocol is all that's needed to make it chain-capable.

func (h *HTTPProxy) ServerAddr() string  { return net.JoinHostPort(h.cfg.Server, strconv.Itoa(h.cfg.Port)) }
func (h *HTTPSProxy) ServerAddr() string { return net.JoinHostPort(h.cfg.Server, strconv.Itoa(h.cfg.Port)) }
func (s *SOCKS5Proxy) ServerAddr() string { return net.JoinHostPort(s.cfg.Server, strconv.Itoa(s.cfg.Port)) }
func (s *Shadowsocks) ServerAddr() string { return net.JoinHostPort(s.cfg.Server, strconv.Itoa(s.cfg.Port)) }
func (t *Trojan) ServerAddr() string      { return net.JoinHostPort(t.cfg.Server, strconv.Itoa(t.cfg.Port)) }
func (v *VMess) ServerAddr() string        { return net.JoinHostPort(v.cfg.Server, strconv.Itoa(v.cfg.Port)) }
func (f *Forward) ServerAddr() string      { return net.JoinHostPort(f.cfg.Server, strconv.Itoa(f.cfg.Port)) }
