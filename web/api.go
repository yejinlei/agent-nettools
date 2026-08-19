package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"time"
)

//go:embed index.html
var staticFiles embed.FS

type WebServer struct {
	config       WebConfig
	server       *http.Server
	startTime    time.Time
	mode         string
	proxies      map[string]proxyInfo
	rules        []ruleInfo
	connections  map[string]connInfo
	log          *LogRing
	stats        *StatsTracker
	onProxySelect func(group, proxy string)
}

type WebConfig struct {
	Enable   bool
	Port     int
	Username string
	Password string
}

func NewWebServer(cfg WebConfig, log *LogRing, stats *StatsTracker) *WebServer {
	return &WebServer{
		config:      cfg,
		proxies:     make(map[string]proxyInfo),
		connections: make(map[string]connInfo),
		log:         log,
		stats:       stats,
	}
}

func (s *WebServer) SetProxies(proxies map[string]proxyInfo) {
	s.proxies = proxies
}

func (s *WebServer) SetRules(rules []ruleInfo) {
	s.rules = rules
}

func (s *WebServer) SetMode(mode string) {
	s.mode = mode
}

func (s *WebServer) OnProxySelect(fn func(group, proxy string)) {
	s.onProxySelect = fn
}

func (s *WebServer) Start(ctx context.Context) error {
	s.startTime = time.Now()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/proxies", s.handleProxies)
	mux.HandleFunc("/api/proxies/", s.handleProxies)
	mux.HandleFunc("/api/rules", s.handleRules)
	mux.HandleFunc("/api/connections", s.handleConnections)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/stats", s.handleStats)

	sub, err := fs.Sub(staticFiles, ".")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	addr := ":9090"
	if s.config.Port > 0 {
		addr = fmt.Sprintf(":%d", s.config.Port)
	}

	s.server = &http.Server{
		Addr:    addr,
		Handler: corsMiddleware(mux),
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Block until the context is cancelled or the server exits. (A previous
	// version had a `default` case here that returned nil immediately, which
	// made Start() unsuitable for foreground/standalone use.)
	select {
	case <-ctx.Done():
		return s.Stop()
	case err, ok := <-errCh:
		if !ok {
			return nil
		}
		return err
	}
}

func (s *WebServer) Stop() error {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}
	return nil
}