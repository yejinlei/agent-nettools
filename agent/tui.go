package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	ClearLn        = "\033[2K"
	ShowCursor     = "\033[?25h"
	HideCursor     = "\033[?25l"
	SaveCursor     = "\033[7m"
	RestoreCursor = "\033[8m"
)

var (
	termWidth  = 80
	termHeight = 24

	cCyan   = "\033[36m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cRed    = "\033[31m"
	cDim    = "\033[2m"
	cWhite  = "\033[37m"
	cReset  = "\033[0m"
	cBold   = "\033[1m"
)

type tui struct {
	cfg      Config
	mem      *Memory
	registry *Registry
	llm      *LLM
	msgs     []Message

	history  []string
	histIdx  int
	turns    int
	tools    int
	context  int
}

func newTUI(cfg Config) *tui {
	mem := NewMemory(cfg.MemoryPath)
	registry := NewRegistry(cfg, mem, promptOrSilent())
	llm := NewLLM(cfg, registry.Defs())
	systemMsg := cfg.SystemPrompt
	if systemMsg == "" {
		systemMsg = DefaultSystemPrompt()
	}
	if mem.HasSSHHosts() {
		systemMsg += "\n\n" + "已记住的 SSH 主机(可直接在 file_copy 用 alias 引用，无需再问用户): " +
			strings.Join(mem.sshAliases(), ", ")
	}
	return &tui{
		cfg:      cfg,
		mem:      mem,
		registry: registry,
		llm:      llm,
		msgs:     []Message{{Role: RoleSystem, Content: systemMsg}},
	}
}

func (t *tui) run(ctx context.Context) error {
	t.renderInfoBox()
	t.renderSuggestion()

	rawMode := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if rawMode {
		if st, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
			defer func() { term.Restore(int(os.Stdin.Fd()), st) }()
			fmt.Print(HideCursor)
			defer fmt.Print(ShowCursor)
		} else {
			rawMode = false
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line, err := t.readLine(rawMode)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" || line == "q" {
			return nil
		}

		t.history = append(t.history, line)
		t.histIdx = len(t.history)
		t.turns++
		t.renderUserLine(line)
		t.msgs = append(t.msgs, Message{Role: RoleUser, Content: line})
		t.updateContext()

		assistant, err := t.thinkLoop(ctx, rawMode)
		if err != nil {
			fmt.Printf("  %s⚠ %s%s\n", cRed, err.Error(), cReset)
			t.msgs = t.msgs[:len(t.msgs)-1]
			t.renderPrompt("")
			continue
		}
		t.msgs = append(t.msgs, assistant)
		t.updateContext()

		if len(assistant.ToolCalls) == 0 {
			if assistant.Content != "" {
				t.renderAILine(assistant.Content)
			}
			t.renderPrompt("")
			continue
		}

		for _, tc := range assistant.ToolCalls {
			t.tools++
			args := ParseToolCallArgs(tc.Function.Arguments)
			argsStr := compactArgs(args)
			fmt.Printf("  %s⚙ %s%s", cYellow, cCyan, tc.Function.Name)
			if argsStr != "" {
				fmt.Printf("%s(%s)%s", cDim, argsStr, cReset)
			}
			fmt.Println(cReset)
			result := t.registry.Call(ctx, tc.Function.Name, args)
			if result != "" {
				t.renderToolResult(result)
			}
			t.msgs = append(t.msgs, Message{
				Role:       RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}
}

func (t *tui) updateContext() {
	t.context = 0
	for _, m := range t.msgs {
		t.context += len(m.Content)
		for _, tc := range m.ToolCalls {
			t.context += len(tc.Function.Arguments)
		}
	}
}

func (t *tui) renderInfoBox() {
	fmt.Println()
	t.box("  " + cCyan + "Version:" + cReset + "   " + fmt.Sprintf("%-40s", cmdVersion()))
	fmt.Println()
}

func cmdVersion() string {
	v, ok := os.LookupEnv("AGENT_NETX_VERSION")
	if ok && v != "" {
		return v
	}
	return "(dev)"
}

func (t *tui) renderSuggestion() {
	fmt.Println(cYellow + " ✦ Try Kimi Code Web UI" + cReset)
	fmt.Println(cDim + "    Clearer task progress, visual sessions & settings management." + cReset)
	fmt.Println(cDim + "    Run /help to see available commands." + cReset)
	fmt.Println()
}

func (t *tui) renderUserLine(line string) {
	fmt.Printf("\n  %s✨ %s%s%s\n", cYellow, cReset, cWhite, line)
}

func (t *tui) renderAILine(content string) {
	for _, l := range wrapLines(content, termWidth-12) {
		fmt.Printf("  %s● %s%s\n", cCyan, cWhite, l)
	}
	fmt.Println()
}

func (t *tui) renderToolResult(result string) {
	preview := result
	if len(preview) > 400 {
		preview = preview[:400] + "\n…(截断)"
	}
	for i, l := range strings.Split(preview, "\n") {
		prefix := "      └─"
		if i > 0 {
			prefix = "      │ "
		}
		fmt.Printf("  %s%s %s\n", cDim, prefix, l)
	}
}

func (t *tui) renderPrompt(line string) {
	w := termWidth
	bar := strings.Repeat("─", w-2)
	top := "╭" + bar + "╮"
	prompt := "│ > " + line
	pad := w - 2 - printableLen(prompt)
	if pad < 0 {
		pad = 0
	}
	prompt += strings.Repeat(" ", pad) + "│"
	bot := "╰" + bar + "╯"
	status := t.statusLine(w)
	pad2 := w - printableLen(status)
	if pad2 < 0 {
		pad2 = 0
	}
	status += strings.Repeat(" ", pad2)

	fmt.Println()
	fmt.Println(top)
	fmt.Print(prompt)
	fmt.Println()
	fmt.Println(bot)
	fmt.Print(status)
}

func (t *tui) statusLine(w int) string {
	hosts := ""
	if t.mem.HasSSHHosts() {
		hosts = " · SSH: " + strings.Join(t.mem.sshAliases(), ",")
		if len(hosts) > 40 {
			hosts = hosts[:38] + "…"
		}
	}
	left := fmt.Sprintf("%s%s%s  %s%s%s%s  %s%s%s",
		cCyan, cBold, shortBaseURL(t.cfg.BaseURL),
		cReset, cBold, "v"+cmdVersion(), cReset,
		cDim, "context: "+fmt.Sprintf("%d%%", pct(t.context)), cReset)
	left += hosts
	return left
}

func pct(ctx int) int {
	if ctx == 0 {
		return 0
	}
	p := ctx * 100 / 131072
	if p > 100 {
		return 100
	}
	return p
}

func (t *tui) box(s string) {
	w := termWidth
	if w < 40 {
		w = 40
	}
	bar := strings.Repeat("─", w-2)
	fmt.Println("╭" + bar + "╮")
	fmt.Print("│ " + s)
	pad := w - 2 - printableLen(s)
	if pad < 0 {
		pad = 0
	}
	fmt.Println(strings.Repeat(" ", pad) + "│")
	fmt.Println("╰" + bar + "╯")
}

func (t *tui) readLine(rawMode bool) (string, error) {
	if !rawMode {
		return t.readLineBuffered()
	}
	t.renderPrompt("")

	var buf []byte
loop:
	for {
		rb, err := readUtf8Rune(os.Stdin)
		if err != nil {
			return "", err
		}
		switch rb[0] {
		case 13:
			break loop
		case 10:
			continue
		case 4:
			return "", io.EOF
		case 3:
			return "", fmt.Errorf("cancelled")
		case 127, 8:
			if len(buf) > 0 {
				_, sz := utf8.DecodeLastRune(buf)
				buf = buf[:len(buf)-sz]
				t.redrawPrompt(buf)
			}
		case 27:
			inner := make([]byte, 3)
			n2, _ := os.Stdin.Read(inner)
			if n2 == 0 || inner[0] != '[' {
				continue
			}
			key := inner[1]
			if n2 >= 3 && key == 'O' {
				key = inner[2]
			}
			switch key {
			case 'A':
				if len(t.history) > 0 && t.histIdx > 0 {
					t.histIdx--
					buf = []byte(t.history[t.histIdx])
					t.redrawPrompt(buf)
				}
			case 'B':
				if t.histIdx < len(t.history)-1 {
					t.histIdx++
					buf = []byte(t.history[t.histIdx])
					t.redrawPrompt(buf)
				}
			case 'D':
				if len(buf) == 0 {
					return "", io.EOF
				}
				_, sz := utf8.DecodeLastRune(buf)
				buf = buf[:len(buf)-sz]
				t.redrawPrompt(buf)
			case 'H':
				if len(buf) > 0 {
					_, sz := utf8.DecodeLastRune(buf)
					buf = buf[:len(buf)-sz]
					t.redrawPrompt(buf)
				}
			}
		default:
			if rb[0] >= 32 {
				buf = append(buf, rb...)
				t.redrawPrompt(buf)
			}
		}
	}
	fmt.Println()
	return string(buf), nil
}

func (t *tui) redrawPrompt(buf []byte) {
	fmt.Printf("\033[%dA", 4)
	t.renderPrompt(string(buf))
}

func (t *tui) readLineBuffered() (string, error) {
	var buf strings.Builder
	tmp := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(tmp)
		if n > 0 {
			buf.WriteString(string(tmp[:n]))
		}
		if err != nil {
			break
		}
		if strings.Contains(buf.String(), "\n") {
			break
		}
	}
	s := buf.String()
	line, _, _ := strings.Cut(s, "\n")
	if line != "" || s == "" {
		return strings.TrimRight(line, "\r\n"), nil
	}
	return "", io.EOF
}

func (t *tui) thinkLoop(ctx context.Context, rawMode bool) (Message, error) {
	if !rawMode {
		fmt.Print(cDim + "  思考中 ...")
		msg, err := t.llm.Complete(ctx, t.msgs)
		fmt.Printf("\r%s\r", ClearLn)
		return msg, err
	}

	done := make(chan struct{})
	frames := []string{"⠋", "⠕", "⠙", "⠘", "⠼", "⠴", "⠆", "⠇"}
	go func() {
		ticker := time.NewTicker(90 * time.Millisecond)
		defer ticker.Stop()
		fmt.Print(SaveCursor + cDim + "  思考中 ")
		i := 0
		for {
			select {
			case <-ticker.C:
				fmt.Print(frames[i%len(frames)])
				i++
			case <-done:
				return
			}
		}
	}()

	msg, err := t.llm.Complete(ctx, t.msgs)
	close(done)
	fmt.Printf(RestoreCursor + "\r%s\r", ClearLn)
	return msg, err
}

func RunTUI(ctx context.Context, cfg Config) error {
	t := newTUI(cfg)
	return t.run(ctx)
}

func shortBaseURL(s string) string {
	if s == "" {
		return ""
	}
	u := strings.TrimPrefix(s, "https://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimSuffix(u, "/v1")
	u = strings.TrimSuffix(u, "/")
	if len(u) > 28 {
		u = u[:25] + "…"
	}
	return u
}

func wrapLines(s string, limit int) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for len(line) > limit {
			chop := limit
			index := strings.LastIndex(line[:limit], " ")
			if index >= limit/2 {
				chop = index
			}
			out = append(out, line[:chop])
			line = line[chop:]
			if len(line) > 0 {
				line = strings.Repeat(" ", 6) + line
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func compactArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	var sb strings.Builder
	first := true
	for k, v := range args {
		if first {
		} else {
			sb.WriteString(", ")
		}
		first = false
		sb.WriteString(k)
		sb.WriteString("=")
		fmt.Fprintf(&sb, "%v", v)
	}
	return sb.String()
}

func readUtf8Rune(r io.Reader) ([]byte, error) {
	first := make([]byte, 1)
	if _, err := r.Read(first); err != nil {
		return nil, err
	}
	b := first[0]
	switch {
	case b < 0x80:
		return first, nil
	case b < 0xE0:
		return readUtf8Tail(r, first, 1)
	case b < 0xF0:
		return readUtf8Tail(r, first, 2)
	default:
		return readUtf8Tail(r, first, 3)
	}
}

func readUtf8Tail(r io.Reader, prefix []byte, n int) ([]byte, error) {
	tail := make([]byte, n)
	if _, err := r.Read(tail); err != nil {
		return prefix, err
	}
	return append(prefix, tail...), nil
}

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