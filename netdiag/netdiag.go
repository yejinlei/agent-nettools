package netdiag

import (
	"fmt"
	"net"
	"strings"

	netstat "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// Connection represents a single socket connection.
type Connection struct {
	Proto      string
	LocalIP    string
	LocalPort  int
	RemoteIP   string
	RemotePort int
	State      string
	PID        int
	Process    string
}

// NetStats aggregates connection counts by state.
type NetStats struct {
	Total       int64
	TCP         int64
	UDP         int64
	ESTABLISHED int64
	TIME_WAIT   int64
	CLOSE_WAIT  int64
	LISTEN      int64
	SYN_SENT    int64
	SYN_RECV    int64
	FIN_WAIT_1  int64
	FIN_WAIT_2  int64
	LAST_ACK    int64
	CLOSING     int64
}

const (
	afInet     = 2
	afInet6    = 10
	sockStream = 1
	sockDgram  = 2
)

// Filter holds the criteria for filtering connections.
type Filter struct {
	Proto string // "tcp" / "udp" / "raw" / "unix" / "all"
	PID   int32
	Port  int    // 0 = any; >0 match Laddr.Port OR Raddr.Port
	State string // e.g. "ESTABLISHED", "LISTEN" (case-insensitive)
	Src   string // e.g. "10.0.0.1" or "10.0.0.1:8080"
	Dst   string
}

// GetConnections returns connections with optional protocol filter and post-filtering.
func GetConnections(proto string, flt *Filter) ([]Connection, error) {
	var raw []netstat.ConnectionStat
	var err error

	kind := "all"
	switch strings.ToLower(proto) {
	case "tcp":
		kind = "tcp"
	case "udp":
		kind = "udp"
	case "unix", "u", "local":
		kind = "unix"
	}

	if flt != nil && flt.PID > 0 {
		// Use ConnectionsPid for the PID-specific path — it avoids a full sweep.
		if kind == "unix" {
			raw, err = netstat.ConnectionsPid("unix", flt.PID)
		} else {
			raw, err = netstat.ConnectionsPid("all", flt.PID)
		}
	} else {
		raw, err = netstat.Connections(kind)
	}
	if err != nil {
		return nil, fmt.Errorf("get connections: %w", err)
	}

	cache := make(map[int32]string)
	result := make([]Connection, 0, len(raw))
	for _, c := range raw {
		conn := toConnection(c, cache)
		if flt != nil && !flt.matches(conn) {
			continue
		}
		result = append(result, conn)
	}
	return result, nil
}

// GetListeners returns TCP sockets in LISTEN state (with optional filter).
func GetListeners(flt *Filter) ([]Connection, error) {
	all, err := GetConnections("all", flt)
	if err != nil {
		return nil, err
	}
	var result []Connection
	for _, c := range all {
		if strings.Contains(c.Proto, "tcp") && c.State == "LISTEN" {
			result = append(result, c)
		}
	}
	return result, nil
}

// GetStats returns aggregate connection counts by state.
func GetStats() (NetStats, error) {
	all, err := GetConnections("all", nil)
	if err != nil {
		return NetStats{}, err
	}
	var stats NetStats
	for _, c := range all {
		stats.Total++
		if strings.Contains(c.Proto, "tcp") {
			stats.TCP++
		} else {
			stats.UDP++
		}
		switch c.State {
		case "ESTABLISHED":
			stats.ESTABLISHED++
		case "TIME_WAIT":
			stats.TIME_WAIT++
		case "CLOSE_WAIT":
			stats.CLOSE_WAIT++
		case "LISTEN":
			stats.LISTEN++
		case "SYN_SENT":
			stats.SYN_SENT++
		case "SYN_RECV", "SYN_RCVD":
			stats.SYN_RECV++
		case "FIN_WAIT1", "FIN_WAIT_1":
			stats.FIN_WAIT_1++
		case "FIN_WAIT2", "FIN_WAIT_2":
			stats.FIN_WAIT_2++
		case "LAST_ACK":
			stats.LAST_ACK++
		case "CLOSING":
			stats.CLOSING++
		}
	}
	return stats, nil
}

func (f *Filter) matches(c Connection) bool {
	if f == nil {
		return true
	}
	if f.Proto != "" && f.Proto != "all" {
		lo := strings.ToLower(f.Proto)
		if !strings.Contains(strings.ToLower(c.Proto), lo) {
			return false
		}
	}
	if f.State != "" && !strings.EqualFold(c.State, f.State) {
		return false
	}
	if f.Port > 0 && c.LocalPort != f.Port && c.RemotePort != f.Port {
		return false
	}
	if f.Src != "" && !ipPortMatch(c.LocalIP, c.LocalPort, f.Src) {
		return false
	}
	if f.Dst != "" && !ipPortMatch(c.RemoteIP, c.RemotePort, f.Dst) {
		return false
	}
	return true
}

// ipPortMatch tests whether (ip, port) matches an address like "1.2.3.4:80"
// or just "1.2.3.4". Empty or wildcard addresses always match.
func ipPortMatch(ip string, port int, spec string) bool {
	if spec == "" {
		return true
	}
	h, p, err := net.SplitHostPort(spec)
	if err != nil {
		// spec is a bare IP; only match IP
		return ip == spec || ip == "::ffff:"+spec || spec == ip
	}
	if ip != h && ip != "::ffff:"+h {
		return false
	}
	if p == "" {
		return true
	}
	var portN int
	fmt.Sscanf(p, "%d", &portN)
	return port == portN
}

func toConnection(c netstat.ConnectionStat, cache map[int32]string) Connection {
	var proto string
	switch c.Type {
	case sockStream:
		proto = "tcp"
	case sockDgram:
		proto = "udp"
	default:
		proto = "raw"
	}
	if c.Family == afInet6 {
		proto += "6"
	}

	conn := Connection{
		Proto:      proto,
		LocalIP:    c.Laddr.IP,
		LocalPort:  int(c.Laddr.Port),
		RemoteIP:   c.Raddr.IP,
		RemotePort: int(c.Raddr.Port),
		State:      c.Status,
		PID:        int(c.Pid),
	}
	if c.Pid > 0 {
		if name, ok := cache[c.Pid]; ok {
			conn.Process = name
		} else {
			name := procName(c.Pid)
			cache[c.Pid] = name
			conn.Process = name
		}
	}
	return conn
}

func procName(pid int32) string {
	proc, err := process.NewProcess(pid)
	if err != nil {
		return ""
	}
	name, err := proc.Name()
	if err != nil {
		return ""
	}
	return name
}
// FormatConnections formats a slice of connections as a readable table.
func FormatConnections(conns []Connection) string {
	if len(conns) == 0 {
		return "(no connections)"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-5s %-22s %-30s %-14s %5s  %s\n",
		"Proto", "Local", "Remote", "State", "PID", "Process"))
	sb.WriteString(strings.Repeat("-", 95) + "\n")
	for _, c := range conns {
		local := fmt.Sprintf("%s:%d", c.LocalIP, c.LocalPort)
		remote := "-"
		if !(c.RemoteIP == "0.0.0.0" && c.RemotePort == 0) &&
			!(c.RemoteIP == "::" && c.RemotePort == 0) {
			remote = fmt.Sprintf("%s:%d", c.RemoteIP, c.RemotePort)
		}
		state := c.State
		if state == "" {
			state = "-"
		}
		proc := c.Process
		if proc == "" {
			proc = "-"
		}
		sb.WriteString(fmt.Sprintf("%-5s %-22s %-30s %-14s %5d  %s\n",
			c.Proto, local, remote, state, c.PID, proc))
	}
	return sb.String()
}

// FormatStats formats NetStats as a readable summary.
func FormatStats(stats NetStats) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("TCP: %4d  UDP: %4d  Total: %4d\n", stats.TCP, stats.UDP, stats.Total))
	sb.WriteString(fmt.Sprintf("  LISTEN: %4d  ESTABLISHED: %4d  TIME_WAIT: %4d\n",
		stats.LISTEN, stats.ESTABLISHED, stats.TIME_WAIT))
	sb.WriteString(fmt.Sprintf("  CLOSE_WAIT: %4d  SYN_SENT: %4d  SYN_RECV: %4d\n",
		stats.CLOSE_WAIT, stats.SYN_SENT, stats.SYN_RECV))
	sb.WriteString(fmt.Sprintf("  FIN_WAIT_1: %4d  FIN_WAIT_2: %4d  LAST_ACK: %4d\n",
		stats.FIN_WAIT_1, stats.FIN_WAIT_2, stats.LAST_ACK))
	sb.WriteString(fmt.Sprintf("  CLOSING: %4d\n", stats.CLOSING))
	return sb.String()
}
