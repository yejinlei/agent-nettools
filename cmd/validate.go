package cmd

import (
	"encoding/json"
	"fmt"

	"agent-netx/config"

	"github.com/spf13/cobra"
)

func validateCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "校验配置文件合法性",
		Long: `校验配置文件 (config.yml) 的合法性并报告所有错误。

  # 校验默认配置
  agent-netx validate

  # 校验指定配置
  agent-netx validate -c ./custom.yml

  # JSON 输出 (给脚本用)
  agent-netx validate --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			errs := config.Validate(cfgPath)
			if len(errs) == 0 {
				if jsonOut {
					fmt.Println(`{"ok":true,"errors":[]}`)
				} else {
					fmt.Println("✅ 配置校验通过")
				}
				return nil
			}
			if jsonOut {
				b, _ := json.Marshal(map[string]any{"ok": false, "errors": errs})
				fmt.Println(string(b))
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "❌ 配置校验失败 (%d 个问题):\n", len(errs))
				for i, e := range errs {
					fmt.Fprintf(cmd.ErrOrStderr(), "  %2d. %s\n", i+1, e.Error())
				}
			}
			return fmt.Errorf("validation failed")
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON 输出")
	return cmd
}
