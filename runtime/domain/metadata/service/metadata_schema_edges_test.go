package service

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestLocalizedSchemaOptionHelpersPreserveShapeAndInputs(t *testing.T) {
	lookup := localizedTextLookup([]metadatamodel.LocalizedText{
		{EntityType: " field_option ", EntityKey: "order.status.ready", Property: "label", Text: "Ready localized"},
		{EntityType: "field_option", EntityKey: "order.status.ready", Property: "description", Text: "Ready description"},
		{EntityType: "field_option", EntityKey: "order.status.pending", Property: "label", Text: "Pending localized"},
	})
	if lookup[localizedTextLookupKey("field_option", "order.status.ready", "label")] != "Ready localized" {
		t.Fatalf("lookup=%v", lookup)
	}
	original := map[string]any{"value": "ready", "label": "Ready", "extra": true}
	localized := localizeSchemaValueOption(original, lookup, "field_option", "order.status")
	if localized["label"] != "Ready localized" || localized["description"] != "Ready description" || localized["extra"] != true || original["label"] != "Ready" {
		t.Fatalf("localized=%v original=%v", localized, original)
	}
	keyFallback := localizeSchemaValueOption(map[string]any{"key": "pending"}, lookup, "field_option", "order.status")
	if keyFallback["label"] != "Pending localized" {
		t.Fatalf("key fallback=%v", keyFallback)
	}

	mapped := []map[string]any{original}
	if got, ok := localizeSchemaValueOptions(mapped, lookup, "field_option", "order.status").([]map[string]any); !ok || got[0]["label"] != "Ready localized" {
		t.Fatalf("mapped options=%#v", got)
	}
	mixed := []any{"plain", map[string]any{"value": "ready"}}
	if got, ok := localizeSchemaValueOptions(mixed, lookup, "field_option", "order.status").([]any); !ok || got[0] != "plain" || got[1].(map[string]any)["label"] != "Ready localized" {
		t.Fatalf("mixed options=%#v", got)
	}
	plain := []any{"one", "two"}
	if got := localizeSchemaValueOptions(plain, lookup, "field_option", "order.status"); len(got.([]any)) != 2 {
		t.Fatalf("plain options=%#v", got)
	}
	if got := localizeSchemaValueOptions("unchanged", lookup, "field_option", "order.status"); got != "unchanged" {
		t.Fatalf("scalar option=%#v", got)
	}
	if firstNonEmptySchemaI18n("", " value ", "later") != "value" || firstNonEmptySchemaI18n(" ") != "" {
		t.Fatal("first non-empty normalization failed")
	}
}

