package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// FailoverGroup is a proxy-group variant whose members form an ordered fallback
// chain: the first listed proxy is tried first; on Connect error it falls
// through to the next until one succeeds or all are exhausted. Distinct from
// URLTest's automatic latency-driven selection, FailoverGroup honors a fixed
// operator-chosen order. Invoked by NewGroup via the "failover" case. Uses the
// existing proxy.Config schema (proxies[] is the ordered list) — no new YAML.
type FailoverGroup struct {
	name string
	cfg  Config
	reg  *Registry
	mu   sync.Mutex
}

func NewFailoverGroup(cfg Config, reg *Registry) (*FailoverGroup, error) {
	if len(cfg.Proxies) == 0 {
		return nil, fmt.Errorf("failover %q: no proxies", cfg.Name)
	}
	return &FailoverGroup{name: cfg.Name, cfg: cfg, reg: reg}, nil
}

func (f *FailoverGroup) Name() string { return f.name }

func (f *FailoverGroup) Connect(ctx context.Context, addr string) (net.Conn, error) {
	f.mu.Lock()
	members := make([]string, len(f.cfg.Proxies))
	copy(members, f.cfg.Proxies)
	f.mu.Unlock()

	var lastErr error
	for _, name := range members {
		p, err := f.reg.Get(name)
		if err != nil {
			lastErr = err
			continue
		}
		conn, err := p.Connect(ctx, addr)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, fmt.Errorf("failover %q: empty", f.name)
	}
	return nil, fmt.Errorf("failover %q: all members failed: %w", f.name, lastErr)
}

func (f *FailoverGroup) Latency(url string) (time.Duration, error) {
	if len(f.cfg.Proxies) == 0 {
		return 0, fmt.Errorf("no proxies")
	}
	p, err := f.reg.Get(f.cfg.Proxies[0])
	if err != nil {
		return 0, err
	}
	return p.Latency(url)
}

func (f *FailoverGroup) Close() error { return nil }
