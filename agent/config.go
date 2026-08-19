package agent

// Config configures the LLM-backed agent.
type Config struct {
	// Enable toggles the agent on.
	Enable bool `yaml:"enable"`
	// BaseURL is the OpenAI-compatible API base URL, e.g. "https://api.openai.com/v1".
	BaseURL string `yaml:"base-url"`
	// APIKey is the bearer token for the API.
	APIKey string `yaml:"api-key"`
	// Model is the model id to call, e.g. "gpt-4o-mini".
	Model string `yaml:"model"`
	// SystemPrompt is prepended to every conversation (optional).
	SystemPrompt string `yaml:"system-prompt"`
	// ConfigPath is the path to the net-tools config file used by tools.
	ConfigPath string `yaml:"-"`
	// MemoryPath is the path to the persistent agent memory file. When empty,
	// DefaultMemoryPath (~/.agent-nettools/agent-memory.json) is used at runtime.
	// Exposed so tests / non-default installs can point it elsewhere.
	MemoryPath string `yaml:"-"`
	// Timeout is the per-request HTTP timeout for LLM calls (seconds). 0 = 120s.
	Timeout int `yaml:"-"`
	// MaxRetries is how many times to retry a transient failure (429/5xx/net).
	// 0 disables retry. Default applied in NewLLM when 0.
	MaxRetries int `yaml:"-"`
}

func DefaultConfig() Config {
	return Config{
		BaseURL: "https://api.openai.com/v1",
		Model:   "gpt-4o-mini",
	}
}

// defaultLLMTimeout is the per-request HTTP timeout in seconds when unset.
const defaultLLMTimeout = 120

// defaultMaxRetries is the retry count for transient LLM failures when unset.
const defaultMaxRetries = 3

// DefaultSystemPrompt is the canned instruction set the agent starts with when
// the user hasn't supplied one in config. Kept here (not a const) so callers
// can extend it (the TUI appends remembered SSH hosts to it at session open).
func DefaultSystemPrompt() string {
	return `你是 agent-nettools 的内置助手。你可以通过调用工具(tools)来操作网络工具集：查看/修改配置、测试代理延迟、启动/停止服务、切换代理分组、添加路由规则、SSH 文件传输、记忆与回忆等。
规则：
1. 修改配置前，先用 get_config 读取当前状态。
2. 写入配置用 update_config，内容是完整的新 YAML。
3. 危险操作（start/stop 服务、覆盖远程文件）先向用户确认，或调用 ask_human。
4. 需要用户决定而工具无法得知的信息（偏好/确认/选择）时，调用 ask_human 人工介入(HIL)。
5. 能从记忆复用的事实优先 recall，避免重复问用户；新事实用 remember 存入记忆。
6. SSH 文件传输：主机信息优先用已记住的 alias；缺信息时工具会自动向用户询问并记入记忆，无需自己追问。
7. 回复用中文，简洁。`
}
