package integration

import (
	"database/sql/driver"
	"errors"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func integrationOutboxScriptRow() integrationSQLQueryStep {
	columns := make([]string, 23)
	for index := range columns {
		columns[index] = "column"
	}
	return integrationSQLQueryStep{
		columns: columns,
		rows: [][]driver.Value{{
			"message", "default", "connector", "connection", "send", "sent", "{}",
			"", "request", "dedup", "fingerprint", "response", "", int64(0), "", "",
			"", "", "", int64(0), "worker", "2026-07-20T00:00:00Z", "2026-07-20T00:00:00Z",
		}},
	}
}

func TestIntegrationDeliveryAcknowledgementReconciliationValidationAndListFailures(t *testing.T) {
	validScope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "acknowledgement reconciliation")
	_, delivery, _ := scriptedIntegrationStores(t, &integrationSQLState{})
	if _, err := delivery.ListOverdueOutboxAcknowledgements(t.Context(), principalmodel.SystemScope{}, 1, "now"); err == nil {
		t.Fatal("invalid system scope accepted")
	}
	if _, err := delivery.ListOverdueOutboxAcknowledgements(t.Context(), validScope, 1, " "); err == nil {
		t.Fatal("blank acknowledgement cutoff accepted")
	}
	for _, limit := range []int{0, 501} {
		_, delivery, _ = scriptedIntegrationStores(t, &integrationSQLState{})
		values, err := delivery.ListOverdueOutboxAcknowledgements(t.Context(), validScope, limit, "now")
		if err != nil || len(values) != 0 {
			t.Fatalf("default limit %d values=%#v err=%v", limit, values, err)
		}
	}

	wantErr := errors.New("acknowledgement list failure")
	for _, step := range []integrationSQLQueryStep{
		{err: wantErr},
		{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}},
		{columns: []string{"bad"}, nextErr: wantErr},
	} {
		_, delivery, _ = scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{step}})
		if _, err := delivery.ListOverdueOutboxAcknowledgements(t.Context(), validScope, 1, "now"); err == nil {
			t.Fatalf("list failure ignored: %+v", step)
		}
	}
}

func TestIntegrationDeliveryAcknowledgementReconciliationMutationFailures(t *testing.T) {
	_, delivery, _ := scriptedIntegrationStores(t, &integrationSQLState{})
	if _, _, err := delivery.MarkOutboxAcknowledgementReconciliationRequired(t.Context(), "", "message", "now"); err == nil {
		t.Fatal("blank workspace accepted")
	}
	for _, identity := range [][2]string{{"", "now"}, {"message", ""}} {
		if _, _, err := delivery.MarkOutboxAcknowledgementReconciliationRequired(t.Context(), "default", identity[0], identity[1]); err == nil {
			t.Fatalf("invalid acknowledgement identity accepted: %q", identity)
		}
	}

	wantErr := errors.New("acknowledgement mutation failure")
	for _, step := range []integrationSQLExecStep{{err: wantErr}, {rowsErr: wantErr}} {
		_, delivery, _ = scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{step}})
		if _, _, err := delivery.MarkOutboxAcknowledgementReconciliationRequired(t.Context(), "default", "message", "now"); err == nil {
			t.Fatalf("mutation failure ignored: %+v", step)
		}
	}
	for _, query := range []integrationSQLQueryStep{{err: wantErr}, {}} {
		_, delivery, _ = scriptedIntegrationStores(t, &integrationSQLState{
			execSteps:  []integrationSQLExecStep{{rows: 1}},
			querySteps: []integrationSQLQueryStep{query},
		})
		if _, _, err := delivery.MarkOutboxAcknowledgementReconciliationRequired(t.Context(), "default", "message", "now"); err == nil {
			t.Fatalf("reload failure ignored: %+v", query)
		}
	}
}

func TestIntegrationDeliveryResponseReferenceAdvanceFailuresAndContention(t *testing.T) {
	wantErr := errors.New("response reference advance failure")
	validRow := integrationOutboxScriptRow()
	for _, step := range []integrationSQLExecStep{{err: wantErr}, {rowsErr: wantErr}} {
		_, delivery, _ := scriptedIntegrationStores(t, &integrationSQLState{
			execSteps:  []integrationSQLExecStep{step},
			querySteps: []integrationSQLQueryStep{validRow},
		})
		if _, _, err := delivery.UpdateOutboxStatusByResponseRef(t.Context(), "default", "connection", "response", "delivered", ""); err == nil {
			t.Fatalf("advance failure ignored: %+v", step)
		}
	}
	for _, reload := range []integrationSQLQueryStep{{err: wantErr}, {}} {
		_, delivery, _ := scriptedIntegrationStores(t, &integrationSQLState{
			execSteps:  []integrationSQLExecStep{{rows: 1}},
			querySteps: []integrationSQLQueryStep{validRow, reload},
		})
		if _, _, err := delivery.UpdateOutboxStatusByResponseRef(t.Context(), "default", "connection", "response", "delivered", ""); err == nil {
			t.Fatalf("advance reload failure ignored: %+v", reload)
		}
	}

	queries := make([]integrationSQLQueryStep, 4)
	execs := make([]integrationSQLExecStep, 4)
	for index := range queries {
		queries[index] = validRow
		execs[index] = integrationSQLExecStep{rows: 0}
	}
	_, delivery, _ := scriptedIntegrationStores(t, &integrationSQLState{execSteps: execs, querySteps: queries})
	if _, found, err := delivery.UpdateOutboxStatusByResponseRef(t.Context(), "default", "connection", "response", "delivered", ""); err == nil || !found {
		t.Fatalf("contention result found=%v err=%v", found, err)
	}
}
