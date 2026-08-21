package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Version is injected at build time via ldflags:
//   go build -ldflags "-X agent-netx/cmd.Version=v0.2.3"
var Version = "dev"

// checkUpdate prints a boxed update banner (to stderr) when the latest GitHub
// release tag is newer than Version. Runs once at startup from Execute().
func checkUpdate() {
	if Version == "" || Version == "dev" {
		return
	}
	latest, err := latestRelease()
	if err != nil {
		return // offline or transient — skip
	}
	if latest == Version {
		return
	}
	c, err := cmpSemver(Version, latest)
	if err != nil || c >= 0 {
		return // up to date
	}
	const w = 72
	bar := strings.Repeat("─", w) // ─
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "╭%s╮\n", bar)        // ╭ ... ╮
	msg := fmt.Sprintf("  \033[33m\033[1magent-netx %s is available (you have %s)\033[0m", latest, Version)
	pad := w - printableLen(msg)
	if pad < 0 {
		pad = 0
	}
	fmt.Fprintf(os.Stderr, "│%s%s│\n", msg, strings.Repeat(" ", pad)) // │ ... │
	fmt.Fprintf(os.Stderr, "╨%s╩\n", bar)        // ╰ ... ╯
	fmt.Fprintln(os.Stderr)
	installURL := "https://github.com/yejinlei/agent-netx/releases/latest/download"
	fmt.Fprintf(os.Stderr, "  \033[1mTo update manually, run:\033[0m\n")
	fmt.Fprintf(os.Stderr, "    PowerShell:  irm %s/install.ps1 | iex\n", installURL)
	fmt.Fprintf(os.Stderr, "    Bash:        curl -fsSL %s/install.sh | sh\n", installURL)
	fmt.Fprintln(os.Stderr)
}

type ghRelease struct {
	TagName string `json:"tag_name"`
}

func latestRelease() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/yejinlei/agent-netx/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	var r ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	return r.TagName, nil
}

// cmpSemver compares two tags like "v0.2.2" → returns -1 / 0 / +1.
func cmpSemver(a, b string) (int, error) {
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		an, err := strconv.Atoi(ap[i])
		if err != nil {
			an = 0
		}
		bn, err := strconv.Atoi(bp[i])
		if err != nil {
			bn = 0
		}
		if an != bn {
			if an < bn {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
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