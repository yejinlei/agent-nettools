package tun

import "net"

// Peer is the seam between a TUN device and an overlay transport (n2n edge,
// stunvpv client, or a future WireGuard/tinc peer). The TUN bridge depends
// only on this interface, not on any concrete transport package — so adding a
// new tunnel type means implementing these three methods, nothing in the tun
// package changes. This is the "layered, extensible" architecture for tunnels.
//
// Both n2n.Edge and stunvpv.Client already satisfy Peer structurally (same
// method names and signatures) without importing this package; the cmd layer
// passes them in. Go structural typing keeps the tun package free of an import
// cycle on n2n/stunvpv.
//
// Data flow:
//   outgoing (host → peer):  TUN.Read → parsePacket → Peer.SendTo(dstIP, pkt)
//   incoming (peer → host):  Peer.OnData(srcIP, pkt) → TunDevice.WritePacket(pkt) → TUN.Write
type Peer interface {
	// OnData registers the callback invoked for every packet arriving from a
	// remote peer over the overlay. The TUN bridge registers WritePacket here.
	OnData(fn func(srcIP net.IP, data []byte))

	// SendTo delivers a raw IP packet to the given destination virtual IP over
	// the overlay. The TUN read loop calls this with packets read from the
	// kernel TUN, routing by destination IP.
	SendTo(dstIP net.IP, data []byte) error

	// VirtualIP returns this node's own address on the overlay. Used for
	// logging/diagnostics and to skip looping packets back to ourselves.
	VirtualIP() net.IP
}