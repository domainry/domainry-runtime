package integrationtest

import (
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"bytes"
	"encoding/json"

	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestBusinessWorkspaceIdentityCreatesCustomerAndPersistsBeforeAutomationHistory(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"), UploadDir: filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	requestStatus := func(token, surface, method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(raw)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Domainry-Product-Surface", surface)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if method != http.MethodGet && method != http.MethodHead {
			req.Header.Set("Idempotency-Key", "automation-business-identity:"+surface+":"+path)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	const (
		userID     = "automation_business_tester_user"
		beforeRule = "customer.business_workspace_create_guard"
	)
	businessSession := runtimeIdentityFixtureSession(t, userID, "automation_business_tester")

	effective := runtimeFixtureAuthorizedSurfaceRequest[map[string]any](t, handler, businessSession.AccessToken, "business_workspace", http.MethodGet, "/permissions/effective", nil)
	if runtimeFixtureFunctionPermissionAllowed(t, effective, "workspace.admin") ||
		!runtimeFixtureObjectActionAllowed(t, effective, "customer", "create") {
		t.Fatalf("backend effective permissions do not match the isolated business role: %#v", effective)
	}
	adminAttempt := requestStatus(businessSession.AccessToken, "admin_console", http.MethodGet, "/automation-rules/executions", nil)
	if adminAttempt.Code != http.StatusForbidden {
		t.Fatalf("business identity must not cross into tenant Admin, got %d: %s", adminAttempt.Code, adminAttempt.Body.String())
	}

	created := runtimeFixtureAuthorizedSurfaceRequest[recordmodel.Record](t, handler, businessSession.AccessToken, "business_workspace", http.MethodPost, "/objects/customer/records", map[string]any{
		"data": map[string]any{
			"name":   "Business workspace automation evidence",
			"status": "prospect",
			"owner":  userID,
		},
	})
	if created.ID == "" || created.Data["verification_status"] != "pending" {
		t.Fatalf("before Automation did not transform the real business create: %#v", created)
	}

	reviewerSession := runtimeIdentityFixtureSession(t, "automation_history_reviewer_user", "automation_history_reviewer")
	reviewerEffective := runtimeFixtureAuthorizedSurfaceRequest[map[string]any](t, handler, reviewerSession.AccessToken, "admin_console", http.MethodGet, "/permissions/effective", nil)
	if !runtimeFixtureFunctionPermissionAllowed(t, reviewerEffective, "automation.rule.history.read") ||
		runtimeFixtureFunctionPermissionAllowed(t, reviewerEffective, "workspace.admin") ||
		runtimeFixtureObjectActionAllowed(t, reviewerEffective, "customer", "create") {
		t.Fatalf("backend effective permissions do not isolate the history reviewer: %#v", reviewerEffective)
	}
	reviewerBusinessAttempt := requestStatus(reviewerSession.AccessToken, "business_workspace", http.MethodPost, "/objects/customer/records", map[string]any{
		"data": map[string]any{"name": "Forbidden Admin create", "status": "prospect", "owner": "automation_history_reviewer_user"},
	})
	if reviewerBusinessAttempt.Code != http.StatusForbidden {
		t.Fatalf("Admin reviewer must not bypass the business Surface to create records, got %d: %s", reviewerBusinessAttempt.Code, reviewerBusinessAttempt.Body.String())
	}
	history := runtimeFixtureAuthorizedSurfaceRequest[automationprojection.AutomationExecutionHistory](
		t, handler, reviewerSession.AccessToken, "admin_console", http.MethodGet,
		"/automation-rules/executions?rule_key="+beforeRule+"&record_id="+created.ID+"&phase=before", nil,
	)
	if history.Count != 1 || len(history.Items) != 1 {
		t.Fatalf("expected one persisted before-rule execution, got %#v", history)
	}
	adminHistory := runtimeFixtureAuthorizedSurfaceRequest[automationprojection.AutomationExecutionHistory](
		t, handler, reviewerSession.AccessToken, "admin_console", http.MethodGet,
		"/automation-rules/executions?rule_key="+beforeRule+"&record_id="+created.ID+"&phase=before", nil,
	)
	if adminHistory.Count != 1 || len(adminHistory.Items) != 1 || adminHistory.Items[0].ID != history.Items[0].ID {
		t.Fatalf("Tenant Admin and Runtime Ops projections must read the same authorized execution evidence: admin=%#v ops=%#v", adminHistory, history)
	}
	execution := history.Items[0]
	if execution.RuleKey != beforeRule || execution.RecordID != created.ID || execution.Phase != "before" ||
		execution.Status != "succeeded" || execution.ActorID != userID || execution.RoleKey != "automation_business_tester" ||
		execution.Candidate["verification_status"] != "pending" {
		t.Fatalf("before-rule history lost backend identity or mutation evidence: %#v", execution)
	}
	instructions, _ := execution.Trace["instructions"].([]any)
	if len(instructions) != 2 {
		t.Fatalf("before-rule history must include assert and derive traces: %#v", execution.Trace)
	}
}

func TestManifestAutomationExecutionSeedSurvivesRestartWithoutDuplication(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"), UploadDir: filepath.Join(dir, "uploads"),
	}
	store := openRuntimePersistenceFixture(t, cfg)

	for restart := 0; restart < 2; restart++ {
		application := newIntegrationRuntime(t, cfg)
		executions, err := automationpersistence.NewAutomationExecutionStore(store).ListExecutions(t.Context(), "default", automationmodel.AutomationExecutionFilter{
			RuleKey: "customer.verify_business_license", RecordID: "customer_customer_acme", Limit: 20,
		})
		if err != nil {
			application.CloseContext(t.Context())
			t.Fatalf("restart %d: list seeded automation execution: %v", restart, err)
		}
		if len(executions) != 1 || executions[0].ID != "automation_execution_seed_customer_verify_business_license" {
			application.CloseContext(t.Context())
			t.Fatalf("restart %d: expected one stable execution seed, got %#v", restart, executions)
		}
		actions, _ := executions[0].Trace["actions"].([]any)
		if len(actions) != 8 {
			application.CloseContext(t.Context())
			t.Fatalf("restart %d: expected complete seeded action trace, got %#v", restart, executions[0].Trace)
		}
		if err := application.CloseContext(t.Context()); err != nil {
			t.Fatalf("restart %d: close app: %v", restart, err)
		}
	}
}

