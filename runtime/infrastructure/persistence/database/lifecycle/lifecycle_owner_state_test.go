package lifecycle

import (
	"database/sql/driver"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func scriptedAgent(t *testing.T, state *lifecycleSQLState) AgentOwnerExecutor {
	t.Helper()
	store := openLifecycleStore(t)
	db := openLifecycleScriptedDB(state)
	t.Cleanup(func() { _ = db.Close() })
	return AgentOwnerExecutor{store: store, db: db}
}

func scriptedReport(t *testing.T, state *lifecycleSQLState) ReportOwnerExecutor {
	t.Helper()
	store := openLifecycleStore(t)
	db := openLifecycleScriptedDB(state)
	t.Cleanup(func() { _ = db.Close() })
	return ReportOwnerExecutor{store: store, db: db, relational: OwnerExecutor{store: store, db: db, owner: "report"}}
}

func TestAgentOwnerPreviewAndFailurePaths(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	policy := ownerPolicy("agent.runtime_state.v1")
	if _, err := scriptedAgent(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}}).Preview(t.Context(), "w", policy, now); err == nil {
		t.Fatal("expected preview error")
	}
	columns := []string{"kind", "state_key", "user_id", "role_key", "payload_json", "updated_at"}
	rows := [][]driver.Value{{"session", "s", "u", "r", []byte(`{"archived":true}`), now.Add(-time.Hour).UnixNano()}, {"proposal", "p", "u", "r", []byte(`{"status":"approved"}`), now.Add(-2 * time.Hour).UnixNano()}, {"proposal", "later", "u", "r", []byte(`{"status":"approved"}`), now.Add(-30 * time.Minute).UnixNano()}}
	e := scriptedAgent(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: columns, rows: rows}}})
	if e.database() == nil {
		t.Fatal("missing agent database")
	}
	fallback := AgentOwnerExecutor{store: e.store}
	if fallback.database() == nil {
		t.Fatal("missing fallback database")
	}
	preview, err := e.Preview(t.Context(), "w", policy, now)
	if err != nil || preview.Rows != 3 || !preview.OldestEligible.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}

	for _, step := range []lifecycleSQLQueryStep{{err: errLifecycleSQL}, {columns: []string{"kind"}, rows: [][]driver.Value{{"session"}}}, {columns: columns, nextErr: errLifecycleSQL}, {columns: columns, rows: rows}} {
		_, _ = scriptedAgent(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{step}}).candidates(t.Context(), "w", now, 1)
	}
	for _, tc := range []struct {
		kind    string
		payload []byte
		want    bool
	}{{"session", []byte("{"), false}, {"session", []byte(`{}`), false}, {"proposal", []byte(`{"status":"draft"}`), false}, {"proposal", []byte(`{"status":"approved"}`), true}, {"other", []byte(`{}`), false}} {
		if got := agentLifecycleEligible(tc.kind, tc.payload); got != tc.want {
			t.Fatalf("kind=%s got=%v", tc.kind, got)
		}
	}

	job := ownerJob(lifecyclemodel.OperationPurge)
	candidate := lifecycleSQLQueryStep{columns: columns, rows: [][]driver.Value{{"session", "s", "u", "r", []byte(`{"archived":true}`), now.UnixNano()}}}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{candidate, {err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{candidate, {columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{candidate, {columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}, execSteps: []lifecycleSQLExecStep{{rowsErr: errLifecycleSQL}}},
	} {
		_, _ = scriptedAgent(t, state).ProcessBatch(t.Context(), job, policy, nil, 0)
	}
	// Archive-only and dry-run paths do not delete.
	job.Operation = lifecyclemodel.OperationArchive
	state := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate, {columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}}
	if _, err := scriptedAgent(t, state).ProcessBatch(t.Context(), job, policy, nil, 1); err != nil {
		t.Fatal(err)
	}
	job.Operation = lifecyclemodel.OperationPurge
	job.DryRun = true
	if result, err := scriptedAgent(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate}}).ProcessBatch(t.Context(), job, policy, nil, 1); err != nil || result.Scanned != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	job.DryRun = false
	hold := lifecyclemodel.LegalHold{StartsAt: now.Add(-time.Hour), Owner: "agent", ResourceType: "agent_runtime_state", ResourceID: "session:s"}
	if result, err := scriptedAgent(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate}}).ProcessBatch(t.Context(), job, policy, []lifecyclemodel.LegalHold{hold}, 1); err != nil || result.Skipped != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	twoCandidates := candidate
	twoCandidates.rows = append(twoCandidates.rows, []driver.Value{"proposal", "older", "u", "r", []byte(`{"status":"approved"}`), now.Add(-time.Hour).UnixNano()})
	dry := job
	dry.DryRun = true
	if _, err := scriptedAgent(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{twoCandidates}}).ProcessBatch(t.Context(), dry, policy, nil, 2); err != nil {
		t.Fatal(err)
	}
}

