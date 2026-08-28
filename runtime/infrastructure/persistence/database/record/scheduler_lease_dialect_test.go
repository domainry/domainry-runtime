package record

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestSchedulerRecordLeaseContractAcrossDialects(t *testing.T) {
	cases := []struct{ name, driver, dsnEnv string }{
		{name: "sqlite", driver: "sqlite"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: test.driver, DatabaseDSN: os.Getenv(test.dsnEnv)}
			if test.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "scheduler-record-lease.db")
			} else if cfg.DatabaseDSN == "" {
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			store, err := database.OpenContext(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			table := fmt.Sprintf("scheduler_lease_%s_%d", test.name, time.Now().UnixNano())
			assertSchedulerRecordLeaseContract(t, store, table)
		})
	}
}

func assertSchedulerRecordLeaseContract(t *testing.T, store *database.RuntimeStore, table string) {
	t.Helper()
	columns := []string{
		store.Identifier("workspace_id") + " VARCHAR(191) NOT NULL",
		store.Identifier("id") + " VARCHAR(191) NOT NULL",
		store.Identifier("created_at") + " VARCHAR(64) NOT NULL",
		store.Identifier("updated_at") + " VARCHAR(64) NOT NULL",
		store.Identifier("status") + " VARCHAR(32)",
		store.Identifier("lease_owner") + " VARCHAR(191)",
		store.Identifier("lease_expires_at") + " VARCHAR(64)",
		store.Identifier("fencing_token") + " BIGINT",
		"PRIMARY KEY (" + store.Identifier("workspace_id") + ", " + store.Identifier("id") + ")",
	}
	if _, err := store.DB().ExecContext(t.Context(), "CREATE TABLE "+store.Identifier(table)+" ("+joinSchedulerDDL(columns)+")"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.DB().ExecContext(t.Context(), "DROP TABLE "+store.Identifier(table)) })
	object := definitionmodel.ObjectSchema{Key: table, Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "text"}, {Key: "lease_owner", Type: "text"}, {Key: "lease_expires_at", Type: "datetime"}, {Key: "fencing_token", Type: "number"},
	}}
	repository := NewRecordStore(store)
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	original := recordmodel.Record{ID: "run-1", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"status": "queued", "lease_owner": "", "lease_expires_at": "", "fencing_token": 0}}
	if err := repository.InsertRecord(t.Context(), "workspace-a", object, original); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	winners := make(chan recordmodel.Record, 100)
	errorsFound := make(chan error, 100)
	var group sync.WaitGroup
	for index := range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			candidate := recordmodel.Record{ID: original.ID, CreatedAt: original.CreatedAt, UpdatedAt: now.Add(time.Second).Format(time.RFC3339Nano), Data: map[string]any{"status": "leased", "lease_owner": fmt.Sprintf("instance-%d", index), "lease_expires_at": now.Add(time.Minute).Format(time.RFC3339Nano), "fencing_token": 1}}
			won, err := repository.UpdateRecordWhere(t.Context(), "workspace-a", object, candidate, map[string]any{"status": "queued", "fencing_token": 0})
			if err != nil {
				errorsFound <- err
			} else if won {
				winners <- candidate
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
	var first recordmodel.Record
	count := 0
	for winner := range winners {
		first, count = winner, count+1
	}
	if count != 1 {
		t.Fatalf("scheduler record lease winners=%d", count)
	}
	reclaimed := recordmodel.Record{ID: original.ID, CreatedAt: original.CreatedAt, UpdatedAt: now.Add(2 * time.Minute).Format(time.RFC3339Nano), Data: map[string]any{"status": "leased", "lease_owner": "instance-restarted", "lease_expires_at": now.Add(3 * time.Minute).Format(time.RFC3339Nano), "fencing_token": 2}}
	conditions := map[string]any{"status": "leased", "lease_owner": first.Data["lease_owner"], "fencing_token": 1}
	if won, err := repository.UpdateRecordWhere(t.Context(), "workspace-a", object, reclaimed, conditions); err != nil || !won {
		t.Fatalf("scheduler record reclaim won=%v err=%v", won, err)
	}
	stale := first
	stale.Data["status"] = "succeeded"
	if won, err := repository.UpdateRecordWhere(t.Context(), "workspace-a", object, stale, conditions); err != nil || won {
		t.Fatalf("stale scheduler terminal write won=%v err=%v", won, err)
	}
}

func joinSchedulerDDL(columns []string) string {
	result := ""
	for index, column := range columns {
		if index > 0 {
			result += ", "
		}
		result += column
	}
	return result
}
