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

	requestStatus := func(token, method, path string, body any) *httptest.ResponseRecorder {
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
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if method != http.MethodGet && method != http.MethodHead {
			req.Header.Set("Idempotency-Key", "automation-business-identity:"+path)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	const (
		userID     = "automation_business_tester_user"
		beforeRule = "customer.create_guard"
	)
	businessSession := runtimeIdentityFixtureSession(t, userID, "automation_business_tester")

	effective := runtimeFixtureAuthorizedRequest[map[string]any](t, handler, businessSession.AccessToken, http.MethodGet, "/records/permissions/effective", nil)
	if runtimeFixtureFunctionPermissionAllowed(t, effective, "runtime.appschema.validate_application_definition") ||
		!runtimeFixtureObjectActionAllowed(t, effective, "customer", "create") {
		t.Fatalf("backend effective permissions do not match the isolated business role: %#v", effective)
	}
	adminAttempt := requestStatus(businessSession.AccessToken, http.MethodGet, "/automation/rules/executions", nil)
	if adminAttempt.Code != http.StatusForbidden {
		t.Fatalf("business identity must not cross into tenant Admin, got %d: %s", adminAttempt.Code, adminAttempt.Body.String())
	}

	created := runtimeFixtureAuthorizedRequest[recordmodel.Record](t, handler, businessSession.AccessToken, http.MethodPost, "/records/objects/customer/records", map[string]any{
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
	reviewerEffective := runtimeFixtureAuthorizedRequest[map[string]any](t, handler, reviewerSession.AccessToken, http.MethodGet, "/records/permissions/effective", nil)
	if !runtimeFixtureFunctionPermissionAllowed(t, reviewerEffective, "runtime.automation.list_automation_executions") ||
		runtimeFixtureFunctionPermissionAllowed(t, reviewerEffective, "runtime.appschema.validate_application_definition") ||
		runtimeFixtureObjectActionAllowed(t, reviewerEffective, "customer", "create") {
		t.Fatalf("backend effective permissions do not isolate the history reviewer: %#v", reviewerEffective)
	}
	reviewerBusinessAttempt := requestStatus(reviewerSession.AccessToken, http.MethodPost, "/records/objects/customer/records", map[string]any{
		"data": map[string]any{"name": "Forbidden Admin create", "status": "prospect", "owner": "automation_history_reviewer_user"},
	})
	if reviewerBusinessAttempt.Code != http.StatusForbidden {
		t.Fatalf("Admin reviewer must not bypass the business Surface to create records, got %d: %s", reviewerBusinessAttempt.Code, reviewerBusinessAttempt.Body.String())
	}
	history := runtimeFixtureAuthorizedRequest[automationprojection.AutomationExecutionHistory](
		t, handler, reviewerSession.AccessToken, http.MethodGet,
		"/automation/rules/executions?rule_key="+beforeRule+"&record_id="+created.ID+"&phase=before", nil,
	)
	if history.Count != 1 || len(history.Items) != 1 {
		t.Fatalf("expected one persisted before-rule execution, got %#v", history)
	}
	adminHistory := runtimeFixtureAuthorizedRequest[automationprojection.AutomationExecutionHistory](
		t, handler, reviewerSession.AccessToken, http.MethodGet,
		"/automation/rules/executions?rule_key="+beforeRule+"&record_id="+created.ID+"&phase=before", nil,
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
		executions, err := automationpersistence.NewAutomationExecutionStore(store).ListExecutions(t.Context(), "workspace-primary", automationmodel.AutomationExecutionFilter{
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

func TestAutomationManagementPermissions(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"), UploadDir: filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	for _, path := range []string{"/automation/rules", "/automation/rules/capabilities", "/automation/rules/executions"} {
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
	}](t, handler, "sales_manager", http.MethodGet, "/automation/rules", nil)
	if rules.Count == 0 {
		t.Fatalf("expected sales_manager to read automation rules")
	}
}

func TestLegacyIntegrationAgentEntrypointIsNotRuntimeOwned(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"), UploadDir: filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	req := httptest.NewRequest(http.MethodPost, "/integration/agents/crm_assistant/tools/createRecord/invoke", nil)
	applyIntegrationIdentity(req, "sales_manager")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound || !bytes.Contains(res.Body.Bytes(), []byte(`"code":"route_not_found"`)) {
		t.Fatalf("legacy Integration-owned Agent ingress must remain retired, got %d: %s", res.Code, res.Body.String())
	}
}
