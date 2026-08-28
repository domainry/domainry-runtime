package validation

import (
	"fmt"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"
	"testing"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestManifestDictionaryAndStructuredOptionEdges(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Dictionaries: []metadatamodel.DictionarySchema{
		{Key: "", Items: []metadatamodel.DictionaryItemSchema{{Key: ""}, {Key: "same"}, {Key: "same"}, {Key: "valid", Value: "中文"}}},
		{Key: "known"},
	}}
	state := newValidationState(manifest, nil)
	state.validateDictionaries()
	fields := []definitionmodel.FieldSchema{
		{Validation: definitionmodel.FieldValidation{Options: []string{"valid", "中文"}}, Default: "missing", Config: map[string]any{"dictionary_key": "missing"}},
		{Options: []string{"raw"}},
		{Options: []any{"raw", 7, map[string]any{}, map[string]any{"value": "中文", "label": "Label"}, map[string]any{"value": "valid"}}},
		{Options: []map[string]any{{"value": "one"}, {"value": "two"}}, DefaultValue: "one"},
		{Options: struct{ Value string }{Value: "encoded"}},
		{Type: "integer", DefaultValue: "many"},
		{Type: "relation", DefaultValue: "record-1"},
	}
	for _, field := range fields {
		state.validateFieldOptions("field", field)
	}
	if len(state.errs) == 0 {
		t.Fatal("expected option diagnostics")
	}
	joined := fmt.Sprint(state.errs)
	if !strings.Contains(joined, "must match one of the stable option values") || !strings.Contains(joined, "relation defaults are unsupported") {
		t.Fatalf("Runtime field-default closure diagnostics missing: %v", state.errs)
	}
	if got := fieldAllowedValues(definitionmodel.FieldSchema{Options: []map[string]any{{"value": " one "}, {"value": ""}}}); len(got) != 1 || got[0] != "one" {
		t.Fatalf("allowed values = %#v", got)
	}
	if containsString([]string{"one"}, "missing") || !containsString([]string{"one"}, "one") {
		t.Fatal("containsString")
	}
}

func TestManifestObjectWritePolicyIsPublishedAndClosed(t *testing.T) {
	valid := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{
		{Key: "profile", Config: map[string]any{"write_policy": "direct_crud"}},
		{Key: "payment", Config: map[string]any{"write_policy": "action_only"}},
	}}, nil)
	valid.validateObjects()
	if len(valid.errs) != 0 {
		t.Fatalf("valid policies: %v", valid.errs)
	}
	invalid := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{
		{Key: "order", Config: map[string]any{"write_policy": "implicit"}},
		{Key: "ledger", Config: map[string]any{"write_policy": true}},
	}}, nil)
	invalid.validateObjects()
	if len(invalid.errs) != 2 {
		t.Fatalf("invalid policy diagnostics=%v", invalid.errs)
	}
}

func TestManifestRejectsStateMachineExternalEffects(t *testing.T) {
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}},
		Validations: []definitionmodel.ValidationSchema{{Type: "state_machine", FieldKey: "status", Config: map[string]any{
			"transitions": []any{
				map[string]any{"from": "draft", "to": "approved", "effects": []any{map[string]any{"type": "create_record", "target_object": "activity"}}},
			},
		}}},
	}}}, nil)
	state.validateObjects()
	if len(state.errs) != 1 || state.errs[0].Path != "objects[0].validations[0].config.transitions" || state.errs[0].Message != "backend.transition.effect_requires_action" {
		t.Fatalf("diagnostics=%#v", state.errs)
	}
}

func TestManifestAutomationValidationEdges(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "mail", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "send"}}}
	manifest := manifestmodel.ManifestSchema{
		Objects:      []definitionmodel.ObjectSchema{{Key: "customer"}},
		Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}},
		AutomationRules: []automationmodel.AutomationRuleSchema{
			{Key: "", ObjectKey: "missing", Trigger: automationmodel.AutomationTriggerSchema{Phase: "during", Operation: "read"}, Execution: automationmodel.AutomationExecutionPolicy{RunAs: "system"}},
			{Key: "same", ObjectKey: "customer", Trigger: automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"}, Execution: automationmodel.AutomationExecutionPolicy{Mode: "async"}, Instructions: []automationmodel.AutomationInstructionSchema{
				{Key: "", Type: "integration_call", ConnectorKey: "missing", ResultAlias: "same"},
				{Key: "duplicate", Type: "reserve", ConnectorKey: "mail", ConnectionKey: "connection", Operation: "missing", ResultAlias: "same", OnError: "continue"},
				{Key: "duplicate", Type: "invoke_business_action"},
			}},
			{Key: "same", ObjectKey: "customer", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"}},
		},
	}
	state := newValidationState(manifest, nil)
	state.validateAutomationRules()
	if len(state.errs) == 0 {
		t.Fatal("expected automation diagnostics")
	}
	if !connectorHasOperation(connector, " send ") || connectorHasOperation(connector, "missing") {
		t.Fatal("connector operation lookup")
	}
	if !automationBeforeInstructionTypeAllowed("derive_fields") || !automationBeforeInstructionTypeAllowed("assert") || automationBeforeInstructionTypeAllowed("invoke_business_action") || automationBeforeInstructionTypeAllowed("emit_event") {
		t.Fatal("before instruction types")
	}
}

