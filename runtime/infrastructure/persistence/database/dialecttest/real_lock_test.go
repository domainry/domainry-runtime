package dialecttest_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/domainry/domainry-foundation/mutation"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type realTransactionDialect struct {
	name   string
	driver string
	dsnEnv string
}

func realTransactionDialects() []realTransactionDialect {
	return []realTransactionDialect{
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
	}
}

// TestRealDialectSerializableLockContracts is opt-in so normal unit runs do not
// require local databases. CI or local verification supplies both DSNs.
func TestRealDialectSerializableLockContracts(t *testing.T) {
	cases := []struct {
		name       string
		driver     string
		dsnEnv     string
		lockConfig string
		isolation  string
	}{
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN", lockConfig: "SET innodb_lock_wait_timeout = 1", isolation: "SELECT ISOLATION_LEVEL FROM performance_schema.events_transactions_current WHERE THREAD_ID = PS_CURRENT_THREAD_ID()"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN", lockConfig: "SET LOCAL lock_timeout = '1s'", isolation: "SHOW transaction_isolation"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(testCase.dsnEnv))
			if dsn == "" {
				t.Skip(testCase.dsnEnv + " is not configured")
			}
			db, err := sql.Open(testCase.driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.PingContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), "DROP TABLE IF EXISTS runtime_lock_contract"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), "CREATE TABLE runtime_lock_contract (id VARCHAR(64) PRIMARY KEY, value_text VARCHAR(64) NOT NULL)"); err != nil {
				t.Fatal(err)
			}
			defer db.ExecContext(t.Context(), "DROP TABLE IF EXISTS runtime_lock_contract")
			if _, err := db.ExecContext(t.Context(), "INSERT INTO runtime_lock_contract (id, value_text) VALUES ('one', 'initial')"); err != nil {
				t.Fatal(err)
			}

			owner, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Rollback()
			var isolation string
			if err := owner.QueryRowContext(t.Context(), testCase.isolation).Scan(&isolation); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.ToLower(isolation), "serializable") {
				t.Fatalf("transaction isolation=%q", isolation)
			}
			var value string
			if err := owner.QueryRowContext(t.Context(), "SELECT value_text FROM runtime_lock_contract WHERE id = 'one' FOR UPDATE").Scan(&value); err != nil {
				t.Fatal(err)
			}

			contender, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
			if err != nil {
				t.Fatal(err)
			}
			defer contender.Rollback()
			if _, err := contender.ExecContext(t.Context(), testCase.lockConfig); err != nil {
				t.Fatal(err)
			}
			_, lockErr := contender.ExecContext(t.Context(), "UPDATE runtime_lock_contract SET value_text = 'contender' WHERE id = 'one'")
			if lockErr == nil {
				t.Fatal("expected a real row-lock conflict")
			}
			mapped := MutationTransactionError(lockErr, "runtime_lock_contract", "one")
			if !mutation.IsTransactionTransient(mapped, mutation.TransactionTransientLockTimeout) {
				t.Fatalf("lock error was not mapped to lock-timeout transient failure: raw=%v mapped=%v", lockErr, mapped)
			}
			_ = contender.Rollback()
			if err := owner.Commit(); err != nil {
				t.Fatal(err)
			}
			_, duplicateErr := db.ExecContext(t.Context(), "INSERT INTO runtime_lock_contract (id, value_text) VALUES ('one', 'duplicate')")
			if duplicateErr == nil {
				t.Fatal("expected a real duplicate primary-key conflict")
			}
			mapped = MutationConstraintError(duplicateErr, "runtime_lock_contract", "one", mutation.MutationConflictUnique)
			if !mutation.IsMutationConflict(mapped, mutation.MutationConflictUnique) {
				t.Fatalf("duplicate error was not mapped to unique conflict: raw=%v mapped=%v", duplicateErr, mapped)
			}
		})
	}
}

func TestRealDialectDeadlockContracts(t *testing.T) {
	for _, dialect := range realTransactionDialects() {
		t.Run(dialect.name, func(t *testing.T) {
			db := openRealTransactionDatabase(t, dialect)
			resetRealTransactionTable(t, db)
			txA := beginSerializableTransaction(t, db)
			txB := beginSerializableTransaction(t, db)
			if _, err := txA.ExecContext(t.Context(), "UPDATE runtime_lock_contract SET value_text = 'a-one' WHERE id = 'one'"); err != nil {
				t.Fatal(err)
			}
			if _, err := txB.ExecContext(t.Context(), "UPDATE runtime_lock_contract SET value_text = 'b-two' WHERE id = 'two'"); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			var wait sync.WaitGroup
			for _, attempt := range []struct {
				tx    *sql.Tx
				query string
			}{{txA, "UPDATE runtime_lock_contract SET value_text = 'a-two' WHERE id = 'two'"}, {txB, "UPDATE runtime_lock_contract SET value_text = 'b-one' WHERE id = 'one'"}} {
				wait.Add(1)
				go func(attempt struct {
					tx    *sql.Tx
					query string
				}) {
					defer wait.Done()
					<-start
					_, err := attempt.tx.ExecContext(t.Context(), attempt.query)
					if err != nil {
						_ = attempt.tx.Rollback()
					} else {
						err = attempt.tx.Commit()
					}
					results <- err
				}(attempt)
			}
			close(start)
			wait.Wait()
			close(results)
			deadlocks, successes := 0, 0
			for err := range results {
				if err == nil {
					successes++
					continue
				}
				mapped := MutationTransactionError(err, "runtime_lock_contract", "deadlock")
				if !mutation.IsTransactionTransient(mapped, mutation.TransactionTransientDeadlock) {
					t.Fatalf("real deadlock was not classified: raw=%v mapped=%v", err, mapped)
				}
				deadlocks++
			}
			if deadlocks != 1 || successes != 1 {
				t.Fatalf("deadlocks=%d successes=%d", deadlocks, successes)
			}
		})
	}
}

