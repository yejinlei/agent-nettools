package cmd

import (
	"fmt"
	"strings"
	"time"

	"agent-netx/web"

	"github.com/spf13/cobra"
)

func logsCmd() *cobra.Command {
	var (
		tail   int
		level  string
		follow bool
		path   string
	)
	cmd := &cobra.Command{
		Use:   "logs [--tail N] [--level L] [--follow] [--path FILE]",
		Short: "查看运行时日志",
		Long: `查看运行时日志（agent-netx start 及各子服务会把日志写入 ~/.agent-netx/agent-netx.log）。

  # 最近 50 行 (默认 level=info)
  agent-netx logs --tail 50

  # 只看 WARN 和 ERROR
  agent-netx logs --level error --tail 100

  # 实时跟踪
  agent-netx logs --follow

  # 指定日志文件
  agent-netx logs --path /tmp/agent.log --tail 200`,
		RunE: func(cmd *cobra.Command, args []string) error {
			lvl := strings.ToLower(level)
			validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true, "all": true}
			if lvl != "" && !validLevels[lvl] {
				return fmt.Errorf("level 必须是 debug/info/warn/error/all")
			}

			if follow {
				tail = 0 // follow 模式下显示所有匹配日志
			}

			var lastIdx int
			poll := func() error {
				entries, err := web.ReadLogFile(path, tail-lastIdx, lvl)
				if err != nil {
					return err
				}
				// ReadLogFile does not persist a cursor, so we track by total count.
				// The tail request returns the LAST n entries; to show only NEW ones
				// on each poll we request tail up to (lastIdx + count), then slice.
				return printEntries(entries, &lastIdx)
			}

			if err := poll(); err != nil {
				return err
			}
			if !follow {
				return nil
			}
			// Follow: poll for new lines. Re-read a larger tail each tick; diff
			// by timestamp since we don't persist a byte cursor.
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			lastSeen := ""
			for range ticker.C {
				// Read more lines than we expect to grow by, then slice at lastSeen.
				entries, err := web.ReadLogFile(path, 200, lvl)
				if err != nil {
					return err
				}
				found := false
				for _, e := range entries {
					if e.Time <= lastSeen {
						continue
					}
					found = true
					fmt.Printf("%s %s %s\n", e.Time, e.Level, e.Message)
					lastSeen = e.Time
				}
				if found {
					_ = lastIdx
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 100, "显示最近 N 行（默认 100）")
	cmd.Flags().StringVar(&level, "level", "info", "级别过滤: debug/info/warn/error/all")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "实时跟踪 (等价于 tail -f)")
	cmd.Flags().StringVar(&path, "path", "", "日志文件路径 (默认 ~/.agent-netx/agent-netx.log)")
	return cmd
}

func printEntries(entries []web.LogEntry, lastIdx *int) error {
	for _, e := range entries {
		fmt.Printf("%s %s %s\n", e.Time, e.Level, e.Message)
	}
	*lastIdx += len(entries)
	return nil
}
