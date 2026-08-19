package cmd

import (
	"context"
	"fmt"
	"net"

	"agent-nettools/config"
	"agent-nettools/dns"
	"agent-nettools/listener"
	"agent-nettools/mitm"
	"agent-nettools/n2n"
	"agent-nettools/proxy"
	"agent-nettools/router"
	"agent-nettools/stunvpv"
	"agent-nettools/tun"
	"agent-nettools/web"
)

// This file holds the per-subsystem runners shared by `start` (full mode) and
// the standalone subcommands (proxy/dns/web/tun/n2n/stunvpv). Each runner
// builds one component from the loaded config and runs it in the foreground
// (blocking until ctx is cancelled or a fatal error occurs). Running them
// standalone is the "non-TUI, tools run independently" mode; the agent's
// `service` tool spawns/stops these same commands from inside the TUI.

func buildRouter(cfg *config.Config) (*router.Router, *proxy.Registry, error) {
	reg, err := proxy.Register(cfg.Proxies)
	if err != nil {
		return nil, nil, fmt.Errorf("register proxies: %w", err)
	}
	rtr, err := router.New(cfg.Mode, cfg.Rules, reg)
	if err != nil {
		return nil, nil, fmt.Errorf("init router: %w", err)
	}
	return rtr, reg, nil
}

// runProxy starts the HTTP/SOCKS5 listener with the configured proxies and
// router. Blocks until ctx is cancelled or a listener fails.
func runProxy(ctx context.Context, cfg *config.Config, logRing *web.LogRing) error {
	rtr, _, err := buildRouter(cfg)
	if err != nil {
		return err
	}

	lst, err := listener.New(listener.Options{
		HTTPPort:   cfg.Listen.HTTP,
		SOCKS5Port: cfg.Listen.SOCKS5,
		Router:     rtr,
	})
	if err != nil {
		return fmt.Errorf("init listener: %w", err)
	}
	if logRing != nil {
		logRing.Write(web.INFO, "proxy: http=:%d socks5=:%d mode=%s", cfg.Listen.HTTP, cfg.Listen.SOCKS5, cfg.Mode)
	}
	go func() {
		<-ctx.Done()
		lst.Stop()
	}()
	return lst.Start()
}

// runDNS starts the local DNS server. Blocks until ctx is cancelled.
func runDNS(ctx context.Context, cfg *config.Config, logRing *web.LogRing) error {
	dnsCfg := dns.DnsConfig{
		Enable:    true,
		Listen:    cfg.DNS.Listen,
		Mode:      cfg.DNS.Mode,
		DoHServer: cfg.DNS.DoHServer,
		DoTServer: cfg.DNS.DoTServer,
		FakeCIDR:  cfg.DNS.FakeCIDR,
	}
	srv, err := dns.NewServer(dnsCfg)
	if err != nil {
		return fmt.Errorf("init dns: %w", err)
	}
	if logRing != nil {
		logRing.Write(web.INFO, "dns: server starting on %s (mode=%s)", cfg.DNS.Listen, cfg.DNS.Mode)
	}
	return srv.Start(ctx)
}

// runWeb starts the dashboard. Blocks until ctx is cancelled.
func runWeb(ctx context.Context, cfg *config.Config) error {
	logRing := web.NewLogRing(1000)
	statsTracker := web.NewStatsTracker()
	webCfg := web.WebConfig{
		Enable:   true,
		Port:     cfg.Web.Port,
		Username: cfg.Web.Username,
		Password: cfg.Web.Password,
	}
	srv := web.NewWebServer(webCfg, logRing, statsTracker)
	return srv.Start(ctx)
}

// runTUN starts the TUN device (route config only for now; packet I/O is a
// pending task). Blocks until ctx is cancelled.
func runTUN(ctx context.Context, cfg *config.Config, logRing *web.LogRing) error {
	tunCfg := tun.TunConfig{
		Enable:  true,
		Device:  cfg.TUN.Device,
		MTU:     cfg.TUN.MTU,
		Gateway: cfg.TUN.Gateway,
		CIDR:    cfg.TUN.CIDR,
		DNS:     cfg.TUN.DNS,
	}
	dev := tun.NewTunDevice(tunCfg)
	if logRing != nil {
		logRing.Write(web.INFO, "tun: device %s starting (cidr=%s)", cfg.TUN.Device, cfg.TUN.CIDR)
	}
	return dev.Start(ctx)
}

