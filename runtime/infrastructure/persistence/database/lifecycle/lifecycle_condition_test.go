package lifecycle

import (
	"database/sql/driver"
	"encoding/json"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestLifecycleStoreRemainingConditionOutcomes(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	if _, found, err := scriptedLifecycleStore(t, &lifecycleSQLState{}).LatestPolicy(t.Context(), principalmodel.InstallationWorkspaceID, "p"); err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test")
	if _, err := scriptedLifecycleStore(t, &lifecycleSQLState{}).ListRunnableCleanupJobs(t.Context(), scope, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := scriptedLifecycleStore(t, &lifecycleSQLState{}).ListPendingDeletionRegistrations(t.Context(), "w", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := scriptedLifecycleStore(t, &lifecycleSQLState{}).ListArchiveEntries(t.Context(), "w", "", 0); err != nil {
		t.Fatal(err)
	}
	claim := scriptedLifecycleStore(t, &lifecycleSQLState{execSteps: []lifecycleSQLExecStep{{rows: 1}}, querySteps: []lifecycleSQLQueryStep{{}}})
	if _, acquired, err := claim.ClaimCleanupJob(t.Context(), "w", "j", "owner", time.Minute, now); err != nil || acquired {
		t.Fatalf("acquired=%v err=%v", acquired, err)
	}

	for _, tc := range []struct {
		target lifecyclemodel.ResourceTarget
		hold   lifecyclemodel.LegalHold
	}{
		{lifecyclemodel.ResourceTarget{WorkspaceID: "w"}, lifecyclemodel.LegalHold{WorkspaceID: "w"}},
		{lifecyclemodel.ResourceTarget{WorkspaceID: "w", Owner: "x"}, lifecyclemodel.LegalHold{WorkspaceID: "w"}},
		{lifecyclemodel.ResourceTarget{WorkspaceID: "w", Owner: "x", ResourceType: "t"}, lifecyclemodel.LegalHold{WorkspaceID: "w", Owner: "x"}},
		{lifecyclemodel.ResourceTarget{WorkspaceID: "w", Owner: "x", ResourceType: "t", ResourceID: "*"}, lifecyclemodel.LegalHold{WorkspaceID: "w", Owner: "x", ResourceType: "t"}},
		{lifecyclemodel.ResourceTarget{WorkspaceID: "w", Owner: "x", ResourceType: "t", ResourceID: ""}, lifecyclemodel.LegalHold{WorkspaceID: "w", Owner: "x", ResourceType: "t", ResourceID: "other"}},
		{lifecyclemodel.ResourceTarget{WorkspaceID: "w", Owner: "x", ResourceType: "t", ResourceID: "id"}, lifecyclemodel.LegalHold{WorkspaceID: "w", Owner: "x", ResourceType: "t"}},
		{lifecyclemodel.ResourceTarget{WorkspaceID: "w", Owner: "x"}, lifecyclemodel.LegalHold{WorkspaceID: "w", Owner: "other"}},
		{lifecyclemodel.ResourceTarget{WorkspaceID: "w", Owner: "x", ResourceType: "t"}, lifecyclemodel.LegalHold{WorkspaceID: "w", Owner: "x", ResourceType: "other"}},
		{lifecyclemodel.ResourceTarget{WorkspaceID: "w", Owner: "x", ResourceType: "t", ResourceID: "id"}, lifecyclemodel.LegalHold{WorkspaceID: "w", Owner: "x", ResourceType: "t", ResourceID: "other"}},
	} {
		payload, _ := json.Marshal(tc.hold)
		state := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: []string{"payload"}, rows: [][]driver.Value{{string(payload)}}}}}
		holds, err := scriptedLifecycleStore(t, state).ActiveLegalHolds(t.Context(), tc.target, now)
		if err != nil {
			t.Fatal(err)
		}
		shouldMatch := (tc.target.Owner == "" || tc.hold.Owner == "" || tc.hold.Owner == tc.target.Owner) &&
			(tc.target.ResourceType == "" || tc.hold.ResourceType == "" || tc.hold.ResourceType == tc.target.ResourceType) &&
			(tc.target.ResourceID == "*" || tc.target.ResourceID == "" || tc.hold.ResourceID == "" || tc.hold.ResourceID == tc.target.ResourceID)
		if (len(holds) == 1) != shouldMatch {
			t.Fatalf("target=%+v hold=%+v holds=%v", tc.target, tc.hold, holds)
		}
	}

	globalState := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{
		{columns: []string{"count", "oldest"}, rows: [][]driver.Value{{int64(0), nil}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}},
		{columns: []string{"event", "payload"}},
	}}
	if _, err := scriptedLifecycleStore(t, globalState).GlobalMetrics(t.Context(), scope, now); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerAndArtifactRemainingConditionOutcomes(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	base := cleanupSpec{policyKey: "p", table: "t", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at"}
	dry := ownerJob(lifecyclemodel.OperationPurge)
	dry.DryRun = true
	state := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{{columns: []string{"id", "updated"}, rows: [][]driver.Value{{"id", nil}}}}}
	if _, err := scriptedOwner(t, state, "x").processSpec(t.Context(), dry, ownerPolicy("p"), base, nil, now, 1); err != nil {
		t.Fatal(err)
	}

	objects := []definitionmodel.ObjectSchema{{Key: "job_run", Config: map[string]any{"scheduler_runtime": false}}, {Key: "job_run_event"}}
	_ = DefaultOwnerExecutors(openLifecycleStore(t), objects...)
	objects = []definitionmodel.ObjectSchema{{Key: "job_run", Config: map[string]any{"scheduler_runtime": true}}}
	_ = DefaultOwnerExecutors(openLifecycleStore(t), objects...)

	candidateColumns := []string{"id", "workspace_id", "object_key", "field_key", "filename", "status", "created_at", "delete_after"}
	artifactState := &lifecycleSQLState{querySteps: []lifecycleSQLQueryStep{
		{columns: candidateColumns, rows: [][]driver.Value{{"id", "workspace-a", "asset", "file_url", "a.txt", "staged", "", ""}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}},
	}}
	artifact := scriptedArtifactStore(t, artifactState, artifactObjects()[:1])
	if result, err := artifact.ReconcileUploadArtifacts(t.Context(), now, 1); err != nil || result.Orphaned != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
