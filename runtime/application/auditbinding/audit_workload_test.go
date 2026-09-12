package auditbinding

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestAuditBuildEventPreservesWorkflowWorkloadLineage(t *testing.T) {
	principal := principalmodel.NewPrincipalFromIdentity(identitysdk.Principal{
		Known: true, WorkspaceID: "workspace", UserID: "workflow:customer_sync", RoleKey: "customer_sync_service",
		Workload: &identitysdk.WorkflowWorkloadPrincipalContext{
			WorkflowKey: "customer_sync", DefinitionVersionID: "version-2", DefinitionVersion: 2,
			ReleaseID: "release-2", ReleaseDigest: "digest-2", TaskID: "task-2", SourceEventID: "event-2", InitiatorSubjectID: "member-2",
		},
	}, "request-2")
	event := AuditBuildEvent(t.Context(), "action.customer_sync", "customer", "customer-2", principal, "Synced customer", nil, nil, map[string]any{"status": "succeeded"})
	if event.ActorID != "workflow:customer_sync" || event.RecordID != "customer-2" {
		t.Fatalf("audit event identity = %+v", event)
	}
	for key, want := range map[string]any{
		"actor_kind":   "workload",
		"workflow_key": "customer_sync", "workflow_definition_version_id": "version-2", "workflow_definition_version": 2,
		"workflow_release_id": "release-2", "workflow_release_digest": "digest-2", "workflow_task_id": "task-2",
		"workflow_source_event_id": "event-2", "workflow_initiator_subject_id": identitysdk.SubjectID("member-2"), "status": "succeeded",
	} {
		if event.Metadata[key] != want {
			t.Fatalf("audit metadata[%q]=%#v want=%#v", key, event.Metadata[key], want)
		}
	}
}
