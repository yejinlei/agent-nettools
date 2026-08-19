package n2n

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type Supernode struct {
	cfg       Config
	conn      *net.UDPConn
	peers     map[string]*peerEntry
	addrIndex map[string]string
	mu        sync.RWMutex
	cancel    context.CancelFunc
	ipPool    *ipAllocator
	crypto    *Crypto
}

type peerEntry struct {
	info      PeerInfo
	lastSeen  time.Time
	community string
	hostname  string
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

func NewSupernode(cfg Config) (*Supernode, error) {
	pool, err := newIPAllocator(cfg.VirtualCIDR)
	if err != nil {
		return nil, fmt.Errorf("ip pool: %w", err)
	}
	var crypto *Crypto
	if cfg.Password != "" {
		crypto = NewCrypto(cfg.Password)
	}
	return &Supernode{cfg: cfg, peers: make(map[string]*peerEntry), addrIndex: make(map[string]string), ipPool: pool, crypto: crypto}, nil
}

func (s *Supernode) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)
	addr, err := net.ResolveUDPAddr("udp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	s.conn, err = net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Printf("n2n: supernode listening on %s (community=%s, cidr=%s)", s.cfg.Listen, s.cfg.Community, s.cfg.VirtualCIDR)
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

func (s *Supernode) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Supernode) handlePacket(data []byte, remoteAddr *net.UDPAddr) {
	if s.crypto != nil {
		var err error
		data, err = s.crypto.Decrypt(data)
		if err != nil {
			return
		}
	}
	pkt, err := UnmarshalPacket(data)
	if err != nil {
		return
	}
	switch pkt.Type {
	case TypeRegister:
		s.handleRegister(pkt, remoteAddr)
	case TypeHeartbeat:
		s.handleHeartbeat(pkt, remoteAddr)
	case TypeP2PConnect:
		s.handleP2PConnect(pkt, remoteAddr)
	case TypeBye:
		s.handleBye(pkt, remoteAddr)
	}
}

func (s *Supernode) handleRegister(pkt Packet, remoteAddr *net.UDPAddr) {
	payload, err := unmarshalRegisterPayload(pkt.Payload)
	if err != nil {
		return
	}
	if payload.Community != s.cfg.Community {
		return
	}
	vip := s.ipPool.allocate()
	if vip == nil {
		return
	}
	realAddrStr := net.JoinHostPort(remoteAddr.IP.String(), fmt.Sprintf("%d", remoteAddr.Port))
	s.mu.Lock()
	s.peers[vip.String()] = &peerEntry{
		info:      PeerInfo{VirtualIP: vip, RealAddr: realAddrStr, LastSeen: time.Now().Unix()},
		lastSeen:  time.Now(),
		community: payload.Community,
		hostname:  payload.Hostname,
	}
	s.addrIndex[realAddrStr] = vip.String()
	peers := make([]PeerInfo, 0, len(s.peers))
	for _, entry := range s.peers {
		peers = append(peers, entry.info)
	}
	s.mu.Unlock()
	log.Printf("n2n: register %s (%s) -> %s", payload.Hostname, realAddrStr, vip)
	ackPayload := make([]byte, 1+len(vip)+2)
	ackPayload[0] = byte(len(vip))
	copy(ackPayload[1:1+len(vip)], vip)
	peerListData := marshalPeerList(peers)
	binary.BigEndian.PutUint16(ackPayload[1+len(vip):3+len(vip)], uint16(len(peerListData)))
	ackPayload = append(ackPayload, peerListData...)
	pkt.SrcIP = vip
	pkt.DstIP = pkt.SrcIP
	pkt.Type = TypeRegisterAck
	pkt.Payload = ackPayload
	s.sendTo(pkt, remoteAddr)
	go s.broadcastPeerUpdate(vip, realAddrStr, remoteAddr)
}

func (s *Supernode) handleHeartbeat(pkt Packet, remoteAddr *net.UDPAddr) {
	srcIP := pkt.SrcIP.String()
	s.mu.Lock()
	if entry, ok := s.peers[srcIP]; ok {
		entry.lastSeen = time.Now()
		entry.info.LastSeen = time.Now().Unix()
	}
	s.mu.Unlock()
	peers := func() []PeerInfo {
		s.mu.RLock()
		defer s.mu.RUnlock()
		result := make([]PeerInfo, 0, len(s.peers))
		for _, entry := range s.peers {
			result = append(result, entry.info)
		}
		return result
	}()
	pkt.Type = TypeHeartbeatAck
	pkt.Payload = marshalPeerList(peers)
	s.sendTo(pkt, remoteAddr)
}

func (s *Supernode) handleP2PConnect(pkt Packet, remoteAddr *net.UDPAddr) {
	dstIP := pkt.DstIP.String()
	s.mu.RLock()
	entry, ok := s.peers[dstIP]
	s.mu.RUnlock()
	if !ok {
		return
	}
	pkt.Type = TypeP2PConnectAck
	pkt.Payload = []byte(entry.info.RealAddr)
	s.sendTo(pkt, remoteAddr)
	revPkt := Packet{Type: TypeP2PConnect, SrcIP: pkt.DstIP, DstIP: pkt.SrcIP, Payload: []byte(remoteAddr.String())}
	targetAddr, _ := net.ResolveUDPAddr("udp", entry.info.RealAddr)
	if targetAddr != nil {
		s.sendTo(revPkt, targetAddr)
	}
}

func (s *Supernode) handleBye(pkt Packet, remoteAddr *net.UDPAddr) {
	srcIP := pkt.SrcIP.String()
	s.mu.Lock()
	if entry, ok := s.peers[srcIP]; ok {
		s.ipPool.release(entry.info.VirtualIP)
		delete(s.addrIndex, entry.info.RealAddr)
		delete(s.peers, srcIP)
		log.Printf("n2n: bye %s", srcIP)
	}
	s.mu.Unlock()
}

func (s *Supernode) broadcastPeerUpdate(vip net.IP, realAddr string, exclude *net.UDPAddr) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.peers {
		if entry.info.RealAddr == realAddr {
			continue
		}
		peerAddr, err := net.ResolveUDPAddr("udp", entry.info.RealAddr)
		if err != nil {
			continue
		}
		updatePkt := Packet{
			Type: TypePeerList, SrcIP: vip,
			Payload: marshalPeerList([]PeerInfo{{VirtualIP: vip, RealAddr: realAddr, LastSeen: time.Now().Unix()}}),
		}
		s.sendTo(updatePkt, peerAddr)
	}
}

func (s *Supernode) sendTo(pkt Packet, addr *net.UDPAddr) {
	data, err := MarshalPacket(pkt)
	if err != nil {
		return
	}
	if s.crypto != nil {
		data, err = s.crypto.Encrypt(data)
		if err != nil {
			return
		}
	}
	s.conn.WriteToUDP(data, addr)
}

func (s *Supernode) cleanupLoop(ctx context.Context) {
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

func (s *Supernode) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	timeout := 5 * time.Minute
	for k, entry := range s.peers {
		if now.Sub(entry.lastSeen) > timeout {
			log.Printf("n2n: cleanup stale peer %s", k)
			s.ipPool.release(entry.info.VirtualIP)
			delete(s.addrIndex, entry.info.RealAddr)
			delete(s.peers, k)
		}
	}
}

func (s *Supernode) Peers() []PeerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]PeerInfo, 0, len(s.peers))
	for _, entry := range s.peers {
		result = append(result, entry.info)
	}
	return result
}