package stunvpv

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/pion/stun/v2"
)

// Message types for control protocol
const (
	msgRegister    byte = 0x01
	msgRegisterAck byte = 0x02
	msgPeerList    byte = 0x03
	msgData        byte = 0x04
)

type PeerInfo struct {
	VirtualIP net.IP
	RelayAddr *net.UDPAddr
	LastSeen  time.Time
	Hostname  string
}

type Server struct {
	cfg      Config
	conn     *net.UDPConn
	peers    map[string]*PeerInfo // vip.String() -> PeerInfo
	mu       sync.RWMutex
	cancel   context.CancelFunc
	ipPool   *ipAllocator
	vipIndex map[string]string // realAddr -> vip
}

type ipAllocator struct {
	network *net.IPNet
	base    net.IP
	used    map[string]bool
	mu      sync.Mutex
}

func newIPAllocator(cidr string) (*ipAllocator, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	base := make(net.IP, len(network.IP))
	copy(base, network.IP)
	base[len(base)-1] = 10
	return &ipAllocator{network: network, base: base, used: make(map[string]bool)}, nil
}

func (a *ipAllocator) allocate() net.IP {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := 10; i < 255; i++ {
		ip := make(net.IP, len(a.base))
		copy(ip, a.base)
		ip[len(ip)-1] = byte(i)
		key := ip.String()
		if !a.used[key] {
			a.used[key] = true
			return ip
		}
	}
	return nil
}

func (a *ipAllocator) release(ip net.IP) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.used, ip.String())
}

func NewServer(cfg Config) (*Server, error) {
	pool, err := newIPAllocator(cfg.VirtualCIDR)
	if err != nil {
		return nil, fmt.Errorf("ip pool: %w", err)
	}
	return &Server{
		cfg:      cfg,
		peers:    make(map[string]*PeerInfo),
		ipPool:   pool,
		vipIndex: make(map[string]string),
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)
	addr, err := net.ResolveUDPAddr("udp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	s.conn, err = net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Printf("stunvpv: server listening on %s (cidr=%s)", s.cfg.Listen, s.cfg.VirtualCIDR)
	go s.cleanupLoop(ctx)
	buf := make([]byte, 65535)
	for {
		select {
		case <-ctx.Done():
			s.conn.Close()
			return nil
		default:
		}
		s.conn.SetDeadline(time.Now().Add(2 * time.Second))
		n, remoteAddr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		go s.handlePacket(data, remoteAddr)
	}
}

func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Server) handlePacket(data []byte, remoteAddr *net.UDPAddr) {
	// Check if this is a STUN message
	if stun.IsMessage(data) {
		s.handleSTUN(data, remoteAddr)
		return
	}
	// Otherwise, it's our control protocol
	s.handleControl(data, remoteAddr)
}

func (s *Server) handleSTUN(data []byte, remoteAddr *net.UDPAddr) {
	m := &stun.Message{}
	if err := stun.Decode(data, m); err != nil {
		return
	}
	// Only respond to binding requests
	if m.Type != stun.BindingRequest {
		return
	}

	// Build binding success response with XOR-MAPPED-ADDRESS
	response := &stun.Message{}
	response.Build(
		stun.BindingSuccess,
		stun.XORMappedAddress{IP: remoteAddr.IP, Port: remoteAddr.Port},
		stun.Software("net-redirect-stunvpv"),
		stun.Fingerprint,
	)
	response.TransactionID = m.TransactionID
	s.conn.WriteTo(response.Raw, remoteAddr)
}

func (s *Server) handleControl(data []byte, remoteAddr *net.UDPAddr) {
	if len(data) < 8 {
		return
	}
	msgType := data[0]
	addrLen := int(data[1])
	if addrLen != 4 && addrLen != 16 {
		return
	}
	if len(data) < 8+addrLen {
		return
	}
	// copy to avoid aliasing the caller's buffer (net.IP is a []byte slice)
	vip := make(net.IP, addrLen)
	copy(vip, data[2:2+addrLen])
	payloadLen := int(binary.BigEndian.Uint16(data[6+addrLen : 8+addrLen]))
	payload := data[8+addrLen:]
	if len(payload) > payloadLen {
		payload = payload[:payloadLen]
	}

	switch msgType {
	case msgRegister:
		s.handleRegister(payload, vip, remoteAddr)
	case msgData:
		s.handleRelayData(vip, payload, remoteAddr)
	}
}

func (s *Server) handleRegister(payload []byte, vip net.IP, remoteAddr *net.UDPAddr) {
	hostname := string(payload)
	// Allocate virtual IP if not specified
	assignedVIP := vip
	if assignedVIP == nil || assignedVIP.IsUnspecified() {
		assignedVIP = s.ipPool.allocate()
		if assignedVIP == nil {
			return
		}
	}

	realAddrStr := remoteAddr.String()
	s.mu.Lock()
	s.peers[assignedVIP.String()] = &PeerInfo{
		VirtualIP: assignedVIP,
		RelayAddr: remoteAddr,
		LastSeen:  time.Now(),
		Hostname:  hostname,
	}
	s.vipIndex[realAddrStr] = assignedVIP.String()
	peers := make([]PeerInfo, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, *p)
	}
	s.mu.Unlock()

	log.Printf("stunvpv: register %s (%s) -> %s", hostname, realAddrStr, assignedVIP)

	// Send RegisterAck
	s.sendRegisterAck(assignedVIP, peers, remoteAddr)

	// Broadcast updated peer list to all other clients
	go s.broadcastPeerList(assignedVIP, remoteAddr)
}

