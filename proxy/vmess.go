package proxy

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

type VMess struct {
	cfg Config
}

func NewVMess(cfg Config) Proxy { return &VMess{cfg: cfg} }

func (v *VMess) Name() string { return v.cfg.Name }

func (v *VMess) Connect(ctx context.Context, addr string) (net.Conn, error) {
	target := fmt.Sprintf("%s:%d", v.cfg.Server, v.cfg.Port)
	conn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("vmess dial %s: %w", target, err)
	}

	host, portStr, _ := net.SplitHostPort(addr)
	var port uint16 = 80
	fmt.Sscanf(portStr, "%d", &port)

	ips, _ := net.LookupIP(host)
	var remoteAddr []byte
	if len(ips) > 0 {
		ip := ips[0].To4()
		if ip != nil {
			remoteAddr = append(remoteAddr, 1)
			remoteAddr = append(remoteAddr, ip...)
		}
	}
	if len(remoteAddr) == 0 {
		remoteAddr = append(remoteAddr, 3, byte(len(host)))
		remoteAddr = append(remoteAddr, []byte(host)...)
	}
	remoteAddr = append(remoteAddr, byte(port>>8), byte(port))

	head := make([]byte, 0, 2+len(remoteAddr))
	head = append(head, 0x01, 0x00)
	head = append(head, remoteAddr...)

	uuidBytes := make([]byte, 32)
	copy(uuidBytes, []byte(v.cfg.UUID))

	c, err := aes.NewCipher(uuidBytes[:16])
	if err != nil { return nil, err }
	iv := uuidBytes[16:32]
	encStream := cipher.NewCFBEncrypter(c, iv)
	decStream := cipher.NewCFBDecrypter(c, iv)

	encHead := make([]byte, len(head))
	encStream.XORKeyStream(encHead, head)

	if _, err := conn.Write(encHead); err != nil { conn.Close(); return nil, err }

	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil { conn.Close(); return nil, err }

	return &vmessConn{Conn: conn, enc: encStream, dec: decStream}, nil
}

type vmessConn struct {
	net.Conn
	enc, dec cipher.Stream
}

func (c *vmessConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 { c.dec.XORKeyStream(b[:n], b[:n]) }
	return n, err
}

func (c *vmessConn) Write(b []byte) (int, error) {
	buf := make([]byte, len(b))
	c.enc.XORKeyStream(buf, b)
	return c.Conn.Write(buf)
}

func (v *VMess) Latency(url string) (time.Duration, error) {
	host := url
	if !strings.Contains(host, ":") { host = host + ":443" }
	start := time.Now()
	conn, err := v.Connect(context.Background(), host)
	if err != nil { return 0, err }
	conn.Close()
	return time.Since(start), nil
}

func (v *VMess) Close() error { return nil }