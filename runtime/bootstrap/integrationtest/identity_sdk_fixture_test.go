package integrationtest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// newIntegrationIdentityFactory supplies Identity-owned test state through the
// public SDK contract. It is intentionally independent from Runtime manifests:
// business metadata never creates users, roles, assignments, or grants.
func newIntegrationIdentityFactory() identitysdk.Factory {
	roles := []runtimetestkit.IdentityFixtureRole{
		integrationIdentityRole("admin", "Administrator", []string{"business.access", "admin_console.access", "workspace.admin"}, true),
		integrationIdentityRole("business_admin", "Business administrator", []string{"business.access", "workspace.admin"}, true),
		integrationIdentityRole("sales_manager", "Sales manager", []string{"business.access", "admin_console.access", "workspace.admin", "integration.entrypoint.invoke"}, true),
		integrationIdentityRole("platform_admin", "Runtime operator", []string{"admin_console.access", "workspace.admin", "scheduler.command", "ops.workflow.read", "ops.workflow.process"}, true),
		integrationIdentityRole("scheduler_operator", "Scheduler operator", []string{"admin_console.access", "workspace.admin", "scheduler.command"}, true),
		integrationIdentityRole("kitchen_lead", "Kitchen lead", []string{"business.access", "workspace.admin", "kitchen_order.import"}, true),
		integrationIdentityRole("inventory_manager", "Inventory manager", []string{"business.access", "workspace.admin"}, true),
		integrationIdentityRole("hr_admin", "HR administrator", []string{"business.access", "workspace.admin"}, true),
		integrationOwnedIdentityRole("sales_rep", "Sales representative", []string{
			"business.access", "customer.read", "customer.update", "contact.read", "contact.create",
			"lead.read", "lead.update", "opportunity.read", "opportunity.update",
			"activity.read", "activity.create", "activity.update", "contract.read",
		}, []identitysdk.FieldPolicy{
			{Resource: "customer", Field: "health_score", Read: true},
			{Resource: "contract", Field: "value", Read: true, Masked: true},
		}),
		integrationIdentityRole("finance_reviewer", "Finance reviewer", []string{
			"business.access", "customer.read", "opportunity.read", "contract.read", "payment.read", "payment.update", "payment.export",
		}, true),
		integrationIdentityRole("restricted", "Restricted user", []string{"business.access", "customer.read"}, true),
		integrationOwnedIdentityRole("automation_business_tester", "Automation business tester", []string{
			"business.access", "customer.read", "customer.create", "customer.update", "customer.apply_automation_verification", "ops.workflow.run",
		}, nil),
		integrationIdentityRole("automation_history_reviewer", "Automation history reviewer", []string{
			"admin_console.access", "automation.rule.read", "automation.rule.history.read",
		}, false),
		integrationIdentityRole("sales", "Sales", []string{
			"business.access", "workflow.run", "ops.workflow.read", "customer_account.read", "sales_order.read", "sales_order.create", "sales_order.update", "inventory_stock.read", "inventory_stock.update", "order_cash_ledger.read", "order_cash_ledger.create",
		}, true),
		integrationIdentityRole("credit_manager", "Credit manager", []string{
			"business.access", "workflow.task.act", "ops.workflow.read", "customer_account.read", "sales_order.read", "sales_order.update", "inventory_stock.read", "inventory_stock.update", "order_cash_ledger.create",
		}, true),
		integrationIdentityRole("finance", "Finance", []string{
			"business.access", "workflow.task.act", "ops.workflow.read", "identity.audit.view", "sales_order.read", "sales_order.update", "shipment.read", "inventory_stock.read", "inventory_stock.update", "invoice.read", "invoice.update", "payment.read", "payment.create", "payment.update", "return_request.read", "return_request.update", "order_cash_ledger.read", "order_cash_ledger.create",
		}, true),
	}
	users := []identitysdk.User{
		{ID: "admin", Name: "Administrator", Email: "admin@example.com", Status: "active"},
		{ID: "runtime_fixture_user", Name: "Runtime fixture user", Email: "runtime-fixture@example.com", Status: "active"},
		{ID: "restricted_user", Name: "Restricted user", Email: "restricted@example.com", Status: "active"},
		{ID: "automation_business_tester_user", Name: "Automation business tester", Email: "automation-business-tester@example.com", Status: "active"},
		{ID: "automation_history_reviewer_user", Name: "Automation history reviewer", Email: "automation-history-reviewer@example.com", Status: "active"},
		{ID: "sales_rep_1", Name: "Sales representative", Email: "sales-rep@example.com", Status: "active"},
		{ID: "sales_manager", Name: "Sales manager", Email: "sales-manager@example.com", Status: "active"},
		{ID: "finance_reviewer", Name: "Finance reviewer", Email: "finance-reviewer@example.com", Status: "active"},
		{ID: "sales_user", Name: "Sales user", Email: "sales@example.com", Status: "active"},
		{ID: "credit_user", Name: "Credit manager", Email: "credit@example.com", Status: "active"},
		{ID: "finance_user", Name: "Finance user", Email: "finance@example.com", Status: "active"},
	}
	return runtimetestkit.NewIdentityFactory(runtimetestkit.IdentityFixtureConfig{
		Roles: roles,
		Users: users,
		UserRoleAssignments: map[string][]string{
			"admin":                            {"admin"},
			"runtime_fixture_user":             {"admin"},
			"restricted_user":                  {"restricted"},
			"automation_business_tester_user":  {"automation_business_tester"},
			"automation_history_reviewer_user": {"automation_history_reviewer"},
			"sales_user":                       {"sales"},
			"credit_user":                      {"credit_manager"},
			"finance_user":                     {"finance"},
		},
	})
}