func TestManifestGovernanceValidationEdges(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{
			{Key: "file", Config: map[string]any{"lifecycle_subject_identity": 1, "lifecycle_subject_file": true, "lifecycle_erase": "anonymize"}},
			{Key: "bad", Config: map[string]any{"lifecycle_erase": true}},
		}}},
		Reports: []reportmodel.ReportSchema{{Key: "known"}},
		SensitiveFieldPolicies: []reportmodel.ReportSensitiveFieldPolicySchema{
			{Key: "", ObjectKey: "missing"},
			{Key: "same", ObjectKey: "customer", Fields: []string{"missing"}},
			{Key: "same", ObjectKey: "customer", Fields: []string{"file"}, Sensitivity: "secret", ReadPolicy: "read", WritePolicy: "write", ExportPolicy: "mask", AuditEvent: "audit", Reason: "reason"},
		},
		ReportExportControls: []reportmodel.ReportExportControlSchema{
			{Key: "", ReportKey: "", SourceObjects: nil},
			{Key: "same", ReportKey: "missing", SourceObjects: []string{"missing"}, SensitiveFieldPolicyKeys: []string{"missing"}},
			{Key: "same", ReportKey: "known", SourceObjects: []string{"customer"}, SensitiveFieldPolicyKeys: []string{"same"}, AuditObject: "audit", DownloadObject: "download", ExportAction: "export", MaxRows: 1, Reason: "reason"},
		},
	}
	state := newValidationState(manifest, nil)
	state.validateGovernance()
	if len(state.errs) == 0 || !manifestHasReport(state, "known") || manifestHasReport(state, "missing") {
		t.Fatalf("governance result = %#v", state.errs)
	}
}

func TestManifestReportExportRecordMappingIsClosedAndObjectTyped(t *testing.T) {
	audit := definitionmodel.ObjectSchema{Key: "report_export_audit", Fields: []definitionmodel.FieldSchema{
		{Key: "report_key", Type: "text"}, {Key: "requested_by", Type: "user"},
		{Key: "status", Type: "select", Options: []map[string]any{{"label": "Requested", "value": "requested"}, {"label": "Prepared", "value": "prepared"}, {"label": "Downloaded", "value": "downloaded"}, {"label": "Denied", "value": "denied"}, {"label": "Expired", "value": "expired"}}},
		{Key: "row_count", Type: "integer"}, {Key: "filters_hash", Type: "text"},
	}}
	download := definitionmodel.ObjectSchema{Key: "report_export_download", Fields: []definitionmodel.FieldSchema{
		{Key: "audit_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "report_export_audit"}},
		{Key: "file_name", Type: "text"}, {Key: "content_hash", Type: "text"}, {Key: "expires_at", Type: "datetime"},
	}}
	control := reportmodel.ReportExportControlSchema{
		Key: "orders-export", ReportKey: "orders", SourceObjects: []string{"orders"}, AuditObject: audit.Key, DownloadObject: download.Key,
		ExportAction: "report_export_audit.prepare_export", MaxRows: 100, Reason: "governed export",
		AllowedQueryKeys: []string{"current"}, AllowedTags: []string{"reviewed"},
		RecordMapping: reportmodel.ReportExportRecordMappingSchema{
			AuditReportKeyField: "report_key", AuditRequesterField: "requested_by", AuditStatusField: "status",
			AuditPreparedStatuses: []string{"requested", "prepared"}, AuditPreparedStatus: "prepared", AuditDownloadedStatus: "downloaded", AuditDeniedStatus: "denied", AuditExpiredStatus: "expired",
			AuditRowCountField: "row_count", AuditScopeHashField: "filters_hash",
			DownloadAuditField: "audit_id", DownloadFilenameField: "file_name", DownloadContentHashField: "content_hash", DownloadExpiresAtField: "expires_at",
		},
	}
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "orders"}, audit, download}, Reports: []reportmodel.ReportSchema{{
		Key: "orders", Dataset: reportmodel.ReportDatasetSchema{
			QueryPredicates: []reportmodel.ReportDatasetPredicate{{Key: "current"}},
			TagPredicates:   []reportmodel.ReportDatasetPredicate{{Key: "reviewed"}},
		},
	}}, ReportExportControls: []reportmodel.ReportExportControlSchema{control}}
	state := newValidationState(manifest, nil)
	state.validateGovernance()
	if len(state.errs) != 0 {
		t.Fatalf("valid explicit record mapping rejected: %v", state.errs)
	}
	manifest.ReportExportControls[0].RecordMapping.AuditRequesterField = "requested_by_identity_user_id"
	state = newValidationState(manifest, nil)
	state.validateGovernance()
	if !strings.Contains(state.errs.Error(), `unknown field "report_export_audit".requested_by_identity_user_id`) {
		t.Fatalf("unknown requester mapping accepted: %v", state.errs)
	}
	manifest.ReportExportControls[0].RecordMapping.AuditRequesterField = "requested_by"
	manifest.ReportExportControls[0].AllowedQueryKeys = []string{"current", "current", "unknown"}
	manifest.ReportExportControls[0].AllowedTags = []string{"unknown"}
	state = newValidationState(manifest, nil)
	state.validateGovernance()
	if !strings.Contains(state.errs.Error(), "allowed_query_keys[1]") || !strings.Contains(state.errs.Error(), "allowed_query_keys[2]") || !strings.Contains(state.errs.Error(), "allowed_tags[0]") {
		t.Fatalf("undeclared or duplicate predicate allowlist accepted: %v", state.errs)
	}
}

