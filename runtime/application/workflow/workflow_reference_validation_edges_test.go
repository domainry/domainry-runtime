package workflow

import (
	"context"
	"errors"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestWorkflowPublishValidatesAgentTaskServicePrincipalAndActionRiskReferences(t *testing.T) {
	schema := workflowReferenceSchemaEdgeStub{snapshot: WorkflowSchemaSnapshot{
		Actions:                []definitionmodel.ActionSchema{{Key: "customer.delete", ObjectKey: "order", Kind: definitionmodel.ActionKindRecordDelete, RiskLevel: "critical"}},
		AgentTasks:             []agentsdk.AgentTaskDefinition{{Key: "customer.review", Version: "1.0.0", AgentKey: "agent", AllowedObjects: []string{"order"}, AllowedActions: []string{"customer.delete"}, AllowedOutcomes: []string{"success", "error"}, SideEffectMode: agentsdk.AgentTaskSideEffectActionAllowed, Enabled: true}},
		AgentServicePrincipals: []agentsdk.AgentServicePrincipalBinding{{Key: "review-service", UserID: "service-user", RoleKey: "service-role", Enabled: true}},
	}}
	validator := NewWorkflowReferenceValidator(schema, func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"order": {Key: "order"}}
	}, nil)
	node := definitionmodel.WorkflowGraphNode{ID: "agent", Type: "agent_task", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "review-service"}}}}
	if issues := validator.validateWorkflowAgentTaskReference(t.Context(), node); len(issues) != 0 {
		t.Fatalf("published high-risk task should rely on forced Proposal policy: %#v", issues)
	}

	node.Contract.AgentTask.TaskVersion = "2.0.0"
	if issues := validator.validateWorkflowAgentTaskReference(t.Context(), node); len(issues) != 1 || issues[0].Code != "backend.workflow.agent_task_not_published" {
		t.Fatalf("version issues=%#v", issues)
	}
	node.Contract.AgentTask.TaskVersion = "1.0.0"
	schema.snapshot.AgentServicePrincipals[0].Enabled = false
	validator.schema = schema
	if issues := validator.validateWorkflowAgentTaskReference(t.Context(), node); len(issues) != 1 || issues[0].Code != "backend.workflow.agent_service_principal_not_published" {
		t.Fatalf("service issues=%#v", issues)
	}
	schema.snapshot.AgentServicePrincipals[0].Enabled = true
	schema.snapshot.Actions = nil
	validator.schema = schema
	if issues := validator.validateWorkflowAgentTaskReference(t.Context(), node); len(issues) != 1 || issues[0].Code != "backend.workflow.agent_action_not_published" {
		t.Fatalf("action issues=%#v", issues)
	}
}

type workflowReferenceSchemaEdgeStub struct {
	snapshot WorkflowSchemaSnapshot
	adapters map[string]bool
}

func (s workflowReferenceSchemaEdgeStub) WorkflowSchemaSnapshot(context.Context, principalmodel.Principal) WorkflowSchemaSnapshot {
	return s.snapshot
}

func (s workflowReferenceSchemaEdgeStub) ConnectorAdapterExists(_ context.Context, key string) bool {
	return s.adapters[key]
}

type workflowReferenceIdentityEdgeStub struct {
	workflowDirectoryTestStub
	users    []identitysdk.User
	roles    []identitysdk.Role
	usersErr error
	rolesErr error
}

func (s workflowReferenceIdentityEdgeStub) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	return s.users, s.usersErr
}

func (s workflowReferenceIdentityEdgeStub) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return s.roles, s.rolesErr
}