func newIntegrationIdentityBinding(t *testing.T, cfg config.Config) identitysdk.Binding {
	t.Helper()
	workspaceID := strings.TrimSpace(cfg.IdentityWorkspaceID)
	if workspaceID == "" {
		workspaceID = "workspace-primary"
	}
	applicationKey := strings.TrimSpace(cfg.IdentityAudience)
	if applicationKey == "" {
		applicationKey = "domainry-runtime"
	}
	binding, err := newIntegrationIdentityFactory().Open(t.Context(), identitysdk.ApplicationRef{
		WorkspaceID: identitysdk.WorkspaceID(workspaceID), ApplicationKey: identitysdk.ApplicationKey(applicationKey), RedirectURLs: append([]string(nil), cfg.IdentityRedirectURLs...),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	return binding
}

func integrationOwnedIdentityRole(key, name string, permissions []string, fields []identitysdk.FieldPolicy) runtimetestkit.IdentityFixtureRole {
	predicate := identitysdk.Predicate{Fact: "owner_id", Operator: identitysdk.OperatorEqual, Value: "$subject.id"}
	role := integrationIdentityRole(key, name, permissions, false)
	role.DataPredicate = &predicate
	role.FieldPolicies = append([]identitysdk.FieldPolicy(nil), fields...)
	return role
}

func integrationIdentityAccessToken(role string) string {
	return integrationIdentityAccessTokenFor(integrationIdentityUser(role), role)
}

func integrationIdentityAccessTokenFor(subject, role string) string {
	return runtimetestkit.IdentityFixtureAccessToken(subject, role)
}

func integrationIdentityUser(role string) string {
	switch role {
	case "sales_rep":
		return "sales_rep_1"
	case "automation_business_tester":
		return "automation_business_tester_user"
	case "automation_history_reviewer":
		return "automation_history_reviewer_user"
	case "sales", "credit_manager", "finance":
		return roleForBusinessWorkflowRole(role)
	default:
		return "runtime_fixture_user"
	}
}

func roleForBusinessWorkflowRole(role string) string {
	switch role {
	case "sales":
		return "sales_user"
	case "credit_manager":
		return "credit_user"
	case "finance":
		return "finance_user"
	default:
		return ""
	}
}

func applyIntegrationIdentity(request *http.Request, role string) {
	request.Header.Set("Authorization", "Bearer "+integrationIdentityAccessToken(role))
	request.Header.Set("X-Workspace-ID", "workspace-primary")
}

func integrationIdentityRole(key, name string, permissions []string, allowAllBusinessData bool) runtimetestkit.IdentityFixtureRole {
	return runtimetestkit.IdentityFixtureRole{
		Key: key, Name: name, Permissions: permissions, AllowAllBusinessData: allowAllBusinessData,
	}
}
