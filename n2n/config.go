package n2n

import "net"

type Config struct {
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

func DefaultConfig() Config {
	return Config{
		Listen:      ":7654",
		Community:   "net-redirect",
		VirtualCIDR: "10.200.0.0/16",
		MTU:         1400,
		Interval:    30,
	}
}

var _ = net.IP{}