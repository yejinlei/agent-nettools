package agent

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session is one chat session persisted to disk. It mirrors the structure
// returned to the LLM so we can load it back into `tui.msgs` verbatim.
type Session struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Model     string    `json:"model"`
	Turns     int       `json:"turns"`
	Messages  []Message `json:"messages"`
}

const maxSessionMessages = 200

// SessionStore manages session files under a directory.
type SessionStore struct {
	dir string
}

// DefaultSessionDir returns the default session directory next to the memory
// file: ~/.agent-netx/sessions/. Falls back to ./sessions/ if home is
// unknown (rare).
func DefaultSessionDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "sessions"
	}
	return filepath.Join(home, ".agent-netx", "sessions")
}

func NewSessionStore(dir string) *SessionStore {
	if dir == "" {
		dir = DefaultSessionDir()
	}
	return &SessionStore{dir: dir}
}

// New creates a fresh session with an auto-generated UUID id and timestamped
// name. Caller should Save() before use if persistence is needed.
func (s *SessionStore) New(name, model string) *Session {
	now := time.Now()
	id := "session_" + newUUID()
	if name == "" {
		name = now.Format("2006-01-02") + " 会话"
	}
	return &Session{
		ID:        id,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
		Model:     model,
		Turns:     0,
		Messages:  []Message{},
	}
}

// Save writes the session to disk (truncates file). Caller should hold a
// write lock if concurrent access is possible (TUI is single-threaded so
// this is a no-op in practice).
func (s *SessionStore) Save(sess *Session) error {
	if sess == nil {
		return nil
	}
	sess.UpdatedAt = time.Now()
	s.trimMessages(sess)

	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}
	b, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	return os.WriteFile(s.sessionFile(sess.ID), b, 0600)
}

// Load reads a session by id or name (name matches exactly; id is tried first).
func (s *SessionStore) Load(idOrName string) (*Session, error) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return nil, fmt.Errorf("empty session id/name")
	}

	// Try exact id first (id always starts with "session_").
	if strings.HasPrefix(idOrName, "session_") {
		return s.loadByID(idOrName)
	}

	// Fall back to name lookup.
	all, _ := s.List()
	for _, sess := range all {
		if sess.Name == idOrName {
			return sess, nil
		}
	}
	return nil, fmt.Errorf("session %q not found (id or name)", idOrName)
}

func (s *SessionStore) loadByID(id string) (*Session, error) {
	b, err := os.ReadFile(s.sessionFile(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session %q not found", id)
		}
		return nil, fmt.Errorf("read session: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	if sess.Messages == nil {
		sess.Messages = []Message{}
	}
	return &sess, nil
}

// List returns all sessions sorted by updatedAt descending.
func (s *SessionStore) List() ([]*Session, error) {
	if _, err := os.Stat(s.dir); os.IsNotExist(err) {
		return nil, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		sess, err := s.loadByID(id)
		if err != nil {
			continue // skip corrupt files
		}
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// MostRecent returns the session with the latest updatedAt, or nil if
// none exist.
func (s *SessionStore) MostRecent() (*Session, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all[0], nil
}

// Delete removes a session by id or name. If the session is not found,
// returns an error.
func (s *SessionStore) Delete(idOrName string) error {
	sess, err := s.Load(idOrName)
	if err != nil {
		return err
	}
	return os.Remove(s.sessionFile(sess.ID))
}

// Rename updates the session's name. If name is empty, defaults to a
// timestamp-based name.
func (s *SessionStore) Rename(idOrName string, newName string) error {
	sess, err := s.Load(idOrName)
	if err != nil {
		return err
	}
	if newName == "" {
		newName = sess.UpdatedAt.Format("2006-01-02") + " 会话"
	}
	sess.Name = newName
	return s.Save(sess)
}

// Count returns the number of persisted sessions.
func (s *SessionStore) Count() (int, error) {
	all, err := s.List()
	if err != nil {
		return 0, err
	}
	return len(all), nil
}

func (s *SessionStore) sessionFile(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// trimMessages keeps system + the latest (maxSessionMessages - 1) messages.
// Caller should save after trimming.
func (s *SessionStore) trimMessages(sess *Session) {
	if len(sess.Messages) <= maxSessionMessages {
		return
	}
	tail := maxSessionMessages - 1
	sess.Messages = append(sess.Messages[:1], sess.Messages[len(sess.Messages)-tail:]...)
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}