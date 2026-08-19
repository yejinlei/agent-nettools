package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// kv is one key/value pair surfaced by Memory.Find (used by the recall tool).
type kv struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Memory is a persistent key/value store the agent uses to remember facts
// across turns and across sessions — most importantly SSH host configs, so it
// does not have to ask the user for credentials every time (see HIL).
//
// It is backed by a JSON file (default ~/.agent-nettools/agent-memory.json,
// 0600). Values are opaque strings; structured records (like an SSH host) are
// stored as JSON-encoded strings under a namespaced key (e.g. "ssh:host:foo").
type Memory struct {
	path string
	mu   sync.RWMutex
	data map[string]string
}

// NewMemory loads (or creates) the memory store at path. A missing file is
// not an error — the store starts empty.
func NewMemory(path string) *Memory {
	m := &Memory{path: path, data: map[string]string{}}
	m.load()
	return m
}

// DefaultMemoryPath returns the conventional memory file location:
// ~/.agent-nettools/agent-memory.json. Falls back to a relative path if the
// home directory can't be determined (rare).
func DefaultMemoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "agent-memory.json"
	}
	return filepath.Join(home, ".agent-nettools", "agent-memory.json")
}

func (m *Memory) load() {
	if m.path == "" {
		return
	}
	b, err := os.ReadFile(m.path)
	if err != nil {
		return // first run: no file yet
	}
	_ = json.Unmarshal(b, &m.data) // corrupt file → start empty
}

func (m *Memory) save() {
	if m.path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.path), 0700)
	b, err := json.MarshalIndent(m.data, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(m.path, b, 0600) // 0600: memory may hold credentials
}

// Get returns the value for key (ok=false if absent).
func (m *Memory) Get(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	return v, ok
}

// Set stores a value and persists immediately.
func (m *Memory) Set(key, value string) {
	m.mu.Lock()
	m.data[key] = value
	m.mu.Unlock()
	m.save()
}

// Find returns all pairs whose key or value contains query (case-insensitive).
// An empty query returns everything — used to summarize memory at session start.
func (m *Memory) Find(query string) []kv {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q := strings.ToLower(query)
	out := make([]kv, 0, len(m.data))
	for k, v := range m.data {
		if q == "" || strings.Contains(strings.ToLower(k), q) || strings.Contains(strings.ToLower(v), q) {
			out = append(out, kv{Key: k, Value: v})
		}
	}
	return out
}

// Has reports whether memory holds any entry under the "ssh:host:" namespace.
// Used to decide whether to advertise remembered hosts to the LLM.
func (m *Memory) HasSSHHosts() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k := range m.data {
		if strings.HasPrefix(k, sshHostKeyPrefix) {
			return true
		}
	}
	return false
}

// sshAliases returns the sorted list of remembered SSH host alias names (the
// part after "ssh:host:"). Used to advertise available hosts to the LLM at
// session open so it can reuse them without a recall round-trip.
func (m *Memory) sshAliases() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for k := range m.data {
		if strings.HasPrefix(k, sshHostKeyPrefix) {
			out = append(out, strings.TrimPrefix(k, sshHostKeyPrefix))
		}
	}
	sort.Strings(out)
	return out
}

// sshHostKeyPrefix namespaces SSH host entries in memory. A value stored under
// this key is a JSON-encoded HostInfo.
const sshHostKeyPrefix = "ssh:host:"
