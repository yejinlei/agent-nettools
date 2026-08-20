package wireguard

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// Config describes one end of a WireGuard-style peer.
type Config struct {
	Private   string // 32-byte private key (64-hex; empty = auto-generate)
	Public    string // peer's public key (64-hex)
	Preshared string // optional 32-byte PSK
	Listen    string // UDP listen addr (":51820" default)
	PeerAddr  string // remote peer UDP address
	Endpoint  string // deprecated alias for PeerAddr
	VirtualIP string // this node's virtual IP (e.g. "10.0.0.2")
	Keepalive int    // keepalive interval in seconds (0 = off)
	Handshake int    // handshake timeout in seconds (default 25)
}

func (c *Config) defaults() {
	if c.Listen == "" {
		c.Listen = ":51820"
	}
	if c.PeerAddr == "" && c.Endpoint != "" {
		c.PeerAddr = c.Endpoint
	}
	if c.PeerAddr == "" {
		c.PeerAddr = "127.0.0.1:51820"
	}
	if c.Handshake <= 0 {
		c.Handshake = 25
	}
	if c.Private == "" || isZero(c.Private) {
		key := make([]byte, 32)
		rand.Read(key)
		c.Private = fmt.Sprintf("%x", key)
	}
}

func isZero(s string) bool {
	for _, r := range s {
		if r != '0' {
			return false
		}
	}
	return true
}

func decodeKey(s string) ([32]byte, error) {
	if s == "" {
		return [32]byte{}, nil
	}
	var out [32]byte
	if len(s) == 64 {
		if _, err := fmt.Sscanf(s, "%64x", &out); err != nil {
			return [32]byte{}, err
		}
		return out, nil
	}
	return [32]byte{}, fmt.Errorf("wireguard: unsupported key encoding (want 64-hex)")
}

// Peer implements tun.Peer over a WireGuard-style UDP tunnel. It pre-computes
// the shared-key from (priv, peerPub), derives AEAD session keys via HKDF, and
// starts relaying immediately (no two-round handshake — the "handshake" is
// implicit: both peers derive the same keys at construction).
//
// On-the-wire data packet:
//
//	16B nonce | AEAD( 1B type | 16B dstIP | payload )
//
// type=0x01 data, type=0x80 keepalive.
type Peer struct {
	cfg Config

	priv      [32]byte
	peerPub   [32]byte
	sharedKey [32]byte

	mu       sync.Mutex
	listen   *net.UDPConn
	sendAead interface{ Seal(dst, nonce, plaintext, additionalData []byte) []byte; NonceSize() int; Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error) }
	recvAead interface{ Seal(dst, nonce, plaintext, additionalData []byte) []byte; NonceSize() int; Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error) }

	onData func(srcIP net.IP, data []byte)
	done   chan struct{}
}

func NewPeer(cfg Config) (*Peer, error) {
	cfg.defaults()
	priv, err := decodeKey(cfg.Private)
	if err != nil {
		return nil, fmt.Errorf("wireguard: invalid private key: %w", err)
	}
	peerPub, err := decodeKey(cfg.Public)
	if err != nil {
		return nil, fmt.Errorf("wireguard: invalid peer public key: %w", err)
	}
	ss, err := curve25519.X25519(priv[:], peerPub[:])
	if err != nil {
		return nil, err
	}
	var sharedKey [32]byte
	copy(sharedKey[:], ss)

	p := &Peer{
		cfg:       cfg,
		priv:      priv,
		peerPub:   peerPub,
		sharedKey: sharedKey,
		done:      make(chan struct{}),
	}
	if err := p.deriveKeys(); err != nil {
		return nil, err
	}
	return p, nil
}

// deriveKeys builds send/recv AEAD from the shared key via HKDF.
func (p *Peer) deriveKeys() error {
	// Salt: sha256(sharedKey || psk). Key material: sha256(sharedKey).
	saltBuf := make([]byte, 0, 64)
	saltBuf = append(saltBuf, p.sharedKey[:]...)
	psk, _ := decodeKey(p.cfg.Preshared)
	saltBuf = append(saltBuf, psk[:]...)
	salt := sha256.Sum256(saltBuf)

	keyBuf := sha256.Sum256(p.sharedKey[:])
	der := hkdf.New(sha256.New, keyBuf[:], salt[:], []byte("wireguard-session"))
	k1 := make([]byte, 32)
	k2 := make([]byte, 32)
	io.ReadFull(der, k1)
	io.ReadFull(der, k2)
	s1, err := chacha20poly1305.New(k1)
	if err != nil {
		return err
	}
	s2, err := chacha20poly1305.New(k2)
	if err != nil {
		return err
	}
	p.sendAead = s1
	p.recvAead = s2
	return nil
}

