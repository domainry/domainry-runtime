package resilience

import (
	"context"
	"sync"
	"time"
)

const DefaultMemoryIdleTTL = 30 * time.Minute

type Stats struct {
	Entries     int
	Capacity    int
	Evictions   uint64
	Expirations uint64
}

type state struct {
	failureCount int
	openUntil    time.Time
	lastAttempt  time.Time
	windowStart  time.Time
	windowCount  int
	lastSeen     time.Time
}

// MemoryStore provides per-instance protection only. Production deployments
// that require cluster-wide decisions inject a shared Store implementation.
type MemoryStore struct {
	mu          sync.Mutex
	states      map[string]state
	capacity    int
	idleTTL     time.Duration
	evictions   uint64
	expirations uint64
}

func NewMemoryStore(capacity int) *MemoryStore {
	if capacity <= 0 {
		capacity = DefaultMemoryCapacity
	}
	return &MemoryStore{states: map[string]state{}, capacity: capacity, idleTTL: DefaultMemoryIdleTTL}
}

func (*MemoryStore) Semantics() string { return SemanticsInstanceLocal }

func (s *MemoryStore) Before(ctx context.Context, key string, config Config, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpired(now)
	current := s.states[key]
	if now.Before(current.openUntil) {
		return ErrCircuitOpen
	}
	if config.MinInterval > 0 && !current.lastAttempt.IsZero() && now.Sub(current.lastAttempt) < config.MinInterval {
		return ErrRateLimited
	}
	if config.RateLimit > 0 {
		window := config.RateWindow
		if window <= 0 {
			window = time.Minute
		}
		if current.windowStart.IsZero() || now.Sub(current.windowStart) >= window {
			current.windowStart, current.windowCount = now, 0
		}
		if current.windowCount >= config.RateLimit {
			return ErrRateLimited
		}
		current.windowCount++
	}
	current.lastSeen = now
	s.put(key, current)
	return nil
}

func (s *MemoryStore) Record(ctx context.Context, key string, config Config, success bool, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpired(now)
	current := s.states[key]
	current.lastAttempt, current.lastSeen = now, now
	if success {
		current.failureCount = 0
		current.openUntil = time.Time{}
	} else {
		current.failureCount++
		threshold := config.FailureThreshold
		if threshold > 0 && current.failureCount >= threshold {
			cooldown := config.Cooldown
			if cooldown <= 0 {
				cooldown = time.Minute
			}
			current.openUntil = now.Add(cooldown)
		}
	}
	s.put(key, current)
	return nil
}

func (s *MemoryStore) put(key string, value state) {
	if _, exists := s.states[key]; !exists && len(s.states) >= s.capacity {
		oldestKey := ""
		var oldest time.Time
		for candidate, current := range s.states {
			if oldestKey == "" || current.lastSeen.Before(oldest) {
				oldestKey, oldest = candidate, current.lastSeen
			}
		}
		if oldestKey != "" {
			delete(s.states, oldestKey)
			s.evictions++
		}
	}
	s.states[key] = value
}

func (s *MemoryStore) evictExpired(now time.Time) {
	if s.idleTTL <= 0 {
		return
	}
	for key, current := range s.states {
		if !current.lastSeen.IsZero() && now.Sub(current.lastSeen) >= s.idleTTL && !now.Before(current.openUntil) {
			delete(s.states, key)
			s.expirations++
		}
	}
}

func (s *MemoryStore) Stats() Stats {
	if s == nil {
		return Stats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{Entries: len(s.states), Capacity: s.capacity, Evictions: s.evictions, Expirations: s.expirations}
}
