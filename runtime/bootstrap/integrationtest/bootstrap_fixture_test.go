package integrationtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeBootstrapsBusinessManifestFixtures(t *testing.T) {
	cases := []struct {
		name          string
		manifest      string
		role          string
		objectKey     string
		dictionaryKey string
		workflowKey   string
		automationKey string
		reportKey     string
	}{
		{
			name:          "crm",
			manifest:      "crm-customer-360.json",
			role:          "sales_manager",
			objectKey:     "customer",
			dictionaryKey: "customer_status",
			workflowKey:   "customer.risk_follow_up",
			reportKey:     "crm_pipeline_health",
		},
		{
			name:          "restaurant",
			manifest:      "restaurant-kitchen.json",
			role:          "kitchen_lead",
			objectKey:     "kitchen_order",
			dictionaryKey: "kitchen_order_status",
			workflowKey:   "kitchen_order.ready_alert",
			reportKey:     "kitchen_throughput",
		},
		{
			name:          "erp",
			manifest:      "erp-inventory.json",
			role:          "inventory_manager",
			objectKey:     "stock_item",
			dictionaryKey: "stock_item_status",
			workflowKey:   "stock_item.low_stock_watch",
			reportKey:     "inventory_reorder_exposure",
		},
		{
			name:          "hr",
			manifest:      "hr-personnel.json",
			role:          "hr_admin",
			objectKey:     "leave_request",
			dictionaryKey: "leave_type",
			workflowKey:   "leave_request_approval",
			reportKey:     "hr_team_leave_exposure",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			application := newIntegrationRuntime(t, config.Config{
				AppLocale:      "en-US",
				DatabaseDriver: "sqlite",
				DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
				ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", tc.manifest),
				UploadDir:      filepath.Join(t.TempDir(), "uploads"),
			})
			defer application.CloseContext(t.Context())
			handler := application.Routes()

			schema := runtimeFixtureRequest[map[string]any](t, handler, tc.role, http.MethodGet, "/business/runtime-schema", nil)
			assertRuntimeFixtureArrayHasKey(t, schema, "objects", tc.objectKey)
			assertRuntimeFixtureArrayHasKey(t, schema, "dictionaries", tc.dictionaryKey)
			if tc.workflowKey != "" {
				assertRuntimeFixtureArrayHasKey(t, schema, "workflows", tc.workflowKey)
			}
			if tc.automationKey != "" {
				assertRuntimeFixtureArrayHasKey(t, schema, "automation_rules", tc.automationKey)
			}
			runtimeFixtureRequest[map[string]any](t, handler, tc.role, http.MethodGet, "/reports/"+tc.reportKey+"/summary", nil)

			page := runtimeFixtureRequest[map[string]any](t, handler, tc.role, http.MethodGet, "/objects/"+tc.objectKey+"/records?page=1&page_size=10", nil)
			total, ok := page["total"].(float64)
			if !ok || total < 1 {
				t.Fatalf("expected seeded records for %s, got %#v", tc.objectKey, page)
			}

			dictionary := runtimeFixtureRequest[map[string]any](t, handler, tc.role, http.MethodGet, "/dictionaries/"+tc.dictionaryKey+"/items", nil)
			items, ok := dictionary["items"].([]any)
			if !ok || len(items) == 0 {
				t.Fatalf("expected dictionary items for %s, got %#v", tc.dictionaryKey, dictionary)
			}

		})
	}
}

