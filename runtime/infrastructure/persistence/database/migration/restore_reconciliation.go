package migration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-orm/query"
)

// ReconcileRestoredDatabase invalidates only expired in-flight leases. It
// preserves the in-flight status so the normal idempotent claimant performs
// the retry, increments fencing tokens so a pre-restore worker cannot commit,
// and never selects terminal rows.
func ReconcileRestoredDatabase(ctx context.Context, database *sql.DB, driver, schema string, restoredAt time.Time) ([]ReconciliationResult, error) {
	if database == nil {
		return nil, fmt.Errorf("restore reconciliation database is required")
	}
	if restoredAt.IsZero() {
		return nil, fmt.Errorf("restore reconciliation timestamp is required")
	}
	parsed, err := ormdialect.Parse(driver)
	if err != nil {
		return nil, fmt.Errorf("restore reconciliation driver %q is unsupported: %w", driver, err)
	}
	dialect, err := ormdialect.New(parsed.Name())
	if err != nil {
		return nil, err
	}
	driverName := string(parsed.Name())
	schema, err = reconciliationSchema(ctx, database, driverName, schema)
	if err != nil {
		return nil, err
	}
	renderer := dialect.WithSchema(schema)
	restoredAtMillis := restoredAt.UTC().UnixMilli()
	plans := RestoreReconciliationPlan()
	results := make([]ReconciliationResult, 0, len(plans))
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin restore reconciliation: %w", err)
	}
	defer tx.Rollback()
	for _, plan := range plans {
		exists, err := reconciliationTableExists(ctx, tx, driverName, schema, plan.Table)
		if err != nil {
			return nil, err
		}
		if !exists {
			results = append(results, ReconciliationResult{Table: plan.Table, Action: plan.Action, Skipped: true})
			continue
		}
		predicate := query.And(
			query.NotEqual("lease_expires_at", int64(0)),
			query.LessThanOrEqual("lease_expires_at", restoredAtMillis),
		)
		switch plan.Table {
		case "_automation_runs":
			predicate = query.And(predicate, query.Equal("run_kind", "instruction"), query.Equal("status", "processing"))
		case "_operations":
			predicate = query.And(predicate, query.In("owner", "action", "dispatch", "record", "workflow"), query.Equal("status", "started"))
		case "_publication_outbox":
			predicate = query.And(predicate, query.Equal("status", "sending"))
		default:
			return nil, fmt.Errorf("restore reconciliation table %s has no executable contract", plan.Table)
		}
		statement, arguments, err := query.NewUpdateBuilder(renderer, plan.Table).
			Set("lease_owner", "").Set("lease_expires_at", int64(0)).
			SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
			Set("updated_at", restoredAtMillis).Where(predicate).Build()
		if err != nil {
			return nil, fmt.Errorf("build restore reconciliation for %s: %w", plan.Table, err)
		}
		result, err := tx.ExecContext(ctx, statement, arguments...)
		if err != nil {
			return nil, fmt.Errorf("execute restore reconciliation for %s: %w", plan.Table, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read restore reconciliation result for %s: %w", plan.Table, err)
		}
		results = append(results, ReconciliationResult{Table: plan.Table, Action: plan.Action, RowsAffected: rows})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit restore reconciliation: %w", err)
	}
	return results, nil
}

func reconciliationSchema(ctx context.Context, database *sql.DB, driver, configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	switch driver {
	case "postgres":
		if configured == "" {
			return "public", nil
		}
	case "mysql":
		if configured == "" {
			if err := database.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&configured); err != nil {
				return "", fmt.Errorf("resolve restore MySQL database: %w", err)
			}
			if configured == "" {
				return "", fmt.Errorf("restore MySQL database is empty")
			}
		}
	}
	return configured, nil
}

func reconciliationTableExists(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, driver, schema, table string) (bool, error) {
	var count int
	var err error
	switch driver {
	case "sqlite":
		err = queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	case "postgres":
		err = queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2`, schema, table).Scan(&count)
	case "mysql":
		err = queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=? AND table_name=?`, schema, table).Scan(&count)
	default:
		return false, fmt.Errorf("restore reconciliation driver %q is unsupported", driver)
	}
	if err != nil {
		return false, fmt.Errorf("inspect restore reconciliation table %s: %w", table, err)
	}
	return count == 1, nil
}
