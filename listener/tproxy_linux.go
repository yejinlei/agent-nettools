//go:build linux

package listener

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
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
// the originally-targeted endpoint. We return that as a *net.TCPAddr so
// serveTProxy can feed it to Router.Pick.
func tproxyListen(addr string) (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, unix.IPPROTO_TCP)
	if err != nil {
		return nil, fmt.Errorf("tproxy socket: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("tproxy setsockopt SO_REUSEADDR: %w", err)
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
	ip4 := lsa.IP.To4()
	if ip4 == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("tproxy bind %s: not an IPv4 address", addr)
	}
	sa := &unix.SockaddrInet4{Port: lsa.Port, Addr: [4]byte(ip4)}
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
	fd     int
	mu     sync.Mutex
	closed bool
	addr   *net.TCPAddr
}

func (l *tproxyListener) Accept() (net.Conn, error) {
	for {
		cfd, _, err := unix.Accept(l.fd)
		if err != nil {
			return nil, err
		}
		if err := unix.SetsockoptInt(cfd, unix.SOL_IP, unix.IP_TRANSPARENT, 1); err != nil {
			unix.Close(cfd)
			continue
		}
		orig := origDst(cfd)
		if orig == nil {
			if orig, err = peerTCPAddr(cfd); err != nil {
				unix.Close(cfd)
				continue
			}
		}
		f := os.NewFile(uintptr(cfd), fmt.Sprintf("tproxy-conn-%d", cfd))
		conn, err := net.FileConn(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		tc, ok := conn.(net.Conn)
		if !ok {
			conn.Close()
			return nil, fmt.Errorf("net.FileConn returned non-Conn")
		}
		return &tproxyConn{Conn: tc, orig: orig}, nil
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

type tproxyConn struct {
	net.Conn
	orig *net.TCPAddr
}

func (c *tproxyConn) RemoteAddr() net.Addr { return c.orig }

func peerTCPAddr(fd int) (*net.TCPAddr, error) {
	sa, err := unix.Getpeername(fd)
	if err != nil {
		return nil, err
	}
	pa, ok := sa.(*unix.SockaddrInet4)
	if !ok {
		return nil, fmt.Errorf("peer addr is not IPv4: %T", sa)
	}
	return &net.TCPAddr{IP: net.IP(pa.Addr[:]), Port: pa.Port}, nil
}

func origDst(fd int) *net.TCPAddr {
	oob := make([]byte, 1024)
	_, oobn, _, _, err := unix.Recvmsg(fd, nil, oob, unix.MSG_PEEK)
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
		return &net.TCPAddr{
			IP:   net.IPv4(msg.Data[4], msg.Data[5], msg.Data[6], msg.Data[7]),
			Port: int(port),
		}
	}
	return nil
}