func TestSnapshotVisibilityFiltersObjectsActionsReportsAndAgentRegistry(t *testing.T) {
	role := accessfixture.Bundle{Key: "sales", Permissions: []string{
		"customer.read", "customer.update", "customer.approve", "integration.tool.crm_sync", "report.read",
	}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true, Write: true}}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}, SurfaceKey: "workspace"}, role)
	snapshot := metadatamodel.MetadataSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{
			{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name"}, {Key: "secret"}}},
			{Key: "invoice", Fields: []definitionmodel.FieldSchema{{Key: "amount"}}},
		},
		Actions: []definitionmodel.ActionSchema{
			{Key: "customer.approve", ObjectKey: "customer"},
			{Key: "customer.reject", ObjectKey: "customer"},
			{Key: "invoice.approve", ObjectKey: "invoice"},
		},
		GuardedWrites: []metadatamodel.MetadataGuardedWriteContract{
			{ObjectKey: "customer", ActionKey: "customer.approve"},
			{ObjectKey: "customer", ActionKey: "customer.reject"},
		},
		Views: []definitionmodel.ViewSchema{{Key: "customers", ObjectKey: "customer"}, {Key: "invoices", ObjectKey: "invoice"}},
		Reports: []reportmodel.ReportSchema{
			{Key: "customer_report", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}}},
			{Key: "invoice_report", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "invoice", Alias: "invoice"}}},
			{Key: "permission_report", RequiredPermissions: []string{"report.read"}},
			{Key: "forbidden_report", RequiredPermissions: []string{"report.manage"}},
			{Key: "global_report"},
		},
		EntryPoints: []definitionmodel.EntryPointSchema{{Key: "unscoped"}, {Key: "sales", RequiredPermissions: []string{"customer.read"}}, {Key: "finance", RequiredPermissions: []string{"finance.read"}}},
		Skills: []agentmodel.SkillSchema{
			{Key: "search", AllowedTools: []string{"searchRecords", "searchRecords", ""}},
			{Key: "create", AllowedTools: []string{"createRecord"}},
			{Key: "integration", AllowedTools: []string{"crm_sync"}},
			{Key: "hidden", AllowedTools: []string{"admin_sync"}},
			{Key: "passive"},
		},
		Agents: []agentmodel.AgentSchema{
			{Key: "sales-agent", Tools: []string{"getRecord", "crm_sync"}, SkillKeys: []string{"search", "integration", "hidden"}},
			{Key: "hidden-agent", Tools: []string{"admin_sync"}},
			{Key: "missing-skill", SkillKeys: []string{"hidden"}},
			{Key: "passive"},
		},
		AgentTasks: []agentmodel.AgentTaskDefinition{
			{Key: "customer-review", AgentKey: "sales-agent", AllowedObjects: []string{"customer", "invoice"}, AllowedActions: []string{"customer.approve", "customer.reject"}, SideEffectMode: agentmodel.AgentTaskSideEffectActionAllowed, Enabled: true},
			{Key: "invoice-review", AgentKey: "sales-agent", AllowedObjects: []string{"invoice"}, SideEffectMode: agentmodel.AgentTaskSideEffectAnalysisOnly, Enabled: true},
			{Key: "customer-reject", AgentKey: "sales-agent", AllowedActions: []string{"customer.reject"}, SideEffectMode: agentmodel.AgentTaskSideEffectActionAllowed, Enabled: true},
			{Key: "hidden-agent-task", AgentKey: "hidden-agent", SideEffectMode: agentmodel.AgentTaskSideEffectAnalysisOnly, Enabled: true},
			{Key: "disabled-task", AgentKey: "sales-agent", SideEffectMode: agentmodel.AgentTaskSideEffectAnalysisOnly},
		},
		AgentEntrypoints: []agentmodel.AgentEntrypointAssignment{
			{Key: "sales", AgentKey: "sales-agent", Surface: "workspace", RequiredPermissions: []string{"customer.read"}, AllowedTaskKeys: []string{"customer-review", "invoice-review"}, AllowedWorkflowKeys: []string{"active", "disabled"}, Enabled: true},
			{Key: "finance", AgentKey: "sales-agent", Surface: "workspace", RequiredPermissions: []string{"finance.read"}, Enabled: true},
			{Key: "admin-surface", AgentKey: "sales-agent", Surface: "admin", RequiredPermissions: []string{"customer.read"}, Enabled: true},
		},
		AgentServicePrincipals:    []agentmodel.AgentServicePrincipalBinding{{Key: "agent-service", UserID: "service-user", RoleKey: "service"}},
		Workflows:                 []definitionmodel.WorkflowSchema{{Key: "active", Enabled: true}, {Key: "disabled"}},
		IdentityProfileExtensions: []profilebindingmodel.Binding{{ObjectKey: "customer"}, {ObjectKey: "invoice"}},
	}
	filtered := SnapshotForPrincipal(snapshot, principal)
	if filtered.SchemaHash == "" || filtered.SnapshotVersion != filtered.SchemaHash || filtered.SchemaHash != SchemaSnapshotHash(filtered) {
		t.Fatalf("filtered schema hash does not describe the returned projection: hash=%q version=%q", filtered.SchemaHash, filtered.SnapshotVersion)
	}
	if len(filtered.Objects) != 1 || filtered.Objects[0].Key != "customer" || len(filtered.Views) != 1 || len(filtered.Actions) != 1 || len(filtered.GuardedWrites) != 1 {
		t.Fatalf("core visibility=%+v", filtered)
	}
	if len(filtered.Reports) != 3 || len(filtered.EntryPoints) != 1 || len(filtered.IdentityProfileExtensions) != 1 {
		t.Fatalf("surface visibility reports=%v entrypoints=%v extensions=%v", filtered.Reports, filtered.EntryPoints, filtered.IdentityProfileExtensions)
	}
	if len(filtered.Skills) != 3 || len(filtered.Agents) != 2 || len(filtered.Skills[0].AllowedTools) != 1 {
		t.Fatalf("agent registry skills=%v agents=%v", filtered.Skills, filtered.Agents)
	}
	if len(filtered.AgentTasks) != 1 || filtered.AgentTasks[0].Key != "customer-review" || len(filtered.AgentTasks[0].AllowedObjects) != 1 || len(filtered.AgentTasks[0].AllowedActions) != 1 {
		t.Fatalf("agent task visibility=%v", filtered.AgentTasks)
	}
	if len(filtered.AgentEntrypoints) != 1 || len(filtered.AgentEntrypoints[0].AllowedTaskKeys) != 1 || len(filtered.AgentEntrypoints[0].AllowedWorkflowKeys) != 1 || len(filtered.AgentServicePrincipals) != 0 {
		t.Fatalf("agent entrypoint visibility=%v service principals=%v", filtered.AgentEntrypoints, filtered.AgentServicePrincipals)
	}
	if got := SnapshotForPrincipal(snapshot, principalmodel.Principal{}); len(got.Objects) != len(snapshot.Objects) || len(got.AgentServicePrincipals) != 0 {
		t.Fatal("unknown principal snapshot should preserve public metadata without service principal bindings")
	}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if got := SnapshotForPrincipal(snapshot, admin); len(got.Objects) != len(snapshot.Objects) || got.SchemaHash != SchemaSnapshotHash(got) {
		t.Fatal("admin snapshot should remain unchanged")
	}
}

