package dialecttest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

func TestIdempotencyClaimIsAtomicAcrossSQLDialects(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		dsnEnv string
	}{
		{name: "sqlite", driver: "sqlite"},
		{name: "postgres", driver: "postgres", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: testCase.driver, IntegrationSecretKey: "dialect-test-secret"}
			if testCase.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "idempotency.db")
			} else {
				cfg.DatabaseDSN = strings.TrimSpace(os.Getenv(testCase.dsnEnv))
				if cfg.DatabaseDSN == "" {
					t.Skip(testCase.dsnEnv + " is not configured")
				}
			}
			store, err := database.OpenContext(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}

			repository := recordpersistence.NewRecordStore(store)
			now := time.Now().UTC()
			key := fmt.Sprintf("dialect-atomic-%d", now.UnixNano())
			var acquired atomic.Int64
			errorsFound := make(chan error, 100)
			var wait sync.WaitGroup
			for worker := 0; worker < 100; worker++ {
				wait.Add(1)
				go func(worker int) {
					defer wait.Done()
					result, claimErr := repository.TryBeginRecordMutation(t.Context(), recordmodel.RecordMutationClaimRequest{
						Execution:          recordmodel.RecordMutationExecution{WorkspaceID: key, Operation: "create", ObjectKey: "customer", IdempotencyKey: key},
						RequestFingerprint: "same-fingerprint", LeaseOwner: fmt.Sprintf("runtime-%d", worker), LeaseTTL: time.Minute, Now: now,
					})
					if claimErr != nil {
						errorsFound <- claimErr
						return
					}
					if result.Decision == idempotency.DecisionAcquired {
						acquired.Add(1)
					} else if result.Decision != idempotency.DecisionInProgress {
						errorsFound <- fmt.Errorf("unexpected decision %q", result.Decision)
					}
				}(worker)
			}
			wait.Wait()
			close(errorsFound)
			for claimErr := range errorsFound {
				t.Fatal(claimErr)
			}
			if acquired.Load() != 1 {
				t.Fatalf("acquired=%d want=1", acquired.Load())
			}
		})
	}
}