func (p *Peer) Name() string      { return "wg" }
func (p *Peer) VirtualIP() net.IP { if p.cfg.VirtualIP == "" { return net.IPv4zero }; return net.ParseIP(p.cfg.VirtualIP) }

func (p *Peer) OnData(fn func(srcIP net.IP, data []byte)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onData = fn
}

// SendTo delivers a raw IP packet to dstIP over the encrypted UDP tunnel.
func (p *Peer) SendTo(dstIP net.IP, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sendAead == nil || p.listen == nil {
		return fmt.Errorf("wireguard: not ready")
	}
	dst := make([]byte, 16)
	if ip := dstIP.To4(); ip != nil {
		copy(dst[12:], ip)
	} else {
		copy(dst, dstIP)
	}
	plain := make([]byte, 0, 1+16+len(data))
	plain = append(plain, 0x01)
	plain = append(plain, dst...)
	plain = append(plain, data...)

	nonce := make([]byte, p.sendAead.NonceSize())
	rand.Read(nonce)
	ciph := p.sendAead.Seal(nil, nonce, plain, nil)
	out := make([]byte, 0, len(nonce)+len(ciph))
	out = append(out, nonce...)
	out = append(out, ciph...)

	remote, err := net.ResolveUDPAddr("udp", p.cfg.PeerAddr)
	if err != nil {
		return err
	}
	p.listen.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = p.listen.WriteToUDP(out, remote)
	return err
}

// Start opens the UDP listener and handles incoming datagrams (handshake ack + data).
func (p *Peer) Start(ctx context.Context) error {
	laddr, err := net.ResolveUDPAddr("udp", p.cfg.Listen)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return fmt.Errorf("wireguard: listen %s: %w", p.cfg.Listen, err)
	}
	p.mu.Lock()
	p.listen = conn
	p.mu.Unlock()

	// Send initial heartbeat so the peer sees us alive and can reply.
	go func() {
		time.Sleep(200 * time.Millisecond)
		p.sendKeepalive()
	}()
	go p.keepalive()

	buf := make([]byte, 65535)
	for {
		select {
		case <-p.done:
			return nil
		default:
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				if errors.Is(err, context.DeadlineExceeded) {
					continue
				}
				return err
			}
			go p.handleDatagram(buf[:n])
		}
	}
}

func (p *Peer) Stop() {
	close(p.done)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listen != nil {
		p.listen.Close()
	}
}

func (p *Peer) handleDatagram(d []byte) {
	p.mu.Lock()
	aead := p.recvAead
	p.mu.Unlock()
	if aead == nil {
		return
	}

	nonceSize := aead.NonceSize()
	if len(d) < nonceSize+17 {
		return
	}
	nonce := d[:nonceSize]
	ciph := d[nonceSize:]
	plain, err := aead.Open(nil, nonce, ciph, nil)
	if err != nil {
		return
	}
	if len(plain) < 17 {
		return
	}
	if plain[0] == 0x80 {
		return // keepalive
	}
	src := net.IP(plain[1:17])
	payload := plain[17:]
	p.mu.Lock()
	fn := p.onData
	p.mu.Unlock()
	if fn != nil {
		fn(src, payload)
	}
}

func (p *Peer) sendKeepalive() {
	p.mu.Lock()
	aead := p.sendAead
	listen := p.listen
	p.mu.Unlock()
	if aead == nil || listen == nil {
		return
	}
	plain := []byte{0x80}
	nonce := make([]byte, aead.NonceSize())
	rand.Read(nonce)
	ciph := aead.Seal(nil, nonce, plain, nil)
	out := make([]byte, len(nonce)+len(ciph))
	copy(out, nonce)
	copy(out[len(nonce):], ciph)
	remote, err := net.ResolveUDPAddr("udp", p.cfg.PeerAddr)
	if err != nil {
		return
	}
	listen.SetWriteDeadline(time.Now().Add(5 * time.Second))
	listen.WriteToUDP(out, remote)
}

func (p *Peer) keepalive() {
	if p.cfg.Keepalive <= 0 {
		return
	}
	ticker := time.NewTicker(time.Second * time.Duration(p.cfg.Keepalive))
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
		}
		p.sendKeepalive()
	}
}