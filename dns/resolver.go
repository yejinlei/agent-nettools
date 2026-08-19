package dns

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type Resolver interface {
	Resolve(domain string, qtype dnsmessage.Type) ([]net.IP, error)
	Type() string
}

type DirectResolver struct{}

func (r *DirectResolver) Type() string { return "direct" }

func (r *DirectResolver) Resolve(domain string, qtype dnsmessage.Type) ([]net.IP, error) {
	netType := "ip"
	switch qtype {
	case dnsmessage.TypeA:
		netType = "ip4"
	case dnsmessage.TypeAAAA:
		netType = "ip6"
	}
	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), domain)
	if err != nil {
		return nil, err
	}
	result := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		ipStr := ip.IP
		if netType == "ip4" && ipStr.To4() == nil {
			continue
		}
		if netType == "ip6" && ipStr.To4() != nil {
			continue
		}
		result = append(result, ipStr)
	}
	return result, nil
}

type DoHResolver struct {
	server string
	client *http.Client
}

func NewDoHResolver(server string) *DoHResolver {
	if server == "" {
		server = "https://cloudflare-dns.com/dns-query"
	}
	return &DoHResolver{
		server: server,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (r *DoHResolver) Type() string { return "doh" }

func (r *DoHResolver) Resolve(domain string, qtype dnsmessage.Type) ([]net.IP, error) {
	if !strings.HasSuffix(domain, ".") {
		domain = domain + "."
	}
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{ID: 0, RecursionDesired: true},
		Questions: []dnsmessage.Question{
			{Name: dnsmessage.MustNewName(domain), Type: qtype, Class: dnsmessage.ClassINET},
		},
	}
	packed, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("pack dns: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(packed)
	reqURL := r.server + "?dns=" + encoded
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-message")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseDNSResponse(body)
}

type DoTResolver struct {
	server string
}

func NewDoTResolver(server string) *DoTResolver {
	if server == "" {
		server = "1.1.1.1:853"
	}
	return &DoTResolver{server: server}
}

func (r *DoTResolver) Type() string { return "dot" }

func (r *DoTResolver) Resolve(domain string, qtype dnsmessage.Type) ([]net.IP, error) {
	if !strings.HasSuffix(domain, ".") {
		domain = domain + "."
	}
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{ID: 0, RecursionDesired: true},
		Questions: []dnsmessage.Question{
			{Name: dnsmessage.MustNewName(domain), Type: qtype, Class: dnsmessage.ClassINET},
		},
	}
	packed, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("pack dns: %w", err)
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", r.server, &tls.Config{
		ServerName: "cloudflare-dns.com",
	})
	if err != nil {
		return nil, fmt.Errorf("dot dial: %w", err)
	}
	defer conn.Close()
	wire := make([]byte, 2+len(packed))
	binary.BigEndian.PutUint16(wire[:2], uint16(len(packed)))
	copy(wire[2:], packed)
	if _, err := conn.Write(wire); err != nil {
		return nil, fmt.Errorf("dot write: %w", err)
	}
	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, fmt.Errorf("dot read len: %w", err)
	}
	respLen := binary.BigEndian.Uint16(lenBuf)
	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		return nil, fmt.Errorf("dot read body: %w", err)
	}
	return parseDNSResponse(respBuf)
}

type CacheResolver struct {
	inner Resolver
	cache *Cache
}

func NewCacheResolver(inner Resolver, ttl time.Duration) *CacheResolver {
	return &CacheResolver{inner: inner, cache: NewCache(ttl)}
}

func (r *CacheResolver) Type() string { return r.inner.Type() + "+cache" }

func (r *CacheResolver) Resolve(domain string, qtype dnsmessage.Type) ([]net.IP, error) {
	key := fmt.Sprintf("%s:%d", domain, qtype)
	if ips, ok := r.cache.Get(key); ok {
		result := make([]net.IP, len(ips))
		for i, s := range ips {
			result[i] = net.ParseIP(s)
		}
		return result, nil
	}
	ips, err := r.inner.Resolve(domain, qtype)
	if err != nil {
		return nil, err
	}
	strs := make([]string, len(ips))
	for i, ip := range ips {
		strs[i] = ip.String()
	}
	r.cache.Set(key, strs)
	return ips, nil
}

func parseDNSResponse(data []byte) ([]net.IP, error) {
	var msg dnsmessage.Message
	if err := msg.Unpack(data); err != nil {
		return nil, fmt.Errorf("unpack dns: %w", err)
	}
	var ips []net.IP
	for _, answer := range msg.Answers {
		if answer.Header.Type == dnsmessage.TypeA {
			if a, ok := answer.Body.(*dnsmessage.AResource); ok {
				ips = append(ips, a.A[:])
			}
		} else if answer.Header.Type == dnsmessage.TypeAAAA {
			if aaaa, ok := answer.Body.(*dnsmessage.AAAAResource); ok {
				ips = append(ips, aaaa.AAAA[:])
			}
		}
	}
	return ips, nil
}