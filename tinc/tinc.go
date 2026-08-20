package tinc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

type Config struct {
	Private   string
	Name      string
	CA        string
	Listen    string
	Endpoints []string
	VirtualIP string
	Keepalive int
}

func (c *Config) defaults() {
	if c.Listen == "" { c.Listen = ":655" }
	if c.Private == "" || len(c.Private) != 128 {
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		c.Private = fmt.Sprintf("%x", priv)
		c.Name = fmt.Sprintf("%x", pub[:8])
	} else if c.Name == "" { c.Name = c.Private[64:72] }
}

type AEAD interface {
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	NonceSize() int
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}

type Peer struct {
	cfg    Config
	pub    []byte
	mu     sync.Mutex
	listen *net.UDPConn
	aead   AEAD
	onData func(srcIP net.IP, data []byte)
	done   chan struct{}
	peers  map[string]*net.UDPAddr
}

func NewPeer(cfg Config) (*Peer, error) {
	cfg.defaults()
	raw, err := hexDecode(cfg.Private)
	if err != nil { return nil, fmt.Errorf("tinc: invalid private key: %w", err) }
	pub := ed25519.PublicKey(raw[32:])
	if cfg.CA == "" { return nil, fmt.Errorf("tinc: CA secret required") }
	salt := sha256.Sum256([]byte(cfg.CA))
	key := make([]byte, 32)
	der := hkdf.New(sha256.New, pub, salt[:], []byte("tinc-session"))
	io.ReadFull(der, key)
	aead, err := chacha20poly1305.New(key)
	if err != nil { return nil, err }
	return &Peer{
		cfg: cfg, pub: pub, aead: aead,
		done: make(chan struct{}),
		peers: make(map[string]*net.UDPAddr),
	}, nil
}

func (p *Peer) Name() string { return p.cfg.Name }
func (p *Peer) VirtualIP() net.IP {
	if p.cfg.VirtualIP == "" { return net.IPv4zero }
	return net.ParseIP(p.cfg.VirtualIP)
}
func (p *Peer) OnData(fn func(srcIP net.IP, data []byte)) {
	p.mu.Lock(); defer p.mu.Unlock()
	p.onData = fn
}

func (p *Peer) SendTo(dstIP net.IP, data []byte) error {
	p.mu.Lock()
	if p.aead == nil || p.listen == nil {
		p.mu.Unlock()
		return fmt.Errorf("tinc: not ready")
	}
	aead := p.aead; listen := p.listen
	peers := make([]*net.UDPAddr, 0, len(p.peers))
	for _, a := range p.peers { peers = append(peers, a) }
	p.mu.Unlock()
	dst := make([]byte, 16)
	if ip := dstIP.To4(); ip != nil { copy(dst[12:], ip) } else { copy(dst, dstIP) }
	plain := append([]byte{0x01}, dst...)
	plain = append(plain, data...)
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil { return err }
	ciph := aead.Seal(nil, nonce, plain, nil)
	out := append([]byte{0x01}, nonce...)
	out = append(out, ciph...)
	listen.SetWriteDeadline(time.Now().Add(5 * time.Second))
	for _, a := range peers { listen.WriteToUDP(out, a) }
	return nil
}

func (p *Peer) Start(ctx context.Context) error {
	laddr, err := net.ResolveUDPAddr("udp", p.cfg.Listen)
	if err != nil { return err }
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil { return fmt.Errorf("tinc: listen %s: %w", p.cfg.Listen, err) }
	p.mu.Lock(); p.listen = conn; p.mu.Unlock()
	for _, ep := range p.cfg.Endpoints {
		addr, err := net.ResolveUDPAddr("udp", ep)
		if err != nil { continue }
		p.peers[ep] = addr
	}
	go func() { time.Sleep(200 * time.Millisecond); p.sendHello() }()
	if p.cfg.Keepalive > 0 { go p.keepalive() }
	buf := make([]byte, 65535)
	for {
		select {
		case <-p.done: return nil
		default:
			conn.SetReadDeadline(time.Now().Add(time.Second))
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() { continue }
				return err
			}
			go p.handleDatagram(buf[:n])
		}
	}
}

func (p *Peer) Stop() {
	close(p.done)
	p.mu.Lock(); defer p.mu.Unlock()
	if p.listen != nil { p.listen.Close() }
}

func (p *Peer) handleDatagram(d []byte) {
	if len(d) < 25 || d[0] != 0x01 { return }
	p.mu.Lock(); aead := p.aead; p.mu.Unlock()
	if aead == nil { return }
	plain, err := aead.Open(nil, d[1:13], d[13:], nil)
	if err != nil || len(plain) < 17 || plain[0] == 0x80 { return }
	src := net.IP(plain[1:17]); payload := plain[17:]
	p.mu.Lock(); fn := p.onData; p.mu.Unlock()
	if fn != nil { fn(src, payload) }
}

func (p *Peer) sendHello() {
	p.mu.Lock()
	listen := p.listen
	peers := make([]*net.UDPAddr, 0, len(p.peers))
	for _, a := range p.peers { peers = append(peers, a) }
	p.mu.Unlock()
	if listen == nil { return }
	msg := append([]byte{0x10}, p.pub...)
	listen.SetWriteDeadline(time.Now().Add(5 * time.Second))
	for _, a := range peers { listen.WriteToUDP(msg, a) }
}

func (p *Peer) keepalive() {
	ticker := time.NewTicker(time.Second * time.Duration(p.cfg.Keepalive))
	defer ticker.Stop()
	for {
		select {
		case <-p.done: return
		case <-ticker.C: p.sendKeepalive()
		}
	}
}

func (p *Peer) sendKeepalive() {
	p.mu.Lock()
	aead := p.aead; listen := p.listen
	peers := make([]*net.UDPAddr, 0, len(p.peers))
	for _, a := range p.peers { peers = append(peers, a) }
	p.mu.Unlock()
	if aead == nil || listen == nil { return }
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil { return }
	ciph := aead.Seal(nil, nonce, []byte{0x80}, nil)
	out := append([]byte{0x01}, nonce...)
	out = append(out, ciph...)
	listen.SetWriteDeadline(time.Now().Add(5 * time.Second))
	for _, a := range peers { listen.WriteToUDP(out, a) }
}

func hexDecode(s string) ([]byte, error) {
	b := make([]byte, len(s)/2)
	for i := range b {
		var v int
		fmt.Sscanf(s[2*i:], "%2x", &v)
		b[i] = byte(v)
	}
	return b, nil
}
