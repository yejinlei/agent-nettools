package dns

import (
	"net"
	"strings"
	"sync"
)

type FakeDns struct {
	mu       sync.RWMutex
	forward  map[string]net.IP
	reverse  map[string]string
	nextIP   net.IP
	network  *net.IPNet
	gateway  net.IP
	dnsIP    net.IP
}

func NewFakeDns(cidr string) (*FakeDns, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	ip := network.IP.To4()
	if ip == nil {
		ip = network.IP.To16()
	}
	fakeIP := make(net.IP, len(ip))
	copy(fakeIP, ip)
	fakeIP[len(fakeIP)-1] = 3

	gateway := make(net.IP, len(ip))
	copy(gateway, ip)
	gateway[len(gateway)-1] = 1

	dns := make(net.IP, len(ip))
	copy(dns, ip)
	dns[len(dns)-1] = 2

	return &FakeDns{
		forward: make(map[string]net.IP),
		reverse: make(map[string]string),
		nextIP:  fakeIP,
		network: network,
		gateway: gateway,
		dnsIP:   dns,
	}, nil
}

func (f *FakeDns) Gateway() net.IP { return f.gateway }

func (f *FakeDns) DNS() net.IP { return f.dnsIP }

func (f *FakeDns) Allocate(domain string) net.IP {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ip, ok := f.forward[domain]; ok {
		return ip
	}
	ip := make(net.IP, len(f.nextIP))
	copy(ip, f.nextIP)
	for i := len(f.nextIP) - 1; i >= 0; i-- {
		f.nextIP[i]++
		if f.nextIP[i] != 0 {
			break
		}
	}
	if !f.network.Contains(f.nextIP) {
		copy(f.nextIP, f.network.IP)
		f.nextIP[len(f.nextIP)-1] = 3
	}
	f.forward[domain] = ip
	f.reverse[ip.String()] = domain
	return ip
}

func (f *FakeDns) Domain(ip net.IP) (string, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	domain, ok := f.reverse[ip.String()]
	return domain, ok
}

func (f *FakeDns) IP(domain string) (net.IP, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	ip, ok := f.forward[domain]
	return ip, ok
}

func (f *FakeDns) IsFake(ip net.IP) bool {
	return f.network.Contains(ip)
}

func (f *FakeDns) Dump() map[string]string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	result := make(map[string]string, len(f.forward))
	for domain, ip := range f.forward {
		result[domain] = ip.String()
	}
	return result
}

func (f *FakeDns) AddStatic(domain, ipStr string) error {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forward[strings.ToLower(domain)] = ip
	f.reverse[ip.String()] = strings.ToLower(domain)
	return nil
}