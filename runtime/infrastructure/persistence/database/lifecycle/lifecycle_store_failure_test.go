package lifecycle

import (
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

var errLifecycleSQL = errors.New("scripted lifecycle SQL failure")

func scriptedLifecycleStore(t *testing.T, state *lifecycleSQLState) LifecycleStore {
	t.Helper()
	repository := NewLifecycleStore(openLifecycleStore(t))
	repository.db = openLifecycleScriptedDB(state)
	t.Cleanup(func() { _ = repository.db.Close() })
	return repository
}

func TestLifecycleStoreWriteFailures(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	policy := lifecyclemodel.PolicyVersion{WorkspaceID: "w", Policy: lifecyclemodel.RetentionPolicy{Key: "p"}}
	hold := lifecyclemodel.LegalHold{ID: "h", WorkspaceID: "w"}
	job := lifecyclemodel.CleanupJob{ID: "j", WorkspaceID: "w"}
	request := lifecyclemodel.SubjectRequest{ID: "r", WorkspaceID: "w"}
	registration := lifecyclemodel.DeletionRegistration{RequestID: "r", WorkspaceID: "w"}
	evidence := lifecyclemodel.AuditEvidence{ID: "a", WorkspaceID: "w"}

	t.Run("policy exec", func(t *testing.T) {
		r := scriptedLifecycleStore(t, &lifecycleSQLState{execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}})
		if r.SavePolicy(t.Context(), policy) == nil {
			t.Fatal("expected error")
		}
	})
	for _, tc := range []struct {
		name  string
		steps []lifecycleSQLExecStep
		call  func(LifecycleStore) error
	}{
		{"hold update", []lifecycleSQLExecStep{{err: errLifecycleSQL}}, func(r LifecycleStore) error { return r.SaveLegalHold(t.Context(), hold) }},
		{"hold rows", []lifecycleSQLExecStep{{rowsErr: errLifecycleSQL}}, func(r LifecycleStore) error { return r.SaveLegalHold(t.Context(), hold) }},
		{"hold insert", []lifecycleSQLExecStep{{rows: 0}, {err: errLifecycleSQL}}, func(r LifecycleStore) error { return r.SaveLegalHold(t.Context(), hold) }},
		{"job insert", []lifecycleSQLExecStep{{err: errLifecycleSQL}}, func(r LifecycleStore) error { return r.SaveCleanupJob(t.Context(), job) }},
		{"job update exec", []lifecycleSQLExecStep{{err: errLifecycleSQL}}, func(r LifecycleStore) error { return r.UpdateCleanupJob(t.Context(), job) }},
		{"job update rows", []lifecycleSQLExecStep{{rowsErr: errLifecycleSQL}}, func(r LifecycleStore) error { return r.UpdateCleanupJob(t.Context(), job) }},
		{"job update lost", []lifecycleSQLExecStep{{rows: 0}}, func(r LifecycleStore) error { return r.UpdateCleanupJob(t.Context(), job) }},
		{"subject update", []lifecycleSQLExecStep{{err: errLifecycleSQL}}, func(r LifecycleStore) error { return r.SaveSubjectRequest(t.Context(), request) }},
		{"subject rows", []lifecycleSQLExecStep{{rowsErr: errLifecycleSQL}}, func(r LifecycleStore) error { return r.SaveSubjectRequest(t.Context(), request) }},
		{"subject insert", []lifecycleSQLExecStep{{rows: 0}, {err: errLifecycleSQL}}, func(r LifecycleStore) error { return r.SaveSubjectRequest(t.Context(), request) }},
		{"transition exec", []lifecycleSQLExecStep{{err: errLifecycleSQL}}, func(r LifecycleStore) error { return r.TransitionSubjectRequest(t.Context(), request, request) }},
		{"transition rows", []lifecycleSQLExecStep{{rowsErr: errLifecycleSQL}}, func(r LifecycleStore) error { return r.TransitionSubjectRequest(t.Context(), request, request) }},
		{"transition lost", []lifecycleSQLExecStep{{rows: 0}}, func(r LifecycleStore) error { return r.TransitionSubjectRequest(t.Context(), request, request) }},
		{"registration update", []lifecycleSQLExecStep{{err: errLifecycleSQL}}, func(r LifecycleStore) error { return r.SaveDeletionRegistration(t.Context(), registration) }},
		{"registration rows", []lifecycleSQLExecStep{{rowsErr: errLifecycleSQL}}, func(r LifecycleStore) error { return r.SaveDeletionRegistration(t.Context(), registration) }},
		{"registration insert", []lifecycleSQLExecStep{{rows: 0}, {err: errLifecycleSQL}}, func(r LifecycleStore) error { return r.SaveDeletionRegistration(t.Context(), registration) }},
		{"audit insert", []lifecycleSQLExecStep{{err: errLifecycleSQL}}, func(r LifecycleStore) error { return r.AppendAuditEvidence(t.Context(), evidence) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := scriptedLifecycleStore(t, &lifecycleSQLState{execSteps: tc.steps})
			if tc.call(r) == nil {
				t.Fatal("expected error")
			}
		})
	}
	request.ImpactPreview = []byte("{")
	if err := scriptedLifecycleStore(t, &lifecycleSQLState{}).SaveSubjectRequest(t.Context(), request); err == nil {
		t.Fatal("expected subject marshal error")
	}
	evidence.Payload = []byte("{")
	if err := scriptedLifecycleStore(t, &lifecycleSQLState{}).AppendAuditEvidence(t.Context(), evidence); err == nil {
		t.Fatal("expected evidence marshal error")
	}
	_ = now
}

