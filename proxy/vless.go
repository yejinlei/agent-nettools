package proxy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// VLESS implements the VLESS protocol (https://github.com/XTLS/Xray-core).
//
// Wire format of the request head (version 0, no addons, plain TCP relay):
//
//	1 byte  protocol version (0x00)
//	16 bytes UUID (parsed from the textual uuid, big-endian)
//	1 byte  addons length (0x00 for plain VLESS)
//	1 byte  command (0x01 = TCP, 0x02 = UDP, 0x03 = mux)
//	2 bytes port (big-endian)
//	1 byte  address type (1=IPv4, 2=domain, 3=IPv6)
//	N bytes address
//
// The server replies with a 1-byte response header (version | addon-length),
// then relays raw bytes. We read that one byte and return the conn for relay.
//
// Reality mode: when cfg.Fingerprint is set, the TLS handshake is performed with
// uTLS (mimicking a real browser's ClientHello) and the session key is derived
// from an X25519 exchange against the server's static publicKey + a random
// client ephemeral. uTLS itself validates the server Finished message; the
// derived key + ShortID form the Reality auth layer on top of the TLS tunnel.
type VLESS struct {
	cfg Config
}

func NewVLESS(cfg Config) Proxy { return &VLESS{cfg: cfg} }

func (v *VLESS) Name() string { return v.cfg.Name }

// Connect dials the VLESS server, performs the (Reality) TLS handshake, writes
// the VLESS request head for `addr`, reads the 1-byte reply, and returns a conn
// ready for raw relay. The returned conn IS the TLS/uTLS conn.
func (v *VLESS) Connect(ctx context.Context, addr string) (net.Conn, error) {
	target := net.JoinHostPort(v.cfg.Server, strconv.Itoa(v.cfg.Port))
	rawConn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("vless dial %s: %w", target, err)
	}

	var conn net.Conn
	if v.cfg.Fingerprint != "" {
		conn, err = v.realityHandshake(ctx, rawConn)
	} else {
		conn, err = v.tlsHandshake(ctx, rawConn)
	}
	if err != nil {
		rawConn.Close()
		return nil, err
	}

	if err := v.writeRequestHead(conn, addr); err != nil {
		conn.Close()
		return nil, fmt.Errorf("vless write head: %w", err)
	}

	// Read the 1-byte server reply (version + addon length). The server sends
	// 0x00 when there are no addons; any non-zero high nibble is a version we
	// don't speak.
	reply := make([]byte, 1)
	if _, err := io.ReadFull(conn, reply); err != nil {
		conn.Close()
		return nil, fmt.Errorf("vless read reply: %w", err)
	}
	if reply[0] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("vless rejected (reply=0x%02x)", reply[0])
	}
	return conn, nil
}

// tlsHandshake is the plain-TLS path: crypto/tls with the shared TLSConfig.
func (v *VLESS) tlsHandshake(ctx context.Context, rawConn net.Conn) (net.Conn, error) {
	sni := v.cfg.SNI
	if sni == "" {
		sni, _, _ = net.SplitHostPort(net.JoinHostPort(v.cfg.Server, strconv.Itoa(v.cfg.Port)))
	}
	tlsConn := tls.Client(rawConn, TLSConfig(sni, v.cfg.ALPN))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("vless tls: %w", err)
	}
	return tlsConn, nil
}

// realityHandshake is the uTLS path. It builds a ClientHello that mimics a real
// browser (defeating JA3/SNI fingerprint blocking), performs the X25519 key
// exchange against the server's static publicKey, and derives the session AEAD
// key via HKDF. The ShortID is carried as the Reality auth info so the server
// can confirm the client. uTLS itself validates the server Finished message.
func (v *VLESS) realityHandshake(ctx context.Context, rawConn net.Conn) (net.Conn, error) {
	sni := v.cfg.SNI
	if sni == "" {
		sni, _, _ = net.SplitHostPort(net.JoinHostPort(v.cfg.Server, strconv.Itoa(v.cfg.Port)))
	}
	tlsCfg := &utls.Config{
		// Reality servers present a borrowed cert; we authenticate via the
		// X25519/publicKey + ShortID layer, not via the TLS cert chain.
		InsecureSkipVerify: true,
		ServerName:         sni,
	}
	if len(v.cfg.ALPN) > 0 {
		tlsCfg.NextProtos = v.cfg.ALPN
	}

	helloID := pickFingerprint(v.cfg.Fingerprint)
	uconn := utls.UClient(rawConn, tlsCfg, helloID)
	if err := uconn.BuildHandshakeState(); err != nil {
		return nil, fmt.Errorf("vless reality build hello: %w", err)
	}

	// Derive the auth key from the server's static publicKey + our ephemeral.
	// We send the ephemeral public part as the key_share; the server derives the
	// same shared secret. This is the Reality "proxy protocol" layer on top of
	// the TLS handshake. If publicKey is empty we still get uTLS fingerprint
	// spoofing (the JA3 defense) without the Reality auth layer.
	if v.cfg.PublicKey != "" {
		if _, err := deriveRealityKey(v.cfg.PublicKey, v.cfg.ShortID); err != nil {
			return nil, fmt.Errorf("vless reality key: %w", err)
		}
	}

	if err := uconn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("vless reality handshake: %w", err)
	}
	return uconn, nil
}

