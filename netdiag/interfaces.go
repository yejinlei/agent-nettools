package netdiag

import (
	"fmt"
	"strings"

	netstat "github.com/shirou/gopsutil/v3/net"
)

// InterfaceStats bundles an interface's metadata with its byte/packet counters.
type InterfaceStats struct {
	Name         string
	MTU          int
	MAC          string
	State        string // "UP" / "DOWN"
	Loopback     bool
	Multicast    bool
	Addrs        []string
	BytesSent    uint64
	BytesRecv    uint64
	PacketsSent  uint64
	PacketsRecv  uint64
	ErrorsIn     uint64
	ErrorsOut    uint64
	DropsIn      uint64
	DropsOut     uint64
	FifoIn       uint64
	FifoOut      uint64
}

func GetInterfaces() ([]InterfaceStats, error) {
	ifaces, err := netstat.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("get interfaces: %w", err)
	}
	ioCounters, err := netstat.IOCounters(true)
	if err != nil {
		return nil, fmt.Errorf("get IO counters: %w", err)
	}
	ioByIface := make(map[string]netstat.IOCountersStat, len(ioCounters))
	for _, io := range ioCounters {
		ioByIface[io.Name] = io
	}

	result := make([]InterfaceStats, 0, len(ifaces))
	for _, ifc := range ifaces {
		s := InterfaceStats{
			Name: ifc.Name,
			MTU:  ifc.MTU,
			MAC:  ifc.HardwareAddr,
		}
		flags := strings.ToLower(strings.Join(ifc.Flags, ","))
		if strings.Contains(flags, "up") {
			s.State = "UP"
		} else {
			s.State = "DOWN"
		}
		s.Loopback = strings.Contains(flags, "loopback")
		s.Multicast = strings.Contains(flags, "multicast")
		for _, a := range ifc.Addrs {
			s.Addrs = append(s.Addrs, a.Addr)
		}
		if io, ok := ioByIface[ifc.Name]; ok {
			s.BytesSent = io.BytesSent
			s.BytesRecv = io.BytesRecv
			s.PacketsSent = io.PacketsSent
			s.PacketsRecv = io.PacketsRecv
			s.ErrorsIn = io.Errin
			s.ErrorsOut = io.Errout
			s.DropsIn = io.Dropin
			s.DropsOut = io.Dropout
			s.FifoIn = io.Fifoin
			s.FifoOut = io.Fifoout
		}
		result = append(result, s)
	}
	return result, nil
}

func FormatInterfaces(stats []InterfaceStats) string {
	if len(stats) == 0 {
		return "(no interfaces)"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-12s %-4s %-4s %-20s %10s  %10s  %8s  %8s  %4s  %4s  %4s  %4s\n",
		"Interface", "MTU", "Up", "MAC", "RecvBytes", "SndBytes", "RecvPkts", "SndPkts",
		"ErrIn", "ErrOut", "DropIn", "DropOut"))
	sb.WriteString(strings.Repeat("-", 110) + "\n")
	for _, s := range stats {
		mac := s.MAC
		if mac == "" {
			mac = "-"
		}
		sb.WriteString(fmt.Sprintf("%-12s %-4d %-4s %-20s %10d  %10d  %8d  %8d  %4d  %4d  %4d  %4d\n",
			s.Name, s.MTU, s.State, mac,
			s.BytesRecv, s.BytesSent, s.PacketsRecv, s.PacketsSent,
			s.ErrorsIn, s.ErrorsOut, s.DropsIn, s.DropsOut))
	}
	return sb.String()
}

func FormatIOAddresses(stats []InterfaceStats) string {
	if len(stats) == 0 {
		return "(no addresses)"
	}
	var sb strings.Builder
	for _, s := range stats {
		if len(s.Addrs) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("%-12s  %s\n", s.Name, strings.Join(s.Addrs, "  ")))
	}
	return sb.String()
}