func TestVisibilityPermissionAndToolHelperEdges(t *testing.T) {
	role := accessfixture.Bundle{
		Key: "member", Permissions: []string{"customer.read", "customer.create", "integration.tool.*", "report.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true, Write: true}},
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, role)
	visible := map[string]bool{"customer": true}
	if !principalCanUseObject(principal, "customer") || principalCanUseObject(principal, "invoice") {
		t.Fatal("object visibility mismatch")
	}
	if !principalCanUseAnyObjectAction(principal, visible, "create") || principalCanUseAnyObjectAction(principal, visible, "delete") {
		t.Fatal("object action visibility mismatch")
	}
	for _, tool := range []string{"listObjects", "listFields", "searchRecords", "getRecord", "list_objects", "list_fields", "search_records", "query_records", "get_record", "createRecord", "create_record", "external"} {
		if !agentToolAllowedForPrincipal(tool, principal, visible) {
			t.Fatalf("tool %q should be allowed", tool)
		}
	}
	if agentToolAllowedForPrincipal("updateRecord", principal, visible) || agentToolAllowedForPrincipal("update_record", principal, visible) || agentToolAllowedForPrincipal("listObjects", principal, nil) || agentToolAllowedForPrincipal("query_records", principal, nil) {
		t.Fatal("unauthorized object tool allowed")
	}
	tools := visibleAgentToolsForPrincipal([]string{" external ", "external", "", "admin"}, principal, visible)
	if len(tools) != 2 || tools[0] != "external" || tools[1] != "admin" {
		t.Fatalf("tools=%v", tools)
	}
	if object, action := splitPermission("sales.customer.approve"); object != "customer" || action != "approve" {
		t.Fatalf("split=%q/%q", object, action)
	}
	if object, action := splitPermission("read"); object != "" || action != "read" {
		t.Fatalf("short split=%q/%q", object, action)
	}
	if principal.HasPermission("invalid") || !principal.HasPermission("report.read") {
		t.Fatal("permission projection mismatch")
	}
	if principal.HasAllPermissions(nil) || !principal.HasAllPermissions([]string{"customer.read"}) || principal.HasAllPermissions([]string{"admin.read"}) {
		t.Fatal("entrypoint visibility mismatch")
	}
	contractSnapshot := metadatamodel.MetadataSchemaSnapshot{
		Agents:     []agentmodel.AgentSchema{{Key: "agent"}},
		AgentTasks: []agentmodel.AgentTaskDefinition{{Key: "task", AgentKey: "agent", Enabled: true}},
		AgentEntrypoints: []agentmodel.AgentEntrypointAssignment{
			{Key: "disabled", AgentKey: "agent"},
			{Key: "missing", AgentKey: "missing", Enabled: true},
			{Key: "active", AgentKey: "agent", Enabled: true, RequiredPermissions: []string{"customer.read"}},
		},
	}
	_, _ = visibleAgentContractsForPrincipal(contractSnapshot, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, role), map[string]bool{}, map[string]bool{})
	if got := visibleAgentStringIntersection([]string{"", "allowed", "allowed", "denied"}, map[string]bool{"allowed": true}); len(got) != 1 || got[0] != "allowed" {
		t.Fatalf("intersection=%v", got)
	}
}