func TestReportOwnerPreviewAndFailurePaths(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	policy := ownerPolicy("report.download.v1")
	if _, err := scriptedReport(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}}).Preview(t.Context(), "w", policy, now); err == nil {
		t.Fatal("expected preview error")
	}
	columns := []string{"state_key", "user_id", "role_key", "payload_json", "updated_at"}
	row := lifecycleSQLQueryStep{columns: columns, rows: [][]driver.Value{{"s", "u", "r", []byte(`{}`), now.Add(-time.Hour).UnixNano()}, {"later", "u", "r", []byte(`{}`), now.Add(-30 * time.Minute).UnixNano()}}}
	preview, err := scriptedReport(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{row}}).Preview(t.Context(), "w", policy, now)
	if err != nil || preview.Rows != 2 || preview.Bytes != 2048 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	reportDB := scriptedReport(t, &lifecycleSQLState{})
	if reportDB.database() == nil || (ReportOwnerExecutor{store: reportDB.store}).database() == nil {
		t.Fatal("missing report database")
	}
	for _, step := range []lifecycleSQLQueryStep{{err: errLifecycleSQL}, {columns: []string{"state"}, rows: [][]driver.Value{{"s"}}}, {columns: columns, nextErr: errLifecycleSQL}, {columns: columns, rows: [][]driver.Value{{"a", "u", "r", []byte(`{}`), now.UnixNano()}, {"b", "u", "r", []byte(`{}`), now.UnixNano()}}}} {
		_, _ = scriptedReport(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{step}}).reportStateCandidates(t.Context(), "w", policy.Policy, now, 1)
	}
	// A policy without agent state kinds returns an empty set.
	emptyPolicy := policy.Policy
	emptyPolicy.Key = "other"
	if states, err := scriptedReport(t, &lifecycleSQLState{}).reportStateCandidates(t.Context(), "w", emptyPolicy, now, 0); err != nil || len(states) != 0 {
		t.Fatalf("states=%v err=%v", states, err)
	}

	job := ownerJob(lifecyclemodel.OperationPurge)
	oneRow := row
	oneRow.rows = oneRow.rows[:1]
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{oneRow, {err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{oneRow, {columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{oneRow, {columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}, execSteps: []lifecycleSQLExecStep{{rowsErr: errLifecycleSQL}}},
	} {
		_, _ = scriptedReport(t, state).ProcessBatch(t.Context(), job, policy, nil, 0)
	}
	job.DryRun = true
	if result, err := scriptedReport(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{oneRow}}).ProcessBatch(t.Context(), job, policy, nil, 1); err != nil || result.Scanned != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	job.DryRun = false
	hold := lifecyclemodel.LegalHold{StartsAt: now.Add(-time.Hour), Owner: "report", ResourceType: "agent_runtime_state", ResourceID: "report_download_task:s"}
	if result, err := scriptedReport(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{oneRow}}).ProcessBatch(t.Context(), job, policy, []lifecyclemodel.LegalHold{hold}, 1); err != nil || result.Skipped != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	archiveJob := job
	archiveJob.Operation = lifecyclemodel.OperationArchive
	if _, err := scriptedReport(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{oneRow, {columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}}).ProcessBatch(t.Context(), archiveJob, policy, nil, 1); err != nil {
		t.Fatal(err)
	}
	exportPolicy := ownerPolicy("report.export.v1")
	exportState := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{
		{columns: columns, rows: [][]driver.Value{{"s", "u", "r", []byte(`{}`), now.UnixNano()}}},
		{err: errLifecycleSQL},
	}}
	if _, err := scriptedReport(t, exportState).ProcessBatch(t.Context(), job, exportPolicy, nil, 1); err == nil {
		t.Fatal("expected report reference error")
	}
	referencedState := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{
		{columns: columns, rows: [][]driver.Value{{"s", "u", "r", []byte(`{}`), now.UnixNano()}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}},
	}}
	if result, err := scriptedReport(t, referencedState).ProcessBatch(t.Context(), job, exportPolicy, nil, 1); err != nil || result.Skipped != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	// Relational report specifications participate before agent report state.
	relSpec := cleanupSpec{policyKey: "report.download.v1", table: "download_task", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at"}
	store := openLifecycleStore(t)
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count", "oldest"}, rows: [][]driver.Value{{int64(1), now.Add(-time.Hour).Format(time.RFC3339Nano)}}}, {columns: columns, rows: [][]driver.Value{{"older", "u", "r", []byte(`{}`), now.Add(-2 * time.Hour).UnixNano()}, {"newer", "u", "r", []byte(`{}`), now.Add(-30 * time.Minute).UnixNano()}}}}},
	} {
		db := openLifecycleScriptedDB(state)
		t.Cleanup(func() { _ = db.Close() })
		executor := ReportOwnerExecutor{store: store, db: db, relational: OwnerExecutor{store: store, db: db, owner: "report", specs: []cleanupSpec{relSpec}}}
		_, _ = executor.Preview(t.Context(), "w", policy, now)
	}
	relCandidate := lifecycleSQLQueryStep{columns: []string{"id", "updated"}, rows: [][]driver.Value{{"id", now.Format(time.RFC3339Nano)}}}
	db := openLifecycleScriptedDB(&lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{relCandidate}})
	t.Cleanup(func() { _ = db.Close() })
	executor := ReportOwnerExecutor{store: store, db: db, relational: OwnerExecutor{store: store, db: db, owner: "report", specs: []cleanupSpec{relSpec}}}
	dryJob := job
	dryJob.DryRun = true
	if result, err := executor.ProcessBatch(t.Context(), dryJob, policy, nil, 1); err != nil || result.Done {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	olderRows := oneRow
	olderRows.rows = append(olderRows.rows, []driver.Value{"older", "u", "r", []byte(`{}`), now.Add(-2 * time.Hour).UnixNano()})
	if _, err := scriptedReport(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{olderRows}}).ProcessBatch(t.Context(), dryJob, policy, nil, 2); err != nil {
		t.Fatal(err)
	}
	db = openLifecycleScriptedDB(&lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}})
	t.Cleanup(func() { _ = db.Close() })
	executor = ReportOwnerExecutor{store: store, db: db, relational: OwnerExecutor{store: store, db: db, owner: "report", specs: []cleanupSpec{relSpec}}}
	if _, err := executor.ProcessBatch(t.Context(), job, policy, nil, 1); err == nil {
		t.Fatal("expected relational report error")
	}

	for _, tc := range []struct {
		kind  string
		state *lifecycleSQLState
		want  bool
	}{{"download", &lifecycleSQLState{}, false}, {"report_export_audit", &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}}, false}, {"report_query_run", &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}}, true}} {
		got, _ := scriptedReport(t, tc.state).reportStateReferenced(t.Context(), "w", tc.kind, "s")
		if got != tc.want {
			t.Fatalf("kind=%s got=%v", tc.kind, got)
		}
	}

	retentionPolicy := policy.Policy
	retentionPolicy.StatusRetention = map[string]time.Duration{"done": time.Hour}
	if reportRetention(retentionPolicy, "done", 0) != time.Hour || reportRetention(policy.Policy, "x", time.Minute) != time.Minute || reportRetention(lifecyclemodel.RetentionPolicy{}, "x", 0) != 0 {
		t.Fatal("retention fallback")
	}
	target := lifecyclemodel.CleanupBatchResult{OldestEligible: now}
	mergeCleanupBatchResult(&target, lifecyclemodel.CleanupBatchResult{Scanned: 1, Archived: 2, Purged: 3, Skipped: 4, Failed: 5, Checkpoint: "c", OldestEligible: now.Add(-time.Hour), Done: false})
	if target.Scanned != 1 || target.Checkpoint != "c" || target.Done {
		t.Fatalf("target=%+v", target)
	}
	mergeCleanupBatchResult(&target, lifecyclemodel.CleanupBatchResult{Done: true})
	mergeCleanupBatchResult(&target, lifecyclemodel.CleanupBatchResult{OldestEligible: now.Add(time.Hour), Done: true})
	emptyTarget := lifecyclemodel.CleanupBatchResult{}
	mergeCleanupBatchResult(&emptyTarget, lifecyclemodel.CleanupBatchResult{OldestEligible: now, Done: true})
}