func TestRuntimeBusinessOnlyManifestSeedsRuntimeOwnedNavigationAndLogin(t *testing.T) {
	cfg := config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	}
	application := newIntegrationRuntime(t, cfg)
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	adminSchema := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodGet, "/business/runtime-schema", nil)
	assertRuntimeFixtureArrayHasKey(t, adminSchema, "objects", "customer")
	assertRuntimeFixtureArrayHasKey(t, adminSchema, "objects", "opportunity")
	assertRuntimeFixtureArrayHasKey(t, adminSchema, "dictionaries", "platform_operation_status")
	assertRuntimeFixtureArrayHasKey(t, adminSchema, "dictionaries", "workflow_execution_status")
	assertRuntimeFixtureArrayHasKey(t, adminSchema, "workflows", "platform.audit_retention_check")
	assertRuntimeFixtureArrayHasKey(t, adminSchema, "workflows", "platform.metadata_health_check")
	if values, ok := adminSchema["views"].([]any); ok && len(values) != 0 {
		t.Fatalf("domain-only manifest should not expose authored views, got %#v", values)
	}
	if values, ok := adminSchema["surfaces"].([]any); ok && len(values) != 0 {
		t.Fatalf("domain-only manifest should not expose authored surfaces, got %#v", values)
	}
	if values, ok := adminSchema["components"].([]any); ok && len(values) != 0 {
		t.Fatalf("domain-only manifest should not expose authored components, got %#v", values)
	}
	if values, ok := adminSchema["entrypoints"].([]any); ok && len(values) != 0 {
		t.Fatalf("domain-only manifest should not expose authored entrypoints, got %#v", values)
	}

	session := runtimeIdentityFixtureSession(t, "admin", "admin")
	identityRequest := httptest.NewRequest(http.MethodGet, "/identity/users", nil)
	identityRequest.Header.Set("Authorization", "Bearer "+session.AccessToken)
	identityResponse := httptest.NewRecorder()
	handler.ServeHTTP(identityResponse, identityRequest)
	if identityResponse.Code != http.StatusNotFound {
		t.Fatalf("Runtime must not own Identity management HTTP routes, got %d: %s", identityResponse.Code, identityResponse.Body.String())
	}

	globalDictionary := runtimeFixtureAuthorizedRequest[map[string]any](t, handler, session.AccessToken, http.MethodGet, "/dictionaries/platform_operation_status/items", nil)
	items, ok := globalDictionary["items"].([]any)
	if !ok || len(items) < 3 {
		t.Fatalf("expected seeded global dictionary items, got %#v", globalDictionary)
	}

	store := openRuntimePersistenceFixture(t, cfg)
	defer store.Close()
	globalExecutions, err := workflowpersistence.NewWorkflowWorkerStore(store).ListExecutions(t.Context(), "workspace-primary", 100)
	if err != nil {
		t.Fatal(err)
	}
	hasSeededExecution := false
	for _, execution := range globalExecutions {
		hasSeededExecution = hasSeededExecution || execution.ID == "platform_seed_workflow_execution_completed"
	}
	if !hasSeededExecution {
		t.Fatalf("expected seeded workflow execution, got %#v", globalExecutions)
	}

	globalAuditEvents, err := auditpersistence.NewAuditStoreFromRuntimeStore(t.Context(), store).ListAuditEvents(t.Context(), "workspace-primary", auditmodel.AuditEventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	hasSeededAudit := false
	for _, event := range globalAuditEvents {
		hasSeededAudit = hasSeededAudit || event.ID == "platform_seed_audit_bootstrap"
	}
	if !hasSeededAudit {
		t.Fatalf("expected seeded audit event, got %#v", globalAuditEvents)
	}

	restrictedSession := runtimeIdentityFixtureSession(t, "restricted_user", "restricted")

	adminPermissions := runtimeFixtureAuthorizedRequest[map[string]any](t, handler, session.AccessToken, http.MethodGet, "/permissions/effective", nil)
	restrictedPermissions := runtimeFixtureAuthorizedRequest[map[string]any](t, handler, restrictedSession.AccessToken, http.MethodGet, "/permissions/effective", nil)
	if !runtimeFixtureObjectActionAllowed(t, adminPermissions, "opportunity", "read") {
		t.Fatalf("expected admin to read opportunity, got %#v", adminPermissions)
	}
	if runtimeFixtureObjectActionAllowed(t, restrictedPermissions, "opportunity", "read") {
		t.Fatalf("expected restricted role not to read opportunity, got %#v", restrictedPermissions)
	}
	if !runtimeFixtureObjectActionAllowed(t, restrictedPermissions, "customer", "read") {
		t.Fatalf("expected restricted role to read customer, got %#v", restrictedPermissions)
	}
}

