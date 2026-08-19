// Package forward implements the generic TCP/UDP forwarding primitives shared
// by the `forward` CLI subcommand and the agent. The four modes mirror SSH's
// port-forwarding mental model so they're easy to remember:
//
//   - Local:   forward local <listen> <dst>            (-L)  local listener → fixed dst
//   - Remote:  forward remote <sshHost> <rlisten> <dst> (-R)  listener on a remote SSH host → fixed dst (dialed from the host)
//   - Dynamic: forward dynamic <listen>                  (-D)  local SOCKS5 listener → any dst (chosen per connection)
//   - TLS:     forward tls <listen> <dst> [sni]                HTTPS listener → plain-HTTP backend (TLS termination)
//
// A Dialer decides how the *destination* is reached. Passing a proxy's
// Connect makes forwarding go *through* that proxy; net.DialContext makes it
// direct. This is the seam that lets forwarding compose with the proxy layer
// (and with Chain) — the same dial callback, one architecture.
package forward

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"time"
)

// Dialer dials a destination address and returns a connection. The zero value
// semantics: nil Dialer = direct net.Dial. A proxy's Connect satisfies this
// signature, so forwarding "through a proxy" is just passing proxy.Connect.
type Dialer func(ctx context.Context, addr string) (net.Conn, error)

// Direct is the default Dialer: plain TCP dial with a 10s timeout.
func Direct(ctx context.Context, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, "tcp", addr)
}

// Local forwards a local TCP listener to a fixed destination via dialer.
// Blocks until ctx is cancelled. listen is a host:port ("127.0.0.1:8080" or
// ":8080"). dst is host:port.
func Local(ctx context.Context, listen, dst string, dialer Dialer) error {
	if dialer == nil {
		dialer = Direct
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listen, err)
	}
	defer ln.Close()
	log.Printf("forward: local %s → %s", listen, dst)
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept %s: %w", listen, err)
		}
		go func(c net.Conn) {
			defer c.Close()
			remote, err := dialer(ctx, dst)
			if err != nil {
				log.Printf("forward: dial %s: %v", dst, err)
				return
			}
			defer remote.Close()
			relay(c, remote)
		}(c)
	}
}

// Dynamic forwards a local SOCKS5 listener that dials any destination the
// client requests, via dialer. This is the SOCKS5 "CONNECT" half (TCP only);
// it reuses the listener package's SOCKS5 framing conceptually but is
// self-contained so `forward dynamic` works standalone.
//
// NOTE: this implements the SOCKS5 no-auth CONNECT command (CMD 0x01) only.
// UDP ASSOCIATE is a future extension (Batch 3).
func Dynamic(ctx context.Context, listen string, dialer Dialer) error {
	if dialer == nil {
		dialer = Direct
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listen, err)
	}
	defer ln.Close()
	log.Printf("forward: dynamic (SOCKS5) on %s", listen)
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept %s: %w", listen, err)
		}
		go handleSOCKS5Connect(ctx, c, dialer)
	}
}

// Remote forwards a listener created ON a remote SSH host back to a local
// destination — SSH-style -R. It connects to sshHost (host:port), then asks
// the SSH server to listen on remoteListen (host:port on the remote side);
// connections arriving there are tunneled back and dialed to local dst.
//
// sshHost is host:port of an SSH server; auth comes from the agent's memory /
// HIL via the caller-providing an *ssh.Client (see DialSSH). To keep this
// package free of SSH deps, the caller passes an already-connected ssh.Client
// and a function that opens a remote listener on it. For the standalone
// command, agent.DialSSH + ssh.Client.Listen is used (wired in cmd/forward.go).
func Remote(ctx context.Context, remoteListen, dst string, dialer Dialer, openRemote func(ctx context.Context, addr string) (net.Listener, error)) error {
	if dialer == nil {
		dialer = Direct
	}
	rl, err := openRemote(ctx, remoteListen)
	if err != nil {
		return fmt.Errorf("open remote listener %s: %w", remoteListen, err)
	}
	defer rl.Close()
	log.Printf("forward: remote %s (on SSH host) → %s (local)", remoteListen, dst)
	go func() { <-ctx.Done(); rl.Close() }()
	for {
		c, err := rl.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("remote accept %s: %w", remoteListen, err)
		}
		go func(c net.Conn) {
			defer c.Close()
			// The remote side gives us a connection that the SSH client already
			// tunneled; we dial the local dst and relay.
			remote, err := dialer(ctx, dst)
			if err != nil {
				log.Printf("forward: dial %s: %v", dst, err)
				return
			}
			defer remote.Close()
			relay(c, remote)
		}(c)
	}
}

