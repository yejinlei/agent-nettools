package netdiag

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Route represents one row of the routing table.
type Route struct {
	Destination string
	Gateway     string
	Interface   string
	Metric      int
}

// GetRoutes parses the system routing table. Windows uses `route print -4`;
// other OSes fall back to `route -n`.
func GetRoutes() ([]Route, error) {
	if runtime.GOOS == "windows" {
		return routesWindows()
	}
	return routesCommand("route", "-n")
}

func routesWindows() ([]Route, error) {
	out, err := exec.Command("route", "print", "-4").CombinedOutput()
	if err != nil {
		return routesCommand("route", "-n")
	}
	return parseWinRoutes(string(out))
}

func parseWinRoutes(s string) ([]Route, error) {
	var routes []Route
	inTable := false
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "Interface List") || strings.HasPrefix(ln, "Persistent Routes") {
			break
		}
		if strings.Contains(ln, "IPv4 Route Table") {
			inTable = true
			continue
		}
		if !inTable || strings.Contains(ln, "List") || strings.Contains(ln, "---") {
			continue
		}
		fields := strings.Fields(ln)
		if len(fields) < 5 {
			continue
		}
		dst := fields[0]
		if dst == "0.0.0.0" {
			dst = "default"
		}
		m, _ := strconv.Atoi(fields[4])
		routes = append(routes, Route{Destination: dst, Gateway: fields[2], Interface: fields[3], Metric: m})
	}
	return routes, nil
}

func routesCommand(cmdName, arg string) ([]Route, error) {
	out, err := exec.Command(cmdName, arg).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("route %s: %w (%s)", arg, err, strings.TrimSpace(string(out)))
	}
	return parseRouteN(string(out))
}

func parseRouteN(s string) ([]Route, error) {
	var routes []Route
	for i, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if i < 2 {
			continue
		}
		fields := strings.Fields(ln)
		if len(fields) < 8 {
			continue
		}
		dst := fields[0]
		if dst == "0.0.0.0" {
			dst = "default"
		}
		m, _ := strconv.Atoi(fields[4])
		routes = append(routes, Route{Destination: dst, Gateway: fields[1], Interface: fields[7], Metric: m})
	}
	return routes, nil
}

// FormatRoutes renders routes as a table.
func FormatRoutes(routes []Route) string {
	if len(routes) == 0 {
		return "(no routes)"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-25s %-18s %-14s %6s\n", "Destination", "Gateway", "Interface", "Metric"))
	sb.WriteString(strings.Repeat("-", 70) + "\n")
	for _, r := range routes {
		sb.WriteString(fmt.Sprintf("%-25s %-18s %-14s %6d\n", r.Destination, r.Gateway, r.Interface, r.Metric))
	}
	return sb.String()
}