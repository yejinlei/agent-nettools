---
name: config-layout
description: config.yml vs agent.yml split (TUI vs non-TUI tooling)
metadata:
  type: project
---

`config.yml`: all proxy/VPN/DNS/TUN/forwarding/rule settings. Loaded by every non-TUI command (start, proxy, dns, tun, forward, sysproxy, ping, status, use, frp, tinc, socat, scp, corsproxy, n2n, stunvpv, wireguard) and by the agent's tools at runtime.

`agent.yml`: LLM-only settings consumed only by `tui` subcommand via `agent.LoadAgentConfig`. Path relative to CWD by default, overridable with `--agent-config`.

Both are scaffolded by `init` (`--config` sets the base dir; `--agent-config` sets the agent file explicitly).

[[agent-config-split]]