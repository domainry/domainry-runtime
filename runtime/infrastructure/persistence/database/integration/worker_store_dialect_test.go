package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestIntegrationWorkerLeaseContractAcrossDialects(t *testing.T) {
	cases := []struct{ name, driver, dsnEnv string }{
		{name: "sqlite", driver: "sqlite"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: test.driver, DatabaseDSN: os.Getenv(test.dsnEnv), IntegrationSecretKey: "dialect-test-secret"}
			if test.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "integration-outbox-lease.db")
			} else if cfg.DatabaseDSN == "" {
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			store, err := database.OpenContext(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
				t.Fatal(err)
			}
			id := fmt.Sprintf("integration-%s-%d", test.name, time.Now().UnixNano())
			assertIntegrationEventDialectLease(t, store, id+"-event")
			assertIntegrationOutboxDialectLease(t, store, id+"-outbox")
		})
	}
}

func assertIntegrationEventDialectLease(t *testing.T, store *database.RuntimeStore, id string) {
	t.Helper()
	workspace := id
	event, _, err := NewIntegrationEventStore(store).UpsertEvent(t.Context(), workspace, integrationmodel.IntegrationEvent{ID: id, WorkspaceID: workspace, Provider: "webhook", EventType: "updated", ExternalID: id + "-external", Status: "received", Payload: map[string]any{}, ReceivedAt: "2026-07-19T00:00:00Z", UpdatedAt: "2026-07-19T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(t.Context(), "DELETE FROM "+store.TableIdentifier("integration_events")+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), workspace)
	})
	repository := NewIntegrationWorkerStore(store)
	start := make(chan struct{})
	winners := make(chan integrationmodel.IntegrationEvent, 100)
	errorsFound := make(chan error, 100)
	var group sync.WaitGroup
	for index := range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			claimed, won, claimErr := repository.ClaimEvent(t.Context(), workspace, event.ID, fmt.Sprintf("runtime-%d", index), "2026-07-19T00:00:00Z")
			if claimErr != nil {
				errorsFound <- claimErr
			} else if won {
				winners <- claimed
			}
		}()
	}
	close(start)
	group.Wait()
	close(winners)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	var first integrationmodel.IntegrationEvent
	count := 0
	for winner := range winners {
		first, count = winner, count+1
	}
	if count != 1 || first.FencingToken != 1 {
		t.Fatalf("integration event winners=%d first=%#v", count, first)
	}
	second, won, err := repository.ClaimEvent(t.Context(), workspace, event.ID, "runtime-restarted", "2026-07-19T00:06:00Z")
	if err != nil || !won || second.FencingToken != 2 {
		t.Fatalf("integration event reclaim=%#v won=%v err=%v", second, won, err)
	}
	if _, err := repository.UpdateEventStatus(t.Context(), workspace, event.ID, first.LeaseOwner, first.FencingToken, "processed", "", "2026-07-19T00:06:00Z"); err == nil {
		t.Fatal("stale integration event worker completed reclaimed event")
	}
	if _, err := repository.UpdateEventStatus(t.Context(), workspace, event.ID, second.LeaseOwner, second.FencingToken, "processed", "", "2026-07-19T00:06:00Z"); err != nil {
		t.Fatalf("current integration event worker complete: %v", err)
	}
}

func assertIntegrationOutboxDialectLease(t *testing.T, store *database.RuntimeStore, id string) {
	t.Helper()
	workspace := id
	message, err := NewIntegrationDeliveryStore(store).InsertOutbox(t.Context(), workspace, integrationmodel.IntegrationOutboxMessage{ID: id, WorkspaceID: workspace, ConnectorKey: "webhook", Operation: "notify", Status: "queued", Payload: map[string]any{}, CreatedAt: "2026-07-19T00:00:00Z", UpdatedAt: "2026-07-19T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(t.Context(), "DELETE FROM "+store.TableIdentifier("runtime_publication_outbox")+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), workspace)
	})
	repository := NewIntegrationWorkerStore(store)
	start := make(chan struct{})
	winners := make(chan integrationmodel.IntegrationOutboxMessage, 100)
	errorsFound := make(chan error, 100)
	var group sync.WaitGroup
	for index := range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			claimed, won, claimErr := repository.ClaimOutbox(t.Context(), workspace, message.ID, fmt.Sprintf("runtime-%d", index), "2026-07-19T00:00:00Z")
			if claimErr != nil {
				errorsFound <- claimErr
			} else if won {
				winners <- claimed
			}
		}()
	}
	close(start)
	group.Wait()
	close(winners)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	var first integrationmodel.IntegrationOutboxMessage
	count := 0
	for winner := range winners {
		first, count = winner, count+1
	}
	if count != 1 || first.FencingToken != 1 {
		t.Fatalf("integration outbox winners=%d first=%#v", count, first)
	}
	second, won, err := repository.ClaimOutbox(t.Context(), workspace, message.ID, "runtime-restarted", "2026-07-19T00:06:00Z")
	if err != nil || !won || second.FencingToken != 2 {
		t.Fatalf("integration outbox reclaim=%#v won=%v err=%v", second, won, err)
	}
	if _, err := repository.UpdateOutboxStatus(t.Context(), workspace, message.ID, first.LeaseOwner, first.FencingToken, "sent", "stale", "", "", "2026-07-19T00:06:00Z"); err == nil {
		t.Fatal("stale integration outbox worker completed reclaimed message")
	}
	if _, err := repository.UpdateOutboxStatus(t.Context(), workspace, message.ID, second.LeaseOwner, second.FencingToken, "sent", "current", "", "", "2026-07-19T00:06:00Z"); err != nil {
		t.Fatalf("current integration outbox worker complete: %v", err)
	}
}