func workflowReferenceValidatorFixture(identity identitysdk.Directory) *WorkflowReferenceValidator {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}, {Key: "disabled", Type: "user", DisabledAt: "now"}, {Key: "status", Type: "text"}}}
	actions := []definitionmodel.ActionSchema{
		{Key: "send", Label: "Send", ObjectKey: "order", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "required", Required: true}, {Key: "optional"}}, Defaults: map[string]any{}},
		{Key: "defaulted", ObjectKey: "order", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "required", Required: true}}, Defaults: map[string]any{"required": "value"}},
		{Key: "field-defaulted", ObjectKey: "order", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "required", Required: true, DefaultValue: "value"}}, Defaults: map[string]any{}},
		{Key: "free", ObjectKey: "order"},
		{Key: "allowed-by-action", Label: "send", ObjectKey: "order"},
	}
	if identity == nil {
		identity = workflowReferenceIdentityEdgeStub{roles: []identitysdk.Role{{Key: "sender"}, {Key: "denied"}, {Key: "action-allowed"}}}
	}
	schema := workflowReferenceSchemaEdgeStub{snapshot: WorkflowSchemaSnapshot{
		Actions:      actions,
		Dictionaries: []appschemamodel.DictionarySchema{{Key: "states"}},
		Integrations: connectormodel.IntegrationSchema{Connectors: []connectormodel.ConnectorSchema{{Key: "erp", Operations: []connectormodel.ConnectorOperationSchema{{Key: "send"}}}}},
	}, adapters: map[string]bool{"email": true, "webhook": true, "custom": true}}
	return NewWorkflowReferenceValidator(schema, func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"order": object}
	}, identity)
}

func TestWorkflowReferenceTriggerObjectFieldDictionaryAndConnectorOutcomes(t *testing.T) {
	validator := workflowReferenceValidatorFixture(nil)
	if len(validator.validateWorkflowTriggerContract(t.Context(), definitionmodel.WorkflowSchema{Key: "flow"})) != 1 {
		t.Fatal("missing trigger issue absent")
	}
	if len(validator.validateWorkflowTriggerContract(t.Context(), definitionmodel.WorkflowSchema{Key: "flow", TriggerContract: &definitionmodel.WorkflowTriggerContract{}})) != 1 {
		t.Fatal("empty trigger type issue absent")
	}
	if len(validator.validateWorkflowTriggerContract(t.Context(), definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "invalid"}})) != 1 {
		t.Fatal("invalid trigger issue absent")
	}
	for _, triggerType := range []string{"manual", "record_created", "record_updated", "action_completed", "scheduled"} {
		if issues := validator.validateWorkflowTriggerContract(t.Context(), definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: triggerType}}); len(issues) != 0 {
			t.Fatalf("type=%s issues=%v", triggerType, issues)
		}
	}
	workflow := definitionmodel.WorkflowSchema{Key: "flow", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "field_changed", ObjectKey: "order", ObjectKeys: []string{"missing"}, FieldKey: "status"}}
	if issues := validator.validateWorkflowTriggerContract(t.Context(), workflow); len(issues) != 1 {
		t.Fatalf("object issues=%v", issues)
	}
	workflow.TriggerContract.FieldKey = "missing"
	workflow.ConditionContract = &definitionmodel.WorkflowConditionContract{Type: "invalid"}
	if issues := validator.validateWorkflowTriggerContract(t.Context(), workflow); len(issues) != 3 {
		t.Fatalf("field/condition issues=%v", issues)
	}
	workflow.ConditionContract = &definitionmodel.WorkflowConditionContract{Type: "always"}
	if issues := validator.validateWorkflowTriggerContract(t.Context(), workflow); len(issues) != 2 {
		t.Fatalf("valid condition issues=%v", issues)
	}
	for _, field := range []string{"id", "created_at", "updated_at", "status"} {
		if !validator.objectHasWorkflowField(t.Context(), "order", field) {
			t.Fatalf("field %s missing", field)
		}
	}
	if validator.objectHasWorkflowField(t.Context(), "order", "disabled") || validator.objectHasWorkflowField(t.Context(), "missing", "status") {
		t.Fatal("invalid field accepted")
	}
	if !validator.workflowDictionaryExists(t.Context(), "states") || validator.workflowDictionaryExists(t.Context(), "missing") {
		t.Fatal("dictionary lookup mismatch")
	}
	for _, testCase := range []struct {
		connector, operation string
		exists               bool
	}{
		{"erp", "", true}, {"erp", "send", true}, {"erp", "missing", false}, {"missing", "", false},
		{"erp", "<nil>", true},
		{"email", "send_email", true}, {"email", "test_connection", true}, {"email", "missing", false},
		{"email", "", true}, {"email", "<nil>", true}, {"webhook", "anything", true}, {"custom", "", true}, {"custom", "anything", true},
	} {
		if actual := validator.workflowConnectorExists(t.Context(), testCase.connector, testCase.operation); actual != testCase.exists {
			t.Fatalf("connector=%s operation=%s actual=%v", testCase.connector, testCase.operation, actual)
		}
	}
}

