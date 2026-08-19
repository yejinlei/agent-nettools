package n2n

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type Edge struct {
	cfg       Config
	conn      *net.UDPConn
	superAddr *net.UDPAddr
	virtualIP net.IP
	peers     map[string]*PeerInfo
	mu        sync.RWMutex
	cancel    context.CancelFunc
	crypto    *Crypto
	hostname  string
	onData    func(srcIP net.IP, data []byte)
}

func NewEdge(cfg Config) (*Edge, error) {
	if cfg.Supernode == "" {
		return nil, fmt.Errorf("supernode address required for edge mode")
	}
	superAddr, err := net.ResolveUDPAddr("udp", cfg.Supernode)
	if err != nil {
		return nil, fmt.Errorf("resolve supernode: %w", err)
	}
	hostname := fmt.Sprintf("edge-%d", time.Now().Unix()%10000)
	var crypto *Crypto
	if cfg.Password != "" {
		crypto = NewCrypto(cfg.Password)
	}
	return &Edge{cfg: cfg, superAddr: superAddr, peers: make(map[string]*PeerInfo), crypto: crypto, hostname: hostname}, nil
}

func (e *Edge) OnData(fn func(srcIP net.IP, data []byte)) { e.onData = fn }

func (e *Edge) Start(ctx context.Context) error {
	ctx, e.cancel = context.WithCancel(ctx)
	addr, err := net.ResolveUDPAddr("udp", e.cfg.Listen)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	e.conn, err = net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Printf("n2n: edge starting on %s, supernode=%s", e.cfg.Listen, e.cfg.Supernode)
	if err := e.register(); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	go e.heartbeatLoop(ctx)
	buf := make([]byte, 65535)
	for {
		select {
		case <-ctx.Done():
			e.conn.Close()
			return nil
		default:
		}
		e.conn.SetDeadline(time.Now().Add(2 * time.Second))
		n, remoteAddr, err := e.conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		go e.handlePacket(data, remoteAddr)
	}
}

func (e *Edge) Stop() {
	e.sendBye()
	if e.cancel != nil {
		e.cancel()
	}
}

func (e *Edge) register() error {
	payload := marshalRegisterPayload(e.cfg.Community, e.hostname, 0)
	pkt := Packet{Type: TypeRegister, Payload: payload}
	data, err := e.marshalAndEncrypt(pkt)
	if err != nil {
		return err
	}
	e.conn.SetDeadline(time.Now().Add(10 * time.Second))
	e.conn.WriteToUDP(data, e.superAddr)
	buf := make([]byte, 65535)
	n, _, err := e.conn.ReadFromUDP(buf)
	if err != nil {
		return fmt.Errorf("register timeout: %w", err)
	}
	respData, err := e.decryptAndUnmarshal(buf[:n])
	if err != nil {
		return err
	}
	if respData.Type != TypeRegisterAck {
		return fmt.Errorf("unexpected response type: %d", respData.Type)
	}
	addrLen := int(respData.Payload[0])
	// copy to avoid aliasing the caller's buffer (net.IP is a []byte slice)
	e.virtualIP = make(net.IP, addrLen)
	copy(e.virtualIP, respData.Payload[1:1+addrLen])
	offset := 1 + addrLen
	peerListLen := int(respData.Payload[offset])
	offset++
	if peerListLen > 0 && offset+peerListLen <= len(respData.Payload) {
		peers, err := unmarshalPeerList(respData.Payload[offset : offset+peerListLen])
		if err == nil {
			e.mu.Lock()
			for _, p := range peers {
				e.peers[p.VirtualIP.String()] = &p
			}
			e.mu.Unlock()
		}
	}
	log.Printf("n2n: registered as %s, %d peers known", e.virtualIP, len(e.peers))
	return nil
}

func (e *Edge) handlePacket(data []byte, remoteAddr *net.UDPAddr) {
	pkt, err := e.decryptAndUnmarshal(data)
	if err != nil {
		return
	}
	switch pkt.Type {
	case TypeHeartbeatAck:
		e.handleHeartbeatAck(pkt)
	case TypeP2PConnect:
		e.handleP2PConnect(pkt, remoteAddr)
	case TypeP2PConnectAck:
		e.handleP2PConnectAck(pkt)
	case TypePeerList:
		e.handlePeerList(pkt)
	case TypeData:
		e.handleData(pkt)
	}
}

