package operations

import (
	"database/sql/driver"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func leaseCountSteps(count int, live, expired int64) []operationsSQLQueryStep {
	steps := make([]operationsSQLQueryStep, count)
	for index := range steps {
		steps[index] = operationsSQLQueryStep{columns: []string{"live", "expired"}, rows: [][]driver.Value{{live, expired}}}
	}
	return steps
}

func TestOperationsDiagnosticsFailureAndPaginationEdges(t *testing.T) {
	now := time.Now().UTC()
	request := operationsmodel.OperationsDiagnosticsRequest{Now: now, Page: 1, PageSize: 1, InstanceID: "instance", WorkspaceID: "workspace"}
	zero := OperationsStore{}
	if _, err := zero.OperationsDiagnosticsSnapshot(t.Context(), request); err == nil {
		t.Fatal("unavailable diagnostics succeeded")
	}

	store := scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if section := store.operationsMigrationDiagnostics(t.Context()); section.Status != "unavailable" {
		t.Fatalf("migration section=%+v", section)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{
		{columns: []string{"total", "dirty"}, rows: [][]driver.Value{{int64(1), int64(1)}}},
	}})
	if section := store.operationsMigrationDiagnostics(t.Context()); section.Status != "blocked" || len(section.Items) != 1 {
		t.Fatalf("migration section=%+v", section)
	}

	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: leaseCountSteps(len(operationsLeaseSpecs), 0, 0)})
	request.Page = 10
	if section := store.operationsLeaseDiagnostics(t.Context(), request); section.Status != "ready" || len(section.Items) != 0 {
		t.Fatalf("empty lease page=%+v", section)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: leaseCountSteps(len(operationsLeaseSpecs), 1, 0)})
	request.Page = 1
	if section := store.operationsLeaseDiagnostics(t.Context(), request); section.NextPage != 2 || len(section.Items) != 1 {
		t.Fatalf("lease page=%+v", section)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if section := store.operationsLeaseDiagnostics(t.Context(), request); section.Status != "unavailable" {
		t.Fatalf("lease failure=%+v", section)
	}

	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if section := store.operationsQueueDiagnostics(t.Context(), request); section.Status != "unavailable" {
		t.Fatalf("queue failure=%+v", section)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if section := store.operationsDLQDiagnostics(t.Context(), request); section.Status != "unavailable" {
		t.Fatalf("DLQ failure=%+v", section)
	}

	if diagnosticFailure("code").ErrorCode != "code" || ageSeconds("invalid", now) != -1 || ageSeconds(now.Format(time.RFC3339), now) < 0 || durationAge(time.Time{}, now) != -1 || durationAge(now.Add(time.Minute), now) != 0 {
		t.Fatal("diagnostic time/failure projections changed")
	}
	degraded := scriptedOperationsStore(t, &operationsSQLState{})
	degraded.readiness = func() database.DatabaseReadiness { return database.DatabaseReadiness{} }
	if section := degraded.operationsPoolDiagnostics(); section.Status != "degraded" {
		t.Fatalf("degraded pool section=%+v", section)
	}
}

func TestOperationsLeaseReleaseSQLStages(t *testing.T) {
	now := time.Now().UTC()
	request := operationsmodel.OperationsLeaseReleaseRequest{Owner: "idempotency_cleanup", ResourceID: "resource", ExpectedLeaseOwner: "instance", ExpectedFencingToken: 2, Now: now}
	store := scriptedOperationsStore(t, &operationsSQLState{})
	if _, _, err := store.ForceReleaseOperationsLease(t.Context(), operationsmodel.OperationsLeaseReleaseRequest{}); err == nil {
		t.Fatal("invalid release request accepted")
	}
	invalidOwner := request
	invalidOwner.Owner = "unknown"
	if _, _, err := store.ForceReleaseOperationsLease(t.Context(), invalidOwner); err == nil {
		t.Fatal("unknown lease owner accepted")
	}
	workspaceRequest := request
	workspaceRequest.Owner = "workflow"
	if _, _, err := store.ForceReleaseOperationsLease(t.Context(), workspaceRequest); err == nil {
		t.Fatal("workspace-scoped release without workspace accepted")
	}

	expired := now.Add(-time.Minute).Format(time.RFC3339Nano)
	validQuery := operationsSQLQueryStep{columns: []string{"lease_owner", "lease_expires_at", "fencing_token"}, rows: [][]driver.Value{{"instance", expired, int64(2)}}}
	for _, test := range []struct {
		name      string
		state     operationsSQLState
		wantError bool
	}{
		{name: "begin", state: operationsSQLState{beginErr: errOperationsSQL}, wantError: true},
		{name: "missing", state: operationsSQLState{querySteps: []operationsSQLQueryStep{{}}}},
		{name: "query", state: operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}}, wantError: true},
		{name: "expiry", state: operationsSQLState{
			querySteps: []operationsSQLQueryStep{{columns: validQuery.columns, rows: [][]driver.Value{{"instance", "invalid", int64(2)}}}},
		}, wantError: true},
		{name: "owner-mismatch", state: operationsSQLState{
			querySteps: []operationsSQLQueryStep{{columns: validQuery.columns, rows: [][]driver.Value{{"other", expired, int64(2)}}}},
		}},
		{name: "token-mismatch", state: operationsSQLState{
			querySteps: []operationsSQLQueryStep{{columns: validQuery.columns, rows: [][]driver.Value{{"instance", expired, int64(3)}}}},
		}},
		{name: "update", state: operationsSQLState{querySteps: []operationsSQLQueryStep{validQuery}, execSteps: []operationsSQLExecStep{{err: errOperationsSQL}}}, wantError: true},
		{name: "rows", state: operationsSQLState{querySteps: []operationsSQLQueryStep{validQuery}, execSteps: []operationsSQLExecStep{{rowsErr: errOperationsSQL}}}, wantError: true},
		{name: "lost", state: operationsSQLState{querySteps: []operationsSQLQueryStep{validQuery}, execSteps: []operationsSQLExecStep{{rows: 0}}}},
		{name: "commit", state: operationsSQLState{querySteps: []operationsSQLQueryStep{validQuery}, commitErr: errOperationsSQL}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := scriptedOperationsStore(t, &test.state)
			_, changed, err := store.ForceReleaseOperationsLease(t.Context(), request)
			if test.wantError {
				if err == nil {
					t.Fatal("failure stage accepted")
				}
			} else if err != nil || changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
		})
	}

	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{validQuery}})
	result, changed, err := store.ForceReleaseOperationsLease(t.Context(), request)
	if err != nil || !changed || result.NextFencingToken != 3 {
		t.Fatalf("result=%+v changed=%v err=%v", result, changed, err)
	}
	workspaceRequest.WorkspaceID = "workspace"
	workspaceStore := scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{}}})
	if _, changed, err := workspaceStore.ForceReleaseOperationsLease(t.Context(), workspaceRequest); err != nil || changed {
		t.Fatalf("missing workspace lease changed=%v err=%v", changed, err)
	}
	where, args := store.operationsLeaseReleaseIdentity(operationsLeaseReleaseSpecs["workflow"], workspaceRequest)
	if where == "" || len(args) != 2 {
		t.Fatalf("workspace identity where=%q args=%v", where, args)
	}
}

