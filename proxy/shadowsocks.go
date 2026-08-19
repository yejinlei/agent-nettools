package proxy

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

type Shadowsocks struct {
	cfg    Config
	key    []byte
	aead   cipher.AEAD
	aesCfg *aesBlockCipher
}

type aesBlockCipher struct {
	key    []byte
	method string
}

func NewShadowsocks(cfg Config) (Proxy, error) {
	ss := &Shadowsocks{cfg: cfg}
	method := strings.ToLower(cfg.Cipher)
	switch method {
	case "aes-128-gcm":
		if len(cfg.Password) < 16 { return nil, fmt.Errorf("ss: password too short for aes-128-gcm") }
		ss.key = []byte(cfg.Password)[:16]
		c, err := aes.NewCipher(ss.key)
		if err != nil { return nil, err }
		ss.aead, err = cipher.NewGCM(c)
		if err != nil { return nil, err }
	case "aes-256-gcm":
		key := keyDerivation(cfg.Password, 32)
		ss.key = key
		c, err := aes.NewCipher(key)
		if err != nil { return nil, err }
		ss.aead, err = cipher.NewGCM(c)
		if err != nil { return nil, err }
	case "chacha20-ietf-poly1305":
		key := sha256.Sum256([]byte(cfg.Password))
		var e error
		ss.aead, e = chacha20poly1305.New(key[:32])
		if e != nil { return nil, e }
	case "aes-128-cfb", "aes-256-cfb", "aes-128-ctr", "aes-256-ctr":
		ss.key = keyDerivation(cfg.Password, keyLen(method))
		ss.aesCfg = &aesBlockCipher{key: ss.key, method: method}
	default:
		return nil, fmt.Errorf("ss unsupported cipher: %s", method)
	}
	return ss, nil
}

func keyDerivation(password string, n int) []byte {
	key := make([]byte, n)
	i := 0
	for i < n {
		h := sha256.Sum256([]byte(password + string(key[:i])))
		copy(key[i:], h[:])
		i += 32
	}
	for i < n {
		h := sha256.Sum256(key)
		copy(key[i:], h[:])
		i += 32
	}
	return key[:n]
}

func keyLen(m string) int {
	switch m {
	case "aes-128-cfb", "aes-128-ctr": return 16
	case "aes-256-cfb", "aes-256-ctr": return 32
	}
	return 16
}

func (ss *Shadowsocks) Name() string { return ss.cfg.Name }

func (ss *Shadowsocks) Connect(ctx context.Context, addr string) (net.Conn, error) {
	target := net.JoinHostPort(ss.cfg.Server, strconv.Itoa(ss.cfg.Port))
	conn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil { return nil, fmt.Errorf("ss dial %s: %w", target, err) }

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	ips, _ := net.LookupIP(host)
	var remote []byte
	if len(ips) > 0 {
		ip := ips[0].To4()
		if ip != nil {
			remote = append(remote, 0x01)
			remote = append(remote, ip...)
		} else {
			remote = append(remote, 0x04)
			remote = append(remote, ips[0].To16()...)
		}
	} else {
		remote = append(remote, 0x03, byte(len(host)))
		remote = append(remote, []byte(host)...)
	}
	remote = append(remote, byte(port>>8), byte(port))

	if ss.aead != nil {
		nonce := make([]byte, ss.aead.NonceSize())
		enc := ss.aead.Seal(nil, nonce, remote, nil)
		enc = append(nonce, enc...)
		if _, err := conn.Write(enc); err != nil { conn.Close(); return nil, err }
		return &ssAEADConn{Conn: conn, cipher: ss.aead}, nil
	}

	iv := make([]byte, 16)
	stream := ss.aesCfg.newStream(iv)
	encIV := make([]byte, 16)
	stream.XORKeyStream(encIV, iv)
	if _, err := conn.Write(encIV); err != nil { conn.Close(); return nil, err }
	decStream := ss.aesCfg.newStream(iv)
	return &ssStreamConn{Conn: conn, enc: stream, dec: decStream}, nil
}

func (a *aesBlockCipher) newStream(iv []byte) cipher.Stream {
	c, _ := aes.NewCipher(a.key)
	switch {
	case strings.HasSuffix(a.method, "-cfb"): return cipher.NewCFBDecrypter(c, iv)
	default: return cipher.NewCTR(c, iv)
	}
}

type ssAEADConn struct {
	net.Conn
	cipher cipher.AEAD
}

const maxAEADRead = 16384

func (c *ssAEADConn) Read(b []byte) (int, error) {
	nonce := make([]byte, c.cipher.NonceSize())
	if _, err := io.ReadFull(c.Conn, nonce); err != nil { return 0, err }
	lenBuf := make([]byte, 2+c.cipher.Overhead())
	if _, err := io.ReadFull(c.Conn, lenBuf); err != nil { return 0, err }
	plain, err := c.cipher.Open(lenBuf[:0], nonce, lenBuf, nil)
	if err != nil { return 0, err }
	if len(plain) != 2 { return 0, fmt.Errorf("ss bad len") }
	n := int(plain[0])<<8 | int(plain[1])
	if n > maxAEADRead { n = maxAEADRead }
	cipherBuf := make([]byte, n+c.cipher.Overhead())
	if _, err := io.ReadFull(c.Conn, cipherBuf); err != nil { return 0, err }
	nonce2 := make([]byte, c.cipher.NonceSize())
	plain, err = c.cipher.Open(b[:0], nonce2, cipherBuf, nil)
	if err != nil { return 0, err }
	return copy(b, plain), nil
}

func (c *ssAEADConn) Write(b []byte) (int, error) {
	chunkSize := maxAEADRead - c.cipher.Overhead()
	nonce := make([]byte, c.cipher.NonceSize())
	total := 0
	for i := 0; i < len(b); i += chunkSize {
		end := i + chunkSize
		if end > len(b) { end = len(b) }
		chunk := b[i:end]
		lenBuf := []byte{byte(len(chunk) >> 8), byte(len(chunk))}
		enc := c.cipher.Seal(nil, nonce, lenBuf, nil)
		enc2 := c.cipher.Seal(nil, nonce, chunk, nil)
		enc = append(enc, enc2...)
		if _, err := c.Conn.Write(enc); err != nil { return total, err }
		total += len(chunk)
	}
	return total, nil
}

type ssStreamConn struct {
	net.Conn
	enc, dec cipher.Stream
}

func (c *ssStreamConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 { c.dec.XORKeyStream(b[:n], b[:n]) }
	return n, err
}

func (c *ssStreamConn) Write(b []byte) (int, error) {
	buf := make([]byte, len(b))
	c.enc.XORKeyStream(buf, b)
	return c.Conn.Write(buf)
}

func (ss *Shadowsocks) Latency(url string) (time.Duration, error) {
	host := url
	if !strings.Contains(host, ":") { host = host + ":443" }
	start := time.Now()
	conn, err := ss.Connect(context.Background(), host)
	if err != nil { return 0, err }
	conn.Close()
	return time.Since(start), nil
}

func (ss *Shadowsocks) Close() error { return nil }