func TestLifecycleStoreReadFailures(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	queryError := func() *lifecycleSQLState {
		return &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}}
	}
	badJSON := func() *lifecycleSQLState {
		return &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: []string{"payload"}, rows: [][]driver.Value{{"{"}}}}}
	}
	rowError := func() *lifecycleSQLState {
		return &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: []string{"a"}, rows: [][]driver.Value{{"x"}}, nextErr: errLifecycleSQL}}}
	}

	for _, tc := range []struct {
		name  string
		state func() *lifecycleSQLState
		call  func(LifecycleStore) error
	}{
		{"latest query", queryError, func(r LifecycleStore) error { _, _, err := r.LatestPolicy(t.Context(), "w", "p"); return err }},
		{"latest json", badJSON, func(r LifecycleStore) error { _, _, err := r.LatestPolicy(t.Context(), "w", "p"); return err }},
		{"list policy query", queryError, func(r LifecycleStore) error { _, err := r.ListPolicies(t.Context(), "w"); return err }},
		{"list policy scan", rowError, func(r LifecycleStore) error { _, err := r.ListPolicies(t.Context(), "w"); return err }},
		{"list policy json", badJSON, func(r LifecycleStore) error { _, err := r.ListPolicies(t.Context(), "w"); return err }},
		{"hold query", queryError, func(r LifecycleStore) error { _, _, err := r.GetLegalHold(t.Context(), "w", "h"); return err }},
		{"hold json", badJSON, func(r LifecycleStore) error { _, _, err := r.GetLegalHold(t.Context(), "w", "h"); return err }},
		{"active query", queryError, func(r LifecycleStore) error {
			_, err := r.ActiveLegalHolds(t.Context(), lifecyclemodel.ResourceTarget{WorkspaceID: "w"}, now)
			return err
		}},
		{"active scan", rowError, func(r LifecycleStore) error {
			_, err := r.ActiveLegalHolds(t.Context(), lifecyclemodel.ResourceTarget{WorkspaceID: "w"}, now)
			return err
		}},
		{"active json", badJSON, func(r LifecycleStore) error {
			_, err := r.ActiveLegalHolds(t.Context(), lifecyclemodel.ResourceTarget{WorkspaceID: "w"}, now)
			return err
		}},
		{"job query", queryError, func(r LifecycleStore) error { _, _, err := r.GetCleanupJob(t.Context(), "w", "j"); return err }},
		{"runnable query", queryError, func(r LifecycleStore) error {
			_, err := r.ListRunnableCleanupJobs(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test"), 1001, now)
			return err
		}},
		{"runnable scan", rowError, func(r LifecycleStore) error {
			_, err := r.ListRunnableCleanupJobs(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test"), 1, now)
			return err
		}},
		{"runnable json", badJSON, func(r LifecycleStore) error {
			_, err := r.ListRunnableCleanupJobs(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test"), 1, now)
			return err
		}},
		{"subject query", queryError, func(r LifecycleStore) error { _, _, err := r.GetSubjectRequest(t.Context(), "w", "r"); return err }},
		{"subject json", badJSON, func(r LifecycleStore) error { _, _, err := r.GetSubjectRequest(t.Context(), "w", "r"); return err }},
		{"external list query", queryError, func(r LifecycleStore) error { _, err := r.ListExternalErasures(t.Context(), "w", " "); return err }},
		{"external list scan", rowError, func(r LifecycleStore) error { _, err := r.ListExternalErasures(t.Context(), "w", "r"); return err }},
		{"external list json", badJSON, func(r LifecycleStore) error { _, err := r.ListExternalErasures(t.Context(), "w", "r"); return err }},
		{"reconcile query", queryError, func(r LifecycleStore) error {
			_, _, err := r.ReconcileExternalErasure(t.Context(), "w", "e", "x", now)
			return err
		}},
		{"reconcile json", badJSON, func(r LifecycleStore) error {
			_, _, err := r.ReconcileExternalErasure(t.Context(), "w", "e", "x", now)
			return err
		}},
		{"deletion list query", queryError, func(r LifecycleStore) error {
			_, err := r.ListPendingDeletionRegistrations(t.Context(), "w", 1001)
			return err
		}},
		{"archive list query", queryError, func(r LifecycleStore) error { _, err := r.ListArchiveEntries(t.Context(), "w", " ", 501); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.call(scriptedLifecycleStore(t, tc.state())) == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLifecycleStoreMutationAndIterationFailures(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test")

	for _, step := range []lifecycleSQLExecStep{{err: errLifecycleSQL}, {rowsErr: errLifecycleSQL}} {
		r := scriptedLifecycleStore(t, &lifecycleSQLState{execSteps: []lifecycleSQLExecStep{step}})
		if _, _, err := r.ClaimCleanupJob(t.Context(), "w", "j", "owner", 0, now); err == nil {
			t.Fatal("expected claim error")
		}
	}
	r := scriptedLifecycleStore(t, &lifecycleSQLState{execSteps: []lifecycleSQLExecStep{{rows: 1}}, querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}})
	if _, _, err := r.ClaimCleanupJob(t.Context(), "w", "j", "owner", time.Minute, now); err == nil {
		t.Fatal("expected claimed read error")
	}

	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"payload", "extra"}, rows: [][]driver.Value{{"x", "extra"}}}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"payload"}, rows: [][]driver.Value{{"{"}}}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"payload"}, nextErr: errLifecycleSQL}}},
	} {
		if _, err := scriptedLifecycleStore(t, state).ExpireSubjectExportReferences(t.Context(), scope, now); err == nil {
			t.Fatal("expected expiration error")
		}
	}
	validRequest := `{"id":"r","workspace_id":"w","kind":"export","status":"succeeded","result_reference":"ref"}`
	r = scriptedLifecycleStore(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: []string{"payload"}, rows: [][]driver.Value{{validRequest}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}})
	if _, err := r.ExpireSubjectExportReferences(t.Context(), scope, now); err == nil {
		t.Fatal("expected expiration save error")
	}

	erasure := lifecyclemodel.ExternalErasure{ID: "e", WorkspaceID: "w"}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
	} {
		if err := scriptedLifecycleStore(t, state).SaveExternalErasures(t.Context(), []lifecyclemodel.ExternalErasure{erasure}); err == nil {
			t.Fatal("expected erasure save error")
		}
	}
	validErasure := `{"id":"e","workspace_id":"w"}`
	for _, step := range []lifecycleSQLExecStep{{err: errLifecycleSQL}, {rowsErr: errLifecycleSQL}} {
		state := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: []string{"payload"}, rows: [][]driver.Value{{validErasure}}}}, execSteps: []lifecycleSQLExecStep{step}}
		if _, _, err := scriptedLifecycleStore(t, state).ReconcileExternalErasure(t.Context(), "w", "e", "x", now); err == nil {
			t.Fatal("expected reconcile error")
		}
	}
}

