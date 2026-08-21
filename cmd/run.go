package cmd

import (
	"context"
	"fmt"
	"os"

	"agent-netx/agent"

	"github.com/spf13/cobra"
)

// runCmd is the CLI companion to the agent's run_local / run_remote tools.
// "Local" shells out to cmd.exe /c or /bin/sh -c; "Remote" dials an SSH host
// (reusing scp's host resolution + memory + HIL) and runs a session.
func runCmd() *cobra.Command {
	var (
		action, alias, host, user, password, keyPath, command string
		port                                                  int
	)
	c := &cobra.Command{
		Use:   "run <local|remote>",
		Short: "本地/远端执行 shell 命令 (独立运行)",
		Long: `本地或远程执行一条 shell 命令。本地通过平台默认 shell(cmd.exe /c 或 /bin/sh -c)；
远端通过 SSH 连接到远端主机并开启 session 执行。

  # 本地执行
  agent-netx run local --cmd "agent-netx n2n -c config.yml"

  # 远端执行 (通过别名, 从记忆读取凭据)
  agent-netx run remote --alias prod --cmd "cd /opt/agent-netx && ./agent-netx start -c config.yml"

  # 远端执行 (直连)
  agent-netx run remote --host 10.0.0.5 --user root --password xxx --cmd "whoami && hostname"

远端首次连接时缺用户/密码/私钥会在终端交互询问, 并记入记忆(~/.agent-netx),
之后用 --alias 即可免重复输入。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("需要指定 local 或 remote")
			}
			action = args[0]
			if action != "local" && action != "remote" {
				return fmt.Errorf("action 必须是 local 或 remote, 不是 %q", action)
			}
			if command == "" {
				return fmt.Errorf("--cmd 必填")
			}

			ctx := context.Background()

			if action == "local" {
				if err := agent.RunLocal(ctx, command); err != nil {
					return err
				}
				fmt.Println("本地命令执行完成")
				return nil
			}

			if alias == "" && host == "" {
				return fmt.Errorf("需要 --alias 或 --host")
			}
			mem := agent.NewMemory(agent.DefaultMemoryPath())
			ask := agent.PromptOrSilentForCmd()
			if ask == nil {
				fmt.Fprintln(os.Stderr, "⚠️ stdin 非 TTY: 缺少的 SSH 连接信息将无法交互询问, 请通过 flag 全部提供。")
			}
			h, err := agent.ResolveHost(ctx, alias, host, user, password, keyPath, port, mem, ask)
			if err != nil {
				return err
			}
			if err := agent.RunRemote(ctx, h, command); err != nil {
				return err
			}
			aliasOrHost := alias
			if aliasOrHost == "" {
				aliasOrHost = host
			}
			fmt.Printf("✅ 远程命令执行完成 (host=%s, %s@%s:%d)\n",
				aliasOrHost, h.User, h.Host, agent.PortOf(h))
			if alias != "" {
				fmt.Printf("💡 已记住主机 %s, 下次用 --alias %s 即可免输。\n", alias, alias)
			}
			return nil
		},
	}
	c.Flags().StringVar(&alias, "alias", "", "主机别名 (远程)")
	c.Flags().StringVar(&host, "host", "", "主机名或 IP (远程)")
	c.Flags().IntVar(&port, "port", 0, "SSH 端口, 默认 22")
	c.Flags().StringVar(&user, "user", "", "登录用户")
	c.Flags().StringVar(&password, "password", "", "密码")
	c.Flags().StringVar(&keyPath, "key-path", "", "私钥文件路径")
	c.Flags().StringVar(&command, "cmd", "", "要执行的 shell 命令")
	return c
}