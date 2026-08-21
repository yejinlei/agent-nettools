package router

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"agent-netx/proxy"
)

type Router struct {
	mu       sync.Mutex
	mode     string
	rules    []Rule
	proxyReg *proxy.Registry
}

type Rule struct {
	Type    string
	Pattern string
	Target  string
}

func New(mode string, rawRules []string, reg *proxy.Registry) (*Router, error) {
	rules := []Rule{}
	for _, raw := range rawRules {
		parts := strings.SplitN(raw, ",", 3)
		if len(parts) < 3 { continue }
		rules = append(rules, Rule{
			Type:    strings.ToUpper(strings.TrimSpace(parts[0])),
			Pattern: strings.TrimSpace(parts[1]),
			Target:  strings.TrimSpace(parts[2]),
		})
	}
	return &Router{mode: mode, rules: rules, proxyReg: reg}, nil
}

func (r *Router) Pick(addr string) (proxy.Proxy, error) {
	r.mu.Lock()
	mode, rules := r.mode, r.rules
	r.mu.Unlock()

	if mode == "direct" { return r.proxyReg.Get("DIRECT") }
	if mode == "global" { return r.selectFirst() }
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil { host = h }
	for _, rule := range rules {
		if r.match(host, rule) { return r.proxyReg.Get(rule.Target) }
	}
	return r.selectFirst()
}

// AddRule appends a rule to the front of the rule list (highest priority).
// This is the runtime mutation path used by the `add_rule` agent tool and
// `/add-rule` TUI command so new rules take effect without restarting.
func (r *Router) AddRule(raw string) {
	parts := strings.SplitN(raw, ",", 3)
	if len(parts) < 3 { return }
	rule := Rule{
		Type:    strings.ToUpper(strings.TrimSpace(parts[0])),
		Pattern: strings.TrimSpace(parts[1]),
		Target:  strings.TrimSpace(parts[2]),
	}
	r.mu.Lock()
	r.rules = append([]Rule{rule}, r.rules...)
	r.mu.Unlock()
}

// AddProxy registers a proxy into the underlying registry so it becomes
// immediately pickable by rules and groups. This is the runtime mutation path
// used by the `add_proxy` agent tool and `/add-proxy` TUI command.
func (r *Router) AddProxy(p proxy.Proxy) error {
	_, err := r.proxyReg.Get(p.Name())
	if err == nil {
		return fmt.Errorf("proxy %q already registered", p.Name())
	}
	r.proxyReg.Register(p)
	return nil
}

// RemoveProxy unregisters a proxy from the registry. Safe to call even if the
// proxy is not present.
func (r *Router) RemoveProxy(name string) {
	r.proxyReg.Unregister(name)
}

func (r *Router) match(host string, rule Rule) bool {
	switch rule.Type {
	case "DOMAIN":
		return host == rule.Pattern
	case "DOMAIN-SUFFIX":
		suffix := rule.Pattern
		if !strings.HasPrefix(suffix, ".") { suffix = "." + suffix }
		return strings.HasSuffix(host, suffix) || host == strings.TrimPrefix(rule.Pattern, ".")
	case "DOMAIN-KEYWORD":
		return strings.Contains(host, rule.Pattern)
	case "IP-CIDR":
		return inCIDR(host, rule.Pattern)
	case "GEOIP":
		return geoipMatch(host)
	case "MATCH":
		return true
	default:
		return false
	}
}

func (r *Router) selectFirst() (proxy.Proxy, error) {
	names := r.proxyReg.Names()
	if len(names) == 0 { return r.proxyReg.Get("DIRECT") }
	return r.proxyReg.Get(names[0])
}

func inCIDR(host string, cidr string) bool {
	ip := net.ParseIP(host)
	if ip == nil { return false }
	_, network, err := net.ParseCIDR(cidr)
	if err != nil { return false }
	return network.Contains(ip)
}

func geoipMatch(host string) bool {
	ip := net.ParseIP(host)
	if ip != nil { return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() }
	ips, err := net.LookupIP(host)
	if err != nil { return false }
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() { return true }
	}
	return false
}

