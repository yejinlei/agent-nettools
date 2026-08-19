package stunvpv

import "net"

type Config struct {
	Enable      bool   `yaml:"enable"`
	Mode        string `yaml:"mode"`         // "supernode" or "client"
	Listen      string `yaml:"listen"`       // TURN server listen address (e.g. ":3478")
	TURNServer  string `yaml:"turn-server"`  // TURN server address for client mode
	Realm       string `yaml:"realm"`        // TURN realm
	Username    string `yaml:"username"`     // TURN auth username
	Password    string `yaml:"password"`     // TURN auth password
	VirtualCIDR string `yaml:"virtual-cidr"` // virtual network CIDR
	MTU         int    `yaml:"mtu"`          // tunnel MTU
}

func DefaultConfig() Config {
	return Config{
		Listen:      ":3478",
		Realm:       "net-redirect",
		VirtualCIDR: "10.201.0.0/16",
		MTU:         1400,
	}
}

var _ = net.IP{}