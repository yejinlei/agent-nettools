//go:build linux

package listener

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"

	"golang.org/x/sys/unix"
)

// tproxyListen creates a TCP TPROXY listener on the given address. It opens a
// raw socket with IP_TRANSPARENT so the kernel's TPROXY redirect lands on it,
// then returns a net.Listener that accepts incoming connections with their
// original destination intact.
//
// Imported by Options.TProxyPort in http.go. Config schema: `listen.tproxy:
// <port>` (Listen.TProxyPort in config.go).
//
// Original destination extraction: after unix.Accept we issue a zero-length
// recvmsg with MSG_PEEK; the kernel attaches an IP_ORIGDSTADDR cmsg carrying
// the SockaddrInet4 of the originally-targeted endpoint. We return that as
// the net.Addr so serveTProxy can feed it to Router.Pick.
func tproxyListen(addr string) (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_REUSEADDR, unix.IPPROTO_TCP)
	if err != nil {
		return nil, fmt.Errorf("tproxy socket: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_IP, unix.IP_TRANSPARENT, 1); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("tproxy setsockopt IP_TRANSPARENT: %w", err)
	}
	lsa, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	sa, err := unix.RunningAddr(lsa)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	if err := unix.Bind(fd, sa); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("tproxy bind %s: %w", addr, err)
	}
	if err := unix.Listen(fd, 128); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("tproxy listen %s: %w", addr, err)
	}
	return &tproxyListener{fd: fd, addr: &net.TCPAddr{IP: lsa.IP, Port: lsa.Port}}, nil
}

type tproxyListener struct {
	fd   int
	mu   sync.Mutex
	closed bool
	addr *net.TCPAddr
}

func (l *tproxyListener) Accept() (net.Conn, error) {
	for {
		cfd, _, err := unix.Accept(l.fd)
		if err != nil {
			return nil, err
		}
		// Set IP_TRANSPARENT on the accepted socket so the kernel knows we
		// will be sending on behalf of the original source.
		if err := unix.SetsockoptInt(cfd, unix.SOL_IP, unix.IP_TRANSPARENT, 1); err != nil {
			unix.Close(cfd)
			continue
		}
		// Original destination (the address the client intended to hit).
		orig := origDst(cfd)
		if orig == nil {
			// Fall back to the peer addr (works when orig-dst is unavailable).
			orig, err = unix.Getpeername(cfd)
			if err != nil {
				unix.Close(cfd)
				continue
			}
		}
		pa, ok := orig.(*unix.SockaddrInet4)
		if !ok {
			unix.Close(cfd)
			continue
		}
		// Wrap cfd in a net.Conn using net.FileConn.
		f := unix.NewFile(uintptr(cfd), fmt.Sprintf("tproxy-conn-%d", cfd))
		conn, err := net.FileConn(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		tc, ok := conn.(net.Conn)
		if !ok {
			conn.Close()
			return nil, fmt.Errorf("net.FileConn returned non-Conn")
		}
		return &tproxyConn{Conn: tc, orig: &net.TCPAddr{
			IP:   net.IP(pa.Addr[:]),
			Port: int(binary.BigEndian.Uint16(pa.Port[:])),
		}}, nil
	}
}

func (l *tproxyListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return unix.Close(l.fd)
}

func (l *tproxyListener) Addr() net.Addr { return l.addr }

// tproxyConn wraps a raw net.Conn and exposes the original destination as
// RemoteAddr, so serveTProxy can hand that address to Router.Pick.
type tproxyConn struct {
	net.Conn
	orig *net.TCPAddr
}

func (c *tproxyConn) RemoteAddr() net.Addr { return c.orig }

// origDst issues a zero-length recvmsg(MSG_PEEK) and parses the
// IP_ORIGDSTADDR control message to recover the originally-targeted address.
// If the control message is not present (e.g. no TPROXY rule), it returns nil.
func origDst(fd int) net.Addr {
	oob := make([]byte, 1024)
	n, oobn, _, _, err := unix.Recvmsg(nil, oob, unix.MSG_PEEK)
	_ = n
	if err != nil || oobn == 0 {
		return nil
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return nil
	}
	for _, msg := range msgs {
		if msg.Header.Level != unix.IPPROTO_IP || msg.Header.Type != unix.IP_ORIGDSTADDR {
			continue
		}
		if len(msg.Data) < 8 {
			continue
		}
		port := binary.BigEndian.Uint16(msg.Data[0:2])
		family := binary.LittleEndian.Uint16(msg.Data[2:4])
		if family != unix.AF_INET {
			continue
		}
		return &unix.SockaddrInet4{
			Family: unix.AF_INET,
			Port:   uint16(port),
			Addr:   [4]byte(msg.Data[4:8]),
		}
	}
	return nil
}