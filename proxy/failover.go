package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

type FailoverProxy struct {
	mu       sync.Mutex
	name     string
	primary  Proxy
	fallback []Proxy
}

func NewFailoverProxy(name string, primary Proxy, fallback []Proxy) *FailoverProxy {
	return &FailoverProxy{name: name, primary: primary, fallback: fallback}
}

func (f *FailoverProxy) Name() string { return f.name }

func (f *FailoverProxy) Connect(ctx context.Context, addr string) (net.Conn, error) {
	f.mu.Lock()
	chain := make([]Proxy, 0, 1+len(f.fallback))
	if f.primary != nil { chain = append(chain, f.primary) }
	chain = append(chain, f.fallback...)
	f.mu.Unlock()
	var lastErr error
	for _, p := range chain {
		conn, err := p.Connect(ctx, addr)
		if err == nil { return conn, nil }
		lastErr = err
	}
	return nil, fmt.Errorf("all proxies failed for %s: %w", f.name, lastErr)
}

func (f *FailoverProxy) Latency(url string) (time.Duration, error) {
	if f.primary == nil { return 0, fmt.Errorf("no primary proxy") }
	return f.primary.Latency(url)
}

func (f *FailoverProxy) Close() error { return nil }

func (f *FailoverProxy) SetFallback(fb []Proxy) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fallback = fb
}

var _ Proxy = (*FailoverProxy)(nil)
