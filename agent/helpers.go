package agent

import (
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// toInt coerces a JSON-decoded numeric value (float64 / int / json.Number / or
// a numeric string like "8080") to int. Returns 0 for missing/non-numeric
// values — callers treat 0 as "unset".
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

// HumanSize renders a byte count as a compact human-readable string for tool
// result previews (e.g. "1.2 MiB"). Exported for the standalone scp command.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for d := n / unit; d >= unit; d /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// orQuerySuffix builds the "(查询: xxx)" tail for an empty recall result so the
// LLM can tell "nothing in memory" from "nothing matched this query".
func orQuerySuffix(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return ""
	}
	return "，查询: " + q
}

// cmdVersion returns the build version string ("vX.Y.Z" or "dev"). The version
// is injected at link time by cmd/Execute via the AGENT_NETX_VERSION env var.
func cmdVersion() string {
	v := os.Getenv("AGENT_NETX_VERSION")
	if v == "" {
		return "dev"
	}
	return v
}

// shortUUID returns 8 hex chars of a fresh random UUID, used as a display
// label for ephemeral things like the default session id shown in the header.
func shortUUID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	return fmt.Sprintf("%08x", b)
}

// printableLen counts the terminal display width of s, stripping ANSI escapes.
func printableLen(s string) int {
	inEsc := false
	n := 0
	for _, r := range s {
		if r == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		n++
	}
	return n
}
