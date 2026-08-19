package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"agent-nettools/agent"
	"agent-nettools/config"
	"agent-nettools/listener"
	"agent-nettools/proxy"
	"agent-nettools/router"
	"agent-nettools/web"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configPath string
var proxyURL string

func init() {
	// --config is persistent so every subcommand (start, tui, proxy, dns, ...)
	// can read the same config path via cmd.Flags().GetString("config").
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "path to config file")
	startCmd().Flags().StringVar(&proxyURL, "proxy", "", "fast mode: proxy URL string (e.g. ss://aes-256-gcm:pass@host:port)")
}

var rootCmd = &cobra.Command{
	Use:   "agent-nettools",
	Short: "A lightweight network proxy client with an LLM agent",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Usage()
	},
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Generate a default config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			if cfgPath == "" {
				cwd, _ := os.Getwd()
				cfgPath = filepath.Join(cwd, "config.yml")
			}
			return os.WriteFile(cfgPath, []byte(config.ExampleConfig), 0644)
		},
	}
}

func startCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the proxy server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if proxyURL != "" {
				return fastStart(cmd)
			}
			return fullStart(cmd)
		},
	}
	return cmd
}

func fastStart(cmd *cobra.Command) error {
	u, err := parseProxyURL(proxyURL)
	if err != nil {
		return fmt.Errorf("parse proxy URL: %w", err)
	}

	pcfg := config.ProxyConfig{
		Name:   u.Scheme,
		Type:   u.Scheme,
		Server: u.Hostname(),
		Port:   mustPort(u.Port()),
	}

	switch u.Scheme {
	case "ss":
		pcfg.Cipher = u.User.Username()
		pass, _ := u.User.Password()
		pcfg.Password = pass
	case "http", "https":
		pcfg.Username = u.User.Username()
		pcfg.Password, _ = u.User.Password()
	case "socks5":
		pcfg.Username = u.User.Username()
		pcfg.Password, _ = u.User.Password()
	case "trojan":
		pcfg.Password = u.User.Username()
		pcfg.SNI = u.Query().Get("sni")
	}

	reg, err := proxy.Register([]config.ProxyConfig{pcfg})
	if err != nil {
		return fmt.Errorf("register proxy: %w", err)
	}

	rtr, err := router.New("global", nil, reg)
	if err != nil {
		return err
	}

	cfgPath, _ := cmd.Flags().GetString("config")
	cfg := &config.Config{Listen: config.Listen{HTTP: 7890, SOCKS5: 7891}, Mode: "global"}
	if cfgPath != "" {
		if c, err := config.Load(cfgPath); err == nil {
			cfg = c
		}
	}

	lst, err := listener.New(listener.Options{
		HTTPPort:   cfg.Listen.HTTP,
		SOCKS5Port: cfg.Listen.SOCKS5,
		Router:     rtr,
	})
	if err != nil {
		return err
	}

	fmt.Printf("net-redirect fast mode (proxy=%s, http=%d, socks5=%d)\n", proxyURL, cfg.Listen.HTTP, cfg.Listen.SOCKS5)
	return lst.Start()
}

func fullStart(cmd *cobra.Command) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	if cfgPath == "" {
		cwd, _ := os.Getwd()
		cfgPath = filepath.Join(cwd, "config.yml")
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logRing := web.NewLogRing(1000)
	// One shared StatsTracker: the proxy listener feeds it traffic counts, the
	// web dashboard reads it via /api/stats. Created once here so both
	// subsystems share the same counts even though they run in goroutines.
	stats := web.NewStatsTracker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Catch interrupts so subsystems get a chance to clean up.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		<-sigCh
		cancel()
	}()

	// Start each enabled subsystem in its own goroutine. Errors are collected
	// so a failure in one doesn't silently abort the others.
	errCh := make(chan error, 8)
	start := func(name string, fn func() error) {
		go func() {
			if err := fn(); err != nil {
				errCh <- fmt.Errorf("%s: %w", name, err)
			}
		}()
	}

	if cfg.DNS.Enable {
		start("dns", func() error { return runDNS(ctx, cfg, logRing) })
	}
	if cfg.Web.Enable {
		start("web", func() error {
			wsrv := web.NewWebServer(web.WebConfig{
				Enable: true, Port: cfg.Web.Port,
				Username: cfg.Web.Username, Password: cfg.Web.Password,
			}, logRing, stats)
			return wsrv.Start(ctx)
		})
	}
	if cfg.TUN.Enable {
		start("tun", func() error { return runTUN(ctx, cfg, logRing) })
	}
	if cfg.MITM.Enable {
		start("mitm", func() error { return runMITM(ctx, cfg, logRing) })
	}
	if cfg.N2N.Enable {
		start("n2n", func() error { return runN2N(ctx, cfg, logRing) })
	}
	if cfg.STUNVPN.Enable {
		start("stunvpv", func() error { return runSTUNVPV(ctx, cfg, logRing) })
	}

	// The proxy listener is the primary foreground service; it blocks here.
	fmt.Printf("net-redirect running (mode=%s, http=%d, socks5=%d)\n", cfg.Mode, cfg.Listen.HTTP, cfg.Listen.SOCKS5)
	proxyErr := make(chan error, 1)
	go func() {
		proxyErr <- runProxy(ctx, cfg, logRing, stats)
	}()

	select {
	case err := <-errCh:
		cancel()
		return err
	case err := <-proxyErr:
		cancel()
		return err
	case <-ctx.Done():
		return nil
	}
}

