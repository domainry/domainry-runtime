package lifecycle

import (
	"database/sql/driver"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func scriptedOwner(t *testing.T, state *lifecycleSQLState, owner string, specs ...cleanupSpec) OwnerExecutor {
	t.Helper()
	store := openLifecycleStore(t)
	db := openLifecycleScriptedDB(state)
	t.Cleanup(func() { _ = db.Close() })
	return OwnerExecutor{store: store, db: db, owner: owner, specs: specs}
}

func ownerJob(operation lifecyclemodel.Operation) lifecyclemodel.CleanupJob {
	return lifecyclemodel.CleanupJob{ID: "job", WorkspaceID: "workspace-a", Operation: operation, UpdatedAt: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)}
}

func ownerPolicy(key string) lifecyclemodel.PolicyVersion {
	return lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: key, Version: "1", DefaultRetention: time.Hour}}
}

func TestOwnerExecutorHighLevelEdges(t *testing.T) {
	now := ownerJob(lifecyclemodel.OperationPurge).UpdatedAt
	if _, err := scriptedOwner(t, &lifecycleSQLState{}, "x").Preview(t.Context(), "workspace-a", ownerPolicy("missing"), now); err == nil {
		t.Fatal("expected missing preview spec")
	}
	if _, err := scriptedOwner(t, &lifecycleSQLState{}, "x").ProcessBatch(t.Context(), ownerJob(lifecyclemodel.OperationPurge), ownerPolicy("missing"), nil, 1); err == nil {
		t.Fatal("expected missing process spec")
	}
	installationSpec := cleanupSpec{policyKey: "p", table: "t", idColumn: "id", timeColumn: "updated_at"}
	e := scriptedOwner(t, &lifecycleSQLState{}, "x", installationSpec)
	if _, err := e.Preview(t.Context(), "workspace-a", ownerPolicy("p"), now); err == nil {
		t.Fatal("expected installation scope error")
	}
	if _, err := e.ProcessBatch(t.Context(), ownerJob(lifecyclemodel.OperationPurge), ownerPolicy("p"), nil, 1); err == nil {
		t.Fatal("expected installation process scope error")
	}

	spec := cleanupSpec{policyKey: "p", table: "t", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at"}
	e = scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}}, "x", spec)
	if _, err := e.Preview(t.Context(), "workspace-a", ownerPolicy("p"), now); err == nil {
		t.Fatal("expected preview SQL error")
	}
	e = scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}}, "x", spec)
	if _, err := e.ProcessBatch(t.Context(), ownerJob(lifecyclemodel.OperationPurge), ownerPolicy("p"), nil, 1); err == nil {
		t.Fatal("expected process SQL error")
	}
	e = scriptedOwner(t, &lifecycleSQLState{}, "x", spec)
	if result, err := e.ProcessBatch(t.Context(), ownerJob(lifecyclemodel.OperationPurge), ownerPolicy("p"), nil, 0); err != nil || result.Done {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	steps := []lifecycleSQLQueryStep{
		{columns: []string{"count", "oldest"}, rows: [][]driver.Value{{int64(1), now.Add(-time.Hour).Format(time.RFC3339Nano)}}},
		{columns: []string{"count", "oldest"}, rows: [][]driver.Value{{int64(2), now.Add(-2 * time.Hour).Format(time.RFC3339Nano)}}},
		{columns: []string{"count", "oldest"}, rows: [][]driver.Value{{int64(1), now.Add(-30 * time.Minute).Format(time.RFC3339Nano)}}},
	}
	e = scriptedOwner(t, &lifecycleSQLState{querySteps: steps}, "x", spec, spec, spec)
	preview, err := e.Preview(t.Context(), "workspace-a", ownerPolicy("p"), now)
	if err != nil || preview.Rows != 4 || preview.Bytes != 4096 || !preview.OldestEligible.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	dryJob := ownerJob(lifecyclemodel.OperationPurge)
	dryJob.DryRun = true
	processSteps := []lifecycleSQLQueryStep{
		{columns: []string{"id", "updated"}, rows: [][]driver.Value{{"new", now.Add(-time.Hour).Format(time.RFC3339Nano)}}},
		{columns: []string{"id", "updated"}, rows: [][]driver.Value{{"old", now.Add(-2 * time.Hour).Format(time.RFC3339Nano)}}},
	}
	e = scriptedOwner(t, &lifecycleSQLState{querySteps: processSteps}, "x", spec, spec)
	if _, err := e.ProcessBatch(t.Context(), dryJob, ownerPolicy("p"), nil, 2); err != nil {
		t.Fatal(err)
	}

	unix := spec
	unix.unixNanoTime = true
	for _, row := range [][]driver.Value{{int64(1), nil}, {int64(1), now.UnixNano()}} {
		e = scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count", "oldest"}, rows: [][]driver.Value{row}}}}, "x", unix)
		if _, _, err := e.previewSpec(t.Context(), "workspace-a", unix, "p", now); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []lifecycleSQLQueryStep{{err: errLifecycleSQL}, {columns: []string{"count", "oldest"}, rows: [][]driver.Value{{int64(1), nil}}}} {
		e = scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{step}}, "x", spec)
		if _, _, err := e.previewSpec(t.Context(), principalmodel.InstallationWorkspaceID, spec, "p", now); err != nil && step.err == nil {
			t.Fatal(err)
		}
	}
	e = scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}}, "x", unix)
	_, _, _ = e.previewSpec(t.Context(), "workspace-a", unix, "p", now)
}

