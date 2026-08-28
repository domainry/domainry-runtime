package integrationtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestGovernedAfterAutomationExecutesSourceActionWorkflowAndEventForBusinessIdentity(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"), UploadDir: filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.Close()
	handler := application.Routes()

	const (
		ruleKey      = "customer.after_create_governed_evidence"
		actionKey    = "customer.apply_automation_verification"
		workflowKey  = "customer.risk_follow_up"
		businessUser = "automation_business_tester_user"
	)
	rule := automationmodel.AutomationRuleSchema{
		Key: ruleKey, Name: "Customer After Create Governed Evidence", ObjectKey: "customer", Enabled: true, Priority: -10,
		Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "create"},
		Conditions: automationmodel.AutomationConditionGroup{Mode: "all", Clauses: []automationmodel.AutomationConditionClause{{
			Reference: "$actor.role", Operator: "eq", Value: "automation_business_tester",
		}}},
		Instructions: []automationmodel.AutomationInstructionSchema{
			{
				Key: "apply_verification", Type: "invoke_business_action", Name: "Apply verification from source-owned Action",
				Config: map[string]any{
					"action_key": actionKey, "object_key": "customer", "record_id": "$record.id",
					"input": map[string]any{"verification_status": "verified", "source": "admin_after_save_acceptance"},
				},
			},
			{
				Key: "start_risk_follow_up", Type: "start_workflow", Name: "Start governed risk follow-up",
				Config: map[string]any{
					"workflow_key": workflowKey,
					"payload":      map[string]any{"object_key": "customer", "record_id": "$record.id", "source": "admin_after_save_acceptance"},
				},
			},
			{
				Key: "emit_acceptance_event", Type: "emit_event", Name: "Emit acceptance evidence",
				Config: map[string]any{
					"event_type": "customer.after_save.accepted", "object_key": "customer", "record_id": "$record.id",
					"message":  "Governed after-save automation completed",
					"metadata": map[string]any{"source": "admin_after_save_acceptance"},
				},
			},
		},
		Execution: automationmodel.AutomationExecutionPolicy{
			Mode: "async", RunAs: "initiator", TimeoutSeconds: 30, MaxDepth: 4, IdempotencyKeys: []string{"owner"},
		},
		AuditEvent: "customer_after_create_governed_evidence",
		Layout: &automationmodel.AutomationLayoutSchema{
			Version: 1,
			Nodes: map[string]automationmodel.AutomationNodePosition{
				"apply_verification": {X: 320, Y: 120}, "start_risk_follow_up": {X: 620, Y: 120}, "emit_acceptance_event": {X: 920, Y: 120},
			},
			Viewport: map[string]float64{"x": 0, "y": 0, "zoom": 1},
		},
	}
	published, approvedDraft := publishSystemDefinitionCreateFixture(
		t, handler, "sales_manager", "automation-after-author", "automation-after-approver",
		"automation-after-governed-evidence", "automation_rule", ruleKey, rule, "automation.rule",
	)
	if published.SourceKind != "builder" || published.SourceID != "automation-after-governed-evidence" {
		t.Fatalf("published Metadata must bind the system-draft source identity: %#v", published)
	}
	draft := runtimeFixtureRequest[changeplanmodel.BusinessChangePlanDraft](
		t, handler, "sales_manager", http.MethodGet, "/tenant-admin/change-plans/automation-after-governed-evidence", nil,
	)
	if approvedDraft.Status != "approved" || approvedDraft.CreatedBy != "automation-after-author" || approvedDraft.UpdatedBy != "automation-after-approver" {
		t.Fatalf("automation governance lost independent author/reviewer evidence before publish: %#v", approvedDraft)
	}
	if draft.Status != "published" || draft.CreatedBy != "automation-after-author" {
		t.Fatalf("automation governance did not persist the published draft: %#v", draft)
	}
	var reviewedPlan changeplanmodel.BusinessSystemChangePlan
	if err := json.Unmarshal(draft.Payload, &reviewedPlan); err != nil {
		t.Fatal(err)
	}
	if len(reviewedPlan.Items) != 1 || reviewedPlan.Items[0].ResourceOwner != "manual" {
		t.Fatalf("new Automation must retain its manual governance owner in the reviewed plan: %#v", reviewedPlan.Items)
	}
	actualRule := runtimeFixtureRequest[automationmodel.AutomationRuleSchema](t, handler, "sales_manager", http.MethodGet, "/automation-rules/"+ruleKey, nil)
	if !actualRule.Enabled || len(actualRule.Instructions) != 3 {
		t.Fatalf("published rule is not queryable with all three after instructions: %#v", actualRule)
	}

	schema := runtimeFixtureRequest[metadatamodel.MetadataSchemaSnapshot](t, handler, "sales_manager", http.MethodGet, "/tenant-admin/runtime-schema", nil)
	foundAction := false
	for _, action := range schema.Actions {
		if action.Key != actionKey {
			continue
		}
		foundAction = true
		if len(action.PayloadFields) != 2 || action.InputType == "" || action.OutputType == "" ||
			action.InputContractSHA256 == "" || action.OutputContractSHA256 == "" || action.EffectSet == nil {
			t.Fatalf("source-owned Action catalog lost its executable payload/owner contract: %#v", action)
		}
	}
	if !foundAction {
		t.Fatalf("source-owned Action %s is absent from the published Runtime schema", actionKey)
	}

	businessSession := runtimeIdentityFixtureSession(t, businessUser, "automation_business_tester")
	effective := runtimeFixtureAuthorizedSurfaceRequest[map[string]any](
		t, handler, businessSession.AccessToken, "business_workspace", http.MethodGet, "/permissions/effective", nil,
	)
	for _, permission := range []string{"customer.create", "customer.update", actionKey, "ops.workflow.run"} {
		if !runtimeFixtureFunctionPermissionAllowed(t, effective, permission) && permission != "customer.create" && permission != "customer.update" {
			t.Fatalf("business identity is missing backend permission %s: %#v", permission, effective)
		}
	}
	if runtimeFixtureFunctionPermissionAllowed(t, effective, "workspace.admin") ||
		!runtimeFixtureObjectActionAllowed(t, effective, "customer", "create") ||
		!runtimeFixtureObjectActionAllowed(t, effective, "customer", "update") {
		t.Fatalf("business identity must remain non-admin with exact customer write access: %#v", effective)
	}

	created := runtimeFixtureAuthorizedSurfaceRequest[recordmodel.Record](
		t, handler, businessSession.AccessToken, "business_workspace", http.MethodPost, "/objects/customer/records",
		map[string]any{"data": map[string]any{
			"name": "After-save governed automation evidence", "status": "risk", "owner": businessUser,
			"business_license_image": "mock://invalid-license", "business_license_no": "91310000INVALID",
		}},
	)
	if created.ID == "" || created.Data["verification_status"] != "pending" {
		t.Fatalf("business create did not commit before the after-save worker: %#v", created)
	}
	activity := runtimeFixtureRequest[struct {
		Outbox []map[string]any `json:"outbox"`
	}](t, handler, "platform_admin", http.MethodGet, "/operations/integrations/activity", nil)
	foundDurableRuleEvent := false
	for _, message := range activity.Outbox {
		eventID, _ := message["event_id"].(string)
		if message["operation"] == ruleKey && bytes.Contains([]byte(eventID), []byte(created.ID)) {
			foundDurableRuleEvent = true
		}
	}
	if !foundDurableRuleEvent {
		t.Fatalf("committed customer did not enqueue the governed after-save event: %#v", activity.Outbox)
	}
	runtimeFixtureRequest[map[string]any](t, handler, "platform_admin", http.MethodPost, "/operations/integrations/outbox/process-due?limit=20", nil)

	refreshed := runtimeFixtureAuthorizedSurfaceRequest[recordmodel.Record](
		t, handler, businessSession.AccessToken, "business_workspace", http.MethodGet, "/objects/customer/records/"+created.ID, nil,
	)
	if refreshed.Data["verification_status"] != "verified" {
		debugHistory := runtimeFixtureRequest[automationprojection.AutomationExecutionHistory](
			t, handler, "sales_manager", http.MethodGet,
			"/automation-rules/executions?rule_key="+url.QueryEscape(ruleKey)+"&record_id="+url.QueryEscape(created.ID)+"&phase=after", nil,
		)
		debugActivity := runtimeFixtureRequest[struct {
			Outbox []map[string]any `json:"outbox"`
		}](t, handler, "platform_admin", http.MethodGet, "/operations/integrations/activity", nil)
		t.Fatalf("source-owned Action result was not visible after the worker completed: record=%#v history=%#v outbox=%#v", refreshed, debugHistory, debugActivity.Outbox)
	}
	processes := runtimeFixtureAuthorizedSurfaceRequest[[]workflowapplication.BusinessWorkflowProcessDTO](
		t, handler, businessSession.AccessToken, "business_workspace", http.MethodGet,
		"/business/workflow/processes?workflow_key="+url.QueryEscape(workflowKey)+"&record_id="+url.QueryEscape(created.ID)+"&limit=20", nil,
	)
	if len(processes) != 1 || processes[0].WorkflowKey != workflowKey || processes[0].Status != "completed" {
		t.Fatalf("start_workflow did not persist one completed business process: %#v", processes)
	}

	reviewerSession := runtimeIdentityFixtureSession(t, "automation_history_reviewer_user", "automation_history_reviewer")
	history := runtimeFixtureAuthorizedSurfaceRequest[automationprojection.AutomationExecutionHistory](
		t, handler, reviewerSession.AccessToken, "admin_console", http.MethodGet,
		"/automation-rules/executions?rule_key="+url.QueryEscape(ruleKey)+"&record_id="+url.QueryEscape(created.ID)+"&phase=after", nil,
	)
	if history.Count != 1 || len(history.Items) != 1 || history.Items[0].Status != "succeeded" ||
		history.Items[0].ActorID != businessUser || history.Items[0].RoleKey != "automation_business_tester" {
		t.Fatalf("after execution history lost status or initiating backend identity: %#v", history)
	}
	traces, _ := history.Items[0].Trace["instructions"].([]any)
	if len(traces) != 3 {
		t.Fatalf("after execution history must preserve all three instruction traces: %#v", history.Items[0].Trace)
	}
	wantTypes := []string{"invoke_business_action", "start_workflow", "emit_event"}
	for index, rawTrace := range traces {
		trace, _ := rawTrace.(map[string]any)
		if trace["type"] != wantTypes[index] || trace["status"] != "success" {
			t.Fatalf("instruction %d lost typed success evidence: %#v", index, trace)
		}
	}
	actionTrace, _ := traces[0].(map[string]any)
	workflowTrace, _ := traces[1].(map[string]any)
	eventTrace, _ := traces[2].(map[string]any)
	if actionTrace["invocation_id"] == nil || actionTrace["invocation_id"] == "" {
		t.Fatalf("source-owned Action trace must bind its durable invocation: %#v", actionTrace)
	}
	workflowData, _ := workflowTrace["data"].(map[string]any)
	eventData, _ := eventTrace["data"].(map[string]any)
	if workflowData["execution_id"] == nil || workflowData["status"] != "completed" ||
		eventData["event_type"] != "customer.after_save.accepted" || eventData["record_id"] != created.ID {
		t.Fatalf("workflow/event traces lost their backend execution evidence: workflow=%#v event=%#v", workflowData, eventData)
	}
}
