package proxy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"agent-nettools/config"
)

type Registry struct {
	mu      sync.RWMutex
	proxies map[string]Proxy
}

func NewRegistry() *Registry { return &Registry{proxies: make(map[string]Proxy)} }

func (r *Registry) Register(p Proxy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.proxies[strings.ToLower(p.Name())] = p
}

func (r *Registry) Get(name string) (Proxy, error) {
	if strings.ToUpper(name) == "DIRECT" { return NewDirect(), nil }
	if strings.ToUpper(name) == "REJECT" { return &RejectProxy{}, nil }
	r.mu.RLock()
	p, ok := r.proxies[strings.ToLower(name)]
	r.mu.RUnlock()
	if !ok { return nil, fmt.Errorf("proxy %q not found", name) }
	return p, nil
}

func (r *Registry) Each(fn func(name string, p Proxy)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, p := range r.proxies { fn(name, p) }
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.proxies))
	for n := range r.proxies { names = append(names, n) }
	return names
}

func Register(cfgs []config.ProxyConfig) (*Registry, error) {
	reg := NewRegistry()
	for _, pc := range cfgs {
		pc.Type = strings.ToLower(pc.Type)
		if isGroup(pc.Type) { continue }
		p, err := NewProxy(toProxyConfig(pc))
		if err != nil { return nil, fmt.Errorf("proxy %q: %w", pc.Name, err) }
		reg.Register(p)
	}
	for _, pc := range cfgs {
		pc.Type = strings.ToLower(pc.Type)
		if !isGroup(pc.Type) { continue }
		p, err := NewGroup(toProxyConfig(pc), reg)
		if err != nil { return nil, fmt.Errorf("group %q: %w", pc.Name, err) }
		reg.Register(p)
	}
	return reg, nil
}

func isGroup(t string) bool {
	switch t {
	case "selector", "url-test", "urltest", "round-robin", "roundrobin":
		return true
	}
	return false
}

func toProxyConfig(pc config.ProxyConfig) Config {
	return Config{
		Name:     pc.Name,
		Type:     pc.Type,
		Server:   pc.Server,
		Port:     pc.Port,
		Username: pc.Username,
		Password: pc.Password,
		Cipher:   pc.Cipher,
		SNI:      pc.SNI,
		ALPN:     pc.ALPN,
		UUID:     pc.UUID,
		AlterID:  pc.AlterID,
		Method:   pc.Method,
		Proxies:  pc.Proxies,
		URL:      pc.URL,
		Interval: pc.Interval,
		Default:  pc.Default,
	}
}

func NewProxy(cfg Config) (Proxy, error) {
	switch strings.ToLower(cfg.Type) {
	case "direct":      return NewDirect(), nil
	case "http":        return NewHTTP(cfg), nil
	case "https":       return NewHTTPS(cfg), nil
	case "socks5":      return NewSOCKS5(cfg), nil
	case "ss", "shadowsocks": return NewShadowsocks(cfg)
	case "trojan":      return NewTrojan(cfg), nil
	case "vmess":       return NewVMess(cfg), nil
	case "forward":     return NewForward(cfg), nil
	default:            return nil, fmt.Errorf("unknown proxy type: %s", cfg.Type)
	}
}

func NewGroup(cfg Config, reg *Registry) (Proxy, error) {
	switch strings.ToLower(cfg.Type) {
	case "selector":         return NewSelector(cfg, reg), nil
	case "url-test", "urltest": return NewURLTest(cfg, reg)
	case "round-robin", "roundrobin": return NewRoundRobin(cfg, reg), nil
	default: return nil, fmt.Errorf("unknown group type: %s", cfg.Type)
	}
}

type RejectProxy struct{}

func (r *RejectProxy) Name() string { return "REJECT" }
func (r *RejectProxy) Connect(ctx context.Context, addr string) (net.Conn, error) { return nil, fmt.Errorf("REJECT") }
func (r *RejectProxy) Latency(url string) (time.Duration, error) { return 0, fmt.Errorf("REJECT") }
func (r *RejectProxy) Close() error { return nil }