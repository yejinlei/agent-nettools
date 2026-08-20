package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

const (
	SaveCursor    = "\033[7m"
	RestoreCursor = "\033[8m"
	ClearLn       = "\033[2K"
	ShowCursor    = "\033[?25h"
	HideCursor    = "\033[?25l"
)

var (
	termHeight = 24
	termWidth  = 80

	sHeaderBar  lipgloss.Style
	sTitle      lipgloss.Style
	sSubtitle   lipgloss.Style
	sStatusBar  lipgloss.Style
	sStatusKey  lipgloss.Style
	sStatusVal  lipgloss.Style
	sUserRail   lipgloss.Style
	sUserText   lipgloss.Style
	sAiRail     lipgloss.Style
	sAiText     lipgloss.Style
	sToolIcon   lipgloss.Style
	sToolName   lipgloss.Style
	sToolArgs   lipgloss.Style
	sToolResult lipgloss.Style
	sPrompt     lipgloss.Style
	sThinking   lipgloss.Style
	sError      lipgloss.Style
)

func init() {
	initStyles()
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		termHeight = h
		termWidth = w
	}
}

func initStyles() {
	cy := lipgloss.Color("39")
	gr := lipgloss.Color("46")
	ye := lipgloss.Color("226")
	mg := lipgloss.Color("213")
	rd := lipgloss.Color("196")
	wh := lipgloss.Color("252")
	dm := lipgloss.Color("245")

	sHeaderBar = lipgloss.NewStyle().Foreground(cy).Bold(true)
	sTitle = lipgloss.NewStyle().Foreground(ye).Bold(true)
	sSubtitle = lipgloss.NewStyle().Foreground(dm)
	sStatusBar = lipgloss.NewStyle().
		Foreground(dm).
		Background(cy).
		Padding(0, 1)
	sStatusKey = lipgloss.NewStyle().Foreground(cy).Bold(true)
	sStatusVal = lipgloss.NewStyle().Foreground(wh)
	sUserRail = lipgloss.NewStyle().Foreground(gr).Bold(true).Width(4)
	sAiRail = lipgloss.NewStyle().Foreground(cy).Bold(true).Width(4)
	sUserText = lipgloss.NewStyle().Foreground(wh)
	sAiText = lipgloss.NewStyle().Foreground(wh)
	sToolIcon = lipgloss.NewStyle().Foreground(ye).Bold(true)
	sToolName = lipgloss.NewStyle().Foreground(cy).Bold(true)
	sToolArgs = lipgloss.NewStyle().Foreground(dm)
	sToolResult = lipgloss.NewStyle().Foreground(dm)
	sPrompt = lipgloss.NewStyle().Foreground(gr).Bold(true)
	sThinking = lipgloss.NewStyle().Foreground(mg)
	sError = lipgloss.NewStyle().Foreground(rd)
}

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
}

