package operations

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func breakGlassFixture(now time.Time) operationsmodel.OperationsBreakGlassGrant {
	return operationsmodel.OperationsBreakGlassGrant{
		ID: "grant", WorkspaceID: "workspace", State: operationsmodel.OperationsBreakGlassActive, ActorID: "actor",
		ApproverIDs: []string{"approver"}, Reason: "reason", IncidentRef: "incident", AlertTarget: "security",
		AuditEventID: "audit", ExpiresAt: now.Add(time.Hour), Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func breakGlassQueryStep(grant operationsmodel.OperationsBreakGlassGrant) operationsSQLQueryStep {
	approvers, _ := json.Marshal(grant.ApproverIDs)
	revokedAt := ""
	if grant.RevokedAt != nil {
		revokedAt = grant.RevokedAt.Format(time.RFC3339Nano)
	}
	return operationsSQLQueryStep{columns: operationsBreakGlassColumns(), rows: [][]driver.Value{{grant.ID, grant.WorkspaceID, string(grant.State), grant.ActorID, string(approvers), grant.Reason, grant.IncidentRef, grant.AlertTarget, grant.AuditEventID, grant.ExpiresAt.Format(time.RFC3339Nano), grant.Revision, grant.CreatedAt.Format(time.RFC3339Nano), grant.UpdatedAt.Format(time.RFC3339Nano), revokedAt, grant.RevokedBy, grant.RevocationNote}}}
}

func TestOperationsBreakGlassSQLStages(t *testing.T) {
	now := time.Now().UTC()
	grant := breakGlassFixture(now)
	for _, test := range []struct {
		name  string
		state operationsSQLState
	}{
		{name: "begin", state: operationsSQLState{beginErr: errOperationsSQL}},
		{name: "active-query", state: operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}}},
		{name: "active-existing", state: operationsSQLState{
			querySteps: []operationsSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}},
		}},
		{name: "insert", state: operationsSQLState{querySteps: []operationsSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}, execSteps: []operationsSQLExecStep{{err: errOperationsSQL}}}},
		{name: "commit", state: operationsSQLState{querySteps: []operationsSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}, commitErr: errOperationsSQL}},
	} {
		t.Run("create-"+test.name, func(t *testing.T) {
			store := scriptedOperationsStore(t, &test.state)
			created, err := store.CreateOperationsBreakGlass(t.Context(), grant)
			if test.name == "active-existing" {
				if err != nil || created {
					t.Fatalf("created=%v err=%v", created, err)
				}
			} else if !errors.Is(err, errOperationsSQL) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	zero := OperationsStore{}
	if _, err := zero.CreateOperationsBreakGlass(t.Context(), grant); err == nil {
		t.Fatal("unavailable create succeeded")
	}
	if _, _, err := zero.GetOperationsBreakGlass(t.Context(), grant.ID); err == nil {
		t.Fatal("unavailable get succeeded")
	}

	store := scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{}}})
	if _, found, err := store.GetOperationsBreakGlass(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if _, _, err := store.GetOperationsBreakGlass(t.Context(), grant.ID); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("get error=%v", err)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if _, err := store.ListOperationsBreakGlass(t.Context(), "workspace", 0); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("list error=%v", err)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{columns: operationsBreakGlassColumns(), rows: [][]driver.Value{{"short"}}}}})
	if _, err := store.ListOperationsBreakGlass(t.Context(), "workspace", 101); err == nil {
		t.Fatal("list scan failure swallowed")
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{columns: operationsBreakGlassColumns(), nextErr: errOperationsSQL}}})
	if _, err := store.ListOperationsBreakGlass(t.Context(), "workspace", 10); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("terminal error=%v", err)
	}

	for _, step := range []operationsSQLExecStep{{err: errOperationsSQL}, {rowsErr: errOperationsSQL}} {
		store = scriptedOperationsStore(t, &operationsSQLState{execSteps: []operationsSQLExecStep{step}})
		if _, err := store.RevokeOperationsBreakGlass(t.Context(), grant, 1); !errors.Is(err, errOperationsSQL) {
			t.Fatalf("revoke error=%v", err)
		}
	}
	revoked := now.Add(time.Minute)
	grant.RevokedAt = &revoked
	if parsed, err := operationsScanBreakGlass(scannerValues(breakGlassQueryStep(grant).rows[0])); err != nil || parsed.RevokedAt == nil {
		t.Fatalf("grant=%+v err=%v", parsed, err)
	}
	for _, index := range []int{4, 9, 11, 12, 13} {
		row := append([]driver.Value(nil), breakGlassQueryStep(grant).rows[0]...)
		row[index] = "invalid"
		if _, err := operationsScanBreakGlass(scannerValues(row)); err == nil {
			t.Fatalf("corrupt field %d accepted", index)
		}
	}
}