func (e *Edge) handleHeartbeatAck(pkt Packet) {
	peers, err := unmarshalPeerList(pkt.Payload)
	if err != nil {
		return
	}
	e.mu.Lock()
	for _, p := range peers {
		if p.VirtualIP.String() != e.virtualIP.String() {
			e.peers[p.VirtualIP.String()] = &p
		}
	}
	e.mu.Unlock()
}

func (e *Edge) handleP2PConnect(pkt Packet, remoteAddr *net.UDPAddr) {
	requesterAddr := string(pkt.Payload)
	log.Printf("n2n: P2P connect from %s (%s)", pkt.SrcIP, requesterAddr)
	ackPkt := Packet{Type: TypeP2PConnectAck, SrcIP: e.virtualIP, DstIP: pkt.SrcIP, Payload: []byte(e.conn.LocalAddr().String())}
	reqAddr, err := net.ResolveUDPAddr("udp", requesterAddr)
	if err == nil {
		e.sendPkt(ackPkt, reqAddr)
	}
}

func (e *Edge) handleP2PConnectAck(pkt Packet) {
	targetAddr := string(pkt.Payload)
	e.mu.Lock()
	if p, ok := e.peers[pkt.SrcIP.String()]; ok {
		p.RealAddr = targetAddr
	}
	e.mu.Unlock()
}

func (e *Edge) handlePeerList(pkt Packet) {
	peers, err := unmarshalPeerList(pkt.Payload)
	if err != nil {
		return
	}
	e.mu.Lock()
	for _, p := range peers {
		if p.VirtualIP.String() != e.virtualIP.String() {
			e.peers[p.VirtualIP.String()] = &p
		}
	}
	e.mu.Unlock()
}

func (e *Edge) handleData(pkt Packet) {
	if e.onData != nil {
		e.onData(pkt.SrcIP, pkt.Payload)
	}
}

func (e *Edge) SendTo(dstIP net.IP, data []byte) error {
	pkt := Packet{Type: TypeData, SrcIP: e.virtualIP, DstIP: dstIP, Payload: data}
	e.mu.RLock()
	peer, ok := e.peers[dstIP.String()]
	e.mu.RUnlock()
	if !ok {
		e.sendPkt(pkt, e.superAddr)
		return nil
	}
	peerAddr, err := net.ResolveUDPAddr("udp", peer.RealAddr)
	if err != nil {
		e.sendPkt(pkt, e.superAddr)
		return nil
	}
	e.sendPkt(pkt, peerAddr)
	return nil
}

func (e *Edge) sendBye() {
	pkt := Packet{Type: TypeBye, SrcIP: e.virtualIP}
	e.sendPkt(pkt, e.superAddr)
}

func (e *Edge) sendPkt(pkt Packet, addr *net.UDPAddr) {
	data, err := e.marshalAndEncrypt(pkt)
	if err != nil {
		return
	}
	e.conn.WriteToUDP(data, addr)
}

func (e *Edge) marshalAndEncrypt(pkt Packet) ([]byte, error) {
	data, err := MarshalPacket(pkt)
	if err != nil {
		return nil, err
	}
	if e.crypto != nil {
		return e.crypto.Encrypt(data)
	}
	return data, nil
}

func (e *Edge) decryptAndUnmarshal(data []byte) (Packet, error) {
	if e.crypto != nil {
		var err error
		data, err = e.crypto.Decrypt(data)
		if err != nil {
			return Packet{}, err
		}
	}
	return UnmarshalPacket(data)
}

func (e *Edge) heartbeatLoop(ctx context.Context) {
	interval := time.Duration(e.cfg.Interval) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	e.sendHeartbeat()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.sendHeartbeat()
		}
	}
}

func (e *Edge) sendHeartbeat() {
	pkt := Packet{Type: TypeHeartbeat, SrcIP: e.virtualIP}
	e.sendPkt(pkt, e.superAddr)
}

func (e *Edge) VirtualIP() net.IP { return e.virtualIP }

func (e *Edge) Peers() []PeerInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]PeerInfo, 0, len(e.peers))
	for _, p := range e.peers {
		result = append(result, *p)
	}
	return result
}

func (e *Edge) PeerCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.peers)
}

var _ = fmt.Sprintf