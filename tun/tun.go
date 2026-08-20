package tun

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sync"

	// Cross-platform TUN library (Linux /dev/net/tun, Windows wintun.dll).
	// Aliased to avoid clashing with this package's own name (`tun`).
	wgtun "golang.zx2c4.com/wireguard/tun"
)

type TunConfig struct {
	Enable  bool
	Device  string
	MTU     int
	Gateway string
	CIDR    string
	DNS     string
}

type TunDevice struct {
	cfg     TunConfig
	dev     wgtun.Device // the kernel TUN (nil until Start opens it)
	peer    Peer         // overlay transport bridged to this TUN (n2n/stunvpv/...)
	inbound chan []byte  // packets from the overlay, waiting to be written to the TUN

	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
}

func NewTunDevice(cfg TunConfig) *TunDevice {
	if cfg.MTU <= 0 {
		cfg.MTU = 1500
	}
	if cfg.Device == "" {
		cfg.Device = "net-redirect"
	}
	if cfg.Gateway == "" {
		cfg.Gateway = "198.18.0.1"
	}
	if cfg.CIDR == "" {
		cfg.CIDR = "198.18.0.0/16"
	}
	if cfg.DNS == "" {
		cfg.DNS = "198.18.0.2"
	}
	return &TunDevice{cfg: cfg}
}

// SetPeer wires an overlay transport to this TUN. Must be called before Start.
// After this, outgoing TUN packets are routed via Peer.SendTo and incoming
// overlay packets arrive through WritePacket (registered as the Peer's OnData).
func (t *TunDevice) SetPeer(p Peer) {
	t.mu.Lock()
	t.peer = p
	t.mu.Unlock()
}

// tunOffset is the headroom passed to the TUN library's Read/Write.
//
// Why 10 (not 0): the wireguard/tun Linux CreateTUN sets IFF_VNET_HDR, so every
// packet is prefixed by a 10-byte virtio-net header. The library's Write does
// `offset -= virtioNetHdrLen` and Read strips it via handleVirtioRead; passing
// offset < 10 therefore panics with a negative slice index on Linux. On Windows
// the offset is just unused headroom, so 10 is harmless there too. This matches
// wireguard-go's own usage (offset = MessageTransportHeaderSize = 16 ≥ 10).
const tunOffset = 10

// openTun creates the kernel TUN device. On Windows wintun.dll is loaded at
// runtime and its absence makes CreateTUN PANIC (lazyProc.Addr panics on a
// missing DLL), so we recover and return a friendly error instead.
func openTun(name string, mtu int) (wgtun.Device, error) {
	var dev wgtun.Device
	var devErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				devErr = fmt.Errorf("wintun.dll 未找到或加载失败：Windows 需要把 wintun.dll 放在程序目录或 system32 下；Linux 需要权限与 /dev/net/tun。原始 panic: %v", r)
			}
		}()
		dev, devErr = wgtun.CreateTUN(name, mtu)
	}()
	if devErr != nil {
		return nil, devErr
	}
	return dev, nil
}

// Start opens the TUN, configures routes, and runs the bidirectional bridge
// loop (TUN ↔ peer) until ctx is cancelled. It blocks; call via go for a
// non-blocking bridge alongside the transport's own Start.
func (t *TunDevice) Start(ctx context.Context) error {
	ctx, t.cancel = context.WithCancel(ctx)
	log.Printf("tun: starting %s (cidr=%s, gateway=%s, mtu=%d, platform=%s)",
		t.cfg.Device, t.cfg.CIDR, t.cfg.Gateway, t.cfg.MTU, runtime.GOOS)

	// 1. Open the kernel TUN BEFORE adding routes — the old code added routes
	//    to a device that did not exist yet.
	dev, err := openTun(t.cfg.Device, t.cfg.MTU)
	if err != nil {
		return fmt.Errorf("open tun: %w", err)
	}
	t.dev = dev
	name, _ := dev.Name()

	// 2. MTU (platform-specific netsh / ip link).
	if err := SetInterfaceMTU(name, t.cfg.MTU); err != nil {
		log.Printf("tun: set mtu on %s: %v (continuing)", name, err)
	}

	// 3. Route the overlay CIDR through this TUN.
	if err := AddRoute(t.cfg.CIDR, t.cfg.Gateway, name); err != nil {
		log.Printf("tun: add route %s via %s: %v (continuing)", t.cfg.CIDR, t.cfg.Gateway, err)
	}

	// 4. Bridge loops.
	t.inbound = make(chan []byte, 1024)
	t.wg.Add(2)
	go t.readLoop(ctx)
	go t.writeLoop(ctx)

	log.Printf("tun: bridge running on %s", name)
	<-ctx.Done()
	t.shutdown()
	return nil
}

