package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type N2NConfig struct {
	Enable      bool   `yaml:"enable"`
	Mode        string `yaml:"mode"`         // "supernode" or "edge"
	Listen      string `yaml:"listen"`       // UDP listen address
	Supernode   string `yaml:"supernode"`    // supernode address (for edge mode)
	Community   string `yaml:"community"`    // community name
	Password    string `yaml:"password"`     // encryption password
	VirtualIP   string `yaml:"virtual-ip"`   // requested virtual IP (empty = auto)
	VirtualCIDR string `yaml:"virtual-cidr"` // virtual network CIDR
	MTU         int    `yaml:"mtu"`          // tunnel MTU
	Interval    int    `yaml:"interval"`     // heartbeat interval (seconds)
}

type STUNVPNConfig struct {
	Enable      bool   `yaml:"enable"`
	Mode        string `yaml:"mode"`         // "supernode" or "client"
	Listen      string `yaml:"listen"`       // TURN server listen address
	TURNServer  string `yaml:"turn-server"`  // TURN server address for client mode
	Realm       string `yaml:"realm"`        // TURN realm
	Username    string `yaml:"username"`     // TURN auth username
	Password    string `yaml:"password"`     // TURN auth password
	VirtualCIDR string `yaml:"virtual-cidr"` // virtual network CIDR
	MTU         int    `yaml:"mtu"`          // tunnel MTU
}

type Config struct {
	Listen  Listen        `yaml:"listen"`
	Mode    string        `yaml:"mode"`
	Proxies []ProxyConfig `yaml:"proxies"`
	Groups  []GroupConfig `yaml:"proxy-groups"`
	Rules   []string      `yaml:"rules"`
	TUN     TunConfig     `yaml:"tun"`
	DNS     DnsConfig     `yaml:"dns"`
	Web     WebConfig     `yaml:"web"`
	MITM    MitmConfig    `yaml:"mitm"`
	N2N     N2NConfig     `yaml:"n2n"`
	STUNVPN STUNVPNConfig `yaml:"stunvpv"`
	Agent   AgentConfig   `yaml:"agent"`
}

type Listen struct {
	HTTP   int `yaml:"http"`
	SOCKS5 int `yaml:"socks5"`
}

type ProxyConfig struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Server   string   `yaml:"server"`
	Port     int      `yaml:"port"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
	Cipher   string   `yaml:"cipher"`
	SNI      string   `yaml:"sni"`
	ALPN     []string `yaml:"alpn"`
	UUID     string   `yaml:"uuid"`
	AlterID  int      `yaml:"alterId"`
	Method   string   `yaml:"method"`
	Proxies  []string `yaml:"proxies"`
	URL      string   `yaml:"url"`
	Interval int      `yaml:"interval"`
	Default  string   `yaml:"default"`
}

type GroupConfig struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Proxies  []string `yaml:"proxies"`
	URL      string   `yaml:"url"`
	Interval int      `yaml:"interval"`
	Default  string   `yaml:"default"`
}

type TunConfig struct {
	Enable  bool   `yaml:"enable"`
	Device  string `yaml:"device"`
	MTU     int    `yaml:"mtu"`
	Gateway string `yaml:"gateway"`
	CIDR    string `yaml:"cidr"`
	DNS     string `yaml:"dns"`
}

type DnsConfig struct {
	Enable    bool   `yaml:"enable"`
	Listen    string `yaml:"listen"`
	Mode      string `yaml:"mode"`
	DoHServer string `yaml:"doh-server"`
	DoTServer string `yaml:"dot-server"`
	FakeCIDR  string `yaml:"fake-cidr"`
}

type WebConfig struct {
	Enable   bool   `yaml:"enable"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type MitmConfig struct {
	Enable   bool   `yaml:"enable"`
	CAPath   string `yaml:"ca-path"`
	CertDir  string `yaml:"cert-dir"`
	HTTPPort int    `yaml:"http-port"`
}