func TestRealDialectSerializableConflictContracts(t *testing.T) {
	for _, dialect := range realTransactionDialects() {
		t.Run(dialect.name, func(t *testing.T) {
			db := openRealTransactionDatabase(t, dialect)
			resetRealTransactionTable(t, db)
			if dialect.name == "mysql" {
				assertMySQLSerializableConflict(t, db)
				return
			}
			txA := beginSerializableTransaction(t, db)
			txB := beginSerializableTransaction(t, db)
			var valueA, valueB string
			if err := txA.QueryRowContext(t.Context(), "SELECT value_text FROM runtime_lock_contract WHERE id = 'one'").Scan(&valueA); err != nil {
				t.Fatal(err)
			}
			if err := txB.QueryRowContext(t.Context(), "SELECT value_text FROM runtime_lock_contract WHERE id = 'one'").Scan(&valueB); err != nil {
				t.Fatal(err)
			}
			if _, err := txA.ExecContext(t.Context(), "UPDATE runtime_lock_contract SET value_text = 'serial-a' WHERE id = 'one'"); err != nil {
				t.Fatal(err)
			}
			if err := txA.Commit(); err != nil {
				t.Fatal(err)
			}
			_, conflictErr := txB.ExecContext(t.Context(), "UPDATE runtime_lock_contract SET value_text = 'serial-b' WHERE id = 'one'")
			if conflictErr == nil {
				conflictErr = txB.Commit()
			} else {
				_ = txB.Rollback()
			}
			mapped := MutationTransactionError(conflictErr, "runtime_lock_contract", "serializable")
			if !mutation.IsTransactionTransient(mapped, mutation.TransactionTransientSerializationFailure) {
				t.Fatalf("real serializable conflict was not classified: values=%q/%q raw=%v mapped=%v", valueA, valueB, conflictErr, mapped)
			}
		})
	}
}

func assertMySQLSerializableConflict(t *testing.T, db *sql.DB) {
	t.Helper()
	txA := beginSerializableTransaction(t, db)
	txB := beginSerializableTransaction(t, db)
	for _, tx := range []*sql.Tx{txA, txB} {
		var count int
		if err := tx.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM runtime_lock_contract WHERE value_text = 'initial'").Scan(&count); err != nil || count != 2 {
			t.Fatalf("serializable snapshot count=%d err=%v", count, err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, attempt := range []struct {
		tx    *sql.Tx
		query string
	}{{txA, "UPDATE runtime_lock_contract SET value_text = 'serial-a' WHERE id = 'one'"}, {txB, "UPDATE runtime_lock_contract SET value_text = 'serial-b' WHERE id = 'two'"}} {
		wait.Add(1)
		go func(attempt struct {
			tx    *sql.Tx
			query string
		}) {
			defer wait.Done()
			<-start
			_, err := attempt.tx.ExecContext(t.Context(), attempt.query)
			if err != nil {
				_ = attempt.tx.Rollback()
			} else {
				err = attempt.tx.Commit()
			}
			results <- err
		}(attempt)
	}
	close(start)
	wait.Wait()
	close(results)
	transientFailures := 0
	for err := range results {
		if err == nil {
			continue
		}
		mapped := MutationTransactionError(err, "runtime_lock_contract", "serializable")
		// InnoDB reports this serializable snapshot/write conflict as error 1213
		// (SQLSTATE 40001), whose more specific Runtime classification is deadlock.
		if !mutation.IsTransactionTransient(mapped, mutation.TransactionTransientDeadlock) {
			t.Fatalf("real MySQL serializable conflict was not classified: raw=%v mapped=%v", err, mapped)
		}
		transientFailures++
	}
	if transientFailures != 1 {
		t.Fatalf("MySQL serializable conflict failures=%d", transientFailures)
	}
}

func openRealTransactionDatabase(t *testing.T, dialect realTransactionDialect) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(dialect.dsnEnv))
	if dsn == "" {
		t.Skip(dialect.dsnEnv + " is not configured")
	}
	db, err := sql.Open(dialect.driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}

func resetRealTransactionTable(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), "DROP TABLE IF EXISTS runtime_lock_contract"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE runtime_lock_contract (id VARCHAR(64) PRIMARY KEY, value_text VARCHAR(64) NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS runtime_lock_contract") })
	if _, err := db.ExecContext(t.Context(), "INSERT INTO runtime_lock_contract (id, value_text) VALUES ('one', 'initial'), ('two', 'initial')"); err != nil {
		t.Fatal(err)
	}
}

func beginSerializableTransaction(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
