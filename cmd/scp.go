package cmd

import (
	"context"
	"fmt"
	"os"

	"agent-nettools/agent"

	"github.com/spf13/cobra"
)

// scpCmd is the standalone SSH/SFTP file-copy subcommand (the "non-TUI, tools
// run independently" mode). It shares the agent's low-level transfer code and
// the same persistent memory + HIL prompter as the TUI's file_copy tool, so a
// host remembered here is reused by the agent and vice-versa.
func scpCmd() *cobra.Command {
	var (
		action, alias, host, user, password, keyPath, src, dst string
		port                                                     int
	)
	cmd := &cobra.Command{
		Use:   "scp",
		Short: "通过 SSH/SFTP 上传/下载单个文件（独立运行）",
		Long: `通过 SSH/SFTP 上传或下载单个文件。

  # 上传本地文件到远程
  agent-nettools scp --action upload --alias prod --src ./app.log --dst /var/log/app.log
  agent-nettools scp --action upload --host 10.0.0.5 --user root --src f.bin --dst /tmp/f.bin

  # 下载远程文件到本地
  agent-nettools scp --action download --alias prod --src /etc/hosts --dst ./hosts.copy

首次连接某主机时缺用户名/密码/私钥会在终端交互询问，并记入记忆(~/.agent-nettools)，
之后用 --alias 即可免重复输入。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if action != "upload" && action != "download" {
				return fmt.Errorf("--action 必须是 upload 或 download")
			}
			if src == "" || dst == "" {
				return fmt.Errorf("--src 和 --dst 必填")
			}
			if alias == "" && host == "" {
				return fmt.Errorf("需要 --alias 或 --host")
			}

			mem := agent.NewMemory(agent.DefaultMemoryPath())
			ask := agent.PromptOrSilentForCmd()
			if ask == nil {
				fmt.Fprintln(os.Stderr, "⚠️ stdin 非 TTY：缺少的连接信息将无法交互询问，请通过 flag 全部提供。")
			}

			ctx := context.Background()
			h, err := agent.ResolveHost(ctx, alias, host, user, password, keyPath, port, mem, ask)
			if err != nil {
				return err
			}
			n, err := agent.FileTransfer(ctx, h, src, dst, action)
			if err != nil {
				return err
			}
			verb := "上传"
			if action == "download" {
				verb = "下载"
			}
			fmt.Printf("✅ %s完成: %s → %s (%s, 主机 %s@%s:%d)\n",
				verb, src, dst, agent.HumanSize(n), h.User, h.Host, agent.PortOf(h))
			if alias != "" {
				fmt.Printf("💡 已记住主机 %s，下次用 --alias %s 即可免输。\n", alias, alias)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&action, "action", "upload", "upload=本机→远程，download=远程→本机")
	cmd.Flags().StringVar(&alias, "alias", "", "主机别名（已记住的可只填这个）")
	cmd.Flags().StringVar(&host, "host", "", "主机名或 IP")
	cmd.Flags().IntVar(&port, "port", 0, "SSH 端口（默认 22）")
	cmd.Flags().StringVar(&user, "user", "", "登录用户")
	cmd.Flags().StringVar(&password, "password", "", "密码（不建议在命令行明文传，交互更安全）")
	cmd.Flags().StringVar(&keyPath, "key-path", "", "私钥文件路径")
	cmd.Flags().StringVar(&src, "src", "", "源文件路径")
	cmd.Flags().StringVar(&dst, "dst", "", "目标文件路径")
	return cmd
}