func TestRuntimeCRMProofServesRoleSpecificSchema(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	managerSchema := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/business/runtime-schema", nil)
	for _, objectKey := range []string{"customer", "contact", "lead", "opportunity", "activity", "contract", "payment"} {
		assertRuntimeFixtureArrayHasKey(t, managerSchema, "objects", objectKey)
	}
	for _, actionKey := range []string{"lead.qualify", "lead.convert", "opportunity.advance_stage", "opportunity.mark_won", "opportunity.mark_lost", "activity.assign_to_me", "activity.start", "activity.complete", "activity.escalate_overdue"} {
		assertRuntimeFixtureArrayHasKey(t, managerSchema, "actions", actionKey)
	}
	assertRuntimeFixtureArrayHasKey(t, managerSchema, "actions", "payment.mark_collected")
	runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/reports/crm_revenue_collection/summary", nil)

	repSchema := runtimeFixtureRequest[map[string]any](t, handler, "sales_rep", http.MethodGet, "/business/runtime-schema", nil)
	for _, objectKey := range []string{"customer", "contact", "lead", "opportunity", "activity"} {
		assertRuntimeFixtureArrayHasKey(t, repSchema, "objects", objectKey)
	}
	assertRuntimeFixtureArrayHasKey(t, repSchema, "objects", "contract")
	assertRuntimeFixtureArrayLacksKey(t, repSchema, "objects", "payment")
	assertRuntimeFixtureArrayLacksKey(t, repSchema, "actions", "payment.mark_collected")

	financeSchema := runtimeFixtureRequest[map[string]any](t, handler, "finance_reviewer", http.MethodGet, "/business/runtime-schema", nil)
	for _, objectKey := range []string{"customer", "opportunity", "contract", "payment"} {
		assertRuntimeFixtureArrayHasKey(t, financeSchema, "objects", objectKey)
	}
	assertRuntimeFixtureArrayLacksKey(t, financeSchema, "objects", "lead")
	assertRuntimeFixtureArrayHasKey(t, financeSchema, "actions", "payment.mark_collected")
}

func TestRuntimeRestrictedRoleScopesMasksAndForbidsRecords(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest manifestmodel.ManifestSchema
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for seedIndex := range manifest.SeedRecords {
		seed := &manifest.SeedRecords[seedIndex]
		if seed.ObjectKey == "contract" && seed.Data["status"] == "draft" {
			seed.Data["owner"] = "sales_rep_1"
		}
	}
	target := filepath.Join(t.TempDir(), "crm-restricted-role.json")
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	application := newIntegrationRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath: target, UploadDir: filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	manager := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/objects/contract/records?page=1&page_size=50", nil)
	restricted := runtimeFixtureRequest[map[string]any](t, handler, "sales_rep", http.MethodGet, "/objects/contract/records?page=1&page_size=50", nil)
	if manager["total"] != float64(2) || restricted["total"] != float64(1) {
		t.Fatalf("record scope difference missing: manager=%#v restricted=%#v", manager, restricted)
	}
	items, _ := restricted["items"].([]any)
	record, _ := items[0].(map[string]any)
	data, _ := record["data"].(map[string]any)
	if data["value"] != "****0.00" {
		t.Fatalf("restricted contract value was not masked: %#v", data)
	}
	runtimeFixtureRequestStatus(t, handler, "sales_rep", http.MethodDelete, "/objects/contract/records/"+record["id"].(string), nil, http.StatusForbidden)
}

func TestRuntimeCRMTransitionStateActionsApplyConfiguredTarget(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	leadID := firstRuntimeFixtureRecordID(t, handler, "sales_manager", "lead")
	runtimeFixtureRequestStatus(t, handler, "sales_manager", http.MethodPost, "/objects/lead/records/"+leadID+"/actions/lead.convert", map[string]any{"data": map[string]any{}}, http.StatusBadRequest)
	leadResult := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/lead/records/"+leadID+"/actions/lead.qualify", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, leadResult, "status", "qualified")
	leadConvertResult := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/lead/records/"+leadID+"/actions/lead.convert", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, leadConvertResult, "status", "converted")

	lostOpportunityID := runtimeFixtureRecordIDByField(t, handler, "sales_manager", "opportunity", "stage", "negotiation")
	lostOpportunityResult := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/opportunity/records/"+lostOpportunityID+"/actions/opportunity.mark_lost", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, lostOpportunityResult, "stage", "lost")

	opportunityID := runtimeFixtureRecordIDByField(t, handler, "sales_manager", "opportunity", "stage", "proposal")
	opportunityResult := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/opportunity/records/"+opportunityID+"/actions/opportunity.advance_stage", map[string]any{"data": map[string]any{"stage": "negotiation"}})
	assertRuntimeFixtureRecordField(t, opportunityResult, "stage", "negotiation")
	wonOpportunityResult := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/opportunity/records/"+opportunityID+"/actions/opportunity.mark_won", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, wonOpportunityResult, "stage", "won")

	activityID := firstRuntimeFixtureRecordID(t, handler, "sales_manager", "activity")
	assignedActivity := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/activity/records/"+activityID+"/actions/activity.assign_to_me", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, assignedActivity, "owner", "runtime_fixture_user")
	startedActivity := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/activity/records/"+activityID+"/actions/activity.start", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, startedActivity, "status", "in_progress")
	overdueActivity := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/activity/records/"+activityID+"/actions/activity.escalate_overdue", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, overdueActivity, "status", "overdue")
	completedActivity := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/activity/records/"+activityID+"/actions/activity.complete", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, completedActivity, "status", "completed")

	paymentID := firstRuntimeFixtureRecordID(t, handler, "finance_reviewer", "payment")
	paymentResult := runtimeFixtureRequest[map[string]any](t, handler, "finance_reviewer", http.MethodPost, "/objects/payment/records/"+paymentID+"/actions/payment.mark_collected", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, paymentResult, "status", "collected")

	contractID := runtimeFixtureRecordIDByField(t, handler, "sales_manager", "contract", "status", "draft")
	runtimeFixtureRequestStatus(t, handler, "sales_manager", http.MethodPost, "/objects/contract/records/"+contractID+"/actions/contract.sign", map[string]any{"data": map[string]any{}}, http.StatusBadRequest)
	approveResult := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/contract/records/"+contractID+"/actions/contract.approve", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, approveResult, "status", "approved")
	signResult := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/objects/contract/records/"+contractID+"/actions/contract.sign", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, signResult, "status", "signed")
}

