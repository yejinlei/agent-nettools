package proxy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

type Selector struct {
	mu     sync.Mutex
	cfg    Config
	reg    *Registry
	choice string
}

func NewSelector(cfg Config, reg *Registry) Proxy {
	defaultChoice := cfg.Default
	if defaultChoice == "" && len(cfg.Proxies) > 0 {
		defaultChoice = cfg.Proxies[0]
	}
	return &Selector{cfg: cfg, reg: reg, choice: defaultChoice}
}

func (s *Selector) Name() string { return s.cfg.Name }

func (s *Selector) Choice() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.choice
}

func (s *Selector) SetChoice(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.reg.Get(name)
	if err != nil {
		return fmt.Errorf("invalid choice %q: %w", name, err)
	}
	s.choice = strings.ToLower(name)
	return nil
}

func (s *Selector) Connect(ctx context.Context, addr string) (net.Conn, error) {
	s.mu.Lock()
	choice := s.choice
	s.mu.Unlock()
	p, err := s.reg.Get(choice)
	if err != nil {
		return nil, fmt.Errorf("selector %q: %w", s.cfg.Name, err)
	}
	return p.Connect(ctx, addr)
}

func (s *Selector) Latency(url string) (time.Duration, error) {
	s.mu.Lock()
	choice := s.choice
	s.mu.Unlock()
	p, err := s.reg.Get(choice)
	if err != nil {
		return 0, err
	}
	return p.Latency(url)
}

func (s *Selector) Close() error { return nil }