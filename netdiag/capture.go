package netdiag

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// Packet represents a captured network packet.
type Packet struct {
	Timestamp time.Time
	Proto     string
	SrcIP     string
	DstIP     string
	SrcPort   int
	DstPort   int
	Length    int
	Info      string
}

// CaptureOpts configures packet capture behavior.
type CaptureOpts struct {
	Proto   string
	Port    int
	Src     string
	Dst     string
	Addr    string
	Count   int
	Timeout int
}

// CapturePackets opens raw sockets and captures packets matching opts.
// Requires root/admin privileges on most systems.
func CapturePackets(opts CaptureOpts) ([]Packet, error) {
	count := opts.Count
	if count <= 0 {
		count = 50
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10
	}
	addr := opts.Addr
	if addr == "" {
		addr = "0.0.0.0"
	}

	protos := []string{"tcp", "udp"}
	if opts.Proto != "all" && opts.Proto != "" {
		protos = []string{opts.Proto}
	}

	type reader struct {
		conn  net.PacketConn
		proto string
	}
	var readers []reader
	var openErrs []string
	for _, p := range protos {
		conn, err := net.ListenPacket("ip4:"+p, addr)
		if err != nil {
			openErrs = append(openErrs, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		readers = append(readers, reader{conn: conn, proto: p})
	}

	if len(readers) == 0 {
		return nil, fmt.Errorf("无法打开原始套接字 (%s), 请以管理员/root 身份运行",
			strings.Join(openErrs, "; "))
	}

	defer func() {
		for _, r := range readers {
			_ = r.conn.Close()
		}
	}()

	packets := make(chan Packet, count)
	done := make(chan struct{})

	for _, r := range readers {
		go readPackets(r.conn, r.proto, opts, packets, done)
	}

	results := make([]Packet, 0, count)
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	for {
		select {
		case pkt := <-packets:
			results = append(results, pkt)
			if len(results) >= count {
				close(done)
				return results, nil
			}
		case <-timer.C:
			close(done)
			return results, nil
		}
	}
}

func readPackets(conn net.PacketConn, proto string, opts CaptureOpts, out chan<- Packet, done chan struct{}) {
	buf := make([]byte, 65535)
	for {
		conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			select {
			case <-done:
				return
			default:
				continue
			}
		}
		pkt, err := parsePacket(buf[:n])
		if err != nil {
			continue
		}
		if !matchesFilter(&pkt, opts) {
			continue
		}
		pkt.Timestamp = time.Now()
		select {
		case out <- pkt:
		case <-done:
			return
		}
	}
}

func matchesFilter(pkt *Packet, opts CaptureOpts) bool {
	if opts.Port > 0 && pkt.SrcPort != opts.Port && pkt.DstPort != opts.Port {
		return false
	}
	if opts.Src != "" && pkt.SrcIP != opts.Src {
		return false
	}
	if opts.Dst != "" && pkt.DstIP != opts.Dst {
		return false
	}
	return true
}

func parsePacket(data []byte) (Packet, error) {
	if len(data) < 20 {
		return Packet{}, fmt.Errorf("too short")
	}

	version := data[0] >> 4
	var pkt Packet

	if version == 4 {
		ihl := int(data[0]&0x0f) * 4
		if ihl < 20 {
			return Packet{}, fmt.Errorf("bad IHL")
		}
		totalLen := int(data[2])<<8 | int(data[3])
		proto := data[9]
		pkt.SrcIP = net.IP(data[12:16]).String()
		pkt.DstIP = net.IP(data[16:20]).String()
		pkt.Length = totalLen

		switch proto {
		case 6:
			if len(data) < ihl+20 {
				return Packet{}, fmt.Errorf("short tcp")
			}
			pkt.SrcPort = int(data[ihl])<<8 | int(data[ihl+1])
			pkt.DstPort = int(data[ihl+2])<<8 | int(data[ihl+3])
			pkt.Proto = "tcp"
			flags := data[ihl+13] >> 4 & 0x0f
			pkt.Info = tcpFlags(flags)
		case 17:
			if len(data) < ihl+8 {
				return Packet{}, fmt.Errorf("short udp")
			}
			pkt.SrcPort = int(data[ihl])<<8 | int(data[ihl+1])
			pkt.DstPort = int(data[ihl+2])<<8 | int(data[ihl+3])
			pkt.Proto = "udp"
		case 1:
			pkt.Proto = "icmp"
			if len(data) >= ihl+2 {
				pkt.Info = fmt.Sprintf("type=%d code=%d", data[ihl], data[ihl+1])
			}
		default:
			pkt.Proto = fmt.Sprintf("proto%d", proto)
		}
	} else if version == 6 {
		if len(data) < 40 {
			return Packet{}, fmt.Errorf("short ipv6")
		}
		nextHdr := data[6]
		payloadLen := int(data[4])<<8 | int(data[5])
		pkt.SrcIP = net.IP(data[8:24]).String()
		pkt.DstIP = net.IP(data[24:40]).String()
		pkt.Length = payloadLen + 40

		switch nextHdr {
		case 6:
			if len(data) < 60 {
				return Packet{}, fmt.Errorf("short tcp")
			}
			pkt.SrcPort = int(data[40])<<8 | int(data[41])
			pkt.DstPort = int(data[42])<<8 | int(data[43])
			pkt.Proto = "tcp"
			flags := data[53] >> 4 & 0x0f
			pkt.Info = tcpFlags(flags)
		case 17:
			if len(data) < 48 {
				return Packet{}, fmt.Errorf("short udp")
			}
			pkt.SrcPort = int(data[40])<<8 | int(data[41])
			pkt.DstPort = int(data[42])<<8 | int(data[43])
			pkt.Proto = "udp"
		case 58:
			pkt.Proto = "icmpv6"
			if len(data) >= 42 {
				pkt.Info = fmt.Sprintf("type=%d code=%d", data[40], data[41])
			}
		default:
			pkt.Proto = fmt.Sprintf("proto%d", nextHdr)
		}
	} else {
		return Packet{}, fmt.Errorf("ip%d", version)
	}

	return pkt, nil
}

func tcpFlags(flags byte) string {
	var parts []string
	if flags&0x02 != 0 {
		parts = append(parts, "SYN")
	}
	if flags&0x01 != 0 {
		parts = append(parts, "ACK")
	}
	if flags&0x08 != 0 {
		parts = append(parts, "FIN")
	}
	if flags&0x04 != 0 {
		parts = append(parts, "RST")
	}
	if flags&0x10 != 0 {
		parts = append(parts, "PSH")
	}
	if flags&0x20 != 0 {
		parts = append(parts, "URG")
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// FormatPackets formats captured packets as a readable table.
func FormatPackets(pkts []Packet) string {
	if len(pkts) == 0 {
		return "(no packets captured)"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-12s %-6s %-50s %5s  %s\n",
		"Time", "Proto", "Address", "Len", "Info"))
	sb.WriteString(strings.Repeat("-", 85) + "\n")
	for _, p := range pkts {
		addr := fmt.Sprintf("%s:%d -> %s:%d", p.SrcIP, p.SrcPort, p.DstIP, p.DstPort)
		sb.WriteString(fmt.Sprintf("%-12s %-6s %-50s %5d  %s\n",
			p.Timestamp.Format("15:04:05.000"), p.Proto, addr, p.Length, p.Info))
	}
	return sb.String()
}