package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type FRPProxy struct {
	cfg Config
}

func NewFRP(cfg Config) Proxy { return &FRPProxy{cfg: cfg} }

func (f *FRPProxy) Name() string  { return f.cfg.Name }
func (f *FRPProxy) Close() error  { return nil }

func (f *FRPProxy) ServerAddr() string {
	port := f.cfg.Port
	if port == 0 {
		port = 7000
	}
	return net.JoinHostPort(f.cfg.Server, strconv.Itoa(port))
}

func (f *FRPProxy) Connect(ctx context.Context, addr string) (net.Conn, error) {
	port := parsePort(addr, 80)
	serverAddr := f.ServerAddr()
	conn, err := dialContext(ctx, "tcp", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("frp: dial %s: %w", serverAddr, err)
	}
	if err := writeFRPHeader(conn, f.cfg.Password, port); err != nil {
		conn.Close()
		return nil, err
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(conn, buf); err != nil {
		conn.Close()
		return nil, fmt.Errorf("frp: read status: %w", err)
	}
	if buf[0] != 0x01 {
		conn.Close()
		return nil, fmt.Errorf("frp: server rejected")
	}
	return conn, nil
}

func (f *FRPProxy) Latency(url string) (time.Duration, error) {
	host := url
	if !strings.Contains(host, ":") {
		host = host + ":80"
	}
	start := time.Now()
	conn, err := f.Connect(context.Background(), host)
	if err != nil {
		return 0, err
	}
	conn.Close()
	return time.Since(start), nil
}

func RunServer(ctx context.Context, secret string, port int, targetHost string) error {
	if targetHost == "" {
		targetHost = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return fmt.Errorf("frp: listen :%d: %w", port, err)
	}
	fmt.Printf("frp: server listening on %s\n", ln.Addr())
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				fmt.Printf("frp: accept: %v\n", err)
				continue
			}
		}
		go handleFRPClient(ctx, conn, secret, targetHost)
	}
}

func handleFRPClient(ctx context.Context, conn net.Conn, secret, targetHost string) {
	defer conn.Close()
	secretBytes, remotePort, err := readFRPHeader(conn)
	if err != nil {
		fmt.Printf("frp: header: %v\n", err)
		conn.Write([]byte{0x02})
		return
	}
	if string(secretBytes) != secret {
		conn.Write([]byte{0x02})
		return
	}
	target := targetHost + ":" + strconv.Itoa(int(remotePort))
	remote, err := net.Dial("tcp", target)
	if err != nil {
		fmt.Printf("frp: dial %s: %v\n", target, err)
		conn.Write([]byte{0x02})
		return
	}
	conn.Write([]byte{0x01})
	relayBidir(conn, remote)
}

func writeFRPHeader(w io.Writer, secret string, port int) error {
	buf := make([]byte, 0, 7+len(secret))
	buf = append(buf, 0, 0, 0, byte(len(secret)))
	buf = append(buf, []byte(secret)...)
	buf = append(buf, 0x01, byte(port>>8), byte(port))
	_, err := w.Write(buf)
	return err
}

func readFRPHeader(r io.Reader) ([]byte, uint16, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return nil, 0, err
	}
	secretLen := int(binary.BigEndian.Uint32(lenBuf))
	if secretLen > 1024 {
		return nil, 0, fmt.Errorf("frp: secret too long")
	}
	secret := make([]byte, secretLen)
	if secretLen > 0 {
		if _, err := io.ReadFull(r, secret); err != nil {
			return nil, 0, err
		}
	}
	tail := make([]byte, 3)
	if _, err := io.ReadFull(r, tail); err != nil {
		return nil, 0, err
	}
	if tail[0] != 0x01 {
		return nil, 0, fmt.Errorf("frp: unknown mode 0x%02x", tail[0])
	}
	port := binary.BigEndian.Uint16(tail[1:3])
	return secret, port, nil
}

func RunClient(ctx context.Context, localPort, remotePort int, serverAddr, secret string) error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(localPort))
	if err != nil {
		return fmt.Errorf("frp: listen :%d: %w", localPort, err)
	}
	fmt.Printf("frp: client listening on %s -> via %s -> :%d\n", ln.Addr(), serverAddr, remotePort)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}
		go relayFRPClient(ctx, conn, serverAddr, secret, remotePort)
	}
}

func relayFRPClient(ctx context.Context, local net.Conn, serverAddr, secret string, remotePort int) {
	defer local.Close()
	frps, err := net.Dial("tcp", serverAddr)
	if err != nil {
		fmt.Printf("frp: dial %s: %v\n", serverAddr, err)
		return
	}
	defer frps.Close()
	if err := writeFRPHeader(frps, secret, remotePort); err != nil {
		return
	}
	status := make([]byte, 1)
	if _, err := io.ReadFull(frps, status); err != nil {
		return
	}
	if status[0] != 0x01 {
		fmt.Printf("frp: server rejected\n")
		return
	}
	relayBidir(local, frps)
}

func relayBidir(a, b net.Conn) {
	done := make(chan error, 2)
	go func() { _, err := io.Copy(a, b); done <- err }()
	go func() { _, err := io.Copy(b, a); done <- err }()
	<-done
}

func parsePort(addr string, def int) int {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		if p, err := strconv.Atoi(addr[i+1:]); err == nil {
			return p
		}
	}
	return def
}

func dialContext(ctx context.Context, proto, addr string) (net.Conn, error) {
	d := &net.Dialer{}
	return d.DialContext(ctx, proto, addr)
}
