package ratelimit

import (
	"os"
	"strings"
	"testing"
)

func TestRateLimiterRuntimeOwnerDoesNotMaterializeSchema(t *testing.T) {
	source, err := os.ReadFile("rate_limiter.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CREATE TABLE", "ormschema.NewTable", "ALTER TABLE", "DROP TABLE"} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("runtime rate-limit owner contains schema mutation %q", forbidden)
		}
	}
}