func TestOwnerArchiveFailures(t *testing.T) {
	job, policy := ownerJob(lifecyclemodel.OperationPurge), ownerPolicy("p")
	spec := cleanupSpec{table: "source", idColumn: "id", tenantColumn: "workspace_id"}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}, {err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}, {columns: []string{"id"}}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}, {columns: []string{"id", "extra"}, rows: [][]driver.Value{{"id"}}}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}, {columns: []string{"id"}, rows: [][]driver.Value{{"id"}}, closeErr: errLifecycleSQL}}},
	} {
		e := scriptedOwner(t, state, "x")
		_, _ = e.archiveCandidate(t.Context(), job, policy, spec, "id")
	}
	state := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}, {columns: []string{"id", "payload"}, rows: [][]driver.Value{{"id", []byte("data")}}}, {columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}}
	if _, err := scriptedOwner(t, state, "x").archiveCandidate(t.Context(), job, policy, spec, "id"); err == nil {
		t.Fatal("expected archive insert error")
	}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
	} {
		_, _ = scriptedOwner(t, state, "x").archivePayload(t.Context(), job, policy, "source", "id", []byte("{}"))
	}
}

func TestOwnerReferenceAndHoldEdges(t *testing.T) {
	checks := []cleanupReferenceCheck{{table: "refs", referenceColumn: "resource_id"}, {table: "refs", tenantColumn: "workspace_id", referenceColumn: "resource_id", fixedColumn: "kind", fixedValue: "x"}}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}, {columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}},
	} {
		_, _ = scriptedOwner(t, state, "x").cleanupCandidateReferenced(t.Context(), "w", "id", checks)
	}
	now := time.Now().UTC()
	ended := now.Add(-time.Minute)
	holds := []lifecyclemodel.LegalHold{{StartsAt: now.Add(time.Minute)}, {StartsAt: now.Add(-time.Hour), EndsAt: &ended}, {StartsAt: now.Add(-time.Hour), Owner: "other"}, {StartsAt: now.Add(-time.Hour), Owner: "x", ResourceType: "t", ResourceID: "id"}}
	if !lifecycleHeld(holds, "x", "t", "id", now) || lifecycleHeld(holds, "x", "t", "other", now) {
		t.Fatal("hold matching failed")
	}
	activeEnd := now.Add(time.Hour)
	for _, hold := range []lifecyclemodel.LegalHold{
		{StartsAt: now.Add(-time.Hour), EndsAt: &activeEnd},
		{StartsAt: now.Add(-time.Hour), Owner: "x"},
		{StartsAt: now.Add(-time.Hour), Owner: "x", ResourceType: "t"},
	} {
		if !lifecycleHeld([]lifecyclemodel.LegalHold{hold}, "x", "t", "id", now) {
			t.Fatalf("hold=%+v did not match", hold)
		}
	}
	if lifecycleHeld([]lifecyclemodel.LegalHold{{StartsAt: now.Add(-time.Hour), Owner: "x", ResourceType: "other"}}, "x", "t", "id", now) {
		t.Fatal("mismatched resource type held")
	}
}

