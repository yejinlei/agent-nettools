package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agent-nettools/config"
	"agent-nettools/proxy"
)

// ToolDef is a function-calling tool definition exposed to the LLM.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolFunc executes a tool by name with JSON arguments, returns a result string.
type ToolFunc func(ctx context.Context, args map[string]any) string

// Registry holds the available tools.
type Registry struct {
	funcs map[string]ToolFunc
	defs  []ToolDef
	cfg   Config
	mem   *Memory // persistent facts (SSH hosts, user prefs); nil disables memory tools
	ask   askFunc // HIL prompter; nil in non-interactive mode → tools surface "no HIL" errors
}

// NewRegistry builds a tool registry. mem and ask may be nil; when ask is nil,
// HIL-dependent tools (file_copy with missing creds, ask_human) return clear
// "no interactive prompter" errors instead of blocking on stdin.
func NewRegistry(cfg Config, mem *Memory, ask askFunc) *Registry {
	r := &Registry{
		funcs: make(map[string]ToolFunc),
		cfg:   cfg,
		mem:   mem,
		ask:   ask,
	}
	r.register()
	return r
}

func (r *Registry) Defs() []ToolDef { return r.defs }

func (r *Registry) Call(ctx context.Context, name string, args map[string]any) string {
	fn, ok := r.funcs[name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", name)
	}
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Fprintf(os.Stderr, "tool panic: %v\n", rec)
		}
	}()
	return fn(ctx, args)
}

func objType(t string) map[string]any {
	return map[string]any{"type": t}
}