func TestManifestSmallValidationAndReviewHelpers(t *testing.T) {
	if cloneJSONMap(make(chan int)) != nil || cloneJSONMap(struct{ Value string }{Value: "ok"})["Value"] != "ok" {
		t.Fatal("cloneJSONMap")
	}
	if (ValidationErrors{}).Error() != "" || (ValidationError{Message: "message"}).Error() != "message" {
		t.Fatal("empty validation errors")
	}
}

func TestManifestSourceIntentShellAndReviewPermissionEdges(t *testing.T) {
	state := newValidationState(manifestmodel.ManifestSchema{
		SchemaVersion: "old",
		SourceIntentCoverage: &manifestmodel.ManifestSourceIntentCoverage{UnmappedPaths: []string{"legacy.path"}, Entries: []manifestmodel.ManifestSourceIntentEntry{
			{SourcePath: "", Status: "unknown", Target: ""},
			{SourcePath: "same", Status: "compiled", Target: "object"},
			{SourcePath: "same", Status: "normalized", Target: "object"},
			{SourcePath: "composed", Status: "composed", Target: "object"},
			{SourcePath: "deferred", Status: "deferred", Target: "object"},
		}}}, nil)
	state.validateRequiredShell()
	state.validateSourceIntentCoverage()
	if len(state.errs) == 0 {
		t.Fatal("expected shell/source intent diagnostics")
	}
	state.manifest.SourceIntentCoverage = nil
	state.validateSourceIntentCoverage()

	result := ReviewManifestUpdate(manifestmodel.ManifestSchema{}, manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "new", Fields: []definitionmodel.FieldSchema{{Key: "required", Required: true}, {Key: "defaulted", Required: true, Default: "value"}}}}}, ReviewOptions{})
	if !result.HasBlockers() {
		t.Fatal("new required field without default was not blocked")
	}
}

func TestManifestSeedValidationEdges(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{
			{Key: "seeded", Fields: []definitionmodel.FieldSchema{{Key: "required", Required: true}, {Key: "relation", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "target"}}, {Key: "status", Validation: definitionmodel.FieldValidation{Options: []string{"open"}}}}},
			{Key: "target"}, {Key: "runtime", Config: map[string]any{"runtime_owned": true}}, {Key: "optional"}, {Key: "unseeded"},
		},
		IdentityProfileExtensions: []profilebindingmodel.Binding{{ObjectKey: "optional"}},
		SeedRecords: []businessseedmodel.SeedRecordSchema{
			{ObjectKey: "identity_user"}, {ObjectKey: "missing"},
			{ObjectKey: "seeded", Data: map[string]any{"__seed_key": "seeded-one", "relation": "$record:target-wrong", "status": "closed", "unknown": true}},
			{ObjectKey: "runtime", Data: map[string]any{"__seed_key": "target-wrong"}},
		},
		Reports: []reportmodel.ReportSchema{{Key: "report", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "unseeded", Alias: "unseeded"}}, EvidenceRequirements: []reportmodel.ReportEvidenceRequirement{{ObjectKey: "seeded", MinimumRecords: 1, RequiredNonEmptyFields: []string{"required"}}}}},
		Actions: []definitionmodel.ActionSchema{{Key: "action", ObjectKey: "unseeded"}},
	}
	state := newValidationState(manifest, nil)
	state.validateSeedRecords()
	if len(state.errs) < 5 {
		t.Fatalf("seed diagnostics = %#v", state.errs)
	}
	if hasNonEmptyString([]string{"", " "}) || !hasNonEmptyString([]string{"", "permission"}) {
		t.Fatal("non-empty string helper")
	}
}

