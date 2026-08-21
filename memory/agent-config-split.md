---
name: agent-config-split
description: agent LLM config lives in standalone agent.yml, not config.yml agent: block
metadata:
  type: project
---

Split the agent's LLM config out of `config.yml`'s `agent:` block into a standalone `agent.yml`. Non-TUI tools still read proxy/VPN/DNS settings from `config.yml` — only the agent's LLM settings (base-url, api-key, model, system-prompt, memory-path, timeout, max-retries) moved.

How it works:
- `agent.ConfigAgent` struct + `agent.LoadAgentConfig(path string) (ConfigAgent, string, error)` + `agent.DefaultAgentConfigPath = "agent.yml"`.
- `cmd/root.go` `tuiCmd()` calls `LoadAgentConfig` first; if the file is missing falls back to `cfg.Agent` block in `config.yml` for backward compat. Adds `--agent-config` flag. Also honors `AGENT_API_KEY` env as final fallback.
- `config.ExampleAgentConfig` constant scaffolds `agent.yml`; `initCmd` generates both `config.yml` and `agent.yml` (idempotent — skips if existing), both under `--config` dir. Also adds `--agent-config` flag to `init`.
- Pushed as commit `f5df84c` on `origin/main`.

[[config-layout]]