func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current proxy config",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			if cfgPath == "" {
				cwd, _ := os.Getwd()
				cfgPath = filepath.Join(cwd, "config.yml")
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			b, _ := yaml.Marshal(cfg)
			fmt.Println(string(b))
			return nil
		},
	}
	return cmd
}

func pingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ping",
		Short: "Test proxy latency",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			if cfgPath == "" {
				cwd, _ := os.Getwd()
				cfgPath = filepath.Join(cwd, "config.yml")
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			reg, err := proxy.Register(cfg.Proxies)
			if err != nil {
				return fmt.Errorf("register proxies: %w", err)
			}
			reg.Each(func(name string, p proxy.Proxy) {
				l, err := p.Latency("https://www.gstatic.com/generate_204")
				if err != nil {
					fmt.Printf("  %-20s ERROR: %s\n", name, err.Error())
				} else {
					fmt.Printf("  %-20s %s\n", name, l.String())
				}
			})
			return nil
		},
	}
	return cmd
}

func useCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use <group> <proxy>",
		Short: "Switch a selector group to a specific proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("need <group> and <proxy> args")
			}
			cfgPath, _ := cmd.Flags().GetString("config")
			if cfgPath == "" {
				cwd, _ := os.Getwd()
				cfgPath = filepath.Join(cwd, "config.yml")
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			found := false
			for i, g := range cfg.Groups {
				if g.Name == args[0] {
					g.Default = args[1]
					cfg.Groups[i] = g
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("group %q not found", args[0])
			}
			b, _ := yaml.Marshal(cfg)
			fmt.Printf("Switched group %s to %s\n", args[0], args[1])
			return os.WriteFile(cfgPath, b, 0644)
		},
	}
	return cmd
}

func tuiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Start the LLM Agent interactive mode (natural-language control)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			if cfgPath == "" {
				cwd, _ := os.Getwd()
				cfgPath = filepath.Join(cwd, "config.yml")
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			sysPrompt := cfg.Agent.SystemPrompt
			if sysPrompt == "" {
				sysPrompt = agent.DefaultSystemPrompt()
			}
			agentCfg := agent.Config{
				Enable:       true,
				BaseURL:      cfg.Agent.BaseURL,
				APIKey:       cfg.Agent.APIKey,
				Model:        cfg.Agent.Model,
				SystemPrompt: sysPrompt,
				ConfigPath:   cfgPath,
				MemoryPath:   agent.DefaultMemoryPath(),
				Timeout:      cfg.Agent.Timeout,
				MaxRetries:   cfg.Agent.MaxRetries,
			}
			if agentCfg.BaseURL == "" {
				agentCfg.BaseURL = "https://api.openai.com/v1"
			}
			if agentCfg.Model == "" {
				agentCfg.Model = "gpt-4o-mini"
			}
			if agentCfg.APIKey == "" {
				return fmt.Errorf("agent: no api-key set (configure agent.api-key in %s or set AGENT_API_KEY env)", cfgPath)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			return agent.RunTUI(ctx, agentCfg)
		},
	}
	return cmd
}

func Execute() {
	rootCmd.AddCommand(initCmd())
	rootCmd.AddCommand(startCmd())
	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(pingCmd())
	rootCmd.AddCommand(useCmd())
	rootCmd.AddCommand(forwardCmd())
	rootCmd.AddCommand(tuiCmd())
	// Standalone subsystem commands (each runs one component in foreground).
	rootCmd.AddCommand(proxyCmd())
	rootCmd.AddCommand(dnsCmd())
	rootCmd.AddCommand(webCmd())
	rootCmd.AddCommand(tunCmd())
	rootCmd.AddCommand(n2nCmd())
	rootCmd.AddCommand(stunvpvCmd())
	// Standalone SSH/SFTP file copy (non-TUI tool; shares memory with the agent).
	rootCmd.AddCommand(scpCmd())
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func mustPort(p string) int {
	if p == "" { return 0 }
	n, _ := strconv.Atoi(p)
	return n
}

func parseProxyURL(raw string) (*url.URL, error) {
	switch {
	case strings.HasPrefix(raw, "ss://"), strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"), strings.HasPrefix(raw, "socks5://"), strings.HasPrefix(raw, "trojan://"):
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		return u, nil
	default:
		return nil, fmt.Errorf("unsupported scheme in %s", raw)
	}
}