func TestManifestObjectViewReferenceActionAndConnectionEdges(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{
			{Key: ""},
			{Key: "source", Fields: []definitionmodel.FieldSchema{{Key: ""}, {Key: "plain", Type: "text"}, {Key: "relation-no-target", Type: "relation"}, {Key: "relation", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "target"}}, {Key: "relation", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "missing"}}}, Validations: []definitionmodel.ValidationSchema{{FieldKey: "missing", Fields: []string{"missing"}}}},
			{Key: "source", Fields: []definitionmodel.FieldSchema{{Key: "plain", Type: "text"}, {Key: "relation-no-target", Type: "relation"}, {Key: "relation", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "target"}}}}, {Key: "target", Fields: []definitionmodel.FieldSchema{{Key: "label"}}},
		},
		Views:   []definitionmodel.ViewSchema{{Key: "", ObjectKey: "missing"}, {Key: "same", ObjectKey: "source"}, {Key: "same", ObjectKey: "source"}},
		Actions: []definitionmodel.ActionSchema{{Key: "", ObjectKey: "missing"}, {Key: "same", ObjectKey: "source"}, {Key: "same", ObjectKey: "source", RequiresPermission: "source.write"}},
		Integrations: integrationmodel.IntegrationSchema{
			Connectors: []integrationmodel.ConnectorSchema{
				{Key: "family", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "real"}}},
				{Key: "legacy", Provider: "local"},
				{Key: "multi-empty", Provider: "multi"}, {Key: "generated-empty", Provider: "generated"}, {Key: "blank-family"},
			},
			Connections: []integrationmodel.ConnectionSchema{
				{Key: "", ConnectorKey: ""}, {Key: "same", ConnectorKey: "missing"},
				{Key: "same", ConnectorKey: "family"}, {Key: "invalid-provider", ConnectorKey: "family", ProviderKey: "multi"},
				{Key: "unknown-provider", ConnectorKey: "family", ProviderKey: "missing"},
				{Key: "generated-provider", ConnectorKey: "family", ProviderKey: "generated"},
				{Key: "legacy-default", ConnectorKey: "legacy"},
				{Key: "multi-default", ConnectorKey: "multi-empty"}, {Key: "generated-default", ConnectorKey: "generated-empty"}, {Key: "blank-default", ConnectorKey: "blank-family"},
				{Key: "provider-keys", ConnectorKey: "legacy", ProviderKey: "local", Config: map[string]any{"providers": true, "mail_providers": true}},
				{Key: "legacy", ConnectorKey: "legacy", ProviderKey: "local", Config: map[string]any{"provider": "local", "nested": map[string]any{"password": "plain", "token_ref": "secret:token", "api_key": "env:KEY", "authorization": "vault:key"}}},
			},
		},
	}
	state := newValidationState(manifest, nil)
	state.validateObjects()
	state.validateViews()
	state.validateActions()
	state.validateIntegrationConnections()
	if len(state.errs) < 10 {
		t.Fatalf("object/integration diagnostics = %#v", state.errs)
	}
	if manifestConnectorHasProvider(integrationmodel.ConnectorSchema{Provider: "local"}, "local") || manifestConnectorHasProvider(integrationmodel.ConnectorSchema{Providers: []integrationmodel.ConnectorProviderSchema{{Key: "one"}}}, "missing") {
		t.Fatal("connector provider lookup")
	}
	paths := inlineSecretConfigPaths(map[string]any{"password": "secret:ref", "token": "env:TOKEN", "secret": "vault:key", "safe": "value"}, "prefix")
	if len(paths) != 0 {
		t.Fatalf("referenced secrets flagged: %#v", paths)
	}
}

