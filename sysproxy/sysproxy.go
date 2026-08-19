// Package sysproxy configures the OS-level system proxy ("one-click system
// proxy" / 一键系统代理). On Windows it flips the Internet Settings registry
// keys; on Linux it sets the GNOME gsettings and emits the conventional env
// vars. The CLI subcommand `sysproxy on|off|status` is the user-facing surface;
// the agent's `service`-style tools call the same functions.
//
// The interface is deliberately tiny so adding a desktop (macOS, KDE, etc.) is
// one new file. Each platform implements Enable, Disable, and Status; the
// non-platform-default files have build tags so only one compiles per GOOS.
package sysproxy

// Settings describes a system-proxy configuration to apply. HTTP and HTTPS are
// the same host:port in the common case but kept separate for flexibility.
type Settings struct {
	HTTP    string // "http://127.0.0.1:7890" or "" to leave unset
	HTTPS   string
	NoProxy string // comma-joined hosts to bypass, e.g. "127.0.0.1,localhost"
}

// Action is the verb the CLI / agent sends: turn on, turn off, or query.
// Prefixed Act* to avoid colliding with the platform Status()/Enable() funcs.
type Action string

const (
	ActOn     Action = "on"
	ActOff    Action = "off"
	ActStatus Action = "status"
)
