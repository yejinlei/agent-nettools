package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Message roles
const (
	RoleSystem     = "system"
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleTool       = "tool"
)

// Message is a chat message in OpenAI format (tool-calls aware).
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
}

// ToolCall is a single function call issued by the model.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

// toolDefJSON is what we send to the API (functions under "tools").
type toolDefJSON struct {
	Type     string `json:"type"` // always "function"
	Function ToolDef `json:"function"`
}

// LLM is an OpenAI-compatible chat client with function calling.
type LLM struct {
	cfg    Config
	tools  []toolDefJSON
	client *http.Client
}

func NewLLM(cfg Config, defs []ToolDef) *LLM {
	tools := make([]toolDefJSON, 0, len(defs))
	for _, d := range defs {
		tools = append(tools, toolDefJSON{Type: "function", Function: d})
	}
	return &LLM{
		cfg:    cfg,
		tools:  tools,
		client: &http.Client{},
	}
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []Message     `json:"messages"`
	Tools       []toolDefJSON `json:"tools,omitempty"`
	Temperature float64      `json:"temperature,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Complete sends the conversation and returns the assistant message.
// If the message contains tool_calls, caller must execute them and re-Complete.
func (l *LLM) Complete(ctx context.Context, messages []Message) (Message, error) {
	if l.cfg.APIKey == "" {
		return Message{}, fmt.Errorf("agent: api-key 未配置（编辑 config.yml 的 agent 段，或设环境变量 AGENT_API_KEY）")
	}
	req := chatRequest{
		Model:       l.cfg.Model,
		Messages:    messages,
		Tools:       l.tools,
		Temperature: 0.3,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, err
	}

	url := strings.TrimRight(l.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return Message{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+l.cfg.APIKey)

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return Message{}, fmt.Errorf("调用 LLM 失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return Message{}, fmt.Errorf("解析 LLM 响应失败 (HTTP %d): %s", resp.StatusCode, string(raw))
	}
	if cr.Error != nil {
		return Message{}, fmt.Errorf("LLM 返回错误: %s", cr.Error.Message)
	}
	if resp.StatusCode >= 400 {
		return Message{}, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, string(raw))
	}
	if len(cr.Choices) == 0 {
		return Message{}, fmt.Errorf("LLM 返回 0 choices (HTTP %d): %s", resp.StatusCode, string(raw))
	}
	return cr.Choices[0].Message, nil
}

// ParseToolCallArgs decodes the JSON arguments string into a map.
func ParseToolCallArgs(s string) map[string]any {
	out := map[string]any{}
	if s == "" {
		return out
	}
	dec := json.NewDecoder(strings.NewReader(s))
	_ = dec.Decode(&out)
	return out
}