func TestManifestWorkflowAndReportValidationEdges(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "known-action", ObjectKey: "source", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "", Required: true}, {Key: "required", Required: true}, {Key: "defaulted", Required: true, DefaultValue: "x"}}}
	graph := &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
		{Type: "action"},
		{Type: "action", Contract: &definitionmodel.WorkflowNodeContract{}},
		{Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "missing"}}},
		{Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "known-action", Input: map[string]any{"unknown": true}}}},
		{Type: "cc"},
		{Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{}},
		{Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "missing"}}},
	}}
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "source", Fields: []definitionmodel.FieldSchema{{Key: "known"}}}, {Key: "snapshot", Fields: []definitionmodel.FieldSchema{{Key: "field"}}}},
		Actions: []definitionmodel.ActionSchema{action},
		Workflows: []definitionmodel.WorkflowSchema{
			{Key: "", RunAs: "missing", Trigger: map[string]any{"phase": "before", "object_key": "missing", "object_keys": []any{"source", "", "missing"}}},
			{Key: "same", Graph: &definitionmodel.WorkflowGraphSchema{Version: 1}},
			{Key: "same", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "after_update", ObjectKey: "source", ObjectKeys: []string{"missing"}}, Graph: graph},
		},
		Reports: []reportmodel.ReportSchema{
			{Key: "", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "missing", Alias: "missing"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "missing", Field: reportmodel.ReportDatasetField{SourceAlias: "other", FieldKey: "missing"}}}}, RequiredPermissions: []string{"missing.read"}, EvidenceRequirements: []reportmodel.ReportEvidenceRequirement{{ObjectKey: "other", MinimumRecords: 0, RequiredNonEmptyFields: []string{"missing"}}}},
			{Key: "same", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "source", Alias: "source"}, Joins: []reportmodel.ReportDatasetJoin{{ObjectKey: "snapshot", Alias: "snapshot", SourceType: "snapshot"}}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "missing", Field: reportmodel.ReportDatasetField{SourceAlias: "source", FieldKey: "missing"}}, {Key: "field", Field: reportmodel.ReportDatasetField{SourceAlias: "snapshot", FieldKey: "field"}}}}, RequiredPermissions: []string{"source.read"}, EvidenceRequirements: []reportmodel.ReportEvidenceRequirement{{ObjectKey: "source", MinimumRecords: 1, RequiredNonEmptyFields: []string{"known"}}}},
			{Key: "same"},
		},
	}
	state := newValidationState(manifest, nil)
	state.validateWorkflows()
	state.validateReports()
	if len(state.errs) < 15 {
		t.Fatalf("workflow/report diagnostics = %#v", state.errs)
	}
	state.validateWorkflowActionInput("action", definitionmodel.ActionSchema{}, map[string]any{"ignored": true})
	if firstNonEmptyString("", " value ") != "value" || firstNonEmptyString("", "") != "" {
		t.Fatal("first non-empty helper")
	}
	keys := workflowObjectKeys(definitionmodel.WorkflowSchema{Trigger: map[string]any{"object_keys": []any{" one ", ""}}})
	if len(keys) != 1 || keys[0] != "one" {
		t.Fatalf("workflow object keys = %#v", keys)
	}
}

func TestManifestIntegrationEventContractTypesInvocationPaths(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Workflows: []definitionmodel.WorkflowSchema{{
			Key: "order.intake", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "integration_event"},
			InputFields: []definitionmodel.WorkflowInputField{{Key: "count", Type: "integer", Required: true}},
		}},
		Integrations: integrationmodel.IntegrationSchema{
			Connections: []integrationmodel.ConnectionSchema{{Key: "source", ProviderKey: "provider"}},
			EventMappings: []integrationmodel.IntegrationEventMappingSchema{{
				Key: "incoming", Provider: "provider", TargetType: "workflow", WorkflowKey: "order.intake",
				WorkflowInput:    map[string]string{"count": "payload.count"},
				EventFields:      []integrationmodel.IntegrationEventFieldSchema{{Path: "payload.count", Type: "integer", Required: true}},
				ExternalIdentity: integrationmodel.IntegrationExternalIdentityMappingSchema{SubjectPath: "actor.id"},
			}},
		},
	}
	state := newValidationState(manifest, nil)
	state.validateIntegrationEventMappings()
	if len(state.errs) != 0 {
		t.Fatalf("valid typed event mapping errors=%#v", state.errs)
	}

	manifest.Integrations.EventMappings[0].EventFields[0].Type = "text"
	state = newValidationState(manifest, nil)
	state.validateIntegrationEventMappings()
	if len(state.errs) == 0 || !strings.Contains(state.errs.Error(), "invocation.input_type_mismatch") {
		t.Fatalf("event-to-Workflow mismatch errors=%#v", state.errs)
	}
	manifest.Integrations.EventMappings[0].EventFields = nil
	state = newValidationState(manifest, nil)
	state.validateIntegrationEventMappings()
	if len(state.errs) == 0 || !strings.Contains(state.errs.Error(), "integration.event_path_undeclared") {
		t.Fatalf("undeclared event path errors=%#v", state.errs)
	}
}