func TestRuntimeCRMOverdueWorkflowOnlyProcessesOverduePayments(t *testing.T) {
	cfg := config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	}
	application := newIntegrationRuntime(t, cfg)
	defer application.CloseContext(t.Context())
	handler := application.Routes()
	publishSchedulerDefinitionFixture(t, cfg, "scheduler_daily_workflow_scan_seed", map[string]any{
		"key": "scheduler_daily_workflow_scan_seed", "name": "Daily Workflow Scan", "status": "enabled", "target_type": "workflow", "target_key": "scheduled:*",
		"schedule_expression": "daily", "timezone": "UTC", "max_attempts": 3, "retry_backoff": "fixed", "retry_delay_seconds": 60, "retry_max_delay_seconds": 3600, "timeout_seconds": 300, "next_run_at": "2026-01-01T00:00:00Z",
	})

	result := runtimeFixtureRequest[map[string]any](t, handler, "platform_admin", http.MethodPost, "/operations/workflow/executions/process?limit=25", nil)
	if processed, _ := result["processed"].(float64); processed != 1 {
		t.Fatalf("expected only one overdue workflow execution, got %#v", result)
	}
	executions, ok := result["executions"].([]any)
	if !ok || len(executions) != 1 {
		t.Fatalf("expected one workflow execution, got %#v", result)
	}
	execution, ok := executions[0].(map[string]any)
	if !ok {
		t.Fatalf("expected workflow execution object, got %#v", executions[0])
	}
	if execution["workflow_key"] != "payment.overdue_escalation" {
		t.Fatalf("expected payment overdue workflow, got %#v", execution)
	}
	store := openRuntimePersistenceFixture(t, cfg)
	defer store.Close()
	persistedExecutions, err := workflowpersistence.NewWorkflowWorkerStore(store).ListExecutions(t.Context(), "workspace-primary", 100)
	if err != nil {
		t.Fatal(err)
	}
	matchedOverdue := false
	for _, persisted := range persistedExecutions {
		if persisted.WorkflowKey == "payment.overdue_escalation" {
			matchedOverdue = matchedOverdue || persisted.RecordID == "payment_payment_acme_overdue"
		}
	}
	if !matchedOverdue {
		t.Fatalf("expected overdue payment record only, got %#v", persistedExecutions)
	}
}

func TestRuntimeCRMReportSummaryUsesRoleVisibleRuntimeData(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	pipeline := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/reports/crm_pipeline_health/summary", nil)
	assertRuntimeFixtureReportMeasure(t, pipeline, "customer_count", "2")
	assertRuntimeFixtureReportMeasure(t, pipeline, "opportunity_count", "2")

	revenue := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/reports/crm_revenue_collection/summary", nil)
	assertRuntimeFixtureReportMeasure(t, revenue, "contract_count", "2")
	assertRuntimeFixtureReportMeasure(t, revenue, "payment_count", "2")

	runtimeFixtureRequestStatus(t, handler, "sales_rep", http.MethodGet, "/reports/crm_revenue_collection/summary", nil, http.StatusNotFound)
}

