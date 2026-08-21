package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func (s *SessionStore) Export(idOrName, dst string) (string, error) {
	sess, err := s.Load(idOrName)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dst) {
		dst = filepath.Join(s.dir, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, b, 0644); err != nil {
		return "", err
	}
	return dst, nil
}

func (s *SessionStore) Import(src string) (*Session, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, err
	}
	if sess.ID == "" {
		sess.ID = "session_" + newUUID()
	}
	if sess.Messages == nil {
		sess.Messages = []Message{}
	}
	if err := s.Save(&sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func init() { _ = fmt.Sprintf }