// TLS forwards an HTTPS listener to a plain-HTTP backend by terminating TLS
// on the listener side and forwarding the decrypted HTTP to dst. This is the
// original `forward` command's behavior, preserved as a mode. sni, if empty,
// is inferred from dst's host.
func TLS(ctx context.Context, listen, dst, sni string) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listen, err)
	}
	defer ln.Close()
	log.Printf("forward: tls %s (HTTPS) → %s (HTTP)", listen, dst)
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept %s: %w", listen, err)
		}
		go func(c net.Conn) {
			defer c.Close()
			tlsCfg := &tls.Config{InsecureSkipVerify: true}
			if sni == "" {
				if h, _, e := net.SplitHostPort(dst); e == nil {
					sni = h
				}
			}
			if sni != "" {
				tlsCfg.ServerName = sni
			}
			tlsConn := tls.Server(c, tlsCfg)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			dst2, err := Direct(ctx, dst)
			if err != nil {
				tlsConn.Close()
				return
			}
			defer dst2.Close()
			relay(tlsConn, dst2)
		}(c)
	}
}

// relay copies bidirectionally and closes both ends. Centralized so all modes
// share one shutdown semantics.
func relay(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(b, a); done <- struct{}{}; b.Close() }()
	go func() { io.Copy(a, b); done <- struct{}{}; a.Close() }()
	<-done
	<-done
}

// handleSOCKS5Connect implements the minimal SOCKS5 no-auth CONNECT handshake
// then relays. It does NOT do username/password auth (no-auth 0x00 only); a
// downstream SOCKS5 proxy is reached via the dialer, not here.
func handleSOCKS5Connect(ctx context.Context, c net.Conn, dialer Dialer) {
	defer c.Close()
	// Greeting: ver, nmethods, methods → reply ver, method (0x00 no-auth).
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return
	}
	if hdr[0] != 0x05 {
		return
	}
	if _, err := io.ReadFull(c, make([]byte, int(hdr[1]))); err != nil {
		return
	}
	c.Write([]byte{0x05, 0x00})

	// Request: ver, cmd, rsv, atyp, addr, port.
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return
	}
	if req[0] != 0x05 || req[1] != 0x01 { // only CONNECT
		c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	host, port, ok := readSOCKS5Addr(c, req[3])
	if !ok {
		c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	dst := net.JoinHostPort(host, strconv.Itoa(port))
	remote, err := dialer(ctx, dst)
	if err != nil {
		log.Printf("forward: socks5 dial %s: %v", dst, err)
		c.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) // host unreachable
		return
	}
	defer remote.Close()
	// Success: bind addr = 0.0.0.0:0.
	c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	relay(c, remote)
}

// readSOCKS5Addr reads the address portion of a SOCKS5 request after atyp.
func readSOCKS5Addr(c net.Conn, atyp byte) (host string, port int, ok bool) {
	switch atyp {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, false
		}
		host = net.IP(b).String()
	case 0x03: // domain
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return "", 0, false
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, false
		}
		host = string(b)
	case 0x04: // IPv6
		b := make([]byte, 16)
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, false
		}
		host = net.IP(b).String()
	default:
		return "", 0, false
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return "", 0, false
	}
	return host, int(uint16(pb[0])<<8|uint16(pb[1])), true
}
