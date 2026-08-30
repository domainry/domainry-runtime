package ratelimit

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// TestRateLimiterConcurrentContractAcrossRealDialects is opt-in for MySQL and
// PostgreSQL. CI can require both by setting RUNTIME_REQUIRE_REAL_DIALECTS=1.
func TestRateLimiterConcurrentContractAcrossRealDialects(t *testing.T) {
	for _, test := range []struct{ name, driver, dsnEnv string }{
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(test.dsnEnv))
			if dsn == "" {
				if os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS") == "1" {
					t.Fatalf("%s is required when RUNTIME_REQUIRE_REAL_DIALECTS=1", test.dsnEnv)
				}
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: test.driver, DatabaseDSN: dsn})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.EnsureRateLimitSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			key := fmt.Sprintf("rate-limit-real-%s-%d", test.name, time.Now().UTC().UnixNano())
			defer func() {
				_, _ = store.DB().ExecContext(t.Context(), "DELETE FROM "+store.TableIdentifier("runtime_rate_limit_bucket")+" WHERE "+store.Identifier("bucket_key")+" = "+store.Placeholder(1), key)
			}()
			assertConcurrentRateLimitCounts(t, store, key)
		})
	}
}

func assertConcurrentRateLimitCounts(t *testing.T, store *database.RuntimeStore, key string) {
	t.Helper()
	const requests = 16
	counts := make(chan int, requests)
	errorsFound := make(chan error, requests)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range requests {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			decision, err := NewRateLimiter(store).Allow(t.Context(), key, requests, time.Minute)
			if err != nil {
				errorsFound <- err
				return
			}
			counts <- decision.Count
		}()
	}
	close(start)
	group.Wait()
	close(counts)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	values := make([]int, 0, requests)
	for count := range counts {
		values = append(values, count)
	}
	sort.Ints(values)
	if len(values) != requests {
		t.Fatalf("counts=%v", values)
	}
	for index, count := range values {
		if count != index+1 {
			t.Fatalf("counts=%v", values)
		}
	}
}