func TestWorkflowReferenceDependencyMapRecursiveAndActionBindingOutcomes(t *testing.T) {
	validator := workflowReferenceValidatorFixture(nil)
	values := map[string]any{
		"object_key": "missing", "field_key": "missing", "field": "$payload.dynamic", "dictionary_key": "missing",
		"connector_key": "erp", "operation": "missing", "patch": map[string]any{"missing": true},
		"nested": map[string]any{"object_key": "order", "field": "missing"},
		"items":  []any{map[string]any{"object_key": "order", "dictionary_key": "missing"}, "ignored"},
	}
	if issues := validator.validateWorkflowDependencyMap(t.Context(), "node", values, "", ""); len(issues) < 6 {
		t.Fatalf("dependency issues=%v", issues)
	}
	valid := map[string]any{"object_key": "order", "field_key": "status", "dictionary_key": "states", "connector_key": "erp", "operation": "send", "patch": map[string]any{"status": true}}
	if issues := validator.validateWorkflowDependencyMap(t.Context(), "node", valid, "", ""); len(issues) != 0 {
		t.Fatalf("valid dependency issues=%v", issues)
	}
	if issues := validator.validateWorkflowDependencyMap(t.Context(), "node", map[string]any{}, "order", "erp"); len(issues) != 0 {
		t.Fatalf("inherited issues=%v", issues)
	}
	for name, values := range map[string]map[string]any{
		"empty dependency":       {"field_key": "", "field": "", "dictionary_key": ""},
		"field without object":   {"field_key": "status"},
		"patch without object":   {"patch": map[string]any{"status": true}},
		"inherited connector op": {"operation": "send"},
	} {
		inheritedConnector := ""
		if name == "inherited connector op" {
			inheritedConnector = "erp"
		}
		if issues := validator.validateWorkflowDependencyMap(t.Context(), "node", values, "", inheritedConnector); len(issues) != 0 {
			t.Fatalf("%s issues=%v", name, issues)
		}
	}
	if issues := validator.validateWorkflowActionDependencies(t.Context(), "node", definitionmodel.ActionSchema{}); len(issues) != 0 {
		t.Fatalf("empty action dependencies=%v", issues)
	}
	if issues := validator.validateWorkflowActionDependencies(t.Context(), "node", definitionmodel.ActionSchema{ObjectKey: "missing"}); len(issues) == 0 {
		t.Fatal("missing action object accepted")
	}
	if issues := validator.validateWorkflowActionBinding(t.Context(), "node", "missing", nil); len(issues) != 1 {
		t.Fatalf("missing action issues=%v", issues)
	}
	if issues := validator.validateWorkflowActionBinding(t.Context(), "node", "send", nil); len(issues) != 1 {
		t.Fatalf("required input issues=%v", issues)
	}
	if issues := validator.validateWorkflowActionBinding(t.Context(), "node", "send", map[string]any{"required": "value", "unknown": true}); len(issues) != 1 {
		t.Fatalf("unknown input issues=%v", issues)
	}
	if issues := validator.validateWorkflowActionBinding(t.Context(), "node", "defaulted", nil); len(issues) != 0 {
		t.Fatalf("defaulted issues=%v", issues)
	}
	if issues := validator.validateWorkflowActionBinding(t.Context(), "node", "field-defaulted", nil); len(issues) != 0 {
		t.Fatalf("field default issues=%v", issues)
	}
	if issues := validator.validateWorkflowActionBinding(t.Context(), "node", "free", map[string]any{"anything": true}); len(issues) != 0 {
		t.Fatalf("free input issues=%v", issues)
	}
	if workflowValueOrDefault(" value ", "fallback") != "value" || workflowValueOrDefault("", "fallback") != "fallback" {
		t.Fatal("value default mismatch")
	}
	if workflowActionName(definitionmodel.ActionSchema{Label: " Label ", Key: "key"}) != "Label" || workflowActionName(definitionmodel.ActionSchema{Key: " key "}) != "key" {
		t.Fatal("action name mismatch")
	}
}

