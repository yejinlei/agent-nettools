package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

var colorEnabled = term.IsTerminal(int(os.Stdout.Fd()))

const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	Strike  = "\033[9m"
	Cyan    = "\033[36m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Red     = "\033[31m"
	Magenta = "\033[35m"
	White   = "\033[37m"
	ClearLn = "\033[2K"
)

func col(c, s string) string {
	if !colorEnabled {
		return s
	}
	return c + s + Reset
}

func RunTUI(ctx context.Context, cfg Config) error {
	mem := NewMemory(cfg.MemoryPath)
	ask := promptOrSilent()
	registry := NewRegistry(cfg, mem, ask)
	llm := NewLLM(cfg, registry.Defs())

	printHeader(cfg, mem)

	systemMsg := cfg.SystemPrompt
	if systemMsg == "" {
		systemMsg = DefaultSystemPrompt()
	}
	if mem.HasSSHHosts() {
		systemMsg += "\n\n已记住的 SSH 主机(可直接在 file_copy 用 alias 引用，无需再问用户): " +
			strings.Join(mem.sshAliases(), ", ")
	}
	messages := []Message{{Role: RoleSystem, Content: systemMsg}}

	reader := bufio.NewReader(os.Stdin)
	var turnCount, toolCount int
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line, err := promptInput(reader, "你")
		if err == io.EOF {
			printGoodbye(turnCount, toolCount)
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
			printGoodbye(turnCount, toolCount)
			return nil
		}

		turnCount++
		messages = append(messages, Message{Role: RoleUser, Content: line})

		thinkingDone := make(chan struct{})
		go func() {
			tick := time.NewTicker(80 * time.Millisecond)
			defer tick.Stop()
			frames := []string{"⠋", "⠕", "⠙", "⠘", "⠼", "⠴", "⠆", "⠇", "⠇", "⠏"}
			fmt.Print(col(Magenta, "  AI "))
			fmt.Print(col(Dim, "思考中 "))
			i := 0
			for {
				select {
				case <-tick.C:
					fmt.Print(col(Magenta, frames[i%len(frames)]))
					i++
				case <-thinkingDone:
					return
				}
			}
		}()

		assistant, err := llm.Complete(ctx, messages)
		close(thinkingDone)
		fmt.Printf("\r%s\r", ClearLn)

		if err != nil {
			fmt.Printf("  AI %s\n", col(Red, "⚠ "+err.Error()))
			messages = messages[:len(messages)-1]
			fmt.Println()
			continue
		}

		messages = append(messages, assistant)

		if len(assistant.ToolCalls) == 0 {
			if assistant.Content != "" {
				fmt.Printf("  AI %s\n", col(Cyan, "▸ ")+wrapLines(assistant.Content, 70))
			}
			fmt.Println()
			break
		}

		for _, tc := range assistant.ToolCalls {
			toolCount++
			args := ParseToolCallArgs(tc.Function.Arguments)
			argsStr := compactArgs(args)
			nameCol := col(Bold+Cyan, tc.Function.Name)
			argsCol := col(Dim, "("+argsStr+")")
			fmt.Printf("  %s%s %s\n", col(Yellow, "⚙"), nameCol, argsCol)

			result := registry.Call(ctx, tc.Function.Name, args)
			if result != "" {
				preview := result
				if len(preview) > 400 {
					preview = preview[:400] + "\n…(截断)"
				}
				for i, l := range strings.Split(preview, "\n") {
					prefix := col(Dim, "     └─")
					if i > 0 {
						prefix = col(Dim, "     │ ")
					}
					fmt.Printf("%s %s\n", prefix, l)
				}
			}
			messages = append(messages, Message{
				Role:       RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}
	panic("unreachable")
}

func printHeader(cfg Config, mem *Memory) {
	w := 60
	top := "╭" + strings.Repeat("─", w-2) + "╮"
	bot := "╰" + strings.Repeat("─", w-2) + "╯"

	fmt.Println()
	fmt.Println(col(Cyan, top))
	title := col(Bold+Yellow, "  agent-nettools") + col(Bold+White, "  LLM Agent")
	fmt.Println(col(White, "│")+" "+strings.TrimRight(title, " ")+" "+col(White, "│"))
	fmt.Println(col(White, "│  ") + col(Dim, "自然语言驱动 · 自动调用工具完成你的网络操作") + col(White, "  │"))
	fmt.Println(col(Cyan, bot))
	fmt.Printf("  "+col(Cyan, "model:")+" %s   "+col(Cyan, "·")+"   "+col(Cyan, "base:")+" %s\n",
		col(Bold+White, cfg.Model), col(Dim, shortBaseURL(cfg.BaseURL)))
	if mem.HasSSHHosts() {
		fmt.Printf("  "+col(Dim, "SSH 主机:")+" %s\n", col(White, strings.Join(mem.sshAliases(), ", ")))
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Println("  "+col(Yellow, "⚠")+" "+col(Dim, "非交互模式：需要人工输入的工具会返回错误而非阻塞"))
	}
	fmt.Println("  "+col(Dim, "退出: Ctrl-D / exit   ·   中断当前请求: Ctrl-C"))
}

func printGoodbye(turns, tools int) {
	fmt.Println()
	fmt.Println("  "+col(Dim, "── 再见 👋  ")+col(White, fmt.Sprintf("%d 轮对话 · %d 次工具调用", turns, tools))+col(Dim, " ──"))
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

func promptInput(r *bufio.Reader, who string) (string, error) {
	fmt.Print(col(Green, who+" > "))
	line, err := r.ReadString('\n')
	fmt.Println(strings.TrimRight(line, "\r\n"))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func wrapLines(s string, limit int) string {
	var out strings.Builder
	for _, line := range strings.Split(s, "\n") {
		for len(line) > limit {
			chop := limit
			idx := strings.LastIndex(line[:limit], " ")
			if idx >= limit/2 {
				chop = idx
			}
			out.WriteString(line[:chop])
			out.WriteRune('\n')
			line = line[chop:]
			if len(line) > 0 {
				line = strings.Repeat(" ", 6) + line
			}
		}
		if line != "" {
			out.WriteString(line)
			out.WriteRune('\n')
		}
	}
	return strings.TrimRight(out.String(), "\n")
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
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
