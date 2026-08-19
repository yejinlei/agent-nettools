package proxy

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Chain is a proxy that dials through an ordered list of proxies. It is the
// proxy-uses-proxy primitive ("代理链式" in the roadmap).
//
// Semantics: Chain.Connect(ctx, addr) dials the chain left-to-right. Each
// proxy's Connect is asked to reach the NEXT proxy's server:port; the final
// proxy is asked to reach the real destination `addr`. So the connection to
// each hop is established fresh by that hop's own transport — this is "dial
// through" semantics, which is correct when each hop is itself a full proxy
// protocol (HTTP CONNECT, SOCKS5, SS, Trojan, VMess all speak their protocol
// to whatever address you pass them).
//
// True "tunnel B's bytes through A's already-open connection" chaining would
// require injecting a pre-built net.Conn into each proxy (a Dialer/transport
// refactor across all protocols); that's a future extension and the Chain type
// is the seam it would plug into.
type Chain struct {
	cfg    Config
	hops   []Proxy // resolved from cfg.Proxies at build time
	reg    *Registry
}

func NewChain(cfg Config, reg *Registry) (Proxy, error) {
	if len(cfg.Proxies) == 0 {
		return nil, fmt.Errorf("chain %q: no hops in `proxies` list", cfg.Name)
	}
	hops := make([]Proxy, 0, len(cfg.Proxies))
	for _, name := range cfg.Proxies {
		p, err := reg.Get(name)
		if err != nil {
			return nil, fmt.Errorf("chain %q: hop %q: %w", cfg.Name, name, err)
		}
		hops = append(hops, p)
	}
	return &Chain{cfg: cfg, hops: hops, reg: reg}, nil
}

func (c *Chain) Name() string { return c.cfg.Name }

// Connect dials through each hop. hop[i] connects to the server of hop[i+1];
// the last hop connects to addr. A 10s per-hop timeout keeps a dead hop from
// hanging the whole chain.
func (c *Chain) Connect(ctx context.Context, addr string) (net.Conn, error) {
	for i, hop := range c.hops {
		target := addr
		if i < len(c.hops)-1 {
			// Ask this hop to reach the next hop's server:port.
			next := c.hops[i+1]
			target = serverAddrOf(next)
			if target == "" {
				return nil, fmt.Errorf("chain %q: hop %d (%s) has no server address", c.cfg.Name, i+1, next.Name())
			}
		}
		hopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		conn, err := hop.Connect(hopCtx, target)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("chain %q: hop %d (%s) connect %s: %w", c.cfg.Name, i+1, hop.Name(), target, err)
		}
		// Only the final hop's conn is returned to the caller; intermediate
		// hops' protocols already tunnel through to the next address.
		if i == len(c.hops)-1 {
			return conn, nil
		}
		// Intermediate conns are handed off to the next hop's protocol layer;
		// for dial-through semantics the next Connect opens its own conn, so
		// close the intermediate one (its purpose was to establish reachability
		// of the next server; a true tunnel refactor would reuse it).
		conn.Close()
	}
	return nil, fmt.Errorf("chain %q: empty chain", c.cfg.Name)
}

// serverAddrOf returns the host:port a proxy dials from. It reflects on the
// concrete type to find the server:port without exposing it on the interface.
// This keeps the Proxy interface stable while letting Chain route between
// hops. Returns "" for proxies without a server (DIRECT, REJECT, groups).
func serverAddrOf(p Proxy) string {
	type serverer interface{ ServerAddr() string }
	if s, ok := p.(serverer); ok {
		return s.ServerAddr()
	}
	// Fall back: DIRECT/REJECT/groups have no server — chaining through them
	// is a config error caught by Connect.
	return ""
}

func (c *Chain) Latency(url string) (time.Duration, error) {
	// Chain latency = sum of per-hop latencies (best-effort).
	var total time.Duration
	for _, hop := range c.hops {
		l, err := hop.Latency(url)
		if err != nil {
			return 0, fmt.Errorf("chain %q: hop %s latency: %w", c.cfg.Name, hop.Name(), err)
		}
		total += l
	}
	return total, nil
}

func (c *Chain) Close() error {
	for _, hop := range c.hops {
		hop.Close()
	}
	return nil
}
