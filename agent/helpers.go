package agent

import (
	"fmt"
	"strings"
)

// toInt coerces a JSON-decoded numeric value (float64 / int / json.Number) to
// int. Returns 0 for missing/non-numeric values — callers treat 0 as "unset".
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
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