// runN2N starts an n2n supernode or edge. Blocks until ctx is cancelled.
func runN2N(ctx context.Context, cfg *config.Config, logRing *web.LogRing) error {
	n2nCfg := n2n.Config{
		Enable:      true,
		Mode:        cfg.N2N.Mode,
		Listen:      cfg.N2N.Listen,
		Supernode:   cfg.N2N.Supernode,
		Community:   cfg.N2N.Community,
		Password:    cfg.N2N.Password,
		VirtualIP:   cfg.N2N.VirtualIP,
		VirtualCIDR: cfg.N2N.VirtualCIDR,
		MTU:         cfg.N2N.MTU,
		Interval:    cfg.N2N.Interval,
	}
	switch cfg.N2N.Mode {
	case "supernode":
		sn, err := n2n.NewSupernode(n2nCfg)
		if err != nil {
			return fmt.Errorf("n2n supernode: %w", err)
		}
		if logRing != nil {
			logRing.Write(web.INFO, "n2n: supernode starting on %s (community=%s)", cfg.N2N.Listen, cfg.N2N.Community)
		}
		return sn.Start(ctx)
	case "edge":
		edge, err := n2n.NewEdge(n2nCfg)
		if err != nil {
			return fmt.Errorf("n2n edge: %w", err)
		}
		edge.OnData(func(srcIP net.IP, data []byte) {
			if logRing != nil {
				logRing.Write(web.DEBUG, "n2n: data from %s (%d bytes)", srcIP, len(data))
			}
		})
		if logRing != nil {
			logRing.Write(web.INFO, "n2n: edge starting, supernode=%s", cfg.N2N.Supernode)
		}
		return edge.Start(ctx)
	default:
		return fmt.Errorf("n2n: unknown mode %q", cfg.N2N.Mode)
	}
}

// runSTUNVPV starts a STUN/TURN supernode (server) or client. Blocks until
// ctx is cancelled.
func runSTUNVPV(ctx context.Context, cfg *config.Config, logRing *web.LogRing) error {
	stunCfg := stunvpv.Config{
		Enable:      true,
		Mode:        cfg.STUNVPN.Mode,
		Listen:      cfg.STUNVPN.Listen,
		TURNServer:  cfg.STUNVPN.TURNServer,
		Realm:       cfg.STUNVPN.Realm,
		Username:    cfg.STUNVPN.Username,
		Password:    cfg.STUNVPN.Password,
		VirtualCIDR: cfg.STUNVPN.VirtualCIDR,
		MTU:         cfg.STUNVPN.MTU,
	}
	switch cfg.STUNVPN.Mode {
	case "supernode":
		sn, err := stunvpv.NewServer(stunCfg)
		if err != nil {
			return fmt.Errorf("stunvpv server: %w", err)
		}
		if logRing != nil {
			logRing.Write(web.INFO, "stunvpv: server starting on %s (cidr=%s)", cfg.STUNVPN.Listen, cfg.STUNVPN.VirtualCIDR)
		}
		return sn.Start(ctx)
	case "client":
		cl, err := stunvpv.NewClient(stunCfg)
		if err != nil {
			return fmt.Errorf("stunvpv client: %w", err)
		}
		cl.OnData(func(srcIP net.IP, data []byte) {
			if logRing != nil {
				logRing.Write(web.DEBUG, "stunvpv: data from %s (%d bytes)", srcIP, len(data))
			}
		})
		if logRing != nil {
			logRing.Write(web.INFO, "stunvpv: client starting, server=%s", cfg.STUNVPN.TURNServer)
		}
		return cl.Start(ctx)
	default:
		return fmt.Errorf("stunvpv: unknown mode %q", cfg.STUNVPN.Mode)
	}
}

// runMITM sets up HTTPS interception. Currently loads/ensures the CA and
// returns; a full intercepting listener is a pending task.
func runMITM(ctx context.Context, cfg *config.Config, logRing *web.LogRing) error {
	caCert, err := mitm.LoadCA(cfg.MITM.CAPath+".crt", cfg.MITM.CAPath+".key")
	if err != nil {
		if logRing != nil {
			logRing.Write(web.WARN, "mitm: cannot load CA, generating new one: %v", err)
		}
		caCert, err = mitm.GenerateCA("net-redirect")
		if err != nil {
			return fmt.Errorf("generate ca: %w", err)
		}
		if err := caCert.SaveTo(cfg.MITM.CAPath); err != nil {
			return fmt.Errorf("save ca: %w", err)
		}
	}
	_ = mitm.NewInterceptor(caCert, cfg.MITM.CertDir)
	if logRing != nil {
		logRing.Write(web.INFO, "mitm: HTTPS interception ready on :%d", cfg.MITM.HTTPPort)
	}
	<-ctx.Done()
	return nil
}
