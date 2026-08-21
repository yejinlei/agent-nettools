package agent

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config configures the LLM-backed agent.
type Config struct {
	Enable bool `yaml:"enable"`
	BaseURL string `yaml:"base-url"`
	APIKey string `yaml:"api-key"`
	Model string `yaml:"model"`
	SystemPrompt string `yaml:"system-prompt"`
	ConfigPath string `yaml:"-"`
	MemoryPath string `yaml:"-"`
	Timeout int `yaml:"-"`
	MaxRetries int `yaml:"-"`
	// ContinueSession tells newTUI to load this existing session id/name and
	// append to it instead of starting fresh. Set from --continue flag; empty
	// means "start a new session". Not serialized to agent.yml (yaml:"-").
	ContinueSession string `yaml:"-"`
}

// ConfigAgent is the standalone LLM configuration read from "agent.yml".
// Keeps the agent's secret-bearing settings (api-key, memory-path, system-prompt)
// out of the main proxy config (config.yml). If agent.yml is absent the caller
// falls back to the legacy agent section embedded in config.yml.
type ConfigAgent struct {
	BaseURL      string `yaml:"base-url"`
	APIKey       string `yaml:"api-key"`
	Model        string `yaml:"model"`
	SystemPrompt string `yaml:"system-prompt"`
	MemoryPath   string `yaml:"memory-path"`
	Timeout      int    `yaml:"timeout"`
	MaxRetries   int    `yaml:"max-retries"`
}

const DefaultAgentConfigPath = "agent.yml"

func LoadAgentConfig(path string) (ConfigAgent, string, error) {
	if path == "" {
		cwd, _ := os.Getwd()
		path = filepath.Join(cwd, DefaultAgentConfigPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ConfigAgent{}, path, err
	}
	var ca ConfigAgent
	if err := yaml.Unmarshal(data, &ca); err != nil {
		return ConfigAgent{}, path, err
	}
	return ca, path, nil
}

func DefaultConfig() Config {
	return Config{
		BaseURL: "https://api.openai.com/v1",
		Model:   "gpt-4o-mini",
	}
}

const defaultLLMTimeout = 120
const defaultMaxRetries = 3

func DefaultSystemPrompt() string {
	return `你是 agent-netx 的内置助手。你可以通过调用工具(tools)来操作网络工具集：查看/修改配置、测试代理延迟、启动/停止服务、切换代理分组、添加路由规则、SSH 文件传输、记忆与回忆等。
规则：
1. 修改已有配置前，先用 get_config 读取当前状态。
2. 覆盖写配置用 update_config，内容是完整的新 YAML。
3. 用户想从零生成一份配置时（描述了想要的代理/端口/分组/规则），用 gen_config：把用户描述转成 spec 对象，工具负责拼装+校验+落盘，不要自己手写 YAML。
4. 危险操作（start/stop 服务、覆盖远程文件、gen_config 覆盖文件）先向用户确认，或调用 ask_human。
5. 需要用户决定而工具无法得知的信息（偏好/确认/选择）时，调用 ask_human 人工介入(HIL)。
6. 能从记忆复用的事实优先 recall，避免重复问用户；新事实用 remember 存入记忆。
7. SSH 文件传输：主机信息优先用已记住的 alias；缺信息时工具会自动向用户询问并记入记忆，无需自己追问。
8. 回复用中文，简洁。`
}