func TestWorkflowReferenceIdentityResolversRunAsAndGraphNodeOutcomes(t *testing.T) {
	identity := workflowReferenceIdentityEdgeStub{users: []identitysdk.User{{ID: "user"}}, roles: []identitysdk.Role{{Key: "directory-role"}}}
	validator := workflowReferenceValidatorFixture(identity)
	users, roles := validator.workflowIdentityReferenceCatalog(t.Context())
	if !users["user"] || !roles["directory-role"] || roles["sender"] {
		t.Fatalf("users=%v roles=%v", users, roles)
	}
	catalogUsers, catalogRoles := users, roles
	failingIdentity := workflowReferenceIdentityEdgeStub{usersErr: errors.New("users"), rolesErr: errors.New("roles")}
	users, roles = workflowReferenceValidatorFixture(failingIdentity).workflowIdentityReferenceCatalog(t.Context())
	if len(users) != 0 || len(roles) != 0 {
		t.Fatalf("failed Identity directory must not fall back to Runtime roles: users=%v roles=%v", users, roles)
	}
	workflow := definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"}}
	resolvers := []definitionmodel.WorkflowAssigneeResolver{
		{Type: "users", UserIDs: []string{"user", "missing"}},
		{Type: "role", RoleKey: "missing"},
		{Type: "record_field", Field: "missing"},
		{Type: "manager", UserField: "missing"},
		{Type: "manager_of", UserField: "owner"},
		{Type: "record_field", Field: "status"},
		{Type: "unknown"},
	}
	if issues := validator.validateWorkflowResolvers(t.Context(), workflow, "node", resolvers, catalogUsers, catalogRoles); len(issues) != 5 {
		t.Fatalf("resolver issues=%v", issues)
	}
	validResolvers := []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"unchecked"}}, {Type: "role", RoleKey: "sender"}, {Type: "record_field", Field: "owner"}}
	validValidator := workflowReferenceValidatorFixture(workflowReferenceIdentityEdgeStub{
		users: []identitysdk.User{{ID: "unchecked"}},
		roles: []identitysdk.Role{{Key: "sender"}},
	})
	validUsers, validRoles := validValidator.workflowIdentityReferenceCatalog(t.Context())
	if issues := validValidator.validateWorkflowResolvers(t.Context(), workflow, "node", validResolvers, validUsers, validRoles); len(issues) != 0 {
		t.Fatalf("valid resolver issues=%v", issues)
	}
	if issues := validValidator.validateWorkflowResolvers(t.Context(), workflow, "node", validResolvers, map[string]bool{}, map[string]bool{}); len(issues) != 0 {
		t.Fatalf("fresh external Identity catalog must defer existence checks until roles and users are provisioned: %v", issues)
	}
	manual := definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}}
	if validator.workflowObjectHasField(t.Context(), manual, "anything") || validator.workflowObjectHasField(t.Context(), manual, "") {
		t.Fatal("manual field fallback mismatch")
	}
	if !validator.workflowObjectHasField(t.Context(), workflow, "owner") || validator.workflowObjectHasField(t.Context(), workflow, "disabled") {
		t.Fatal("workflow object field mismatch")
	}
	mixedObjects := workflow
	mixedObjects.TriggerContract = &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKeys: []string{"missing", "order"}}
	if validator.workflowObjectHasField(t.Context(), mixedObjects, "owner") {
		t.Fatal("field missing from one trigger object must fail closed")
	}
	missingObjects := workflow
	missingObjects.TriggerContract = &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "missing"}
	if validator.workflowObjectHasField(t.Context(), missingObjects, "owner") {
		t.Fatal("missing object field found")
	}
	runAsValidator := workflowReferenceValidatorFixture(nil)
	action := runAsValidator.workflowActions(t.Context())["send"]
	if issues := runAsValidator.validateWorkflowRunAsActionPermission(t.Context(), definitionmodel.WorkflowSchema{}, "node", action); issues != nil {
		t.Fatalf("empty run-as issues=%v", issues)
	}
	withoutPermission := action
	withoutPermission.Key = ""
	if issues := runAsValidator.validateWorkflowRunAsActionPermission(t.Context(), definitionmodel.WorkflowSchema{RunAs: "sender"}, "node", withoutPermission); issues != nil {
		t.Fatalf("empty permission issues=%v", issues)
	}
	if issues := runAsValidator.validateWorkflowRunAsActionPermission(t.Context(), definitionmodel.WorkflowSchema{RunAs: "missing"}, "node", action); len(issues) != 1 {
		t.Fatalf("missing role issues=%v", issues)
	}
	if issues := runAsValidator.validateWorkflowRunAsActionPermission(t.Context(), definitionmodel.WorkflowSchema{RunAs: "denied"}, "node", action); len(issues) != 0 {
		t.Fatalf("known role issues=%v", issues)
	}
	if issues := runAsValidator.validateWorkflowRunAsActionPermission(t.Context(), definitionmodel.WorkflowSchema{RunAs: "sender"}, "node", action); len(issues) != 0 {
		t.Fatalf("allowed issues=%v", issues)
	}
	if issues := runAsValidator.validateWorkflowRunAsActionPermission(t.Context(), definitionmodel.WorkflowSchema{RunAs: "action-allowed"}, "node", runAsValidator.workflowActions(t.Context())["allowed-by-action"]); len(issues) != 0 {
		t.Fatalf("same-key Action permission issues=%v", issues)
	}

	approval := definitionmodel.WorkflowApprovalNodeContract{DueSeconds: -1, ReminderActionKey: "send", ReminderInput: map[string]any{"required": "value"}, Resolvers: resolvers, EscalationResolvers: resolvers}
	cc := definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "send", Resolvers: resolvers, Input: map[string]any{"required": "value"}}
	actionContract := definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "send", ObjectKey: "missing", Input: map[string]any{"required": "value"}}
	graphWorkflow := workflow
	graphWorkflow.RunAs = "denied"
	graphWorkflow.Graph = &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{
		{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &approval}},
		{ID: "cc", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &cc}},
		{ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &actionContract}},
		{ID: "ignored", Type: "trigger"},
	}}
	if issues := validator.validateWorkflowReferences(t.Context(), graphWorkflow); len(issues) == 0 {
		t.Fatal("expected graph reference issues")
	}
	negativeEscalation := definitionmodel.WorkflowApprovalNodeContract{EscalationSeconds: -1, ReminderActionKey: "missing"}
	missingCCAction := definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "missing"}
	emptyActionObject := definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "free"}
	validActionObject := definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "free", ObjectKey: "order"}
	missingAction := definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "missing"}
	graphWorkflow.RunAs = ""
	graphWorkflow.Graph = &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{
		{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &negativeEscalation}},
		{ID: "cc", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &missingCCAction}},
		{ID: "empty-action-object", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &emptyActionObject}},
		{ID: "valid-action-object", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &validActionObject}},
		{ID: "missing-action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &missingAction}},
	}}
	if issues := validator.validateWorkflowReferences(t.Context(), graphWorkflow); len(issues) != 4 {
		t.Fatalf("focused graph issues=%v", issues)
	}
	graphWorkflow.Graph = nil
	if issues := validator.validateWorkflowReferences(t.Context(), graphWorkflow); len(issues) != 0 {
		t.Fatalf("nil graph issues=%v", issues)
	}
}