func (s *Server) sendRegisterAck(vip net.IP, peers []PeerInfo, to *net.UDPAddr) {
	payload := marshalPeerList(peers)
	msg := makeControlMessage(msgRegisterAck, vip, payload)
	s.conn.WriteTo(msg, to)
}

func (s *Server) broadcastPeerList(exceptVIP net.IP, exceptAddr *net.UDPAddr) {
	s.mu.RLock()
	peers := make([]PeerInfo, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, *p)
	}
	s.mu.RUnlock()

	payload := marshalPeerList(peers)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.peers {
		if p.VirtualIP.Equal(exceptVIP) {
			continue
		}
		msg := makeControlMessage(msgPeerList, p.VirtualIP, payload)
		s.conn.WriteTo(msg, p.RelayAddr)
	}
}

func (s *Server) handleRelayData(dstVIP net.IP, payload []byte, from *net.UDPAddr) {
	// Look up destination peer
	dstKey := dstVIP.String()
	s.mu.RLock()
	dstPeer, ok := s.peers[dstKey]
	s.mu.RUnlock()

	if !ok {
		return
	}

	// Look up source VIP
	srcKey := from.String()
	s.mu.RLock()
	srcVIP, srcOK := s.vipIndex[srcKey]
	s.mu.RUnlock()
	if !srcOK {
		return
	}

	srcIP := net.ParseIP(srcVIP)
	if srcIP == nil {
		return
	}

	msg := makeControlMessage(msgData, srcIP, payload)
	s.conn.WriteTo(msg, dstPeer.RelayAddr)
}

func (s *Server) handleBye(remoteAddr *net.UDPAddr) {
	realAddrStr := remoteAddr.String()
	s.mu.Lock()
	vip, ok := s.vipIndex[realAddrStr]
	if ok {
		if p, exists := s.peers[vip]; exists {
			s.ipPool.release(p.VirtualIP)
			log.Printf("stunvpv: bye %s (%s)", vip, realAddrStr)
		}
		delete(s.peers, vip)
		delete(s.vipIndex, realAddrStr)
	}
	s.mu.Unlock()
}

func (s *Server) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

func (s *Server) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	timeout := 5 * time.Minute
	for k, entry := range s.peers {
		if now.Sub(entry.LastSeen) > timeout {
			log.Printf("stunvpv: cleanup stale peer %s", k)
			s.ipPool.release(entry.VirtualIP)
			delete(s.vipIndex, entry.RelayAddr.String())
			delete(s.peers, k)
		}
	}
}

func makeControlMessage(msgType byte, vip net.IP, payload []byte) []byte {
	addrLen := len(vip.To4())
	if addrLen == 0 {
		addrLen = 16
	}
	vipBytes := vip.To16()
	if addrLen == 4 {
		vipBytes = vip.To4()
	}
	headerLen := 8 + addrLen
	buf := make([]byte, headerLen+len(payload))
	buf[0] = msgType
	buf[1] = byte(addrLen)
	copy(buf[2:2+addrLen], vipBytes)
	binary.BigEndian.PutUint16(buf[6+addrLen:8+addrLen], uint16(len(payload)))
	copy(buf[headerLen:], payload)
	return buf
}

func marshalPeerList(peers []PeerInfo) []byte {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf[0:2], uint16(len(peers)))
	for _, p := range peers {
		vip := p.VirtualIP.To4()
		if vip == nil {
			vip = p.VirtualIP.To16()
		}
		addrLen := len(vip)
		relayAddr := p.RelayAddr.String()
		entry := make([]byte, 2+addrLen+2+len(relayAddr))
		binary.BigEndian.PutUint16(entry[0:2], uint16(addrLen))
		copy(entry[2:2+addrLen], vip)
		binary.BigEndian.PutUint16(entry[2+addrLen:4+addrLen], uint16(len(relayAddr)))
		copy(entry[4+addrLen:], []byte(relayAddr))
		buf = append(buf, entry...)
	}
	return buf
}

func unmarshalPeerList(data []byte) ([]PeerInfo, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("peer list too short")
	}
	count := int(binary.BigEndian.Uint16(data[0:2]))
	peers := make([]PeerInfo, 0, count)
	offset := 2
	for i := 0; i < count; i++ {
		if offset+2 > len(data) {
			break
		}
		addrLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if offset+addrLen+2 > len(data) {
			break
		}
		vip := make(net.IP, addrLen)
		copy(vip, data[offset:offset+addrLen])
		offset += addrLen
		addrStrLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if offset+addrStrLen > len(data) {
			break
		}
		relayAddr := string(data[offset : offset+addrStrLen])
		offset += addrStrLen
		relayUDPAddr, _ := net.ResolveUDPAddr("udp", relayAddr)
		peers = append(peers, PeerInfo{
			VirtualIP: vip,
			RelayAddr: relayUDPAddr,
		})
	}
	return peers, nil
}

var _ = time.Second