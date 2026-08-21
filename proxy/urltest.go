package proxy

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

type URLTest struct {
	mu        sync.Mutex
	cfg       Config
	reg       *Registry
	best      string
	latencies map[string]time.Duration
	failed    map[string]bool
}

func NewURLTest(cfg Config, reg *Registry) (Proxy, error) {
	ut := &URLTest{
		cfg:       cfg,
		reg:       reg,
		latencies: make(map[string]time.Duration),
		failed:    make(map[string]bool),
	}
	if len(cfg.Proxies) > 0 {
		ut.best = cfg.Proxies[0]
	}
	return ut, nil
}

func (u *URLTest) Name() string { return u.cfg.Name }

func (u *URLTest) Connect(ctx context.Context, addr string) (net.Conn, error) {
	u.mu.Lock()
	best := u.best
	u.mu.Unlock()
	p, err := u.reg.Get(best)
	if err != nil {
		return nil, err
	}
	conn, err := p.Connect(ctx, addr)
	if err != nil {
		u.markFailed(best)
		return nil, err
	}
	return conn, nil
}

func (u *URLTest) Latency(url string) (time.Duration, error) {
	u.mu.Lock()
	best := u.best
	u.mu.Unlock()
	p, err := u.reg.Get(best)
	if err != nil {
		return 0, err
	}
	t, err := p.Latency(url)
	if err != nil {
		u.markFailed(best)
		return 0, err
	}
	return t, nil
}

func (u *URLTest) Close() error { return nil }

// PickBest returns the currently selected proxy name, skipping permanently
// failed ones. Thread-safe.
func (u *URLTest) PickBest() (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.pickBestLocked()
}

func (u *URLTest) pickBestLocked() (string, error) {
	if u.best != "" && !u.failed[u.best] {
		return u.best, nil
	}
	for _, name := range u.cfg.Proxies {
		if !u.failed[name] {
			u.best = name
			return u.best, nil
		}
	}
	return "", fmt.Errorf("all proxies in group %q are currently unavailable", u.cfg.Name)
}

func (u *URLTest) markFailed(name string) {
	u.mu.Lock()
	u.failed[name] = true
	u.mu.Unlock()
}

func (u *URLTest) Probe() {
	interval := time.Duration(u.cfg.Interval) * time.Second
	if interval == 0 {
		interval = 30 * time.Second
	}
	url := u.cfg.URL
	if url == "" {
		url = "https://www.gstatic.com/generate_204"
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	probe := func() {
		type lr struct {
			name    string
			latency time.Duration
			err     error
		}
		results := []lr{}
		var wg sync.WaitGroup
		for _, name := range u.cfg.Proxies {
			wg.Add(1)
			go func(n string) {
				defer wg.Done()
				p, err := u.reg.Get(n)
				if err != nil {
					results = append(results, lr{n, 0, err})
					return
				}
				t, err := p.Latency(url)
				results = append(results, lr{n, t, err})
			}(name)
		}
		wg.Wait()
		sort.Slice(results, func(i, j int) bool {
			if results[i].err != nil { return false }
			if results[j].err != nil { return true }
			return results[i].latency < results[j].latency
		})
		u.mu.Lock()
		for _, r := range results {
			u.latencies[r.name] = r.latency
			if r.err != nil {
				u.failed[r.name] = true
			} else {
				u.failed[r.name] = false
			}
		}
		for _, r := range results {
			if r.err == nil {
				u.best = r.name
				break
			}
		}
		u.mu.Unlock()
	}

	probe()
	go func() {
		for range ticker.C { probe() }
	}()
}
