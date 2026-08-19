package dns

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type Server struct {
	cfg     DnsConfig
	resolver Resolver
	fakeDns *FakeDns
	udpConn *net.UDPConn
	tcpLn   net.Listener
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
}

type DnsConfig struct {
	Enable    bool
	Listen    string
	Mode      string
	DoHServer string
	DoTServer string
	FakeCIDR  string
}

func NewServer(cfg DnsConfig) (*Server, error) {
	var resolver Resolver
	switch cfg.Mode {
	case "doh":
		resolver = NewDoHResolver(cfg.DoHServer)
	case "dot":
		resolver = NewDoTResolver(cfg.DoTServer)
	default:
		resolver = &DirectResolver{}
	}
	resolver = NewCacheResolver(resolver, 5*time.Minute)

	var fakeDns *FakeDns
	if cfg.FakeCIDR != "" {
		var err error
		fakeDns, err = NewFakeDns(cfg.FakeCIDR)
		if err != nil {
			return nil, fmt.Errorf("fake dns: %w", err)
		}
	}
	return &Server{cfg: cfg, resolver: resolver, fakeDns: fakeDns}, nil
}

func (s *Server) FakeDns() *FakeDns { return s.fakeDns }

func (s *Server) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)
	udpAddr, err := net.ResolveUDPAddr("udp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("resolve udp: %w", err)
	}
	s.udpConn, err = net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("resolve tcp: %w", err)
	}
	s.tcpLn, err = net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		s.udpConn.Close()
		return fmt.Errorf("listen tcp: %w", err)
	}
	log.Printf("dns: listening on %s (mode=%s)", s.cfg.Listen, s.cfg.Mode)
	s.wg.Add(2)
	go s.serveUDP(ctx)
	go s.serveTCP(ctx)
	<-ctx.Done()
	s.Stop()
	return nil
}

func (s *Server) Stop() {
	s.cancel()
	if s.udpConn != nil {
		s.udpConn.Close()
	}
	if s.tcpLn != nil {
		s.tcpLn.Close()
	}
	s.wg.Wait()
}

func (s *Server) serveUDP(ctx context.Context) {
	defer s.wg.Done()
	buf := make([]byte, 1500)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		s.udpConn.SetDeadline(time.Now().Add(2 * time.Second))
		n, addr, err := s.udpConn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		go s.handleUDPQuery(data, addr)
	}
}

func (s *Server) serveTCP(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		s.tcpLn.(*net.TCPListener).SetDeadline(time.Now().Add(2 * time.Second))
		conn, err := s.tcpLn.Accept()
		if err != nil {
			continue
		}
		go s.handleTCPQuery(conn)
	}
}

func (s *Server) handleUDPQuery(data []byte, addr *net.UDPAddr) {
	response := s.processQuery(data)
	if response == nil {
		return
	}
	s.udpConn.WriteToUDP(response, addr)
}

func (s *Server) handleTCPQuery(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return
	}
	msgLen := binary.BigEndian.Uint16(lenBuf)
	msgBuf := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, msgBuf); err != nil {
		return
	}
	response := s.processQuery(msgBuf)
	if response == nil {
		return
	}
	respLen := make([]byte, 2)
	binary.BigEndian.PutUint16(respLen, uint16(len(response)))
	conn.Write(respLen)
	conn.Write(response)
}

func (s *Server) processQuery(data []byte) []byte {
	var msg dnsmessage.Message
	if err := msg.Unpack(data); err != nil {
		return nil
	}
	if len(msg.Questions) == 0 {
		return nil
	}
	q := msg.Questions[0]
	domain := strings.TrimSuffix(q.Name.String(), ".")

	if s.fakeDns != nil {
		if ip, ok := s.fakeDns.IP(domain); ok && q.Type == dnsmessage.TypeA {
			msg.Response = true
			msg.Answers = []dnsmessage.Resource{
				{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 300},
					Body: &dnsmessage.AResource{A: [4]byte(ip.To4())}},
			}
			packed, _ := msg.Pack()
			return packed
		}
	}

	ips, err := s.resolver.Resolve(domain, q.Type)
	if err != nil {
		log.Printf("dns: resolve %s: %v", domain, err)
		msg.Header.RCode = dnsmessage.RCode(2)
		msg.Response = true
		packed, _ := msg.Pack()
		return packed
	}

	msg.Response = true
	for _, ip := range ips {
		if ip.To4() != nil && q.Type == dnsmessage.TypeA {
			msg.Answers = append(msg.Answers, dnsmessage.Resource{
				Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 300},
				Body:   &dnsmessage.AResource{A: [4]byte(ip.To4())},
			})
		} else if ip.To4() == nil && q.Type == dnsmessage.TypeAAAA {
			msg.Answers = append(msg.Answers, dnsmessage.Resource{
				Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET, TTL: 300},
				Body:   &dnsmessage.AAAAResource{AAAA: [16]byte(ip.To16())},
			})
		}
	}
	packed, _ := msg.Pack()
	return packed
}