func TestManifestIdentityProfileExtensionAndRemainingCoreEdges(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "profile", UX: map[string]any{"kind": "wrong"}, Fields: []definitionmodel.FieldSchema{{Key: "plain", Type: "text"}, {Key: "email", Type: "text"}}}},
		IdentityProfileExtensions: []profilebindingmodel.Binding{
			{},
			{ObjectKey: "missing", IdentityRelationField: "relation"},
			{ObjectKey: "profile", IdentityRelationField: "missing", Cardinality: "many", DefaultVisibility: "invalid", SummaryFields: []string{"missing"}, ProfileTabLabels: map[string]string{"unknown": "Unknown"}, ProfileTabFields: map[string][]string{"unknown": {"missing"}}, ProfileTabRelatedObjects: map[string][]string{"unknown": {"missing"}}, RequiredPermissions: []string{"missing.permission"}},
			{ObjectKey: "profile", IdentityRelationField: "plain"},
			{ObjectKey: "profile", IdentityRelationField: "plain", ProfileTabs: []string{"tab"}, ProfileTabRelatedObjects: map[string][]string{"tab": {"profile"}}},
		},
	}
	state := newValidationState(manifest, []integrationmodel.ConnectorSchema{{Key: ""}, {Key: " catalog "}})
	state.validateIdentityProfileExtensions()
	if len(state.errs) < 12 {
		t.Fatalf("profile diagnostics = %#v", state.errs)
	}
	state.manifest.NotificationTemplates = []notificationmodel.NotificationTemplate{{}}
	state.validateNotificationTemplates()
	if seedKey(businessseedmodel.SeedRecordSchema{}, 0) != "" {
		t.Fatal("empty seed generated a key")
	}

	previousExtension := profilebindingmodel.Binding{ObjectKey: "profile", ProfileTabs: []string{"old"}, ProfileTabLabels: map[string]string{"old": "Old"}, ProfileTabFields: map[string][]string{"old": {"one"}}, SummaryFields: []string{"one"}, RequiredPermissions: []string{"one"}, DefaultVisibility: "when_readable", StandaloneWorkspace: true}
	nextExtension := profilebindingmodel.Binding{ObjectKey: "profile", ProfileTabs: []string{"new"}, ProfileTabLabels: map[string]string{"new": "New"}, ProfileTabFields: map[string][]string{"new": {"two"}}, SummaryFields: []string{"two"}, RequiredPermissions: nil, DefaultVisibility: "hidden", StandaloneWorkspace: false}
	review := ReviewManifestUpdate(manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{previousExtension}}, manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{nextExtension}}, ReviewOptions{})
	if len(review.Changes) < 4 {
		t.Fatalf("profile review = %#v", review.Changes)
	}
	if !profileTabContractRemoves(previousExtension, nextExtension) || profileTabContractRemoves(profilebindingmodel.Binding{}, profilebindingmodel.Binding{}) {
		t.Fatal("profile tab removal detection")
	}
	if !profileTabContractRemoves(profilebindingmodel.Binding{ProfileTabComponents: map[string][]string{"tab": {"one"}}}, profilebindingmodel.Binding{ProfileTabComponents: map[string][]string{}}) {
		t.Fatal("profile component removal detection")
	}
	restrict := ReviewManifestUpdate(
		manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{{ObjectKey: "profile", DefaultVisibility: "when_readable"}}},
		manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{{ObjectKey: "profile", DefaultVisibility: "hidden", RequiredPermissions: []string{"new"}}}},
		ReviewOptions{},
	)
	if len(restrict.Changes) == 0 || restrict.Changes[0].Kind != "restrict_identity_profile_visibility" {
		t.Fatalf("restrict profile review = %#v", restrict.Changes)
	}
}

func TestManifestReviewAndUpdateRemainingEdges(t *testing.T) {
	fixture := loadFixtureManifest(t, "domain-only-minimal.json")
	if err := ValidateManifestUpdate(fixture, fixture, ReviewOptions{}); err != nil {
		t.Fatalf("unchanged valid manifest update: %v", err)
	}
	previous := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "object", Fields: []definitionmodel.FieldSchema{{Key: "field"}}}},
	}
	next := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "object", Fields: []definitionmodel.FieldSchema{{Key: "field", Required: true}}}}}
	review := ReviewManifestUpdate(previous, next, ReviewOptions{})
	found := false
	for _, change := range review.Changes {
		found = found || change.Kind == "add_required_without_default"
	}
	if !found {
		t.Fatalf("required transition review = %#v", review.Changes)
	}
}

