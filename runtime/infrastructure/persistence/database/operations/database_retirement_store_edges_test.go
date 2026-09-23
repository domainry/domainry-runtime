package operations

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func retirementQueryStep(retirement operationsmodel.DatabaseRetirement) operationsSQLQueryStep {
	raw, _ := json.Marshal(retirement)
	return operationsSQLQueryStep{columns: []string{"retirement_json"}, rows: [][]driver.Value{{string(raw)}}}
}

func TestDatabaseRetirementStoreSQLStages(t *testing.T) {
	now := time.Now().UTC()
	retirement := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "legacy"})
	for _, step := range []operationsSQLExecStep{{err: errOperationsSQL}, {rowsErr: errOperationsSQL}} {
		store := scriptedOperationsStore(t, &operationsSQLState{execSteps: []operationsSQLExecStep{step}})
		if _, err := store.RegisterDatabaseRetirement(t.Context(), retirement); !errors.Is(err, errOperationsSQL) {
			t.Fatalf("register error=%v", err)
		}
		store = scriptedOperationsStore(t, &operationsSQLState{execSteps: []operationsSQLExecStep{step}})
		if _, err := store.TransitionDatabaseRetirement(t.Context(), retirement, operationsmodel.DatabaseRetirementQuarantined); !errors.Is(err, errOperationsSQL) {
			t.Fatalf("transition error=%v", err)
		}
	}

	store := scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{}}})
	if _, found, err := store.GetDatabaseRetirement(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	for _, step := range []operationsSQLQueryStep{{err: errOperationsSQL}, {columns: []string{"retirement_json"}, rows: [][]driver.Value{{"{"}}}} {
		store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{step}})
		if _, _, err := store.GetDatabaseRetirement(t.Context(), retirement.ID); err == nil {
			t.Fatal("invalid get stage accepted")
		}
	}

	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if _, err := store.ListDatabaseRetirements(t.Context(), operationsmodel.DatabaseRetirementQuarantined, 0); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("list query error=%v", err)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{
		{columns: []string{"retirement_json", "extra"}, rows: [][]driver.Value{{"{}", "extra"}}},
	}})
	if _, err := store.ListDatabaseRetirements(t.Context(), "", 501); err == nil {
		t.Fatal("list scan error swallowed")
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{
		{columns: []string{"retirement_json"}, rows: [][]driver.Value{{"{"}}},
	}})
	if _, err := store.ListDatabaseRetirements(t.Context(), "", 10); err == nil {
		t.Fatal("list JSON error swallowed")
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{
		{columns: []string{"retirement_json"}, nextErr: errOperationsSQL},
	}})
	if _, err := store.ListDatabaseRetirements(t.Context(), "", 10); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("list terminal error=%v", err)
	}
}

func TestDatabaseRetirementAccessSQLStages(t *testing.T) {
	now := time.Now().UTC()
	retirement := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "legacy"})
	retirement.Evidence.Observation.SourceCounts = nil
	for _, test := range []struct {
		name       string
		state      operationsSQLState
		wantSQLErr bool
	}{
		{name: "begin", state: operationsSQLState{beginErr: errOperationsSQL}, wantSQLErr: true},
		{
			name:       "query",
			state:      operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}},
			wantSQLErr: true,
		},
		{
			name: "decode",
			state: operationsSQLState{querySteps: []operationsSQLQueryStep{
				{columns: []string{"retirement_json"}, rows: [][]driver.Value{{"{"}}},
			}},
		},
		{
			name: "update",
			state: operationsSQLState{
				querySteps: []operationsSQLQueryStep{retirementQueryStep(retirement)},
				execSteps:  []operationsSQLExecStep{{err: errOperationsSQL}},
			},
			wantSQLErr: true,
		},
		{
			name: "commit",
			state: operationsSQLState{
				querySteps: []operationsSQLQueryStep{retirementQueryStep(retirement)},
				commitErr:  errOperationsSQL,
			},
			wantSQLErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := scriptedOperationsStore(t, &test.state)
			err := store.RecordDatabaseRetirementAccess(t.Context(), retirement.ID, "read", "runtime", now)
			if err == nil || test.wantSQLErr && !errors.Is(err, errOperationsSQL) {
				t.Fatalf("error=%v wantSQLErr=%v", err, test.wantSQLErr)
			}
		})
	}
	store := scriptedOperationsStore(t, &operationsSQLState{})
	for _, input := range []struct {
		operation string
		source    string
		at        time.Time
	}{{"delete", "runtime", now}, {"read", "unknown", now}, {"write", "runtime", time.Time{}}} {
		if err := store.RecordDatabaseRetirementAccess(t.Context(), retirement.ID, input.operation, input.source, input.at); err == nil {
			t.Fatalf("invalid access accepted: %+v", input)
		}
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{retirementQueryStep(retirement)}})
	if err := store.RecordDatabaseRetirementAccess(t.Context(), retirement.ID, "write", "worker", now); err != nil {
		t.Fatalf("write access rejected: %v", err)
	}
	retirement.Evidence.Observation.SourceCounts = map[string]uint64{"runtime": 1}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{retirementQueryStep(retirement)}})
	if err := store.RecordDatabaseRetirementAccess(t.Context(), retirement.ID, "read", "runtime", now); err != nil {
		t.Fatalf("existing source counts rejected: %v", err)
	}
}
