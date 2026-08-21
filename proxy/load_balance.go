package proxy

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"
)

// LoadBalance picks a member pseudo-randomly per-connect, spreading load
// across the group. It is invoked by NewGroup from proxy/registry.go via the
// "load-balance"/"loadbalance" case. Uses the existing proxy.Config schema —
// no new YAML fields.
type LoadBalance struct {
	name string
	cfg  Config
	reg  *Registry
	mu   sync.Mutex
}

func NewLoadBalance(cfg Config, reg *Registry) (*LoadBalance, error) {
	if len(cfg.Proxies) == 0 {
		return nil, fmt.Errorf("load-balance %q: no proxies", cfg.Name)
	}
	return &LoadBalance{name: cfg.Name, cfg: cfg, reg: reg}, nil
}

func (l *LoadBalance) Name() string { return l.name }

func (l *LoadBalance) Connect(ctx context.Context, addr string) (net.Conn, error) {
	l.mu.Lock()
	members := make([]string, len(l.cfg.Proxies))
	copy(members, l.cfg.Proxies)
	l.mu.Unlock()

	if len(members) == 0 {
		return nil, fmt.Errorf("load-balance %q: no proxies", l.name)
	}
	name := members[rand.Intn(len(members))]
	p, err := l.reg.Get(name)
	if err != nil {
		return nil, fmt.Errorf("load-balance %q: %w", l.name, err)
	}
	return p.Connect(ctx, addr)
}

func (l *LoadBalance) Latency(url string) (time.Duration, error) {
	if len(l.cfg.Proxies) == 0 {
		return 0, fmt.Errorf("no proxies")
	}
	p, err := l.reg.Get(l.cfg.Proxies[0])
	if err != nil {
		return 0, err
	}
	return p.Latency(url)
}

func (l *LoadBalance) Close() error { return nil }