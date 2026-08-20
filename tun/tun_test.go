package tun

import (
	"net"
	"sync"
	"testing"
)

// fakePeer is a test double for tun.Peer. It records every SendTo call and,
// when OnData is registered, lets the test inject packets as if they arrived
// from the overlay. Used to unit-test the bridge plumbing without a kernel TUN
// (real packet I/O needs /dev/net/tun or wintun.dll, unavailable in CI).
type fakePeer struct {
	mu     sync.Mutex
	sent   []sentPkt
	onData func(srcIP net.IP, data []byte)
	virtIP net.IP
}

type sentPkt struct {
	dst  net.IP
	data []byte
}

func (f *fakePeer) OnData(fn func(srcIP net.IP, data []byte)) { f.onData = fn }
func (f *fakePeer) SendTo(dstIP net.IP, data []byte) error {
	f.mu.Lock()
	f.sent = append(f.sent, sentPkt{dst: dstIP, data: append([]byte(nil), data...)})
	f.mu.Unlock()
	return nil
}
func (f *fakePeer) VirtualIP() net.IP { return f.virtIP }

// TestParsePacket_IPv4TCP verifies the TUN read loop's parser extracts the
// destination IP and TCP port from a minimal crafted IPv4/TCP packet. This is
// the function the read loop calls to route outgoing packets by dst IP.
func TestParsePacket_IPv4TCP(t *testing.T) {
	// Minimal IPv4 header (20 bytes, no options) + 20-byte TCP header.
	pkt := make([]byte, 40)
	pkt[0] = 0x45 // version=4, IHL=5
	pkt[9] = 6    // protocol = TCP
	// src 10.0.0.1
	pkt[12], pkt[13], pkt[14], pkt[15] = 10, 0, 0, 1
	// dst 192.168.1.42
	pkt[16], pkt[17], pkt[18], pkt[19] = 192, 168, 1, 42
	// TCP dst port 443 (at offset 20+2..20+4)
	pkt[22], pkt[23] = 0x01, 0xBB // 443

	dstIP, dstPort, proto, err := parsePacket(pkt)
	if err != nil {
		t.Fatalf("parsePacket: %v", err)
	}
	if !dstIP.Equal(net.IPv4(192, 168, 1, 42)) {
		t.Fatalf("dstIP = %v, want 192.168.1.42", dstIP)
	}
	if proto != 6 {
		t.Fatalf("proto = %d, want 6 (TCP)", proto)
	}
	if dstPort != 443 {
		t.Fatalf("dstPort = %d, want 443", dstPort)
	}
}

// TestParsePacket_RejectsNonIPv4 confirms non-IPv4 packets are skipped by the
// read loop (parsePacket returns an error so the caller `continue`s).
func TestParsePacket_RejectsNonIPv4(t *testing.T) {
	pkt := make([]byte, 40)
	pkt[0] = 0x60 // version=6
	if _, _, _, err := parsePacket(pkt); err == nil {
		t.Fatal("expected error for IPv6, got nil")
	}
}

// TestParsePacket_TooShort guards against short reads panicking on slice access.
func TestParsePacket_TooShort(t *testing.T) {
	if _, _, _, err := parsePacket(make([]byte, 10)); err == nil {
		t.Fatal("expected error for short packet, got nil")
	}
}

// TestWritePacket_QueuesAndDrops verifies the inbound queue contract: while
// Start has not opened a kernel TUN, WritePacket still enqueues into the
// buffered channel up to its capacity, then drops (non-blocking) instead of
// blocking the transport's read loop.
func TestWritePacket_QueuesAndDrops(t *testing.T) {
	dev := NewTunDevice(TunConfig{Enable: true, MTU: 1500, CIDR: "198.18.0.0/16"})
	// Mirror Start's channel setup without opening a real TUN.
	dev.inbound = make(chan []byte, 2)

	dev.WritePacket([]byte("pkt-a"))
	dev.WritePacket([]byte("pkt-b"))
	// Third write exceeds capacity(2) → dropped, must not block.
	dev.WritePacket([]byte("pkt-c"))

	got := [][]byte{<-dev.inbound, <-dev.inbound}
	if string(got[0]) != "pkt-a" || string(got[1]) != "pkt-b" {
		t.Fatalf("queued = %v, want [pkt-a pkt-b]", got)
	}
}

// TestWritePacket_NilChannelNoOp confirms WritePacket is safe to call before
// Start allocates the inbound channel (the bridge's OnData may fire during a
// race between SetPeer and Start).
func TestWritePacket_NilChannelNoOp(t *testing.T) {
	dev := NewTunDevice(TunConfig{Enable: true})
	// dev.inbound is still nil — must not panic.
	dev.WritePacket([]byte("x"))
}

// TestSetPeer_StoreAndRetrieve confirms SetPeer stores the peer the read loop
// consumes under lock.
func TestSetPeer_StoreAndRetrieve(t *testing.T) {
	dev := NewTunDevice(TunConfig{Enable: true})
	p := &fakePeer{virtIP: net.IPv4(10, 0, 0, 5)}
	dev.SetPeer(p)

	dev.mu.Lock()
	got := dev.peer
	dev.mu.Unlock()
	if got != p {
		t.Fatalf("SetPeer did not store the peer: got %#v", got)
	}
}

// TestPeerInterface_StructuralSatisfaction is a compile-time guarantee that a
// minimal type implementing OnData/SendTo/VirtualIP satisfies tun.Peer. It
// documents the structural-typing seam: n2n.Edge and stunvpv.Client satisfy
// Peer the same way, without importing this package.
func TestPeerInterface_StructuralSatisfaction(t *testing.T) {
	var _ Peer = (*fakePeer)(nil)
	// Also confirm the fake behaves as a Peer through the interface.
	var p Peer = &fakePeer{virtIP: net.IPv4(10, 0, 0, 9)}
	p.OnData(func(net.IP, []byte) {})
	if err := p.SendTo(net.IPv4(1, 2, 3, 4), []byte("hi")); err != nil {
		t.Fatalf("SendTo via interface: %v", err)
	}
	_ = p.VirtualIP()
}