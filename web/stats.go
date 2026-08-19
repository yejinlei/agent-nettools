package web

import (
	"sync"
	"sync/atomic"
)

type ProxyStats struct {
	Name        string `json:"name"`
	Upload      int64  `json:"upload"`
	Download    int64  `json:"download"`
	Connections int64  `json:"connections"`
}

type StatsTracker struct {
	mu    sync.RWMutex
	stats map[string]*proxyStat
}

type proxyStat struct {
	upload      int64
	download    int64
	connections atomic.Int64
}

func NewStatsTracker() *StatsTracker {
	return &StatsTracker{stats: make(map[string]*proxyStat)}
}

func (s *StatsTracker) getOrCreate(name string) *proxyStat {
	s.mu.RLock()
	ps, ok := s.stats[name]
	s.mu.RUnlock()
	if ok {
		return ps
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps, ok = s.stats[name]; ok {
		return ps
	}
	ps = &proxyStat{}
	s.stats[name] = ps
	return ps
}

func (s *StatsTracker) RecordTraffic(proxyName string, bytesUp, bytesDown int64) {
	ps := s.getOrCreate(proxyName)
	atomic.AddInt64(&ps.upload, bytesUp)
	atomic.AddInt64(&ps.download, bytesDown)
}

func (s *StatsTracker) AddConnection(proxyName string) {
	ps := s.getOrCreate(proxyName)
	ps.connections.Add(1)
}

func (s *StatsTracker) RemoveConnection(proxyName string) {
	ps := s.getOrCreate(proxyName)
	ps.connections.Add(-1)
}

func (s *StatsTracker) GetStats() map[string]ProxyStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]ProxyStats, len(s.stats))
	for name, ps := range s.stats {
		result[name] = ProxyStats{
			Name:        name,
			Upload:      atomic.LoadInt64(&ps.upload),
			Download:    atomic.LoadInt64(&ps.download),
			Connections: ps.connections.Load(),
		}
	}
	return result
}