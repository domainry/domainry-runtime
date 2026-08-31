package integrationtest

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestBusinessActorRecordActionWorkflowTaskAndRefreshEndToEnd(t *testing.T) {
	manifestPath := orderToCashSurfaceTestManifest(t)
	cfg := config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   manifestPath,
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	}
	runtime := newIntegrationRuntime(t, cfg)
	defer runtime.CloseContext(t.Context())
	handler := runtime.Routes()
	publishSchedulerDefinitionFixture(t, cfg, "scheduler_p7_business_workflow", map[string]any{
		"key": "scheduler_p7_business_workflow", "name": "P7 Business Workflow Worker",
		"status": "enabled", "target_type": "workflow", "target_key": "scheduled:*",
		"schedule_expression": "daily", "timezone": "UTC", "max_attempts": 3,
		"retry_backoff": "fixed", "retry_delay_seconds": 60, "retry_max_delay_seconds": 3600,
		"timeout_seconds": 300, "next_run_at": "2026-01-01T00:00:00Z",
	})
	asUser := func(userID string) map[string]string {
		return map[string]string{
			"Authorization": "Bearer " + integrationIdentityAccessTokenFor(userID, roleForBusinessWorkflowUser(userID)),
		}
	}

	customer := runtimeFixtureRequestWithHeaders[map[string]any](
		t, handler, "sales", http.MethodGet,
		"/objects/customer_account/records?page=1&page_size=1", nil, asUser("sales_user"),
	)
	customerItems, _ := customer["items"].([]any)
	if len(customerItems) != 1 {
		t.Fatalf("expected seeded customer account, got %#v", customer)
	}
	customerRecord, _ := customerItems[0].(map[string]any)
	customerID, _ := customerRecord["id"].(string)
	if customerID == "" {
		t.Fatalf("expected customer record id, got %#v", customerRecord)
	}

	order := runtimeFixtureRequestWithHeaders[recordmodel.Record](
		t, handler, "sales", http.MethodPost, "/objects/sales_order/records",
		map[string]any{"data": map[string]any{
			"order_number": "SO-P7-BUSINESS", "customer": customerID, "sku": "WIDGET-1",
			"ordered_quantity": 2, "reserved_quantity": 0, "fulfilled_quantity": 0,
			"cancelled_quantity": 0, "unit_price": 25, "order_amount": 50,
			"discount_percent": 0, "risk_status": "reviewed", "status": "draft", "owner": "sales_user",
		}},
		asUser("sales_user"),
	)
	if order.ID == "" || order.Data["status"] != "draft" {
		t.Fatalf("record creation did not return draft order: %#v", order)
	}

	submitted := runtimeFixtureRequestWithHeaders[map[string]any](
		t, handler, "sales", http.MethodPost,
		"/objects/sales_order/records/"+order.ID+"/actions/sales_order.submit",
		map[string]any{"data": map[string]any{"request_id": "submit-p7"}},
		asUser("sales_user"),
	)
	assertRuntimeFixtureRecordField(t, submitted, "status", "submitted")

	// The Action mutation commits the workflow intent transactionally. The Ops
	// worker only advances that durable intent; it does not grant Business access.
	operationsHeaders := map[string]string{
		"Authorization": "Bearer " + integrationIdentityAccessToken("platform_admin"),
	}
	var creditTasks []workflowapplication.BusinessWorkflowTaskDTO
	for attempt := 0; attempt < 40 && len(creditTasks) == 0; attempt++ {
		runtimeFixtureRequestWithHeaders[workflowapplication.OpsWorkflowProcessBatchDTO](
			t, handler, "platform_admin", http.MethodPost,
			"/operations/workflow/executions/process?limit=25", nil, operationsHeaders,
		)
		creditTasks = runtimeFixtureRequestWithHeaders[[]workflowapplication.BusinessWorkflowTaskDTO](
			t, handler, "credit_manager", http.MethodGet,
			"/business/workflow/tasks?status=open&limit=20", nil, asUser("credit_user"),
		)
		if len(creditTasks) == 0 {
			time.Sleep(25 * time.Millisecond)
		}
	}
	if len(creditTasks) != 1 || creditTasks[0].AssigneeUserID != "credit_user" {
		t.Fatalf("expected credit manager task, got %#v", creditTasks)
	}
	runtimeFixtureRequestWithHeaders[workflowapplication.BusinessWorkflowProcessDTO](
		t, handler, "credit_manager", http.MethodPost,
		"/business/workflow/tasks/"+creditTasks[0].ID+"/approve",
		map[string]any{"comment": "credit approved"}, asUser("credit_user"),
	)

	financeTasks := runtimeFixtureRequestWithHeaders[[]workflowapplication.BusinessWorkflowTaskDTO](
		t, handler, "finance", http.MethodGet,
		"/business/workflow/tasks?status=open&limit=20", nil, asUser("finance_user"),
	)
	if len(financeTasks) != 1 || financeTasks[0].AssigneeUserID != "finance_user" {
		t.Fatalf("expected independent finance task, got %#v", financeTasks)
	}
	completed := runtimeFixtureRequestWithHeaders[workflowapplication.BusinessWorkflowProcessDTO](
		t, handler, "finance", http.MethodPost,
		"/business/workflow/tasks/"+financeTasks[0].ID+"/approve",
		map[string]any{"comment": "finance approved"}, asUser("finance_user"),
	)
	if completed.Status != "completed" {
		t.Fatalf("expected completed workflow process, got %#v", completed)
	}

	refreshed := runtimeFixtureRequestWithHeaders[recordmodel.Record](
		t, handler, "sales", http.MethodGet, "/objects/sales_order/records/"+order.ID, nil, asUser("sales_user"),
	)
	if refreshed.Data["status"] != "approved" {
		t.Fatalf("refreshed Business record status=%v want=approved: %#v", refreshed.Data["status"], refreshed)
	}
}

func orderToCashSurfaceTestManifest(t *testing.T) string {
	t.Helper()
	source := filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "order-to-cash.json")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	roles, _ := manifest["roles"].([]any)
	manifest["roles"] = append(roles, map[string]any{
		"key": "platform_admin", "name": "Runtime Operator",
		"permissions":  []any{"ops.workflow.process", "ops.workflow.read"},
		"record_scope": "all_records",
		"data_permissions": []any{map[string]any{
			"object_key": "sales_order", "scope": "none", "read": false, "write": false,
		}},
	})
	users, _ := manifest["users"].([]any)
	manifest["users"] = append(users, map[string]any{
		"id": "platform_admin", "name": "Runtime Operator",
		"email": "runtime-operator@example.com", "role_keys": []any{"platform_admin"},
	})
	workflows, _ := manifest["workflows"].([]any)
	if len(workflows) != 1 {
		t.Fatalf("expected one order-to-cash workflow, got %d", len(workflows))
	}
	workflow, _ := workflows[0].(map[string]any)
	actionTrigger := map[string]any{
		"type": "action_completed", "object_key": "sales_order", "event": "sales_order.submit",
	}
	workflow["trigger"] = actionTrigger
	workflow["trigger_contract"] = actionTrigger
	normalized, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "order-to-cash-surface-e2e.json")
	if err := os.WriteFile(target, normalized, 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}

func roleForBusinessWorkflowUser(userID string) string {
	switch userID {
	case "sales_user":
		return "sales"
	case "credit_user":
		return "credit_manager"
	case "finance_user":
		return "finance"
	default:
		return ""
	}
}