func TestRuntimeBackendRecordUpdateVersionConflict(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	page := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/objects/customer/records?page=1&page_size=1", nil)
	items, ok := page["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected seeded customer record, got %#v", page)
	}
	record, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected customer record object, got %#v", items[0])
	}
	recordID, _ := record["id"].(string)
	expectedUpdatedAt, _ := record["updated_at"].(string)
	if recordID == "" || expectedUpdatedAt == "" {
		t.Fatalf("expected customer id and updated_at, got %#v", record)
	}

	stalePatch, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"expected_updated_at": "2000-01-01T00:00:00Z",
			"segment":             "expansion",
		},
	})
	if err != nil {
		t.Fatalf("marshal stale patch: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/objects/customer/records/"+recordID, bytes.NewReader(stalePatch))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "runtime-fixture-stale-update")
	applyIntegrationIdentity(req, "sales_manager")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected stale update to return 409, got %d: %s", res.Code, res.Body.String())
	}
	if !bytes.Contains(res.Body.Bytes(), []byte("backend.record.version_conflict")) {
		t.Fatalf("expected version conflict error code, got %s", res.Body.String())
	}
}

func runtimeFixtureRequest[T any](t *testing.T, handler http.Handler, role string, method string, path string, body any) T {
	return runtimeFixtureRequestWithHeaders[T](t, handler, role, method, path, body, nil)
}

func runtimeFixtureRequestWithHeaders[T any](t *testing.T, handler http.Handler, role string, method string, path string, body any, headers map[string]string) T {
	t.Helper()
	var reqBody *bytes.Reader
	if body == nil {
		reqBody = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s %s request: %v", method, path, err)
		}
		reqBody = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Idempotency-Key", method+":"+path+":"+time.Now().UTC().Format(time.RFC3339Nano))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	applyIntegrationIdentity(req, role)
	// Business fixtures state their source-owned product context explicitly.
	// Pure Admin fixtures keep the header absent so the single allowed audience
	if strings.HasPrefix(path, "/operations/") {
		if method != http.MethodGet && method != http.MethodHead {
			req.Header.Set("X-Operation-Reason", "Runtime integration fixture controlled operation")
			req.Header.Set("X-Operation-Confirmation", "confirmed")
		}
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code < 200 || res.Code >= 300 {
		t.Fatalf("%s %s returned %d: %s", method, path, res.Code, res.Body.String())
	}
	var out T
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s %s response: %v\n%s", method, path, err, res.Body.String())
	}
	return out
}

func runtimeFixtureAuthorizedRequest[T any](t *testing.T, handler http.Handler, token string, method string, path string, body any) T {
	key := ""
	if method != http.MethodGet && method != http.MethodHead {
		key = method + ":" + path + ":" + time.Now().UTC().Format(time.RFC3339Nano)
	}
	return runtimeFixtureAuthorizedRequestWithKey[T](t, handler, token, method, path, key, body)
}

func runtimeFixtureAuthorizedRequestWithKey[T any](t *testing.T, handler http.Handler, token string, method string, path string, key string, body any) T {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code < 200 || res.Code >= 300 {
		t.Fatalf("%s %s returned %d: %s", method, path, res.Code, res.Body.String())
	}
	var out T
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, res.Body.String())
	}
	return out
}

func runtimeFixtureRequestStatus(t *testing.T, handler http.Handler, role string, method string, path string, body any, expectedStatus int) {
	t.Helper()
	var reqBody *bytes.Reader
	if body == nil {
		reqBody = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s %s request: %v", method, path, err)
		}
		reqBody = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Idempotency-Key", method+":"+path+":"+time.Now().UTC().Format(time.RFC3339Nano))
	}
	applyIntegrationIdentity(req, role)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != expectedStatus {
		t.Fatalf("%s %s returned %d, expected %d: %s", method, path, res.Code, expectedStatus, res.Body.String())
	}
}

func firstRuntimeFixtureRecordID(t *testing.T, handler http.Handler, role string, objectKey string) string {
	t.Helper()
	page := runtimeFixtureRequest[map[string]any](t, handler, role, http.MethodGet, "/objects/"+objectKey+"/records?page=1&page_size=1", nil)
	items, ok := page["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected seeded %s record, got %#v", objectKey, page)
	}
	record, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected %s record object, got %#v", objectKey, items[0])
	}
	id, _ := record["id"].(string)
	if id == "" {
		t.Fatalf("expected %s record id, got %#v", objectKey, record)
	}
	return id
}