func TestOwnerChildFailurePaths(t *testing.T) {
	job, policy := ownerJob(lifecyclemodel.OperationPurge), ownerPolicy("p")
	oneID := lifecycleSQLQueryStep{columns: []string{"id"}, rows: [][]driver.Value{{"child"}}}
	exists := lifecycleSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}
	for _, call := range []func(OwnerExecutor) (int64, int64, error){
		func(e OwnerExecutor) (int64, int64, error) {
			return e.archiveIntegrationEventMappingIntents(t.Context(), job, policy, "event", true)
		},
		func(e OwnerExecutor) (int64, int64, error) {
			return e.archiveWorkflowProcessChildren(t.Context(), job, policy, "process", true)
		},
	} {
		for _, state := range []*lifecycleSQLState{
			{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
			{querySteps: []lifecycleSQLQueryStep{{columns: []string{"id", "extra"}, rows: [][]driver.Value{{"id", "x"}}}}},
			{querySteps: []lifecycleSQLQueryStep{{columns: []string{"id"}, nextErr: errLifecycleSQL}}},
			{querySteps: []lifecycleSQLQueryStep{oneID, exists}, execSteps: []lifecycleSQLExecStep{{rowsErr: errLifecycleSQL}}},
			{querySteps: []lifecycleSQLQueryStep{oneID, {err: errLifecycleSQL}}},
			{querySteps: []lifecycleSQLQueryStep{oneID, exists}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
		} {
			_, _, _ = call(scriptedOwner(t, state, "x"))
		}
	}
	_, _, _ = scriptedOwner(t, &lifecycleSQLState{}, "x").archiveIntegrationEventMappingIntents(t.Context(), job, policy, "event", false)
	_, _, _ = scriptedOwner(t, &lifecycleSQLState{}, "x").archiveIntegrationEventMappingIntents(t.Context(), job, policy, "event", true)
	_, _, _ = scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{}, {}, {}}}, "x").archiveWorkflowProcessChildren(t.Context(), job, policy, "process", false)
	_, _, _ = scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{}, {}, {}}}, "x").archiveWorkflowProcessChildren(t.Context(), job, policy, "process", true)
	chunk := lifecycleSQLQueryStep{columns: []string{"sequence", "content", "created"}, rows: [][]driver.Value{{int64(1), "x", "now"}}}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{{err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"sequence"}, rows: [][]driver.Value{{int64(1)}}}}},
		{querySteps: []lifecycleSQLQueryStep{{columns: []string{"sequence", "content", "created"}, nextErr: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{chunk, exists}, execSteps: []lifecycleSQLExecStep{{rowsErr: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{chunk, {err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{chunk, exists}, execSteps: []lifecycleSQLExecStep{{err: errLifecycleSQL}}},
	} {
		_, _, _ = scriptedOwner(t, state, "x").archiveRecordBatchChunks(t.Context(), job, policy, "batch", true)
	}
	_, _, _ = scriptedOwner(t, &lifecycleSQLState{}, "x").archiveRecordBatchChunks(t.Context(), job, policy, "batch", false)
	_, _, _ = scriptedOwner(t, &lifecycleSQLState{}, "x").archiveRecordBatchChunks(t.Context(), job, policy, "batch", true)
}

func TestOwnerProcessSpecFailurePaths(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	job, policy := ownerJob(lifecyclemodel.OperationPurge), ownerPolicy("p")
	base := cleanupSpec{policyKey: "p", table: "source", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at"}
	candidate := func(value driver.Value) lifecycleSQLQueryStep {
		return lifecycleSQLQueryStep{columns: []string{"id", "updated"}, rows: [][]driver.Value{{"id", value}}}
	}
	exists := lifecycleSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}

	for _, step := range []lifecycleSQLQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{"id"}}}, {columns: []string{"id", "updated"}, nextErr: errLifecycleSQL}} {
		_, _ = scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{step}}, "x").processSpec(t.Context(), job, policy, base, nil, now, 1)
	}
	two := lifecycleSQLQueryStep{columns: []string{"id", "updated"}, rows: [][]driver.Value{{"new", now.Add(-time.Hour).Format(time.RFC3339Nano)}, {"old", now.Add(-2 * time.Hour).Format(time.RFC3339Nano)}}}
	dryJob := job
	dryJob.DryRun = true
	if _, err := scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{two}}, "x").processSpec(t.Context(), dryJob, policy, base, nil, now, 2); err != nil {
		t.Fatal(err)
	}
	archiveJob := job
	archiveJob.Operation = lifecyclemodel.OperationArchive
	if _, err := scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate([]byte(now.Format(time.RFC3339Nano))), exists}}, "x").processSpec(t.Context(), archiveJob, policy, base, nil, now, 1); err != nil {
		t.Fatal(err)
	}
	unix := base
	unix.unixNanoTime = true
	if _, err := scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate(now.UnixNano()), exists}, execSteps: []lifecycleSQLExecStep{{rows: 1}}}, "x").processSpec(t.Context(), job, policy, unix, nil, now, 1); err != nil {
		t.Fatal(err)
	}

	referenced := base
	referenced.referenceChecks = []cleanupReferenceCheck{{table: "refs", referenceColumn: "id"}}
	for _, state := range []*lifecycleSQLState{
		{querySteps: []lifecycleSQLQueryStep{candidate(now.Format(time.RFC3339Nano)), {err: errLifecycleSQL}}},
		{querySteps: []lifecycleSQLQueryStep{candidate(now.Format(time.RFC3339Nano)), {columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}},
	} {
		_, _ = scriptedOwner(t, state, "x").processSpec(t.Context(), job, policy, referenced, nil, now, 1)
	}

	// Archive lookup, child processing, and final purge failures.
	_, _ = scriptedOwner(t, &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate(now.Format(time.RFC3339Nano)), {err: errLifecycleSQL}}}, "x").processSpec(t.Context(), job, policy, base, nil, now, 1)
	for _, spec := range []cleanupSpec{
		{policyKey: "p", table: "record_batch_jobs", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at"},
		{policyKey: "p", table: "integration_events", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at"},
		{policyKey: "p", table: "workflow_process_instances", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", workflowProcessChildren: true},
	} {
		state := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate(now.Format(time.RFC3339Nano)), exists, {err: errLifecycleSQL}}}
		_, _ = scriptedOwner(t, state, "x").processSpec(t.Context(), job, policy, spec, nil, now, 1)
	}
	for _, exec := range []lifecycleSQLExecStep{{err: errLifecycleSQL}, {rowsErr: errLifecycleSQL}} {
		state := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{candidate(now.Format(time.RFC3339Nano)), exists}, execSteps: []lifecycleSQLExecStep{exec}}
		_, _ = scriptedOwner(t, state, "x").processSpec(t.Context(), job, policy, base, nil, now, 1)
	}
}
