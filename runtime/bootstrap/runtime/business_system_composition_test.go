package runtime

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"

	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

	publicationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
)

func TestBusinessReferenceGraphCompositionFindsCrossOwnerConsumers(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{
		Key:  "customer",
		Name: "Customer",
		Fields: []definitionmodel.FieldSchema{{
			Key:        "status",
			Name:       "Status",
			Type:       "select",
			Validation: definitionmodel.FieldValidation{Options: []string{"new", "qualified"}},
		}},
	}}
	adminRole := accessfixture.Bundle{
		Key: "admin",
		Permissions: []string{
			"workspace.admin", "customer.read", "customer.update",
			"job_definition.read", "job_run.read", "job_dead_letter.read",
			"scheduler.definition.read", "ops.workflow.read", "workflow.process.read",
			"integration.audit.view",
		},
		RecordScope:  "all_records",
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true, Write: true}},
	}
	application, admin := newMetadataCompositionAppWithManifest(t, "references", objects, []accessfixture.Bundle{adminRole}, func(manifest *manifestmodel.ManifestSchema) {
		manifest.Actions = []definitionmodel.ActionSchema{{Key: "customer.qualify", ObjectKey: "customer", Label: "Qualify", Kind: "record_update", RequiresPermission: "customer.update", AuditEvent: "customer_qualified", IdempotencyKeys: []string{"request_id"}}}
		manifest.Workflows = []definitionmodel.WorkflowSchema{{Key: "customer.approval", Name: "Customer approval", Enabled: true, Trigger: map[string]any{"type": "field_changed", "object_key": "customer", "field_key": "status"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "field_changed", ObjectKey: "customer", FieldKey: "status"}, Condition: map[string]any{"field": "status", "equals": "new"}, ConditionContract: &definitionmodel.WorkflowConditionContract{Type: "field_equals", Field: "status", Value: "new"}, Action: map[string]any{"type": "workflow_graph"}, IdempotencyKeys: []string{"record_id", "status"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "started", Type: "trigger", Name: "Customer changed"},
			{ID: "qualify", Type: "action", Name: "Qualify", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "customer.qualify", ObjectKey: "customer", OnError: "fail"}}},
		}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start-qualify", Source: "started", Target: "qualify"}}}}}
		manifest.AutomationRules = []automationmodel.AutomationRuleSchema{{Key: "customer.after_update", Name: "Customer updated", ObjectKey: "customer", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update", ChangedFields: []string{"status"}, FromState: "new", ToState: "qualified"}, Instructions: []automationmodel.AutomationInstructionSchema{{Key: "start", Type: "start_workflow", Config: map[string]any{"workflow_key": "customer.approval"}}}}}
		manifest.Integrations = integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "crm", Type: "mock", Provider: "test", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "sync", Method: "POST", ExecutionMode: "sync", SideEffect: "write", TimeoutDefaultSeconds: 10, TimeoutMaxSeconds: 30}}}}}
		manifest.Reports = []reportmodel.ReportSchema{{Key: "customer.summary", Name: "Customer summary", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}}, RequiredPermissions: []string{"customer.read"}}}
	})
	adminAccess := accessfixture.FromPrincipal(admin)
	adminAccess.DataPolicies = append(adminAccess.DataPolicies,
		accessfixture.DataPolicyFixture{ObjectKey: "job_definition", Scope: "all_records", Read: true},
		accessfixture.DataPolicyFixture{ObjectKey: "job_run", Scope: "all_records", Read: true},
		accessfixture.DataPolicyFixture{ObjectKey: "job_dead_letter", Scope: "all_records", Read: true},
	)
	accessfixture.Set(&admin, adminAccess)
	defer application.CloseContext(t.Context())
	if _, err := publicationpersistence.NewPublicationStore(application.store).InsertOutbox(t.Context(), "workspace-primary", integrationmodel.IntegrationOutboxMessage{ID: "outbox-1", WorkspaceID: "workspace-primary", ConnectorKey: "crm", Operation: "sync", Status: "queued", CreatedBy: admin.UserID}); err != nil {
		t.Fatal(err)
	}
	graph, err := application.records.Applications().BusinessReferences.Graph(t.Context(), admin)
	if err != nil || graph.Hash == "" || len(graph.Edges) == 0 {
		t.Fatalf("graph=%#v err=%v", graph, err)
	}
	actionImpact := changeplanprojection.ChangePlanReferenceImpact(graph, "action", "customer.qualify")
	if !actionImpact.DeletionBlocked || !referenceCompositionHasConsumer(actionImpact.DirectConsumers, "workflow", "customer.approval", "invokes_action") {
		t.Fatalf("impact=%#v", actionImpact)
	}
	workflowImpact := changeplanprojection.ChangePlanReferenceImpact(graph, "workflow", "customer.approval")
	if !referenceCompositionHasConsumer(workflowImpact.DirectConsumers, "automation", "customer.after_update", "starts_workflow") {
		t.Fatalf("workflow impact=%#v", workflowImpact)
	}
	connectorImpact := changeplanprojection.ChangePlanReferenceImpact(graph, "connector_operation", "crm.sync")
	if !referenceCompositionHasConsumer(connectorImpact.DirectConsumers, "outbox_message", "outbox-1", "delivers_operation") {
		t.Fatalf("connector impact=%#v", connectorImpact)
	}
}

func TestBusinessSystemSnapshotCompositionHidesGovernanceFacts(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text"}}}}
	roles := []accessfixture.Bundle{
		{Key: "admin", Permissions: []string{"workspace.admin"}, RecordScope: "all_records", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true, Write: true}}},
		{Key: "viewer", Permissions: []string{"customer.read"}, RecordScope: "all_records", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true}}},
	}
	application, _ := newMetadataCompositionApp(t, "visibility", objects, roles)
	defer application.CloseContext(t.Context())
	viewer := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "viewer", WorkspaceID: "workspace-primary"}}, roles[1])
	snapshot, err := application.records.Applications().BusinessSystem.Snapshot(t.Context(), viewer)
	if err != nil || snapshot.ResourceVisibility["schema"] != "visible" || len(snapshot.SeedRecords) != 0 || len(snapshot.ResourceSources) != 0 {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if len(snapshot.HiddenResourceCategories) == 0 || snapshot.SnapshotHash == "" {
		t.Fatalf("snapshot normalization=%#v", snapshot)
	}
}

func referenceCompositionHasConsumer(edges []changeplanmodel.ReferenceEdge, fromType, fromKey, kind string) bool {
	for _, edge := range edges {
		if edge.FromType == fromType && edge.FromKey == fromKey && edge.Kind == kind {
			return true
		}
	}
	return false
}