func controlFixture(now time.Time) operationsmodel.OperationsControl {
	return operationsmodel.OperationsControl{SystemPurpose: "maintenance", Kind: operationsmodel.OperationsControlKind("scheduler"), Owner: "owner", State: operationsmodel.OperationsControlState("enabled"), Reason: "reason", Reference: "ref", UpdatedBy: "actor", Revision: 1, UpdatedAt: now}
}

func controlQueryStep(control operationsmodel.OperationsControl) operationsSQLQueryStep {
	return operationsSQLQueryStep{columns: operationsControlTestColumns(), rows: [][]driver.Value{{control.SystemPurpose, string(control.Kind), control.Owner, string(control.State), control.Reason, control.Reference, control.UpdatedBy, control.Revision, control.UpdatedAt.Format(time.RFC3339Nano)}}}
}

func operationsControlTestColumns() []string {
	return []string{"system_purpose", "control_kind", "owner", "state", "reason", "reference", "updated_by", "revision", "updated_at"}
}

func TestOperationsControlSQLStages(t *testing.T) {
	control := controlFixture(time.Now().UTC())
	zero := OperationsStore{}
	if _, _, err := zero.GetOperationsControl(t.Context(), control.SystemPurpose, control.Kind, control.Owner); err == nil {
		t.Fatal("unavailable get succeeded")
	}
	if _, err := zero.ListOperationsControls(t.Context(), control.SystemPurpose, "", 0); err == nil {
		t.Fatal("unavailable list succeeded")
	}
	if _, err := zero.PutOperationsControl(t.Context(), control, 0); err == nil {
		t.Fatal("unavailable put succeeded")
	}

	store := scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{}}})
	if _, found, err := store.GetOperationsControl(t.Context(), control.SystemPurpose, control.Kind, control.Owner); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if _, _, err := store.GetOperationsControl(t.Context(), control.SystemPurpose, control.Kind, control.Owner); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("get error=%v", err)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if _, err := store.ListOperationsControls(t.Context(), control.SystemPurpose, control.Kind, 201); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("list error=%v", err)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if _, err := store.ListOperationsControls(t.Context(), control.SystemPurpose, control.Kind, 0); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("zero-limit list error=%v", err)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{columns: operationsControlTestColumns(), rows: [][]driver.Value{{"short"}}}}})
	if _, err := store.ListOperationsControls(t.Context(), control.SystemPurpose, "", 10); err == nil {
		t.Fatal("scan failure swallowed")
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{columns: operationsControlTestColumns(), nextErr: errOperationsSQL}}})
	if _, err := store.ListOperationsControls(t.Context(), control.SystemPurpose, "", 10); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("terminal error=%v", err)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{})
	if _, err := store.PutOperationsControl(t.Context(), control, -1); err == nil {
		t.Fatal("negative revision accepted")
	}
	for _, test := range []struct {
		name     string
		revision int64
		state    operationsSQLState
	}{
		{name: "insert", state: operationsSQLState{execSteps: []operationsSQLExecStep{{err: errOperationsSQL}}, querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}}},
		{name: "update", revision: 1, state: operationsSQLState{execSteps: []operationsSQLExecStep{{err: errOperationsSQL}}}},
		{name: "rows", revision: 1, state: operationsSQLState{execSteps: []operationsSQLExecStep{{rowsErr: errOperationsSQL}}}},
		{name: "insert-missing-after-conflict", state: operationsSQLState{execSteps: []operationsSQLExecStep{{err: errOperationsSQL}}, querySteps: []operationsSQLQueryStep{{}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := scriptedOperationsStore(t, &test.state)
			if _, err := store.PutOperationsControl(t.Context(), control, test.revision); !errors.Is(err, errOperationsSQL) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	row := controlQueryStep(control).rows[0]
	row[8] = "invalid"
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{columns: operationsControlTestColumns(), rows: [][]driver.Value{row}}}})
	if _, _, err := store.GetOperationsControl(t.Context(), control.SystemPurpose, control.Kind, control.Owner); err == nil {
		t.Fatal("invalid control time accepted")
	}
}
