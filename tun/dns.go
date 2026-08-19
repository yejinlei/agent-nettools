package tun

import (
	"net"
	"sync"
)

type DnsInterceptor struct {
	mu        sync.RWMutex
	domainIPs map[string]net.IP
	ipDomain  map[string]string
	fakeCIDR  *net.IPNet
	realDNS   string
}

func NewDnsInterceptor(fakeCIDR string, realDNS string) (*DnsInterceptor, error) {
	_, cidr, err := net.ParseCIDR(fakeCIDR)
	if err != nil {
		return nil, err
	}
	return &DnsInterceptor{
		domainIPs: make(map[string]net.IP),
		ipDomain:  make(map[string]string),
		fakeCIDR:  cidr,
		realDNS:   realDNS,
	}, nil
}

func (d *DnsInterceptor) Record(domain string, realIP, fakeIP net.IP) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.domainIPs[domain] = realIP
	d.ipDomain[realIP.String()] = domain
	d.ipDomain[fakeIP.String()] = domain
}

func (d *DnsInterceptor) LookupDomain(ip net.IP) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	domain, ok := d.ipDomain[ip.String()]
	return domain, ok
}

func (d *DnsInterceptor) LookupIP(domain string) (net.IP, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	ip, ok := d.domainIPs[domain]
	return ip, ok
}

func (d *DnsInterceptor) IsFake(ip net.IP) bool {
	return d.fakeCIDR.Contains(ip)
}

func (d *DnsInterceptor) RealDNS() string {
	return d.realDNS
}

var _ = &sync.Mutex{}