func (r *Registry) register() {
	// get_config: dump current config as YAML
	r.defs = append(r.defs, ToolDef{
		Name:        "get_config",
		Description: "读取当前 net-tools 配置（完整 YAML）。无参数。",
		Parameters:  objType("object"),
	})
	r.funcs["get_config"] = func(ctx context.Context, args map[string]any) string {
		cfg, err := config.Load(r.configPath())
		if err != nil {
			return "error: " + err.Error()
		}
		b, _ := config.YAMLMarshal(cfg)
		return "```yaml\n" + string(b) + "\n```"
	}

	// update_config: write full YAML config
	r.defs = append(r.defs, ToolDef{
		Name:        "update_config",
		Description: "用完整的新 YAML 内容覆盖配置文件。写入前请先 get_config 看当前状态再修改。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"yaml": map[string]any{"type": "string", "description": "完整的 YAML 配置内容"},
			},
			"required": []string{"yaml"},
		},
	})
	r.funcs["update_config"] = func(ctx context.Context, args map[string]any) string {
		yamlStr, _ := args["yaml"].(string)
		if strings.TrimSpace(yamlStr) == "" {
			return "error: yaml is empty"
		}
		// validate by parsing
		if _, err := config.LoadFromBytes([]byte(yamlStr)); err != nil {
			return "error: invalid YAML: " + err.Error()
		}
		if err := os.WriteFile(r.configPath(), []byte(yamlStr), 0644); err != nil {
			return "error: " + err.Error()
		}
		return "配置已写入 " + r.configPath()
	}

	// ping_proxy: test latency of all proxies
	r.defs = append(r.defs, ToolDef{
		Name:        "ping_proxies",
		Description: "测试配置中所有代理的延迟（毫秒）。无参数。",
		Parameters:  objType("object"),
	})
	r.funcs["ping_proxies"] = func(ctx context.Context, args map[string]any) string {
		cfg, err := config.Load(r.configPath())
		if err != nil {
			return "error: " + err.Error()
		}
		reg, err := proxy.Register(cfg.Proxies)
		if err != nil {
			return "error: " + err.Error()
		}
		var sb strings.Builder
		reg.Each(func(name string, p proxy.Proxy) {
			l, err := p.Latency("https://www.gstatic.com/generate_204")
			if err != nil {
				fmt.Fprintf(&sb, "  %-20s ERROR: %s\n", name, err.Error())
			} else {
				fmt.Fprintf(&sb, "  %-20s %s\n", name, l.String())
			}
		})
		return sb.String()
	}

	// switch_group: change a selector group's default
	r.defs = append(r.defs, ToolDef{
		Name:        "switch_group",
		Description: "把某个 selector 分组切换到指定代理节点。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"group": map[string]any{"type": "string", "description": "分组名"},
				"proxy": map[string]any{"type": "string", "description": "目标代理名"},
			},
			"required": []string{"group", "proxy"},
		},
	})
	r.funcs["switch_group"] = func(ctx context.Context, args map[string]any) string {
		group, _ := args["group"].(string)
		target, _ := args["proxy"].(string)
		cfg, err := config.Load(r.configPath())
		if err != nil {
			return "error: " + err.Error()
		}
		found := false
		for i, g := range cfg.Groups {
			if g.Name == group {
				cfg.Groups[i].Default = target
				found = true
				break
			}
		}
		if !found {
			return fmt.Sprintf("error: group %q not found", group)
		}
		b, _ := config.YAMLMarshal(cfg)
		if err := os.WriteFile(r.configPath(), b, 0644); err != nil {
			return "error: " + err.Error()
		}
		return fmt.Sprintf("已把分组 %s 切换到 %s", group, target)
	}

	// add_rule: prepend a routing rule
	r.defs = append(r.defs, ToolDef{
		Name:        "add_rule",
		Description: "在规则列表开头插入一条路由规则，如 DOMAIN,google.com,Auto。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"rule": map[string]any{"type": "string", "description": "规则字符串，格式 TYPE,VALUE,TARGET"},
			},
			"required": []string{"rule"},
		},
	})
	r.funcs["add_rule"] = func(ctx context.Context, args map[string]any) string {
		rule, _ := args["rule"].(string)
		cfg, err := config.Load(r.configPath())
		if err != nil {
			return "error: " + err.Error()
		}
		cfg.Rules = append([]string{rule}, cfg.Rules...)
		b, _ := config.YAMLMarshal(cfg)
		if err := os.WriteFile(r.configPath(), b, 0644); err != nil {
			return "error: " + err.Error()
		}
		return "已添加规则: " + rule
	}

	// service: start/stop individual subsystems by name (spawns the matching
	// standalone subcommand in the background; stop kills the tracked PID).
	// This lets the agent control each tool independently from the TUI,
	// without restarting the whole binary.
	r.defs = append(r.defs, ToolDef{
		Name: "service",
		Description: "启动/停止单个子服务。可管理: proxy / dns / web / tun / n2n / stunvpv。" +
			"start=后台启动该子服务；stop=停止该子服务；status=查看各服务运行状态。" +
			"示例: action=start, name=proxy。启动操作会复用配置文件中的端口/地址。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"start", "stop", "status"},
					"description": "start=后台启动子服务，stop=停止子服务，status=查看运行状态",
				},
				"name": map[string]any{
					"type": "string",
					"enum": []string{"proxy", "dns", "web", "tun", "n2n", "stunvpv"},
					"description": "子服务名（与独立子命令同名）",
				},
			},
			"required": []string{"action"},
		},
	})
	r.funcs["service"] = func(ctx context.Context, args map[string]any) string {
		action, _ := args["action"].(string)
		switch action {
		case "status":
			return r.serviceStatus()
		case "start":
			name, _ := args["name"].(string)
			return r.serviceStart(name)
		case "stop":
			name, _ := args["name"].(string)
			return r.serviceStop(name)
		}
		return "error: unknown action " + action
	}

	// file_copy: SSH/SFTP file transfer (upload or download) between this
	// machine and a remote host. Credentials are resolved in this priority:
	// explicit args → memory (ssh:host:<alias>) → HIL prompt to the human.
	// The first interactive setup is saved to memory so it never asks again.
	// This is the concrete feature that exercises HIL + memory together.
	r.defs = append(r.defs, ToolDef{
		Name: "file_copy",
		Description: "通过 SSH/SFTP 上传或下载单个文件。方向: upload=本机→远程, download=远程→本机。" +
			"host 可填已记住的主机别名(memory 里有)，或直接填 host/IP；缺用户名/密码/私钥时会向用户询问并存入记忆，下次不再问。" +
			"示例: action=upload, alias=prod, src=./app.log, dst=/var/log/app.log",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"upload", "download"},
					"description": "upload=本机→远程，download=远程→本机",
				},
				"alias":  map[string]any{"type": "string", "description": "主机别名(可选)。填已记住的别名可免输其余字段；新别名会把本次信息存入记忆"},
				"host":   map[string]any{"type": "string", "description": "主机名或 IP(可选，alias 已记住时可不填)"},
				"port":   map[string]any{"type": "integer", "description": "SSH 端口(可选，默认22)"},
				"user":   map[string]any{"type": "string", "description": "登录用户(可选)"},
				"password": map[string]any{"type": "string", "description": "密码(可选)"},
				"keyPath":  map[string]any{"type": "string", "description": "私钥文件路径(可选)"},
				"src":    map[string]any{"type": "string", "description": "源文件路径(upload=本地，download=远程)"},
				"dst":    map[string]any{"type": "string", "description": "目标文件路径(upload=远程，download=本地)"},
			},
			"required": []string{"action", "src", "dst"},
		},
	})
	r.funcs["file_copy"] = func(ctx context.Context, args map[string]any) string {
		action, _ := args["action"].(string)
		if action != "upload" && action != "download" {
			return "error: action 必须是 upload 或 download"
		}
		alias, _ := args["alias"].(string)
		host, _ := args["host"].(string)
		user, _ := args["user"].(string)
		password, _ := args["password"].(string)
		keyPath, _ := args["keyPath"].(string)
		port := toInt(args["port"])
		src, _ := args["src"].(string)
		dst, _ := args["dst"].(string)
		if src == "" || dst == "" {
			return "error: src 和 dst 必填"
		}
		if alias == "" && host == "" {
			return "error: 需要填 alias 或 host"
		}

		h, err := ResolveHost(ctx, alias, host, user, password, keyPath, port, r.mem, r.ask)
		if err != nil {
			return "error: " + err.Error()
		}

		n, err := FileTransfer(ctx, h, src, dst, action)
		if err != nil {
			return "error: " + err.Error()
		}
		verb := "上传"
		if action == "download" {
			verb = "下载"
		}
		return fmt.Sprintf("✅ %s完成: %s → %s (%s, 主机 %s@%s:%d)",
			verb, src, dst, HumanSize(n), h.User, h.Host, h.port())
	}

	// ask_human: explicit Human-in-the-Loop. Lets the LLM pause and ask the
	// user a question when it lacks information no tool can provide (a
	// preference, a confirmation, a choice between options). The answer is
	// returned into the tool result so the model can continue.
	r.defs = append(r.defs, ToolDef{
		Name:        "ask_human",
		Description: "向用户提问以获取人工输入(HIL)。当缺少工具无法自行决定的信息(偏好/确认/选择)时调用。非交互模式会返回错误。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "要问用户的问题"},
				"choices":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "可选: 供用户选择的选项列表"},
			},
			"required": []string{"question"},
		},
	})
	r.funcs["ask_human"] = func(ctx context.Context, args map[string]any) string {
		question, _ := args["question"].(string)
		if question == "" {
			return "error: question 必填"
		}
		if r.ask == nil {
			return "error: 当前非交互模式，无法向用户提问。请改用其它工具或补充参数后重试。"
		}
		var choices []string
		if raw, ok := args["choices"].([]any); ok {
			for _, c := range raw {
				if s, ok := c.(string); ok && s != "" {
					choices = append(choices, s)
				}
			}
		}
		q := question
		if len(choices) > 0 {
			q = question + "\n  选项: " + strings.Join(choices, " / ")
			q += "\n  (输入序号或内容): "
		} else {
			q += "\n  > "
		}
		ans := r.ask(ctx, q)
		if strings.TrimSpace(ans) == "" {
			return "(用户未作答)"
		}
		return ans
	}

	// recall: surface remembered facts so the LLM can reuse prior knowledge.
	// Empty query returns everything (used at session open to prime context).
	r.defs = append(r.defs, ToolDef{
		Name:        "recall",
		Description: "从记忆中查找已记的事实(SSH 主机、偏好等)。query 为空时返回全部。用于会话开始时回顾或确认是否需要再问用户。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "查找关键词(可选，空=全部)"},
			},
		},
	})
	r.funcs["recall"] = func(ctx context.Context, args map[string]any) string {
		if r.mem == nil {
			return "(记忆未启用)"
		}
		query, _ := args["query"].(string)
		pairs := r.mem.Find(query)
		if len(pairs) == 0 {
			return "(记忆为空" + orQuerySuffix(query) + ")"
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "记忆中 %d 条记录:\n", len(pairs))
		for _, p := range pairs {
			fmt.Fprintf(&sb, "  %s = %s\n", p.Key, p.Value)
		}
		return sb.String()
	}

	// remember: persist a fact so it survives across sessions and feeds recall.
	r.defs = append(r.defs, ToolDef{
		Name:        "remember",
		Description: "把一条事实存入记忆(跨会话保留)。key 建议带命名空间如 user:pref 或 site:xxx。下次可被 recall 找到。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":   map[string]any{"type": "string", "description": "键名(建议加命名空间前缀)"},
				"value": map[string]any{"type": "string", "description": "值"},
			},
			"required": []string{"key", "value"},
		},
	})
	r.funcs["remember"] = func(ctx context.Context, args map[string]any) string {
		if r.mem == nil {
			return "error: 记忆未启用"
		}
		k, _ := args["key"].(string)
		v, _ := args["value"].(string)
		if k == "" {
			return "error: key 必填"
		}
		r.mem.Set(k, v)
		return fmt.Sprintf("已记住: %s = %s", k, v)
	}

	// help: list available subcommands
	r.defs = append(r.defs, ToolDef{
		Name:        "list_commands",
		Description: "列出所有可用的 CLI 子命令和它们的用途。无参数。",
		Parameters:  objType("object"),
	})
	r.funcs["list_commands"] = func(ctx context.Context, args map[string]any) string {
		return `init       生成示例 config.yml
start      启动代理（-c 配置 / --proxy 快速模式）
status     显示当前配置
ping       测试所有代理延迟
use        切换手动分组
forward    HTTPS→HTTP 劫持转发
tui        启动 LLM Agent 交互模式（就是你现在用的）`
	}
}

func (r *Registry) configPath() string {
	if r.cfg.ConfigPath != "" {
		return r.cfg.ConfigPath
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, "config.yml")
}