type AgentConfig struct {
	Enable      bool   `yaml:"enable"`
	BaseURL     string `yaml:"base-url"`
	APIKey      string `yaml:"api-key"`
	Model       string `yaml:"model"`
	SystemPrompt string `yaml:"system-prompt"` // optional custom system prompt for the agent
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{Mode: "rule"}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Listen.HTTP == 0 {
		cfg.Listen.HTTP = 7890
	}
	if cfg.Listen.SOCKS5 == 0 {
		cfg.Listen.SOCKS5 = 7891
	}
	if cfg.Mode == "" {
		cfg.Mode = "rule"
	}
	cfg.Mode = strings.ToLower(cfg.Mode)
	if cfg.TUN.MTU == 0 {
		cfg.TUN.MTU = 1500
	}
	if cfg.TUN.Gateway == "" {
		cfg.TUN.Gateway = "198.18.0.1"
	}
	if cfg.TUN.CIDR == "" {
		cfg.TUN.CIDR = "198.18.0.0/16"
	}
	if cfg.TUN.DNS == "" {
		cfg.TUN.DNS = "198.18.0.2"
	}
	if cfg.TUN.Device == "" {
		cfg.TUN.Device = "net-redirect"
	}
	if cfg.DNS.Listen == "" {
		cfg.DNS.Listen = ":53"
	}
	if cfg.DNS.Mode == "" {
		cfg.DNS.Mode = "direct"
	}
	if cfg.DNS.FakeCIDR == "" {
		cfg.DNS.FakeCIDR = "198.18.0.0/15"
	}
	if cfg.Web.Port == 0 {
		cfg.Web.Port = 9090
	}
	if cfg.MITM.HTTPPort == 0 {
		cfg.MITM.HTTPPort = 8081
	}
	if cfg.MITM.CAPath == "" {
		cfg.MITM.CAPath = "ca.crt"
	}
	if cfg.MITM.CertDir == "" {
		cfg.MITM.CertDir = "certs"
	}
	if cfg.N2N.Listen == "" {
		cfg.N2N.Listen = ":7654"
	}
	if cfg.N2N.Community == "" {
		cfg.N2N.Community = "net-redirect"
	}
	if cfg.N2N.VirtualCIDR == "" {
		cfg.N2N.VirtualCIDR = "10.200.0.0/16"
	}
	if cfg.N2N.MTU == 0 {
		cfg.N2N.MTU = 1400
	}
	if cfg.N2N.Interval == 0 {
		cfg.N2N.Interval = 30
	}
	if cfg.STUNVPN.Listen == "" {
		cfg.STUNVPN.Listen = ":3478"
	}
	if cfg.STUNVPN.Realm == "" {
		cfg.STUNVPN.Realm = "net-redirect"
	}
	if cfg.STUNVPN.VirtualCIDR == "" {
		cfg.STUNVPN.VirtualCIDR = "10.201.0.0/16"
	}
	if cfg.STUNVPN.MTU == 0 {
		cfg.STUNVPN.MTU = 1400
	}
	if cfg.Agent.BaseURL == "" {
		cfg.Agent.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Agent.Model == "" {
		cfg.Agent.Model = "gpt-4o-mini"
	}
	if cfg.Agent.APIKey == "" {
		if k := os.Getenv("AGENT_API_KEY"); k != "" {
			cfg.Agent.APIKey = k
		}
	}
	return cfg, nil
}

// LoadFromBytes parses YAML config from a byte slice (no file read).
// Used by the agent to validate a candidate YAML before writing.
func LoadFromBytes(data []byte) (*Config, error) {
	cfg := &Config{Mode: "rule"}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	// reuse Load's defaulting by writing to a temp file would be wasteful;
	// replicate the essential defaults inline.
	if cfg.Listen.HTTP == 0 {
		cfg.Listen.HTTP = 7890
	}
	if cfg.Listen.SOCKS5 == 0 {
		cfg.Listen.SOCKS5 = 7891
	}
	if cfg.Mode == "" {
		cfg.Mode = "rule"
	}
	cfg.Mode = strings.ToLower(cfg.Mode)
	if cfg.Agent.BaseURL == "" {
		cfg.Agent.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Agent.Model == "" {
		cfg.Agent.Model = "gpt-4o-mini"
	}
	return cfg, nil
}

// YAMLMarshal marshals a value to YAML (exposed for the agent package).
func YAMLMarshal(v any) ([]byte, error) {
	return yaml.Marshal(v)
}

const ExampleConfig = `
# agent-nettools example configuration

listen:
  http: 7890
  socks5: 7891

# Mode: global / rule / direct
mode: rule

proxies:
  - name: proxy-http
    type: http
    server: 1.2.3.4
    port: 443
    username: user
    password: pass

  - name: proxy-socks5
    type: socks5
    server: 5.6.7.8
    port: 1080

  - name: proxy-https
    type: https
    server: 9.10.11.12
    port: 443
    sni: example.com

  - name: ss-1
    type: ss
    server: 13.14.15.16
    port: 8388
    cipher: aes-256-gcm
    password: my-secret

  - name: trojan-1
    type: trojan
    server: 17.18.19.20
    port: 443
    password: trojan-pass
    sni: trojan.example.com

  - name: vmess-1
    type: vmess
    server: 21.22.23.24
    port: 8080
    uuid: 35f70a81-0e7f-4a3a-b180-2f4f2c9d2f4e
    alterId: 0
    cipher: auto

proxy-groups:
  - name: Auto
    type: url-test
    proxies: [proxy-http, proxy-socks5, ss-1]
    url: https://www.gstatic.com/generate_204
    interval: 300

  - name: Manual
    type: selector
    proxies: [proxy-http, proxy-socks5, ss-1, trojan-1, vmess-1, DIRECT]
    default: proxy-http

rules:
  - DOMAIN,google.com,Auto
  - DOMAIN-SUFFIX,.google.com,Auto
  - IP-CIDR,8.8.8.8/32,DIRECT
  - GEOIP,CN,DIRECT
  - MATCH,Auto

# DNS server configuration
dns:
  enable: false
  listen: ":53"
  mode: direct   # direct | doh | dot
  doh-server: "https://cloudflare-dns.com/dns-query"
  dot-server: "1.1.1.1:853"
  fake-cidr: "198.18.0.0/15"

# Web dashboard
web:
  enable: false
  port: 9090
  username: ""
  password: ""

# TUN transparent proxy
tun:
  enable: false
  device: "net-redirect"
  mtu: 1500
  gateway: "198.18.0.1"
  cidr: "198.18.0.0/16"
  dns: "198.18.0.2"

# MITM HTTPS inspection
mitm:
  enable: false
  ca-path: "ca.crt"
  cert-dir: "certs"
  http-port: 8081

# n2n virtual LAN (P2P VPN)
# Can run as supernode (hub) or edge (node)
n2n:
  enable: false
  mode: "edge"        # "supernode" or "edge"
  listen: ":7654"     # UDP listen port
  supernode: ""       # supernode address for edge mode (e.g. "1.2.3.4:7654")
  community: "net-redirect"
  password: ""        # encryption password (optional)
  virtual-cidr: "10.200.0.0/16"  # virtual network CIDR (supernode only)
  mtu: 1400
  interval: 30        # heartbeat interval (seconds)

# STUN/TURN virtual LAN (standard protocol P2P VPN)
# Can run as supernode (STUN/TURN server) or client
stunvpv:
  enable: false
  mode: "supernode"    # "supernode" or "client"
  listen: ":3478"      # STUN/TURN server listen port
  turn-server: ""      # TURN server address for client mode (e.g. "1.2.3.4:3478")
  realm: "net-redirect"
  username: ""         # TURN auth username
  password: ""         # TURN auth password
  virtual-cidr: "10.201.0.0/16"
  mtu: 1400

# LLM Agent (natural-language control via the tui subcommand)
# Set api-key here or export AGENT_API_KEY env var.
agent:
  enable: false
  base-url: "https://api.openai.com/v1"   # any OpenAI-compatible endpoint
  api-key: ""                             # your API key
  model: "gpt-4o-mini"
`