func TestManifestValidatorConditionOutcomes(t *testing.T) {
	state := newValidationState(manifestmodel.ManifestSchema{
		SchemaVersion:        manifestmodel.CurrentManifestSchemaVersion,
		SourceIntentCoverage: &manifestmodel.ManifestSourceIntentCoverage{Version: "1"},
		Integrations:         integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: ""}, {Key: "zero"}, {Key: "with-op", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "send"}}}}},
		SeedRecords:          []businessseedmodel.SeedRecordSchema{{ObjectKey: "object", Data: map[string]any{"__seed_key": "explicit"}}, {ObjectKey: "object"}, {}},
	}, []integrationmodel.ConnectorSchema{{Key: "catalog"}})
	state.validateRequiredShell()
	state.validateSourceIntentCoverage()
	if mapString(map[string]any{"nil": nil}, "nil") != "" {
		t.Fatal("nil map string")
	}

	state.manifest.AutomationRules = []automationmodel.AutomationRuleSchema{
		{Key: "after", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "create"}, Execution: automationmodel.AutomationExecutionPolicy{RunAs: "initiator"}, Instructions: []automationmodel.AutomationInstructionSchema{{Key: "zero", Type: "integration_call", ConnectorKey: "zero", ConnectionKey: "connection", Operation: "send"}}},
		{Key: "before", Trigger: automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"}, Execution: automationmodel.AutomationExecutionPolicy{Mode: "sync"}, Instructions: []automationmodel.AutomationInstructionSchema{{Key: "call", Type: "integration_call", ConnectorKey: "with-op", ConnectionKey: "connection", Operation: "send", OnError: "block"}}},
		{Key: "before-default", Trigger: automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"}},
	}
	state.validateAutomationRules()

	state.manifest.Objects = []definitionmodel.ObjectSchema{{Key: "object", Fields: []definitionmodel.FieldSchema{
		{Key: "retain", Config: map[string]any{"lifecycle_erase": "retain"}},
		{Key: "delete", Config: map[string]any{"lifecycle_erase": "delete"}},
		{Key: "invalid", Config: map[string]any{"lifecycle_erase": "invalid"}},
	}}}
	state = newValidationState(state.manifest, nil)
	state.validateGovernance()
	state.validateFieldOptions("field", definitionmodel.FieldSchema{Default: "value"})

	state.validateSeedFieldValue("relation", "object", definitionmodel.FieldSchema{Type: "relation"}, "$record:")
	state.validateSeedFieldValue("option", "object", definitionmodel.FieldSchema{Validation: definitionmodel.FieldValidation{Options: []string{"open"}}}, "")
	state.manifest.SeedRecords = []businessseedmodel.SeedRecordSchema{{ObjectKey: "object", Data: map[string]any{}}}
	state.manifest.Objects = []definitionmodel.ObjectSchema{{Key: "object", Fields: []definitionmodel.FieldSchema{{Key: "default", Required: true, Default: "x"}, {Key: "default-value", Required: true, DefaultValue: "x"}}}}
	state = newValidationState(state.manifest, nil)
	state.validateSeedRecords()
	seedKey(businessseedmodel.SeedRecordSchema{ObjectKey: "object", Data: map[string]any{}}, 0)
	seedKey(businessseedmodel.SeedRecordSchema{ObjectKey: "object", Data: map[string]any{"__seed_key": ""}}, 0)
}