// readLoop pulls packets from the kernel TUN and forwards them to the overlay
// peer by destination IP. Handles both Windows (Read returns 1 packet) and
// Linux (Read may return BatchSize > 1) by iterating 0..n.
func (t *TunDevice) readLoop(ctx context.Context) {
	defer t.wg.Done()
	dev := t.dev
	if dev == nil {
		return
	}
	batch := dev.BatchSize()
	if batch < 1 {
		batch = 1
	}
	bufs := make([][]byte, batch)
	sizes := make([]int, batch)
	for i := range bufs {
		// Each buffer holds offset headroom + one max-size IP packet.
		bufs[i] = make([]byte, tunOffset+t.cfg.MTU+14)
	}
	for {
		n, err := dev.Read(bufs, sizes, tunOffset)
		if err != nil {
			if errors.Is(err, os.ErrClosed) || ctx.Err() != nil {
				return
			}
			log.Printf("tun: read: %v", err)
			continue
		}
		t.mu.Lock()
		peer := t.peer
		t.mu.Unlock()
		for i := 0; i < n; i++ {
			if sizes[i] < 1 {
				continue
			}
			pkt := bufs[i][tunOffset : tunOffset+sizes[i]]
			dstIP, _, _, perr := parsePacket(pkt)
			if perr != nil {
				continue // non-IPv4 / unparseable — skip
			}
			if peer != nil {
				// Copy: bufs are reused next iteration; the overlay may queue.
				if err := peer.SendTo(dstIP, append([]byte(nil), pkt...)); err != nil {
					log.Printf("tun: sendto %s: %v", dstIP, err)
				}
			}
		}
	}
}

// writeLoop drains packets injected by the overlay (via WritePacket) and writes
// them to the kernel TUN, where the host IP stack delivers them to apps.
func (t *TunDevice) writeLoop(ctx context.Context) {
	defer t.wg.Done()
	dev := t.dev
	for {
		select {
		case pkt, ok := <-t.inbound:
			if !ok {
				return
			}
			// Place the IP packet at offset; the library fills the virtio
			// header headroom itself (zeroed on Linux by handleGRO).
			buf := make([][]byte, 1)
			buf[0] = make([]byte, tunOffset+len(pkt))
			copy(buf[0][tunOffset:], pkt)
			if _, err := dev.Write(buf, tunOffset); err != nil {
				if errors.Is(err, os.ErrClosed) || ctx.Err() != nil {
					return
				}
				log.Printf("tun: write: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

// WritePacket injects a packet received from the overlay into the kernel TUN.
// Non-blocking: if the write queue is full we drop (tunnel backpressure) rather
// than blocking the transport's read loop.
func (t *TunDevice) WritePacket(pkt []byte) {
	if t.inbound == nil {
		return
	}
	select {
	case t.inbound <- pkt:
	default:
		log.Printf("tun: inbound queue full, dropping %d bytes", len(pkt))
	}
}

// shutdown closes the TUN and drains the bridge loops, then removes routes.
func (t *TunDevice) shutdown() {
	if t.dev != nil {
		t.dev.Close()
	}
	if t.inbound != nil {
		close(t.inbound)
	}
	t.cleanup()
	t.wg.Wait()
}

func (t *TunDevice) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
	t.wg.Wait()
}

// configureRoutes is kept for compatibility with any caller that used the old
// route-only Start; the real Start now inlines the same route steps after
// opening the device.
func (t *TunDevice) configureRoutes(ctx context.Context) error {
	if runtime.GOOS == "windows" {
		cmds := []string{
			fmt.Sprintf("netsh interface ipv4 add route %s %s %s", t.cfg.CIDR, t.cfg.Device, t.cfg.Gateway),
		}
		for _, cmd := range cmds {
			if err := exec.CommandContext(ctx, "cmd", "/c", cmd).Run(); err != nil {
				log.Printf("tun: route cmd failed: %s: %v", cmd, err)
			}
		}
	} else {
		exec.CommandContext(ctx, "ip", "route", "add", t.cfg.CIDR, "dev", t.cfg.Device).Run()
	}
	return nil
}

func (t *TunDevice) cleanup() {
	if runtime.GOOS == "windows" {
		exec.Command("cmd", "/c",
			fmt.Sprintf("netsh interface ipv4 delete route %s %s", t.cfg.CIDR, t.cfg.Device)).Run()
	} else {
		exec.Command("ip", "route", "del", t.cfg.CIDR, "dev", t.cfg.Device).Run()
	}
}

// parsePacket extracts the destination IP (and TCP/UDP port when present) from a
// raw IPv4 packet. Non-IPv4 or malformed packets return an error so callers can
// skip them. Currently IPv6 is not parsed (the overlay routes by virtual IPv4).
func parsePacket(data []byte) (dstIP net.IP, dstPort int, protocol byte, err error) {
	if len(data) < 20 {
		return nil, 0, 0, fmt.Errorf("packet too short")
	}
	version := data[0] >> 4
	if version != 4 {
		return nil, 0, 0, fmt.Errorf("unsupported IP version: %d", version)
	}
	headerLen := int(data[0]&0x0F) * 4
	if headerLen < 20 || headerLen > len(data) {
		return nil, 0, 0, fmt.Errorf("invalid header length")
	}
	protocol = data[9]
	dstIP = net.IP(data[16:20])
	if protocol == 6 {
		if len(data) < headerLen+20 {
			return dstIP, 0, protocol, nil
		}
		dstPort = int(binary.BigEndian.Uint16(data[headerLen+2 : headerLen+4]))
	} else if protocol == 17 {
		if len(data) < headerLen+8 {
			return dstIP, 0, protocol, nil
		}
		dstPort = int(binary.BigEndian.Uint16(data[headerLen+2 : headerLen+4]))
	}
	return dstIP, dstPort, protocol, nil
}