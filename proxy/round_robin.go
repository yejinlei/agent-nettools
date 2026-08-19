package proxy

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

type RoundRobin struct {
	cfg Config
	reg *Registry
	idx atomic.Int64
}

func NewRoundRobin(cfg Config, reg *Registry) Proxy {
	return &RoundRobin{cfg: cfg, reg: reg}
}

func (r *RoundRobin) Name() string { return r.cfg.Name }

func (r *RoundRobin) Connect(ctx context.Context, addr string) (net.Conn, error) {
	if len(r.cfg.Proxies) == 0 {
		return nil, fmt.Errorf("round-robin %q: no proxies", r.cfg.Name)
	}
	i := r.idx.Add(1) - 1
	if i < 0 { i = 0 }
	name := r.cfg.Proxies[int(i)%len(r.cfg.Proxies)]
	p, err := r.reg.Get(name)
	if err != nil {
		return nil, fmt.Errorf("round-robin %q: %w", r.cfg.Name, err)
	}
	return p.Connect(ctx, addr)
}

func (r *RoundRobin) Latency(url string) (time.Duration, error) {
	if len(r.cfg.Proxies) == 0 { return 0, fmt.Errorf("no proxies") }
	p, err := r.reg.Get(r.cfg.Proxies[0])
	if err != nil { return 0, err }
	return p.Latency(url)
}

func (r *RoundRobin) Close() error { return nil }