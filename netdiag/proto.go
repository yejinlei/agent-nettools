package netdiag

import (
	"fmt"
	"strings"

	netstat "github.com/shirou/gopsutil/v3/net"
)

// ProtoStats wraps one row from net.ProtoCounters.
type ProtoStats struct {
	Protocol string
	Stats    map[string]int64
}

const (
	ProtoTCP    = "tcp"
	ProtoUDP    = "udp"
	ProtoICMP   = "icmp"
	ProtoIP     = "ip"
	ProtoIPv6   = "ip_ext"
	ProtoICMPV6 = "icmpv6"
)

func GetProtoStats(protocols []string) ([]ProtoStats, error) {
	if len(protocols) == 0 {
		protocols = []string{ProtoTCP, ProtoUDP, ProtoICMP, ProtoIP, ProtoIPv6, ProtoICMPV6}
	}
	raw, err := netstat.ProtoCounters(protocols)
	if err != nil {
		return nil, fmt.Errorf("get proto stats: %w", err)
	}
	result := make([]ProtoStats, 0, len(raw))
	for _, p := range raw {
		result = append(result, ProtoStats{
			Protocol: p.Protocol,
			Stats:    p.Stats,
		})
	}
	return result, nil
}

func FormatProtoStats(stats []ProtoStats) string {
	if len(stats) == 0 {
		return "(no protocol stats)"
	}
	var sb strings.Builder
	for i, p := range stats {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("== %s ==\n", p.Protocol))
		if len(p.Stats) == 0 {
			sb.WriteString("  (no counters)\n")
			continue
		}
		sb.WriteString(fmt.Sprintf("  %-30s %12s\n", "Counter", "Value"))
		sb.WriteString("  " + strings.Repeat("-", 44) + "\n")
		for k, v := range p.Stats {
			sb.WriteString(fmt.Sprintf("  %-30s %12d\n", k, v))
		}
	}
	return sb.String()
}