func TestLifecycleStoreRemainingEdges(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test")

	for _, call := range []func(LifecycleStore) error{
		func(r LifecycleStore) error { _, err := r.ListPolicies(t.Context(), "w"); return err },
		func(r LifecycleStore) error {
			_, err := r.ActiveLegalHolds(t.Context(), lifecyclemodel.ResourceTarget{WorkspaceID: "w"}, now)
			return err
		},
		func(r LifecycleStore) error {
			_, err := r.ListRunnableCleanupJobs(t.Context(), scope, 1, now)
			return err
		},
		func(r LifecycleStore) error { _, err := r.ListExternalErasures(t.Context(), "w", "r"); return err },
	} {
		state := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: []string{"payload", "extra"}, rows: [][]driver.Value{{"{}", "x"}}}}}
		if call(scriptedLifecycleStore(t, state)) == nil {
			t.Fatal("expected scan error")
		}
	}

	jobColumns := []string{"status", "checkpoint", "owner", "expires", "token", "updated", "payload"}
	for _, step := range []lifecycleSQLQueryStep{
		{},
		{columns: jobColumns, rows: [][]driver.Value{{"pending", "", "", "", int64(0), "", "{"}}},
	} {
		_, found, err := scriptedLifecycleStore(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{step}}).GetCleanupJob(t.Context(), "w", "j")
		if step.columns == nil && (err != nil || found) {
			t.Fatalf("missing found=%v err=%v", found, err)
		}
		if step.columns != nil && err == nil {
			t.Fatal("expected job JSON error")
		}
	}

	r := scriptedLifecycleStore(t, &lifecycleSQLState{execSteps: []lifecycleSQLExecStep{{rows: 1}}})
	if err := r.UpdateCleanupJob(t.Context(), lifecyclemodel.CleanupJob{}); err == nil {
		t.Fatal("missing cleanup workspace accepted")
	}
	next := lifecyclemodel.SubjectRequest{ImpactPreview: []byte("{")}
	if err := scriptedLifecycleStore(t, &lifecycleSQLState{}).TransitionSubjectRequest(t.Context(), lifecyclemodel.SubjectRequest{}, next); err == nil {
		t.Fatal("expected transition marshal error")
	}
	r = scriptedLifecycleStore(t, &lifecycleSQLState{execSteps: []lifecycleSQLExecStep{{rows: 1}}})
	if err := r.TransitionSubjectRequest(t.Context(), lifecyclemodel.SubjectRequest{}, lifecyclemodel.SubjectRequest{}); err == nil {
		t.Fatal("missing subject request workspace accepted")
	}
	r = scriptedLifecycleStore(t, &lifecycleSQLState{execSteps: []lifecycleSQLExecStep{{rows: 1}}})
	if err := r.SaveDeletionRegistration(t.Context(), lifecyclemodel.DeletionRegistration{}); err == nil {
		t.Fatal("missing deletion registration workspace accepted")
	}

	registrationColumns := []string{"request", "workspace", "identity", "pending", "evidence", "updated"}
	if _, err := scriptedLifecycleStore(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: append(registrationColumns, "extra"), rows: [][]driver.Value{{"r", "w", "u", true, "e", "", "x"}}}}}).ListPendingDeletionRegistrations(t.Context(), "w", 1); err == nil {
		t.Fatal("expected registration scan error")
	}
	archiveColumns := []string{"id", "workspace", "owner", "table", "resource", "policy", "version", "job", "hash", "payload", "archived"}
	values := []driver.Value{"id", "w", "o", "t", "r", "p", "v", "j", "h", "{}", ""}
	if _, err := scriptedLifecycleStore(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: append(archiveColumns, "extra"), rows: [][]driver.Value{append(values, "x")}}}}).ListArchiveEntries(t.Context(), "w", "t", 1); err == nil {
		t.Fatal("expected archive scan error")
	}
}