func newTUI(cfg Config) *tui {
	mem := NewMemory(cfg.MemoryPath)
	ask := promptOrSilent()
	registry := NewRegistry(cfg, mem, ask)
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
	t.renderHeader()

	rawMode := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if rawMode {
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			rawMode = false
		} else {
			defer func() { term.Restore(int(os.Stdin.Fd()), oldState) }()
			fmt.Print(HideCursor)
			defer fmt.Print(ShowCursor)
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
			t.renderGoodbye()
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
			t.renderGoodbye()
			return nil
		}

		t.history = append(t.history, line)
		t.histIdx = len(t.history)
		t.turns++
		t.msgs = append(t.msgs, Message{Role: RoleUser, Content: line})
		t.renderUserLine(line)

		assistant, err := t.thinkLoop(ctx, rawMode)
		if err != nil {
			fmt.Println(t.renderError("⚠ " + err.Error()))
			t.msgs = t.msgs[:len(t.msgs)-1]
			t.renderPrompt("")
			t.renderStatusBar()
			continue
		}
		t.msgs = append(t.msgs, assistant)

		if len(assistant.ToolCalls) == 0 {
			if assistant.Content != "" {
				t.renderAILine(assistant.Content)
			}
			t.renderPrompt("")
			t.renderStatusBar()
			continue
		}

		for _, tc := range assistant.ToolCalls {
			t.tools++
			args := ParseToolCallArgs(tc.Function.Arguments)
			argsStr := compactArgs(args)
			fmt.Println("  " + t.renderToolCall(tc.Function.Name, argsStr))
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
	panic("unreachable")
}

func (t *tui) renderHeader() {
	w := termWidth - 2
	if w < 40 {
		w = 40
	}
	bar := strings.Repeat("▬", w)
	fmt.Println()
	fmt.Println(sHeaderBar.Render(bar))
	fmt.Println(sTitle.Render("  agent-nettools") + " " + sSubtitle.Render("自然语言驱动 · 自动调用工具完成你的网络操作"))
	fmt.Println(sHeaderBar.Render(bar))
	fmt.Println()
}

func (t *tui) renderStatusBar() {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	parts := []string{
		sStatusKey.Render("model") + ":" + sStatusVal.Render(t.cfg.Model),
		sStatusKey.Render("base") + ":" + sStatusVal.Render(shortBaseURL(t.cfg.BaseURL)),
	}
	if t.mem.HasSSHHosts() {
		hosts := strings.Join(t.mem.sshAliases(), ",")
		if len(hosts) > 30 {
			hosts = hosts[:28] + "…"
		}
		parts = append(parts, sStatusKey.Render("ssh") + ":" + sStatusVal.Render(hosts))
	}
	statusText := strings.Join(parts, "   ·   ")
	bar := sStatusBar.Render(" " + statusText + " ")
	for lipgloss.Width(bar) < termWidth {
		bar += " "
	}
	fmt.Printf("\033[%d;1H", termHeight)
	fmt.Printf("\033[1A")
	fmt.Print(bar + "\n")
}

func (t *tui) renderUserLine(line string) {
	fmt.Println()
	fmt.Println(sUserRail.Render("你  ") + sUserText.Render(line))
}

func (t *tui) renderAILine(content string) {
	for _, l := range wrapLines(content, termWidth-12) {
		fmt.Println(sAiRail.Render("AI  ") + sAiText.Render(l))
	}
	fmt.Println()
}

func (t *tui) renderToolCall(name, args string) string {
	if args == "" {
		return sToolIcon.Render("⚙ ") + sToolName.Render(name)
	}
	return sToolIcon.Render("⚙ ") + sToolName.Render(name) + sToolArgs.Render("("+args+")")
}

func (t *tui) renderToolResult(result string) {
	preview := result
	if len(preview) > 400 {
		preview = preview[:400] + "\n…(截断)"
	}
	for i, l := range strings.Split(preview, "\n") {
		prefix := "     └─"
		if i > 0 {
			prefix = "     │ "
		}
		fmt.Println(sToolResult.Render(prefix + " " + l))
	}
}

func (t *tui) renderError(s string) string {
	return "  AI " + sError.Render(s)
}

func (t *tui) renderPrompt(line string) {
	fmt.Print(sPrompt.Render("你 > ") + line)
}

func (t *tui) renderGoodbye() {
	fmt.Println()
	fmt.Println(sSubtitle.Render("  ── 再见 👋  ") +
		sStatusVal.Render(fmt.Sprintf("%d 轮对话 · %d 次工具调用", t.turns, t.tools)) +
		sSubtitle.Render(" ──"))
}

func (t *tui) readLine(rawMode bool) (string, error) {
	t.renderPrompt("")
	if !rawMode {
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

	var buf []byte
loop:
	for {
		b := make([]byte, 1)
		n, err := os.Stdin.Read(b)
		if n == 0 {
			if err != nil {
				return "", err
			}
			continue
		}
		ch := b[0]
		switch ch {
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
				buf = buf[:len(buf)-1]
				fmt.Printf("\r%s%s", ClearLn, sPrompt.Render("你 > ")+string(buf)+" ")
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
					fmt.Printf("\r%s%s", ClearLn, sPrompt.Render("你 > ")+string(buf))
				}
			case 'B':
				if t.histIdx < len(t.history)-1 {
					t.histIdx++
					buf = []byte(t.history[t.histIdx])
					fmt.Printf("\r%s%s", ClearLn, sPrompt.Render("你 > ")+string(buf))
				}
			case 'D':
				if len(buf) == 0 {
					return "", io.EOF
				}
				buf = buf[:len(buf)-1]
				fmt.Printf("\r%s%s", ClearLn, sPrompt.Render("你 > ")+string(buf)+" ")
			case 'H':
				if len(buf) > 0 {
					buf = buf[:len(buf)-1]
					fmt.Printf("\r%s%s", ClearLn, sPrompt.Render("你 > ")+string(buf)+" ")
				}
			}
		default:
			if ch >= 32 {
				buf = append(buf, ch)
				fmt.Print(string(ch))
			}
		}
	}
	fmt.Println()
	return string(buf), nil
}

func (t *tui) thinkLoop(ctx context.Context, rawMode bool) (Message, error) {
	if !rawMode {
		fmt.Print(sThinking.Render("  AI 思考中 ..."))
		msg, err := t.llm.Complete(ctx, t.msgs)
		fmt.Printf("\r%s\r", ClearLn)
		return msg, err
	}

	done := make(chan struct{})
	frames := []string{"⠋", "⠕", "⠙", "⠘", "⠼", "⠴", "⠆", "⠇", "⠇", "⠏"}
	go func() {
		ticker := time.NewTicker(90 * time.Millisecond)
		defer ticker.Stop()
		fmt.Print(SaveCursor + sThinking.Render("  AI 思考中 "))
		i := 0
		for {
			select {
			case <-ticker.C:
				fmt.Print(sThinking.Render(frames[i%len(frames)]))
				i++
			case <-done:
				return
			}
		}
	}()

	msg, err := t.llm.Complete(ctx, t.msgs)
	close(done)
	fmt.Printf("\033[8m\r%s\r", ClearLn)
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
			idx := strings.LastIndex(line[:limit], " ")
			if idx >= limit/2 {
				chop = idx
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
		if !first {
			sb.WriteString(", ")
		}
		first = false
		sb.WriteString(k)
		sb.WriteString("=")
		fmt.Fprintf(&sb, "%v", v)
	}
	return sb.String()
}