package n2n

import (
	"encoding/binary"
	"fmt"
	"net"
)

const (
	TypeRegister     byte = 0x01
	TypeRegisterAck  byte = 0x02
	TypeHeartbeat    byte = 0x03
	TypeHeartbeatAck byte = 0x04
	TypeData         byte = 0x05
	TypeP2PConnect   byte = 0x06
	TypeP2PConnectAck byte = 0x07
	TypePeerList     byte = 0x08
	TypeBye          byte = 0x09
)

type Packet struct {
	Type    byte
	SrcIP   net.IP
	DstIP   net.IP
	Payload []byte
}

func MarshalPacket(pkt Packet) ([]byte, error) {
	src := pkt.SrcIP.To4()
	if src == nil {
		src = pkt.SrcIP.To16()
	}
	dst := pkt.DstIP.To4()
	if dst == nil {
		dst = pkt.DstIP.To16()
	}
	addrLen := len(src)
	if addrLen == 0 {
		addrLen = 4
	}
	if len(dst) != addrLen {
		dst = make([]byte, addrLen)
	}
	headerLen := 1 + 1 + addrLen + addrLen + 2
	buf := make([]byte, headerLen+len(pkt.Payload))
	buf[0] = pkt.Type
	buf[1] = byte(addrLen)
	copy(buf[2:2+addrLen], src)
	copy(buf[2+addrLen:2+addrLen*2], dst)
	binary.BigEndian.PutUint16(buf[2+addrLen*2:4+addrLen*2], uint16(len(pkt.Payload)))
	copy(buf[headerLen:], pkt.Payload)
	return buf, nil
}

func UnmarshalPacket(data []byte) (Packet, error) {
	if len(data) < 4 {
		return Packet{}, fmt.Errorf("packet too short")
	}
	pkt := Packet{}
	pkt.Type = data[0]
	addrLen := int(data[1])
	if addrLen != 4 && addrLen != 16 {
		return Packet{}, fmt.Errorf("invalid addr len: %d", addrLen)
	}
	headerLen := 2 + addrLen*2 + 2
	if len(data) < headerLen {
		return Packet{}, fmt.Errorf("packet too short for header")
	}
	// copy to avoid aliasing the caller's buffer (net.IP is a []byte slice);
	// packets are stored in peer maps and passed to data callbacks.
	pkt.SrcIP = make(net.IP, addrLen)
	copy(pkt.SrcIP, data[2:2+addrLen])
	pkt.DstIP = make(net.IP, addrLen)
	copy(pkt.DstIP, data[2+addrLen:2+addrLen*2])
	payloadLen := int(binary.BigEndian.Uint16(data[2+addrLen*2 : 4+addrLen*2]))
	if len(data) < headerLen+payloadLen {
		return Packet{}, fmt.Errorf("packet too short for payload")
	}
	pkt.Payload = make([]byte, payloadLen)
	copy(pkt.Payload, data[headerLen:headerLen+payloadLen])
	return pkt, nil
}

type RegisterPayload struct {
	Community string
	Hostname  string
	Port      uint16
}

type PeerInfo struct {
	VirtualIP net.IP `json:"virtual_ip"`
	RealAddr  string `json:"real_addr"`
	IsLocal   bool   `json:"is_local"`
	LatencyMs int64  `json:"latency_ms"`
	LastSeen  int64  `json:"last_seen"`
}

func marshalRegisterPayload(community, hostname string, port uint16) []byte {
	comLen := len(community)
	hostLen := len(hostname)
	buf := make([]byte, 2+comLen+2+hostLen+2)
	binary.BigEndian.PutUint16(buf[0:2], uint16(comLen))
	copy(buf[2:2+comLen], []byte(community))
	binary.BigEndian.PutUint16(buf[2+comLen:4+comLen], uint16(hostLen))
	copy(buf[4+comLen:4+comLen+hostLen], []byte(hostname))
	binary.BigEndian.PutUint16(buf[4+comLen+hostLen:6+comLen+hostLen], port)
	return buf
}

func unmarshalRegisterPayload(data []byte) (RegisterPayload, error) {
	if len(data) < 6 {
		return RegisterPayload{}, fmt.Errorf("payload too short")
	}
	comLen := int(binary.BigEndian.Uint16(data[0:2]))
	community := string(data[2 : 2+comLen])
	offset := 2 + comLen
	hostLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	offset += 2
	hostname := string(data[offset : offset+hostLen])
	offset += hostLen
	port := binary.BigEndian.Uint16(data[offset : offset+2])
	return RegisterPayload{Community: community, Hostname: hostname, Port: port}, nil
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
		realAddr := p.RealAddr
		entry := make([]byte, 2+addrLen+2+len(realAddr))
		binary.BigEndian.PutUint16(entry[0:2], uint16(addrLen))
		copy(entry[2:2+addrLen], vip)
		binary.BigEndian.PutUint16(entry[2+addrLen:4+addrLen], uint16(len(realAddr)))
		copy(entry[4+addrLen:], []byte(realAddr))
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
		// copy to avoid aliasing the caller's buffer (net.IP is a []byte slice)
		vip := make(net.IP, addrLen)
		copy(vip, data[offset:offset+addrLen])
		offset += addrLen
		addrStrLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if offset+addrStrLen > len(data) {
			break
		}
		realAddr := string(data[offset : offset+addrStrLen])
		offset += addrStrLen
		peers = append(peers, PeerInfo{VirtualIP: vip, RealAddr: realAddr})
	}
	return peers, nil
}

var _ = net.IPv4