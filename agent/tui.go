package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// RunTUI starts the interactive natural-language agent REPL.
// The user types a request in plain language; the LLM decides which tools to
// call to accomplish it, executes them, and replies in natural language.
func RunTUI(ctx context.Context, cfg Config) error {
	mem := NewMemory(cfg.MemoryPath)
	ask := promptOrSilent()
	registry := NewRegistry(cfg, mem, ask)
	llm := NewLLM(cfg, registry.Defs())

	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║        agent-nettools · LLM Agent 模式                 ║")
	fmt.Println("╠══════════════════════════════════════════════════════════╣")
	fmt.Println("║  用自然语言描述你想做的事，我会调用工具完成。         ║")
	fmt.Println("║  例: \"把 google 走 ss-1\" / \"测一下所有代理延迟\"        ║")
	fmt.Println("║  新增: SSH 文件传输(上传/下载) / 记忆 / 人工介入(HIL) ║")
	fmt.Println("║  Ctrl-D 退出 · Ctrl-C 中断当前请求                    ║")
	fmt.Printf("╚══════════════════════════════════════════════════════════╝\n")
	fmt.Printf("model: %s  base: %s\n", cfg.Model, cfg.BaseURL)
	if ask == nil {
		fmt.Println("⚠️ 非交互模式(stdin 非 TTY):需要人工输入的工具会返回错误而非阻塞。")
	}
	if mem.HasSSHHosts() {
		fmt.Printf("💡 已记住的 SSH 主机: %s\n", strings.Join(mem.sshAliases(), ", "))
	}

	systemMsg := cfg.SystemPrompt
	if systemMsg == "" {
		systemMsg = DefaultSystemPrompt()
	}
	// Prime the conversation with remembered SSH hosts so the LLM can reuse
	// them without first calling recall — fewer round-trips, better UX.
	if mem.HasSSHHosts() {
		systemMsg += "\n\n已记住的 SSH 主机(可直接在 file_copy 用 alias 引用，无需再问用户): " +
			strings.Join(mem.sshAliases(), ", ")
	}
	messages := []Message{
		{Role: RoleSystem, Content: systemMsg},
	}

	stdinFd := int(os.Stdin.Fd())
	isTerm := term.IsTerminal(stdinFd)
	_ = isTerm

	reader := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		fmt.Print("\n你> ")
		line, err := readLine(reader)
		if err == io.EOF {
			fmt.Println("\n再见 👋")
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
			fmt.Println("再见 👋")
			return nil
		}

		messages = append(messages, Message{Role: RoleUser, Content: line})

		turns := 0
		const maxTurns = 8
		for {
			turns++
			if turns > maxTurns {
				fmt.Println("\n[超过最大工具调用轮数，已中止]")
				break
			}
			fmt.Print("AI> 思考中...")
			assistant, err := llm.Complete(ctx, messages)
			fmt.Print("\r          \r")
			if err != nil {
				fmt.Printf("AI> ⚠️ %s\n", err.Error())
				messages = messages[:len(messages)-1]
				break
			}

			messages = append(messages, assistant)

			if len(assistant.ToolCalls) == 0 {
				if assistant.Content != "" {
					fmt.Printf("AI> %s\n", assistant.Content)
				}
				break
			}

			for _, tc := range assistant.ToolCalls {
				args := ParseToolCallArgs(tc.Function.Arguments)
				fmt.Printf("  ⚙️ 调用工具 %s(%s)\n", tc.Function.Name, compactArgs(args))
				result := registry.Call(ctx, tc.Function.Name, args)
				preview := result
				if len(preview) > 300 {
					preview = preview[:300] + " …(截断)"
				}
				fmt.Printf("     ↳ %s\n", indent(preview))
				messages = append(messages, Message{
					Role:       RoleTool,
					Content:    result,
					ToolCallID: tc.ID,
				})
			}
		}

		if len(messages) > 40 {
			messages = append(messages[:1], messages[len(messages)-20:]...)
		}
	}
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

func indent(s string) string {
	return strings.ReplaceAll(s, "\n", "\n     ")
}
