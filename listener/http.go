package listener

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"agent-nettools/router"
)

type Options struct {
	HTTPPort   int
	SOCKS5Port int
	Router     *router.Router
}

type Listener struct {
	opts Options

	mu      sync.Mutex
	httpLn  net.Listener
	socksLn net.Listener
	closed  bool
	stopCh  chan struct{}
	errs    chan error
}

func New(opts Options) (*Listener, error) {
	return &Listener{opts: opts}, nil
}

// Start binds the HTTP and SOCKS5 listeners synchronously (so port conflicts
// surface immediately) and then blocks serving until Stop is called or a
// listener suffers a fatal error. Returns the first fatal error, or nil if
// stopped cleanly.
func (l *Listener) Start() error {
	l.stopCh = make(chan struct{})
	l.errs = make(chan error, 2)

	if l.opts.HTTPPort > 0 {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", l.opts.HTTPPort))
		if err != nil {
			return fmt.Errorf("http listen :%d: %w", l.opts.HTTPPort, err)
		}
		l.mu.Lock()
		l.httpLn = ln
		l.mu.Unlock()
		go l.serveHTTP(ln)
	}
	if l.opts.SOCKS5Port > 0 {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", l.opts.SOCKS5Port))
		if err != nil {
			l.Stop() // release anything already bound
			return fmt.Errorf("socks5 listen :%d: %w", l.opts.SOCKS5Port, err)
		}
		l.mu.Lock()
		l.socksLn = ln
		l.mu.Unlock()
		go l.serveSOCKS5(ln)
	}

	// If neither port is configured there is nothing to serve.
	if l.opts.HTTPPort == 0 && l.opts.SOCKS5Port == 0 {
		return nil
	}

	select {
	case <-l.stopCh:
		return nil
	case err := <-l.errs:
		l.Stop()
		return err
	}
}

// Stop closes both listeners and unblocks Start. Idempotent.
func (l *Listener) Stop() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	httpLn, socksLn, stopCh := l.httpLn, l.socksLn, l.stopCh
	l.mu.Unlock()

	if httpLn != nil {
		httpLn.Close()
	}
	if socksLn != nil {
		socksLn.Close()
	}
	if stopCh != nil {
		close(stopCh)
	}
}

func (l *Listener) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// fatalAccept reports a listener accept error. Shutdown-induced errors (closed)
// are silent; genuine errors unblock Start via errs.
func (l *Listener) fatalAccept(ln net.Listener, src string, err error) {
	if l.isClosed() {
		return
	}
	select {
	case l.errs <- fmt.Errorf("%s: %w", src, err):
	default:
	}
}

func (l *Listener) serveHTTP(ln net.Listener) {
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			l.fatalAccept(ln, "http listener", err)
			return
		}
		go l.handleHTTP(conn)
	}
}

func (l *Listener) handleHTTP(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	reqLine, err := reader.ReadString('\n')
	if err != nil { return }
	method, target, _, err := parseRequestLine(strings.TrimSpace(reqLine))
	if err != nil { return }

	if method != "CONNECT" {
		u, err := url.Parse(target)
		if err != nil { return }
		if u.Scheme == "https" { return }
		remoteAddr := u.Host
		if !strings.Contains(remoteAddr, ":") { remoteAddr = remoteAddr + ":80" }

		proxy, err := l.opts.Router.Pick(remoteAddr)
		if err != nil {
			log.Printf("pick proxy for %s: %v", remoteAddr, err)
			return
		}

		// Get upstream connection — through proxy or direct
		var remote net.Conn
		if proxy.Name() == "DIRECT" {
			remote, err = net.DialTimeout("tcp", remoteAddr, 10*time.Second)
		} else {
			remote, err = proxy.Connect(context.Background(), remoteAddr)
		}
		if err != nil {
			log.Printf("connect %s via %s: %v", remoteAddr, proxy.Name(), err)
			return
		}

		req := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: %s\r\n\r\n", method, target, u.Host)
		remote.Write([]byte(req))
		go io.Copy(conn, remote)
		io.Copy(remote, conn)
		remote.Close()
		return
	}

	proxy, err := l.opts.Router.Pick(target)
	if err != nil {
		log.Printf("pick proxy for %s: %v", target, err)
		return
	}
	remote, err := proxy.Connect(context.Background(), target)
	if err != nil {
		log.Printf("proxy connect %s via %s: %v", target, proxy.Name(), err)
		return
	}
	conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	go io.Copy(remote, conn)
	io.Copy(conn, remote)
}

func parseRequestLine(line string) (string, string, string, error) {
	parts := strings.Fields(line)
	if len(parts) < 2 { return "", "", "", fmt.Errorf("bad request") }
	return parts[0], parts[1], parts[2], nil
}

func (l *Listener) serveSOCKS5(ln net.Listener) {
	defer ln.Close()
	for {
		c, err := ln.Accept()
		if err != nil {
			if l.isClosed() {
				return
			}
			log.Printf("socks5 accept: %v", err)
			return
		}
		go l.handleSOCKS5(c)
	}
}

func (l *Listener) handleSOCKS5(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 256)
	n, err := io.ReadFull(conn, buf[:3])
	if err != nil || n < 3 { return }
	conn.Write([]byte{0x05, 0x00})

	cmdBuf := make([]byte, 256)
	m, err := io.ReadFull(conn, cmdBuf[:5])
	if err != nil || m < 5 { return }
	at := cmdBuf[3]
	switch at {
	case 0x01:
		io.ReadFull(conn, cmdBuf[:6])
	case 0x03:
		len := int(cmdBuf[4])
		io.ReadFull(conn, cmdBuf[:5+len])
	case 0x04:
		io.ReadFull(conn, cmdBuf[:18])
	}

	host := ""
	port := uint16(0)
	switch at {
	case 0x01:
		ip := net.IPv4(cmdBuf[4], cmdBuf[5], cmdBuf[6], cmdBuf[7])
		host = ip.String()
		port = uint16(cmdBuf[8])<<8 | uint16(cmdBuf[9])
	case 0x03:
		host = string(cmdBuf[5 : 5+cmdBuf[4]])
		port = uint16(cmdBuf[5+cmdBuf[4]])<<8 | uint16(cmdBuf[5+cmdBuf[4]+1])
	}
	remoteAddr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	proxy, err := l.opts.Router.Pick(remoteAddr)
	if err != nil {
		log.Printf("pick proxy for %s: %v", remoteAddr, err)
		return
	}

	var remote net.Conn
	if proxy.Name() == "DIRECT" {
		remote, err = net.DialTimeout("tcp", remoteAddr, 10*time.Second)
	} else {
		remote, err = proxy.Connect(context.Background(), remoteAddr)
	}
	if err != nil {
		log.Printf("connect %s via %s: %v", remoteAddr, proxy.Name(), err)
		return
	}
	defer remote.Close()

	boundIP, boundPort := getBound(conn)
	boundIPBytes := boundIP.To4()
	if boundIPBytes == nil { boundIPBytes = boundIP.To16() }
	resp := []byte{0x05, 0x00, 0x00, 0x01}
	resp = append(resp, boundIPBytes...)
	resp = append(resp, byte(boundPort>>8), byte(boundPort))
	conn.Write(resp)

	go io.Copy(remote, conn)
	io.Copy(conn, remote)
}

func getBound(conn net.Conn) (net.IP, uint16) {
	addr, _ := net.ResolveTCPAddr("tcp", conn.LocalAddr().String())
	return addr.IP, uint16(addr.Port)
}