func TestManifestWorkflowConditionOutcomes(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "action", Defaults: map[string]any{"with-action-default": "x"}, PayloadFields: []definitionmodel.ActionPayloadField{
		{Key: "optional"}, {Key: "with-field-default", Required: true, DefaultValue: "x"}, {Key: "with-action-default", Required: true}, {Key: "present", Required: true},
	}}
	validGraph := func(nodes ...definitionmodel.WorkflowGraphNode) *definitionmodel.WorkflowGraphSchema {
		return &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: nodes}
	}
	state := newValidationState(manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "object", Fields: []definitionmodel.FieldSchema{{Key: "field"}}}},
		Actions: []definitionmodel.ActionSchema{action, {Key: "notification"}},
		Workflows: []definitionmodel.WorkflowSchema{
			{Key: "after", Trigger: map[string]any{"phase": "after"}, Graph: validGraph(definitionmodel.WorkflowGraphNode{Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{}}}, definitionmodel.WorkflowGraphNode{Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{}}})},
			{Key: "insertion", Trigger: map[string]any{"insertion_point": "save"}, Graph: validGraph(definitionmodel.WorkflowGraphNode{Type: "trigger"})},
			{Key: "before-type", Trigger: map[string]any{"type": "before_save"}, Graph: validGraph(definitionmodel.WorkflowGraphNode{Type: "trigger"})},
			{Key: "lifecycle", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "lifecycle_event", ObjectKey: "", ObjectKeys: []string{""}}, Graph: validGraph(definitionmodel.WorkflowGraphNode{Type: "trigger"})},
			{Key: "empty-graph", Graph: &definitionmodel.WorkflowGraphSchema{Version: 2}},
			{Key: "valid", Graph: validGraph(definitionmodel.WorkflowGraphNode{Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "action", Input: map[string]any{"present": true, "optional": true, "with-field-default": true, "with-action-default": true}}}}, definitionmodel.WorkflowGraphNode{Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "notification"}}})},
		},
		Reports: []reportmodel.ReportSchema{{Key: "allowed", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "object", Alias: "object"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "field", Field: reportmodel.ReportDatasetField{SourceAlias: "object", FieldKey: "field"}}}}, RequiredPermissions: []string{"object.read"}}},
	}, nil)
	state.validateWorkflows()
	state.validateReports()
	workflowObjectKeys(definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{ObjectKey: "", ObjectKeys: []string{""}}})
}

func TestManifestReviewShortCircuitOutcomes(t *testing.T) {
	base := profilebindingmodel.Binding{ObjectKey: "profile", IdentityRelationField: "identity", Cardinality: "one", DefaultVisibility: "when_readable", ProfileTabs: []string{"tab"}, ProfileTabLabels: map[string]string{"tab": "Tab"}, ProfileTabFields: map[string][]string{"tab": {"one"}}, ProfileTabComponents: map[string][]string{"tab": {"one"}}}
	cases := [][2]profilebindingmodel.Binding{
		{base, func() profilebindingmodel.Binding { next := base; next.Cardinality = "many"; return next }()},
		{base, func() profilebindingmodel.Binding {
			next := base
			next.ProfileTabFields = map[string][]string{"tab": {"two"}}
			return next
		}()},
		{base, func() profilebindingmodel.Binding {
			next := base
			next.ProfileTabComponents = map[string][]string{"tab": {"two"}}
			return next
		}()},
		{func() profilebindingmodel.Binding {
			previous := base
			previous.DefaultVisibility = "hidden"
			return previous
		}(), base},
		{base, func() profilebindingmodel.Binding {
			next := base
			next.StandaloneWorkspace = true
			return next
		}()},
		{func() profilebindingmodel.Binding {
			previous := base
			previous.StandaloneWorkspace = true
			return previous
		}(), base},
	}
	for _, pair := range cases {
		review := manifestReview{previous: manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{pair[0]}}, next: manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{pair[1]}}}
		review.reviewIdentityProfileExtensions()
	}
	hidden := base
	hidden.DefaultVisibility = "hidden"
	review := manifestReview{previous: manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{hidden}}, next: manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{hidden}}}
	review.reviewIdentityProfileExtensions()
	restrictedVisibility := base
	restrictedVisibility.DefaultVisibility = "hidden"
	review = manifestReview{previous: manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{base}}, next: manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{restrictedVisibility}}}
	review.reviewIdentityProfileExtensions()
	changedLabel := base
	changedLabel.ProfileTabLabels = map[string]string{"tab": "Changed"}
	if !profileTabContractRemoves(base, changedLabel) {
		t.Fatal("changed tab label not detected")
	}
	removedValue := base
	removedValue.ProfileTabFields = map[string][]string{"tab": {}}
	if !profileTabContractRemoves(base, removedValue) {
		t.Fatal("removed tab field not detected")
	}
	identityProfileExtensionMap([]profilebindingmodel.Binding{{ObjectKey: ""}, {ObjectKey: "profile"}})

	review = manifestReview{}
	review.reviewFields("object", definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "default"}, {Key: "default-value"}}}, definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "default", Required: true, Default: "x"}, {Key: "default-value", Required: true, DefaultValue: "x"}, {Key: "new-default", Required: true, Default: "x"}, {Key: "new-default-value", Required: true, DefaultValue: "x"}}})
	review.reviewNewRequiredFields("object", definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "optional"}, {Key: "default-value", Required: true, DefaultValue: "x"}}})

	objectMap(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: ""}}})
	fieldMap(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: ""}}})
	hasNewString([]string{""}, nil)
}