func TestLifecycleMetricsSQLPaths(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test")
	metricQueries := func() []lifecycleSQLQueryStep {
		return []lifecycleSQLQueryStep{
			{columns: []string{"count", "oldest"}, rows: [][]driver.Value{{int64(1001), now.Add(-25 * time.Hour).Format(time.RFC3339Nano)}}},
			{columns: []string{"count"}, rows: [][]driver.Value{{int64(2)}}},
			{columns: []string{"event", "payload"}, rows: [][]driver.Value{{"lifecycle.cleanup.failed", `{}`}, {"lifecycle.cleanup.succeeded", "{"}, {"lifecycle.cleanup.succeeded", `{"payload":"e30="}`}, {"lifecycle.cleanup.succeeded", `{"payload":{"purged":3}}`}}},
		}
	}
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "workspace", true: "global"}[global], func(t *testing.T) {
			r := scriptedLifecycleStore(t, &lifecycleSQLState{querySteps: metricQueries()})
			var metrics lifecyclemodel.Metrics
			var err error
			if global {
				metrics, err = r.GlobalMetrics(t.Context(), scope, now)
			} else {
				metrics, err = r.Metrics(t.Context(), "w", now)
			}
			if err != nil || metrics.EligibleBacklog != 1001 || metrics.LegalHoldCount != 2 || !metrics.Warning {
				t.Fatalf("metrics=%+v err=%v", metrics, err)
			}
		})
	}
	if _, err := scriptedLifecycleStore(t, &lifecycleSQLState{}).GlobalMetrics(t.Context(), principalmodel.SystemScope{}, now); err == nil {
		t.Fatal("expected scope error")
	}

	for _, global := range []bool{false, true} {
		for stage := 0; stage < 5; stage++ {
			steps := metricQueries()
			switch stage {
			case 0:
				steps = []lifecycleSQLQueryStep{{err: errLifecycleSQL}}
			case 1:
				steps[1] = lifecycleSQLQueryStep{err: errLifecycleSQL}
			case 2:
				steps[2] = lifecycleSQLQueryStep{err: errLifecycleSQL}
			case 3:
				steps[2] = lifecycleSQLQueryStep{columns: []string{"event"}, rows: [][]driver.Value{{"x"}}}
			case 4:
				steps[2] = lifecycleSQLQueryStep{columns: []string{"event", "payload"}, nextErr: errLifecycleSQL}
			}
			r := scriptedLifecycleStore(t, &lifecycleSQLState{querySteps: steps})
			var err error
			if global {
				_, err = r.GlobalMetrics(t.Context(), scope, now)
			} else {
				_, err = r.Metrics(t.Context(), "w", now)
			}
			if err == nil {
				t.Fatalf("global=%v stage=%d expected error", global, stage)
			}
		}
	}
}