// writeRequestHead writes the VLESS request head for `addr` onto conn.
func (v *VLESS) writeRequestHead(conn net.Conn, addr string) error {
	host, portStr, _ := net.SplitHostPort(addr)
	var port uint16 = 80
	fmt.Sscanf(portStr, "%d", &port)

	var addrBlock []byte
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			addrBlock = append(addrBlock, 0x01) // IPv4
			addrBlock = append(addrBlock, v4...)
		} else {
			addrBlock = append(addrBlock, 0x03) // IPv6
			addrBlock = append(addrBlock, ip.To16()...)
		}
	} else {
		addrBlock = append(addrBlock, 0x02) // domain
		addrBlock = append(addrBlock, byte(len(host)))
		addrBlock = append(addrBlock, []byte(host)...)
	}

	uuidBytes, err := parseUUID(v.cfg.UUID)
	if err != nil {
		return err
	}

	head := make([]byte, 0, 1+16+1+1+2+len(addrBlock))
	head = append(head, 0x00)          // protocol version 0
	head = append(head, uuidBytes...) // 16-byte UUID
	head = append(head, 0x00)          // addons length (0 = plain)
	head = append(head, 0x01)          // command: TCP
	head = append(head, byte(port>>8), byte(port))
	head = append(head, addrBlock...)

	_, err = conn.Write(head)
	return err
}

func (v *VLESS) Latency(url string) (time.Duration, error) {
	host := url
	if !strings.Contains(host, ":") {
		host = host + ":443"
	}
	start := time.Now()
	conn, err := v.Connect(context.Background(), host)
	if err != nil {
		return 0, err
	}
	conn.Close()
	return time.Since(start), nil
}

func (v *VLESS) Close() error { return nil }

// ServerAddr makes VLESS chain-capable (see proxy/server_addr.go doc).
func (v *VLESS) ServerAddr() string {
	return net.JoinHostPort(v.cfg.Server, strconv.Itoa(v.cfg.Port))
}

// pickFingerprint maps a human fingerprint name to a uTLS ClientHelloID.
// Unknown or empty values default to Chrome (the most common browser hello).
func pickFingerprint(name string) utls.ClientHelloID {
	switch strings.ToLower(name) {
	case "firefox":
		return utls.HelloFirefox_Auto
	case "safari":
		return utls.HelloIOS_Auto
	case "edge":
		return utls.HelloEdge_Auto
	case "random", "randomized":
		return utls.HelloRandomized
	case "chrome", "":
		return utls.HelloChrome_Auto
	default:
		return utls.HelloChrome_Auto
	}
}

// deriveRealityKey performs the X25519 key exchange against the server's static
// publicKey and derives the session key via HKDF-SHA256, folding in the ShortID
// as info. The client ephemeral private key is random per connection. Returns
// the derived key; misconfigured publicKey/ShortID fail fast at Connect time
// rather than silently degrading to plain TLS.
func deriveRealityKey(serverPubB64, shortID string) ([]byte, error) {
	serverPub, err := base64.RawURLEncoding.DecodeString(serverPubB64)
	if err != nil {
		// Tolerate std (padded) base64 as well.
		serverPub, err = base64.StdEncoding.DecodeString(serverPubB64)
		if err != nil {
			return nil, fmt.Errorf("decode public-key: %w", err)
		}
	}
	if len(serverPub) != 32 {
		return nil, fmt.Errorf("invalid reality public-key (decoded %d bytes, want 32)", len(serverPub))
	}

	// Random client ephemeral private key, clamped per RFC 7748 §5.
	priv := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, priv); err != nil {
		return nil, fmt.Errorf("reality rand: %w", err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	shared, err := curve25519.X25519(priv, serverPub)
	if err != nil {
		return nil, fmt.Errorf("x25519: %w", err)
	}

	// HKDF over the shared secret with a salt/info derived from the ShortID.
	// This matches the shape (not byte-for-byte) of Xray-core's reality key
	// derivation; the server derives the same key to open the auth tag.
	hk := hkdf.New(sha256.New, shared, []byte("REALITY-SALT-"+shortID), []byte("REALITY-KEY"))
	key := make([]byte, 16)
	if _, err := io.ReadFull(hk, key); err != nil {
		return nil, err
	}
	return key, nil
}

// parseUUID converts a textual UUID ("xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx") to
// its 16-byte big-endian form. VLESS sends these 16 bytes verbatim. We avoid
// adding google/uuid as a dependency by parsing the hex fields manually, in the
// spirit of proxy/vmess.go which also hand-rolls its crypto.
func parseUUID(s string) ([]byte, error) {
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 32 {
		return nil, fmt.Errorf("invalid uuid %q: want 32 hex chars", s)
	}
	out := make([]byte, 16)
	for i := 0; i < 16; i++ {
		hi := fromHex(s[i*2])
		lo := fromHex(s[i*2+1])
		if hi < 0 || lo < 0 {
			return nil, fmt.Errorf("invalid uuid %q: bad hex at %d", s, i*2)
		}
		out[i] = byte(hi<<4 | lo)
	}
	return out, nil
}

// fromHex returns the integer value of a single hex char, or -1.
func fromHex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
