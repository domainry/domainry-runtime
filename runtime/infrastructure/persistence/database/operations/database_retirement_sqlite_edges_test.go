package operations

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/datamigration"
)

func inspectSQLiteWithState(t *testing.T, state *operationsSQLState) error {
	t.Helper()
	db := openOperationsScriptedDB(state)
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		return err
	}
	_, err = inspectSQLiteTransaction(t.Context(), tx)
	_ = tx.Rollback()
	return err
}

func sqliteTableStep() operationsSQLQueryStep {
	return operationsSQLQueryStep{columns: []string{"name"}, rows: [][]driver.Value{{"records"}}}
}

func sqliteColumnStep() operationsSQLQueryStep {
	return operationsSQLQueryStep{columns: []string{"cid", "name", "type", "notnull", "default", "pk"}, rows: [][]driver.Value{{int64(0), "id", "TEXT", int64(1), nil, int64(1)}}}
}

func sqliteIndexStep() operationsSQLQueryStep {
	return operationsSQLQueryStep{columns: []string{"seq", "name", "unique", "origin", "partial"}, rows: [][]driver.Value{{int64(0), "idx_records", int64(1), "c", int64(0)}}}
}

func cloneRetirementInventory(value datamigration.Inventory) datamigration.Inventory {
	value.Tables = append([]datamigration.TableInventory(nil), value.Tables...)
	value.Triggers = append([]datamigration.TriggerInventory(nil), value.Triggers...)
	return value
}

func operationsQueryState(steps ...operationsSQLQueryStep) operationsSQLState {
	return operationsSQLState{querySteps: steps}
}

func TestInspectSQLiteTransactionEveryFailureStage(t *testing.T) {
	for _, test := range []struct {
		name  string
		state operationsSQLState
	}{
		{name: "tables-query", state: operationsQueryState(operationsSQLQueryStep{err: errOperationsSQL})},
		{name: "table-scan", state: operationsQueryState(operationsSQLQueryStep{columns: []string{"name"}, rows: [][]driver.Value{{nil}}})},
		{name: "columns-query", state: operationsQueryState(sqliteTableStep(), operationsSQLQueryStep{err: errOperationsSQL})},
		{name: "column-scan", state: operationsQueryState(sqliteTableStep(), operationsSQLQueryStep{columns: sqliteColumnStep().columns, rows: [][]driver.Value{{"bad"}}})},
		{name: "columns-terminal", state: operationsQueryState(sqliteTableStep(), operationsSQLQueryStep{columns: sqliteColumnStep().columns, nextErr: errOperationsSQL})},
		{name: "indexes-query", state: operationsQueryState(sqliteTableStep(), sqliteColumnStep(), operationsSQLQueryStep{err: errOperationsSQL})},
		{name: "index-scan", state: operationsQueryState(sqliteTableStep(), sqliteColumnStep(), operationsSQLQueryStep{columns: sqliteIndexStep().columns, rows: [][]driver.Value{{"bad"}}})},
		{name: "indexes-terminal", state: operationsQueryState(sqliteTableStep(), sqliteColumnStep(), operationsSQLQueryStep{columns: sqliteIndexStep().columns, nextErr: errOperationsSQL})},
		{name: "tables-terminal", state: operationsQueryState(operationsSQLQueryStep{columns: []string{"name"}, nextErr: errOperationsSQL})},
		{name: "triggers-query", state: operationsQueryState(operationsSQLQueryStep{columns: []string{"name"}}, operationsSQLQueryStep{err: errOperationsSQL})},
		{name: "trigger-scan", state: operationsQueryState(operationsSQLQueryStep{columns: []string{"name"}}, operationsSQLQueryStep{columns: []string{"name", "table", "sql"}, rows: [][]driver.Value{{"short"}}})},
		{name: "triggers-terminal", state: operationsQueryState(operationsSQLQueryStep{columns: []string{"name"}}, operationsSQLQueryStep{columns: []string{"name", "table", "sql"}, nextErr: errOperationsSQL})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := inspectSQLiteWithState(t, &test.state); err == nil {
				t.Fatal("failure stage accepted")
			}
		})
	}
	state := operationsQueryState(sqliteTableStep(), sqliteColumnStep(), sqliteIndexStep(), operationsSQLQueryStep{columns: []string{"name", "table", "sql"}, rows: [][]driver.Value{{"trg", "records", "CREATE TRIGGER"}}})
	if err := inspectSQLiteWithState(t, &state); err != nil {
		t.Fatalf("valid inventory rejected: %v", err)
	}
}

func TestVerifySQLiteColumnDropPreservationEdges(t *testing.T) {
	object := operationsmodel.DatabaseObjectIdentity{Kind: "column", ParentName: "records", Name: "obsolete"}
	base := datamigration.Inventory{Engine: datamigration.EngineSQLite, Tables: []datamigration.TableInventory{{Name: "records", Columns: []datamigration.ColumnInventory{{Name: "id"}, {Name: "obsolete"}}, Indexes: []datamigration.IndexInventory{{Name: "idx_keep", Columns: []string{"id"}}, {Name: "idx_drop", Columns: []string{"obsolete"}}}, ForeignKeys: []datamigration.ForeignInventory{{Columns: []string{"id"}}}}}, Triggers: []datamigration.TriggerInventory{{Name: "trg_keep", Table: "records", Definition: "after update id"}, {Name: "trg_drop", Table: "records", Definition: "after update obsolete"}, {Name: "other_table", Table: "other", Definition: "after update id"}}}
	after := datamigration.Inventory{Engine: datamigration.EngineSQLite, Tables: []datamigration.TableInventory{{Name: "records", Columns: []datamigration.ColumnInventory{{Name: "id"}}, Indexes: []datamigration.IndexInventory{{Name: "idx_keep", Columns: []string{"id"}}}, ForeignKeys: []datamigration.ForeignInventory{{Columns: []string{"id"}}}}}, Triggers: []datamigration.TriggerInventory{{Name: "trg_keep", Table: "records", Definition: "after update id"}}}
	original := inspectSQLiteRetirementTransaction
	defer func() { inspectSQLiteRetirementTransaction = original }()
	for _, test := range []struct {
		name   string
		before datamigration.Inventory
		after  datamigration.Inventory
		err    error
		ok     bool
	}{
		{name: "inspect", before: base, err: errOperationsSQL},
		{name: "before-missing", before: datamigration.Inventory{}, after: after},
		{name: "after-missing", before: base, after: datamigration.Inventory{}},
		{name: "column-present", before: base, after: base},
		{name: "index-missing", before: base, after: func() datamigration.Inventory {
			value := cloneRetirementInventory(after)
			value.Tables[0].Indexes = nil
			return value
		}()},
		{name: "foreign-change", before: base, after: func() datamigration.Inventory {
			value := cloneRetirementInventory(after)
			value.Tables[0].ForeignKeys = nil
			return value
		}()},
		{name: "trigger-missing", before: base, after: func() datamigration.Inventory {
			value := cloneRetirementInventory(after)
			value.Triggers = nil
			return value
		}()},
		{name: "success", before: base, after: after, ok: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspectSQLiteRetirementTransaction = func(context.Context, *sql.Tx) (datamigration.Inventory, error) { return test.after, test.err }
			err := verifySQLiteColumnDropPreservesSchema(t.Context(), nil, test.before, object)
			if test.ok && err != nil {
				t.Fatalf("error=%v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("invalid preservation accepted")
			}
		})
	}
}
