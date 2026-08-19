package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// askFunc prompts the human with question and returns their answer.
// In interactive TUI mode it reads from stdin (passwords hidden). In
// non-interactive mode (piped stdin, e.g. tests) it is a no-op that returns
// empty — so tools fail gracefully with "no HIL prompter" instead of blocking.
type askFunc func(ctx context.Context, question string) string

// interactiveAsk returns an askFunc that reads from the terminal. Password
// prompts (detected by the 🔒 marker or "password/密码" wording) use
// term.ReadPassword so the secret isn't echoed. Returns nil when stdin is not
// a TTY, which makes resolveHost fall back to its error path instead of
// hanging on a read.
func interactiveAsk() askFunc {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	return func(ctx context.Context, question string) string {
		fmt.Print(question)
		if isPasswordPrompt(question) {
			b, err := term.ReadPassword(fd)
			fmt.Println()
			if err != nil {
				return ""
			}
			return string(b)
		}
		var line string
		_, err := fmt.Fscanln(os.Stdin, &line)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(line)
	}
}

// pwMarker prefixes a question so interactiveAsk reads it as a hidden password.
const pwMarker = "🔒:"

func isPasswordPrompt(q string) bool {
	low := strings.ToLower(q)
	return strings.HasPrefix(q, pwMarker) || strings.Contains(low, "密码") || strings.Contains(low, "password")
}

// askPassword builds a hidden-password prompt and returns the typed value.
func askPassword(ctx context.Context, ask askFunc) string {
	if ask == nil {
		return ""
	}
	return ask(ctx, pwMarker+" SSH 密码: ")
}

// silentAsk is a no-op askFunc for non-interactive contexts. It always returns
// empty, which causes resolveHost to surface a clear "no HIL prompter" error
// rather than blocking forever on a read from a pipe.
func silentAsk() askFunc {
	return func(ctx context.Context, question string) string {
		return ""
	}
}

// promptOrSilent returns an interactive prompter when stdin is a TTY, else a
// silent one. This is the single chokepoint the TUI uses to decide HIL.
func promptOrSilent() askFunc {
	if a := interactiveAsk(); a != nil {
		return a
	}
	return silentAsk()
}

// PromptOrSilentForCmd is the exported wrapper the standalone `scp` subcommand
// uses to obtain the same HIL prompter the TUI uses. Returns nil when stdin is
// not a TTY (non-interactive), so the caller can warn and fall back to flags.
func PromptOrSilentForCmd() askFunc {
	return interactiveAsk() // nil if not a TTY; scp.go handles nil explicitly
}