func TestAutomationAndConnectionManagementPermissions(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"), UploadDir: filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	for _, path := range []string{"/automation-rules", "/automation-rules/capabilities", "/automation-rules/executions", "/tenant-admin/integrations/connections"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		applyIntegrationIdentity(req, "sales_rep")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("expected sales_rep to be denied %s, got %d: %s", path, res.Code, res.Body.String())
		}
	}

	rules := runtimeFixtureRequest[struct {
		Count int `json:"count"`
	}](t, handler, "sales_manager", http.MethodGet, "/automation-rules", nil)
	if rules.Count == 0 {
		t.Fatalf("expected sales_manager to read automation rules")
	}
	connections := runtimeFixtureRequest[struct {
		Count int `json:"count"`
	}](t, handler, "sales_manager", http.MethodGet, "/tenant-admin/integrations/connections", nil)
	if connections.Count < 2 {
		t.Fatalf("expected sales_manager to manage manifest Connections, got %d", connections.Count)
	}
}

func TestAgentProposalAndApprovedExecutionRespectsAfterAutomationBoundary(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"), UploadDir: filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPut, "/tenant-admin/integrations/external-identities/crm-agent-test", map[string]any{
		"provider": "test-agent", "external_subject": "crm-agent-1", "external_subject_type": "user",
		"actor_id": "sales_manager_user", "role_key": "sales_manager", "status": "active",
	})
	before := runtimeFixtureRequest[struct {
		Total int `json:"total"`
	}](t, handler, "sales_manager", http.MethodGet, "/objects/customer/records?page=1&page_size=1", nil)

	createdRecordID := ""
	for _, approved := range []bool{false, true} {
		body := map[string]any{
			"external_identity": map[string]any{"provider": "test-agent", "external_subject": "crm-agent-1", "external_subject_type": "user"},
			"approved":          approved,
			"input": map[string]any{
				"object_key": "customer",
				"data":       map[string]any{"name": "Agent asynchronous verification", "status": "prospect", "owner": "sales_manager", "business_license_image": "upload://agent.png", "business_license_no": "WRONG"},
			},
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode Agent request: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/integrations/agents/crm_assistant/tools/createRecord/invoke", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+integrationIdentityAccessTokenFor("sales_manager_user", "sales_manager"))
		req.Header.Set("X-Workspace-ID", "default")
		req.Header.Set("X-Domainry-Product-Surface", "business_workspace")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if !approved {
			if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"status":"approval_required"`)) {
				t.Fatalf("expected unapproved Agent proposal to remain pending, got %d: %s", res.Code, res.Body.String())
			}
			continue
		}
		if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"status":"executed"`)) {
			t.Fatalf("expected approved Agent execution to commit the source record, got %d: %s", res.Code, res.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode approved Agent response: %v", err)
		}
		invocation, _ := response["invocation"].(map[string]any)
		metadata, _ := invocation["metadata"].(map[string]any)
		result, _ := metadata["result"].(map[string]any)
		createdRecordID, _ = result["record_id"].(string)
		if createdRecordID == "" {
			t.Fatalf("approved Agent response omitted the created record identity: %#v", response)
		}
	}
	queued := runtimeFixtureRequest[struct {
		Outbox []map[string]any `json:"outbox"`
	}](t, handler, "platform_admin", http.MethodGet, "/operations/integrations/activity", nil)
	if len(queued.Outbox) != 1 {
		t.Fatalf("approved Agent mutation did not durably enqueue after Automation: %#v", queued)
	}
	processed := runtimeFixtureRequest[struct {
		Retried      int `json:"retried"`
		DeadLettered int `json:"dead_lettered"`
	}](t, handler, "platform_admin", http.MethodPost, "/operations/integrations/outbox/process-due?limit=20", nil)
	if processed.Retried+processed.DeadLettered != 1 {
		t.Fatalf("expected invalid Agent-created license to produce durable Automation failure evidence: %#v", processed)
	}
	created := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/objects/customer/records/"+createdRecordID, nil)
	createdData, _ := created["data"].(map[string]any)
	if createdData["business_license_no"] != "WRONG" || createdData["verification_status"] != nil {
		t.Fatalf("failed after Automation must preserve the Agent-created source record without verified fields: %#v", created)
	}

	after := runtimeFixtureRequest[struct {
		Total int `json:"total"`
	}](t, handler, "sales_manager", http.MethodGet, "/objects/customer/records?page=1&page_size=1", nil)
	if after.Total != before.Total+1 {
		t.Fatalf("only the approved Agent execution should insert one customer: before=%d after=%d", before.Total, after.Total)
	}
}
