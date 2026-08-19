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

type Client struct {
	cfg        Config
	conn       *net.UDPConn
	serverAddr *net.UDPAddr
	virtualIP  net.IP
	peers      map[string]*PeerInfo
	mu         sync.RWMutex
	cancel     context.CancelFunc
	onData     func(srcIP net.IP, data []byte)
	hostname   string
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.TURNServer == "" {
		return nil, fmt.Errorf("TURN server address required for client mode")
	}
	serverAddr, err := net.ResolveUDPAddr("udp", cfg.TURNServer)
	if err != nil {
		return nil, fmt.Errorf("resolve server: %w", err)
	}
	hostname := fmt.Sprintf("client-%d", time.Now().Unix()%10000)
	return &Client{
		cfg:        cfg,
		serverAddr: serverAddr,
		peers:      make(map[string]*PeerInfo),
		hostname:   hostname,
	}, nil
}

func (c *Client) OnData(fn func(srcIP net.IP, data []byte)) { c.onData = fn }

func (c *Client) Start(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)
	addr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	c.conn, err = net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Printf("stunvpv: client starting on %s, server=%s", c.conn.LocalAddr(), c.cfg.TURNServer)

	// Step 1: STUN binding request to discover our public address
	if err := c.stunBind(); err != nil {
		log.Printf("stunvpv: stun bind: %v (continuing anyway)", err)
	}

	// Step 2: Register with the server
	if err := c.register(); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	// Step 3: Start heartbeat loop
	go c.heartbeatLoop(ctx)

	// Step 4: Read loop for incoming data
	buf := make([]byte, 65535)
	for {
		select {
		case <-ctx.Done():
			c.conn.Close()
			return nil
		default:
		}
		c.conn.SetDeadline(time.Now().Add(2 * time.Second))
		n, remoteAddr, err := c.conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		go c.handlePacket(data, remoteAddr)
	}
}

func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Client) stunBind() error {
	m := &stun.Message{}
	m.Build(stun.BindingRequest, stun.Software("net-redirect-stunvpv"), stun.Fingerprint)
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.conn.WriteTo(m.Raw, c.serverAddr); err != nil {
		return err
	}

	buf := make([]byte, 65535)
	n, _, err := c.conn.ReadFromUDP(buf)
	if err != nil {
		return fmt.Errorf("stun response: %w", err)
	}

	resp := &stun.Message{}
	if err := stun.Decode(buf[:n], resp); err != nil {
		return err
	}

	var mappedAddr stun.XORMappedAddress
	if err := mappedAddr.GetFrom(resp); err == nil {
		log.Printf("stunvpv: public address: %s:%d", mappedAddr.IP, mappedAddr.Port)
	}
	return nil
}

func (c *Client) register() error {
	msg := makeControlMessage(msgRegister, nil, []byte(c.hostname))
	c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.conn.WriteTo(msg, c.serverAddr); err != nil {
		return fmt.Errorf("register send: %w", err)
	}

	buf := make([]byte, 65535)
	n, _, err := c.conn.ReadFromUDP(buf)
	if err != nil {
		return fmt.Errorf("register timeout: %w", err)
	}

	respData := buf[:n]
	if len(respData) < 8 {
		return fmt.Errorf("register response too short")
	}
	if respData[0] != msgRegisterAck {
		return fmt.Errorf("unexpected response type: %d", respData[0])
	}

	addrLen := int(respData[1])
	if addrLen != 4 && addrLen != 16 {
		return fmt.Errorf("invalid addr len: %d", addrLen)
	}
	// copy to avoid aliasing the read buffer (net.IP is a []byte slice);
	// respData aliases buf, which would otherwise be kept alive and mutated.
	c.virtualIP = make(net.IP, addrLen)
	copy(c.virtualIP, respData[2:2+addrLen])

	payloadLen := int(binary.BigEndian.Uint16(respData[6+addrLen : 8+addrLen]))
	payload := respData[8+addrLen:]
	if len(payload) > payloadLen {
		payload = payload[:payloadLen]
	}

	if len(payload) > 2 {
		peers, err := unmarshalPeerList(payload)
		if err == nil {
			c.mu.Lock()
			for _, p := range peers {
				if !p.VirtualIP.Equal(c.virtualIP) {
					c.peers[p.VirtualIP.String()] = &p
				}
			}
			c.mu.Unlock()
		}
	}

	log.Printf("stunvpv: registered as %s, %d peers known", c.virtualIP, len(c.peers))
	return nil
}

func (c *Client) handlePacket(data []byte, remoteAddr *net.UDPAddr) {
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
	srcIP := make(net.IP, addrLen)
	copy(srcIP, data[2:2+addrLen])
	payloadLen := int(binary.BigEndian.Uint16(data[6+addrLen : 8+addrLen]))
	payload := data[8+addrLen:]
	if len(payload) > payloadLen {
		payload = payload[:payloadLen]
	}

	switch msgType {
	case msgRegisterAck:
		// Already handled during registration
	case msgPeerList:
		c.handlePeerList(payload)
	case msgData:
		c.handleData(srcIP, payload)
	}
}

func (c *Client) handlePeerList(payload []byte) {
	peers, err := unmarshalPeerList(payload)
	if err != nil {
		return
	}
	c.mu.Lock()
	for _, p := range peers {
		if !p.VirtualIP.Equal(c.virtualIP) {
			c.peers[p.VirtualIP.String()] = &p
		}
	}
	c.mu.Unlock()
}

func (c *Client) handleData(srcIP net.IP, payload []byte) {
	if c.onData != nil {
		c.onData(srcIP, payload)
	}
}

func (c *Client) SendTo(dstIP net.IP, data []byte) error {
	msg := makeControlMessage(msgData, dstIP, data)
	_, err := c.conn.WriteTo(msg, c.serverAddr)
	return err
}

func (c *Client) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sendHeartbeat()
		}
	}
}

func (c *Client) sendHeartbeat() {
	msg := makeControlMessage(msgRegister, c.virtualIP, []byte(c.hostname))
	c.conn.WriteTo(msg, c.serverAddr)
}

func (c *Client) VirtualIP() net.IP { return c.virtualIP }

func (c *Client) Peers() []PeerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]PeerInfo, 0, len(c.peers))
	for _, p := range c.peers {
		result = append(result, *p)
	}
	return result
}

func (c *Client) PeerCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.peers)
}

var _ = fmt.Sprintf