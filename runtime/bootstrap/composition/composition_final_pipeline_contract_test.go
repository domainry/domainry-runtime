package composition

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	businessintegration "github.com/domainry/domainry-runtime/runtime/application/integration"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestPipelineCompositionAllowsMissingOptionalAutomation(t *testing.T) {
	runtime, _, object, record, principal := pipelineFailureFixture(t, 0, false, false)
	transition := newPipelineTransitionApplicationService(runtime)
	runtime.automationApplicationService = nil
	if _, err := transition.Persist(t.Context(), object, record, record.Data, pipelineAction(), nil, "stage_new", "stage_old", principal); err != nil {
		t.Fatalf("pipeline persist without optional automation error=%v", err)
	}
}

func TestCompositionDeclaredConnectorPorts(t *testing.T) {
	registry := businessintegration.NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
		{Key: "other", Provider: "missing"}, {Key: "declared", Provider: "missing"},
	}})
	provider := runtimeWorkflowSchemaProvider{records: &runtimeAssembly{connectorRegistry: registry}}
	if provider.ConnectorAdapterExists(t.Context(), "declared") {
		t.Fatal("declared connector without adapter must not report ready")
	}
	if provider.ConnectorAdapterExists(t.Context(), "other") {
		t.Fatal("second declared connector without adapter must not report ready")
	}
}

func TestCompositionRegistriesListNonEmptyOwners(t *testing.T) {
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifestmodel.ManifestSchema{
		Workflows:       []definitionmodel.WorkflowSchema{{Key: "customer.follow_up"}},
		AutomationRules: []automationmodel.AutomationRuleSchema{{Key: "customer.created"}},
	}})
	if workflows := (runtimeWorkflowRegistry{records: runtime}).List(); len(workflows) != 1 || workflows[0].Key != "customer.follow_up" {
		t.Fatalf("workflow registry=%#v", workflows)
	}
	if rules := (runtimeAutomationRuleRegistry{records: runtime}).List(); len(rules) != 1 || rules[0].Key != "customer.created" {
		t.Fatalf("automation registry=%#v", rules)
	}
}

func TestCompositionFinalNilAdapterEdges(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default", UserID: "user-1"}}
	workflowAdapter := businessReferenceRuntimeAdapter{records: &runtimeAssembly{}, workflows: assembleWorkflowApplication(&runtimeAssembly{})}
	if values, err := workflowAdapter.WorkflowProcesses(t.Context(), principal, workflowmodel.WorkflowProcessFilter{}); err != nil || len(values) != 0 {
		t.Fatalf("workflow values=%#v err=%v", values, err)
	}
	for _, adapter := range []businessReferenceRuntimeAdapter{{}, {records: &runtimeAssembly{}}} {
		if values, err := adapter.PublishedSchedulerDefinitions(t.Context(), principal); err != nil || len(values) != 0 {
			t.Fatalf("scheduler values=%#v err=%v", values, err)
		}
	}
	assembled := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	_, _ = (businessReferenceRuntimeAdapter{records: assembled}).PublishedSchedulerDefinitions(t.Context(), principal)

}

func TestCompositionFinalRecordAssuranceEdges(t *testing.T) {
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	validate := buildRecordApplicationDependencies(runtime).ValidateExportAssurance
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default", UserID: "user-1"}}
	for _, object := range []definitionmodel.ObjectSchema{
		{Key: "nil-policy"},
		{Key: "empty-policy", ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{}},
	} {
		if evidence, err := validate(t.Context(), object, principal, nil, ""); err != nil || evidence != nil {
			t.Fatalf("object=%s evidence=%#v err=%v", object.Key, evidence, err)
		}
	}
	normal := definitionmodel.ObjectSchema{Key: "normal", ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}}}
	if evidence, err := validate(t.Context(), normal, principal, nil, ""); err != nil || evidence["methods"] != definitionmodel.ActionAssuranceNormalLogin {
		t.Fatalf("normal evidence=%#v err=%v", evidence, err)
	}
	otp := definitionmodel.ObjectSchema{Key: "otp", ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceOTP}}}
	if _, err := validate(t.Context(), otp, principal, map[string]any{"export": true}, ""); err == nil {
		t.Fatal("missing assurance grant was accepted")
	}
	store := &actionWiringAssuranceStore{}
	runtime.actionAssuranceStore = store
	validate = buildRecordApplicationDependencies(runtime).ValidateExportAssurance
	payload := map[string]any{"export": true}
	issuer := actionservice.NewActionAssuranceDomainService(store, nil)
	token, _, err := issuer.IssueVerifiedGrant(t.Context(), actionmodel.ActionAssuranceIssueRequest{
		WorkspaceID: principal.WorkspaceID, UserID: principal.UserID,
		ActionKey: definitionmodel.ObjectExportAssuranceActionKey(otp.Key), ObjectKey: otp.Key,
		Payload: payload, VerifiedMethods: []string{definitionmodel.ActionAssuranceOTP},
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if evidence, err := validate(t.Context(), otp, principal, payload, token); err != nil || evidence["grant_id"] == "" {
		t.Fatalf("otp evidence=%#v err=%v", evidence, err)
	}
}

func TestCompositionFinalSchedulerAndWorkflowSchemaEdges(t *testing.T) {
	calls := 0
	adapter := schedulerOperationRuntimeAdapter{processExecutions: func(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
		calls++
		return workflowmodel.WorkflowProcessResult{}, nil
	}}
	if _, err := adapter.ProcessDueWorkflowExecutionsForTarget(t.Context(), "target", 1, principalmodel.Principal{}); err != nil || calls != 1 {
		t.Fatalf("fallback calls=%d err=%v", calls, err)
	}
	adapter.processTargetedExecutions = func(context.Context, string, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
		calls++
		return workflowmodel.WorkflowProcessResult{}, nil
	}
	if _, err := adapter.ProcessDueWorkflowExecutionsForTarget(t.Context(), "target", 1, principalmodel.Principal{}); err != nil || calls != 2 {
		t.Fatalf("targeted calls=%d err=%v", calls, err)
	}

	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	runtime.actionService = nil
	_ = (runtimeWorkflowSchemaProvider{records: runtime}).WorkflowSchemaSnapshot(t.Context(), principalmodel.Principal{})
	runtime = newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifestmodel.ManifestSchema{Actions: []definitionmodel.ActionSchema{{Key: "manifest.action", ObjectKey: "manifest"}}}})
	runtime.actionService.ReplaceDefinitions([]definitionmodel.ActionSchema{{Key: "different.action", ObjectKey: "different"}})
	_ = (runtimeWorkflowSchemaProvider{records: runtime}).WorkflowSchemaSnapshot(t.Context(), principalmodel.Principal{})
}
