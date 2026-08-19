package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
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
	cfg     Config
	tools   []toolDefJSON
	client  *http.Client
	retries int // max transient-failure retries (429/5xx/net/timeout)
}

func NewLLM(cfg Config, defs []ToolDef) *LLM {
	tools := make([]toolDefJSON, 0, len(defs))
	for _, d := range defs {
		tools = append(tools, toolDefJSON{Type: "function", Function: d})
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultLLMTimeout
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	return &LLM{
		cfg:    cfg,
		tools:  tools,
		client: &http.Client{Timeout: time.Duration(timeout) * time.Second},
		retries: maxRetries,
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
// Transient failures (429 / 5xx / network / timeout) are retried with
// exponential backoff up to l.retries times, so a momentarily rate-limited or
// flaky endpoint (the "rpm exhausted" case) doesn't kill the whole session.
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

	var lastErr error
	for attempt := 0; attempt <= l.retries; attempt++ {
		if attempt > 0 {
			// Back off: 1s, 2s, 4s, … (jittered slightly by attempt index).
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return Message{}, ctx.Err()
			case <-time.After(backoff):
			}
		}

		msg, retryable, err := l.doOnce(ctx, url, body)
		if err == nil {
			return msg, nil
		}
		lastErr = err
		if !retryable {
			return Message{}, err
		}
	}
	return Message{}, fmt.Errorf("LLM 调用失败（已重试 %d 次）: %w", l.retries, lastErr)
}

// doOnce performs a single chat-completions request. The retryable bool tells
// Complete whether the failure is worth retrying (429/5xx/network/timeout vs.
// 4xx auth/bad-request which won't get better).
func (l *LLM) doOnce(ctx context.Context, url string, body []byte) (Message, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return Message{}, false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+l.cfg.APIKey)

	resp, err := l.client.Do(httpReq)
	if err != nil {
		// network / timeout / context — all retryable (ctx cancellation isn't,
		// but Complete will surface it via the ctx.Err() check above anyway).
		return Message{}, true, fmt.Errorf("调用 LLM 失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var cr chatResponse
	if jerr := json.Unmarshal(raw, &cr); jerr != nil {
		// 5xx often returns non-JSON (proxy/gateway error page) — retryable.
		if resp.StatusCode >= 500 {
			return Message{}, true, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
		}
		return Message{}, false, fmt.Errorf("解析 LLM 响应失败 (HTTP %d): %s", resp.StatusCode, truncate(raw, 200))
	}
	if cr.Error != nil {
		return Message{}, false, fmt.Errorf("LLM 返回错误: %s", cr.Error.Message)
	}
	// 429 (rate limit) and 5xx (server) are transient — retry.
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return Message{}, true, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}
	if resp.StatusCode >= 400 {
		return Message{}, false, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}
	if len(cr.Choices) == 0 {
		return Message{}, false, fmt.Errorf("LLM 返回 0 choices (HTTP %d): %s", resp.StatusCode, truncate(raw, 200))
	}
	return cr.Choices[0].Message, false, nil
}

// truncate clamps a byte slice to n bytes for error-message readability.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
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
