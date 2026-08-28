package resilience

import (
	"context"
	"errors"
	"time"
)

const (
	DefaultMemoryCapacity  = 10_000
	SemanticsInstanceLocal = "instance_local"
)

var (
	ErrCircuitOpen = errors.New("runtime policy circuit open")
	ErrRateLimited = errors.New("runtime policy rate limited")
)

type Config struct {
	FailureThreshold int
	Cooldown         time.Duration
	MinInterval      time.Duration
	RateLimit        int
	RateWindow       time.Duration
}

type Store interface {
	Before(context.Context, string, Config, time.Time) error
	Record(context.Context, string, Config, bool, time.Time) error
	Semantics() string
}
