package tun

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"sync"
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
	cfg    TunConfig
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

func (t *TunDevice) Start(ctx context.Context) error {
	ctx, t.cancel = context.WithCancel(ctx)
	log.Printf("tun: starting %s (cidr=%s, gateway=%s, mtu=%d)",
		t.cfg.Device, t.cfg.CIDR, t.cfg.Gateway, t.cfg.MTU)
	if err := t.configureRoutes(ctx); err != nil {
		return fmt.Errorf("configure routes: %w", err)
	}
	log.Printf("tun: running on %s (platform=%s)", t.cfg.Device, runtime.GOOS)
	<-ctx.Done()
	t.cleanup()
	return nil
}

func (t *TunDevice) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
	t.wg.Wait()
}

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