func TestOperationsLeaseSnapshotAndCountEdges(t *testing.T) {
	now := time.Now().UTC()
	zero := OperationsStore{}
	if _, err := zero.OperationsLeaseSnapshot(t.Context(), "instance", now); err == nil {
		t.Fatal("unavailable lease snapshot succeeded")
	}
	store := scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if _, err := store.OperationsLeaseSnapshot(t.Context(), "instance", now); err == nil {
		t.Fatal("lease query failure swallowed")
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{columns: []string{"live", "expired"}, rows: [][]driver.Value{{int64(2), int64(3)}}}}})
	if live, expired, err := store.operationsLeaseCounts(t.Context(), "leases", "", now); err != nil || live != 2 || expired != 3 {
		t.Fatalf("live=%d expired=%d err=%v", live, expired, err)
	}
	expiredOnly := leaseCountSteps(len(operationsLeaseSpecs), 0, 0)
	expiredOnly[0] = operationsSQLQueryStep{columns: []string{"live", "expired"}, rows: [][]driver.Value{{int64(0), int64(1)}}}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: expiredOnly})
	if snapshot, err := store.OperationsLeaseSnapshot(t.Context(), "instance", now); err != nil || snapshot.Live != 0 || snapshot.Expired != 1 || len(snapshot.Owners) != 1 {
		t.Fatalf("expired-only snapshot=%+v err=%v", snapshot, err)
	}
}
