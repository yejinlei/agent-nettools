package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type proxyInfo struct {
	Name    string        `json:"name"`
	Type    string        `json:"type"`
	Alive   bool          `json:"alive"`
	Latency time.Duration `json:"latency"`
}

type ruleInfo struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Proxy   string `json:"proxy"`
}

type connInfo struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	Destination string `json:"destination"`
	Proxy      string `json:"proxy"`
	Upload     int64  `json:"upload"`
	Download   int64  `json:"download"`
	StartTime  string `json:"start_time"`
}

type statusInfo struct {
	Uptime    string `json:"uptime"`
	Mode      string `json:"mode"`
	ProxyCount int   `json:"proxy_count"`
	RuleCount  int   `json:"rule_count"`
	MemoryMB  int    `json:"memory_mb"`
}

func (s *WebServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	info := statusInfo{
		Uptime:    time.Since(s.startTime).Round(time.Second).String(),
		Mode:      s.mode,
		ProxyCount: len(s.proxies),
		RuleCount:  len(s.rules),
		MemoryMB:  0,
	}
	writeJSON(w, info)
}

func (s *WebServer) handleProxies(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		s.handleProxyUpdate(w, r)
		return
	}
	proxies := make([]proxyInfo, 0, len(s.proxies))
	for _, p := range s.proxies {
		proxies = append(proxies, p)
	}
	writeJSON(w, proxies)
}

func (s *WebServer) handleProxyUpdate(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/proxies/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "proxy name required", http.StatusBadRequest)
		return
	}
	name := parts[0]
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if s.onProxySelect != nil {
		s.onProxySelect(name, body.Name)
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *WebServer) handleRules(w http.ResponseWriter, r *http.Request) {
	rules := make([]ruleInfo, 0, len(s.rules))
	for _, rule := range s.rules {
		rules = append(rules, rule)
	}
	writeJSON(w, rules)
}

func (s *WebServer) handleConnections(w http.ResponseWriter, r *http.Request) {
	conns := make([]connInfo, 0, len(s.connections))
	for _, c := range s.connections {
		conns = append(conns, c)
	}
	writeJSON(w, conns)
}

// handleStats reports per-proxy traffic + connection counts. The StatsTracker
// is fed from the proxy listener path (listener/http.go countingConns), so this
// endpoint only shows live data once the listener is wired to the tracker via
// the fullStart path (the standalone web command without a proxy shows zeros).
func (s *WebServer) handleStats(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeJSON(w, map[string]ProxyStats{})
		return
	}
	writeJSON(w, s.stats.GetStats())
}

func (s *WebServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsubscribe := s.log.Subscribe()
	defer unsubscribe()

	recent := s.log.Recent(50)
	for _, entry := range recent {
		data, _ := json.Marshal(entry)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case entry := <-ch:
			data, _ := json.Marshal(entry)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}