func runtimeFixtureRecordIDByField(t *testing.T, handler http.Handler, role string, objectKey string, field string, expected any) string {
	t.Helper()
	page := runtimeFixtureRequest[map[string]any](t, handler, role, http.MethodGet, "/objects/"+objectKey+"/records?page=1&page_size=50", nil)
	items, ok := page["items"].([]any)
	if !ok {
		t.Fatalf("expected seeded %s records, got %#v", objectKey, page)
	}
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		data, _ := record["data"].(map[string]any)
		if data[field] == expected {
			id, _ := record["id"].(string)
			if id != "" {
				return id
			}
		}
	}
	t.Fatalf("expected %s record with %s=%v, got %#v", objectKey, field, expected, items)
	return ""
}

func assertRuntimeFixtureRecordField(t *testing.T, payload map[string]any, field string, expected any) {
	t.Helper()
	record, ok := payload["record"].(map[string]any)
	if !ok {
		t.Fatalf("expected action result record, got %#v", payload)
	}
	data, ok := record["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected action result record data, got %#v", record)
	}
	if data[field] != expected {
		t.Fatalf("expected record %s=%v, got %#v", field, expected, data)
	}
}

func assertRuntimeFixtureReportMeasure(t *testing.T, payload map[string]any, measureKey, expected string) {
	t.Helper()
	rows, ok := payload["rows"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("expected one typed report result row in %#v", payload)
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("expected typed report result row in %#v", payload)
	}
	measures, ok := row["measures"].(map[string]any)
	if !ok || fmt.Sprint(measures[measureKey]) != expected {
		t.Fatalf("expected report measure %s=%s, got %#v", measureKey, expected, payload)
	}
}

func assertRuntimeFixtureArrayLacksKey(t *testing.T, payload map[string]any, field string, key string) {
	t.Helper()
	values, ok := payload[field].([]any)
	if !ok {
		t.Fatalf("expected %s array in %#v", field, payload)
	}
	for _, value := range values {
		item, ok := value.(map[string]any)
		if ok && item["key"] == key {
			t.Fatalf("expected %s not to contain key %s, got %#v", field, key, values)
		}
	}
}

func assertRuntimeFixtureArrayHasKey(t *testing.T, payload map[string]any, field string, key string) {
	t.Helper()
	values, ok := payload[field].([]any)
	if !ok {
		t.Fatalf("expected %s array in %#v", field, payload)
	}
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if item["key"] == key {
			return
		}
	}
	t.Fatalf("expected %s to contain key %s, got %#v", field, key, values)
}

func runtimeFixtureObjectActionAllowed(t *testing.T, payload map[string]any, objectKey string, action string) bool {
	t.Helper()
	objects, ok := payload["objects"].([]any)
	if !ok {
		t.Fatalf("expected permission objects in %#v", payload)
	}
	for _, value := range objects {
		object, ok := value.(map[string]any)
		if !ok || object["object_key"] != objectKey {
			continue
		}
		actions, ok := object["actions"].([]any)
		if !ok {
			t.Fatalf("expected permission actions for %s in %#v", objectKey, object)
		}
		for _, rawAction := range actions {
			decision, ok := rawAction.(map[string]any)
			if ok && decision["action"] == action {
				allowed, _ := decision["allowed"].(bool)
				return allowed
			}
		}
		t.Fatalf("expected permission action %s.%s in %#v", objectKey, action, actions)
	}
	t.Fatalf("expected permission object %s in %#v", objectKey, objects)
	return false
}

func runtimeFixtureFunctionPermissionAllowed(t *testing.T, payload map[string]any, permissionKey string) bool {
	t.Helper()
	values, ok := payload["function_permissions"].([]any)
	if !ok {
		t.Fatalf("expected function permissions in %#v", payload)
	}
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok || item["key"] != permissionKey {
			continue
		}
		decision, ok := item["decision"].(map[string]any)
		if !ok {
			t.Fatalf("expected function permission decision for %s in %#v", permissionKey, item)
		}
		allowed, _ := decision["allowed"].(bool)
		return allowed
	}
	return false
}

func runtimeFixtureSliceHasKey(values []map[string]any, key string) bool {
	for _, item := range values {
		if item["key"] == key || item["id"] == key {
			return true
		}
	}
	return false
}
