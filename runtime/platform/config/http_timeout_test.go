package config

import (
	"testing"
	"time"
)

func TestHTTPTimeoutsLoadFromEnvironment(t *testing.T) {
	t.Setenv("HTTP_READ_HEADER_TIMEOUT", "3s")
	t.Setenv("HTTP_READ_TIMEOUT", "11s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "45s")
	t.Setenv("HTTP_IDLE_TIMEOUT", "70s")
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "9s")
	cfg := FromEnv()
	if cfg.HTTPReadHeaderTimeout != 3*time.Second || cfg.HTTPReadTimeout != 11*time.Second || cfg.HTTPWriteTimeout != 45*time.Second || cfg.HTTPIdleTimeout != 70*time.Second || cfg.HTTPShutdownTimeout != 9*time.Second {
		t.Fatalf("unexpected HTTP timeout config: %#v", cfg)
	}
}
