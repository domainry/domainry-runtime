package dialecttest_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresPoolSoakHasNoLeakIdleTransactionsOrRetryStorm(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("RUNTIME_POSTGRES_TEST_DSN is not configured")
	}
	connectionConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	connectionConfig.RuntimeParams["application_name"] = "domainry_pool_soak"
	db := sql.OpenDB(stdlib.GetConnector(*connectionConfig))
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(250 * time.Millisecond)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `DROP TABLE IF EXISTS runtime_pool_soak; CREATE TABLE runtime_pool_soak (worker_id INTEGER NOT NULL, sequence_no INTEGER NOT NULL, PRIMARY KEY (worker_id, sequence_no))`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS runtime_pool_soak`) })

	const workers, operationsPerWorker = 32, 100
	var attempts atomic.Int64
	errorsCh := make(chan error, workers)
	var group sync.WaitGroup
	for workerID := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for sequence := range operationsPerWorker {
				attempts.Add(1)
				tx, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
				if err != nil {
					errorsCh <- err
					return
				}
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO runtime_pool_soak (worker_id, sequence_no) VALUES ($1, $2)`, workerID, sequence); err != nil {
					_ = tx.Rollback()
					errorsCh <- err
					return
				}
				if err := tx.Commit(); err != nil {
					errorsCh <- err
					return
				}
			}
		}()
	}
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	if attempts.Load() != workers*operationsPerWorker {
		t.Fatalf("unexpected retry amplification: attempts=%d operations=%d", attempts.Load(), workers*operationsPerWorker)
	}
	var rows int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runtime_pool_soak`).Scan(&rows); err != nil || rows != workers*operationsPerWorker {
		t.Fatalf("soak row count=%d err=%v", rows, err)
	}
	var idleInTransaction int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pg_stat_activity WHERE application_name = 'domainry_pool_soak' AND state = 'idle in transaction'`).Scan(&idleInTransaction); err != nil {
		t.Fatal(err)
	}
	if idleInTransaction != 0 {
		t.Fatalf("soak left %d idle-in-transaction sessions", idleInTransaction)
	}
	if stats := db.Stats(); stats.InUse != 0 || stats.OpenConnections > 8 {
		t.Fatalf("pool leaked after soak: %+v", stats)
	}
	db.SetMaxIdleConns(0)
	deadline := time.Now().Add(2 * time.Second)
	for db.Stats().OpenConnections > 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if stats := db.Stats(); stats.InUse != 0 || stats.OpenConnections > 1 {
		t.Fatalf("pool did not reclaim idle connections: %+v", stats)
	}
}
