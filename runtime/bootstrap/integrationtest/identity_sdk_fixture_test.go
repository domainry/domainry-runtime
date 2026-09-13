package integrationtest

import (
	"context"
	"net/http"
	"sort"
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
	roles := integrationIdentityFixtureRoles()
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

func integrationIdentityFixtureRoles() []runtimetestkit.IdentityFixtureRole {
	crmManagerPermissions := []string{
		"business.access", "admin_console.access", "integration.entrypoint.invoke",
		"report.summary.get",
		"notification.inbox.list", "notification.inbox.item.get",
		"notification.inbox.item.mark_read", "notification.inbox.item.acknowledge",
		"notification.deliveries.list", "runtime.notifications.list_deliveries",
		"integration.invocations.list",
		"customer.read", "customer.create", "customer.update", "customer.export",
		"contact.read", "contact.create", "contact.update",
		"lead.read", "lead.create", "lead.update",
		"opportunity.read", "opportunity.create", "opportunity.update", "opportunity.export",
		"activity.read", "activity.create", "activity.update", "activity.export",
		"contract.read", "contract.create", "contract.update", "payment.read",
		"customer.mark_risk", "lead.qualify", "lead.convert", "opportunity.advance_stage", "opportunity.mark_won", "opportunity.mark_lost",
		"activity.assign_to_me", "activity.start", "activity.complete", "activity.escalate_overdue", "contract.approve", "contract.sign",
		"payment.mark_collected",
		"runtime.automation.list_automation_rules", "runtime.automation.get_automation_rule", "runtime.automation.get_execution_catalog",
		"runtime.automation.validate_automation_rule", "runtime.automation.simulate_rule_candidate", "runtime.automation.simulate_rule",
	}
	return []runtimetestkit.IdentityFixtureRole{
		integrationServiceIdentityRole("crm_risk_follow_up_service", "CRM Risk Follow Up Service", "customer.record_risk_follow_up"),
		integrationServiceIdentityRole("crm_lead_routing_service", "CRM Lead Routing Service", "lead.record_ready_for_conversion"),
		integrationServiceIdentityRole("crm_payment_escalation_service", "CRM Payment Escalation Service", "payment.read", "payment.record_overdue_escalation"),
		integrationServiceIdentityRole("inventory_watch_service", "Inventory Watch Service", "stock_item.read", "stock_item.low_stock_watch.execute"),
		integrationServiceIdentityRole("termination_scheduler_service", "Termination Scheduler Service", "termination_case.read", "termination_case.execute_due"),
		integrationServiceIdentityRole("kitchen_alert_service", "Kitchen Alert Service", "kitchen_order.read", "kitchen_order.ready_alert.execute"),
		integrationIdentityRole("admin", "Administrator", []string{
			"business.access", "admin_console.access", "report.summary.get", "customer.read", "customer.create", "opportunity.read", "lead.read", "audit.business.read", "audit.ops.read",
		}, true),
		integrationIdentityRole("business_admin", "Business administrator", []string{
			"business.access", "customer.read", "customer.create", "customer.update", "customer.export", "opportunity.read", "opportunity.create", "opportunity.update",
		}, true),
		integrationIdentityRole("sales_manager", "Sales manager", crmManagerPermissions, true),
		integrationIdentityRole("platform_admin", "Runtime operator", []string{
			"admin_console.access", "runtime.workflows.process_ops_workflow_executions",
			"lead.activate_due_candidates", "lead.create_daily_review_tasks", "lead.fail_due_candidates", "lead.read", "lead.update",
		}, true),
		integrationIdentityRole("scheduler_operator", "Scheduler operator", []string{
			"admin_console.access", "scheduler_probe.read",
		}, true),
		integrationIdentityRole("kitchen_lead", "Kitchen lead", []string{
			"business.access", "report.summary.get", "kitchen_order.read", "kitchen_order.create", "kitchen_order.update", "kitchen_order.import", "kitchen_order.start_cooking", "kitchen_order.ready_alert.execute",
		}, true),
		integrationIdentityRole("inventory_manager", "Inventory manager", []string{
			"business.access", "report.summary.get", "stock_item.read", "stock_item.create", "stock_item.update", "purchase_request.read", "purchase_request.create", "purchase_request.update", "stock_item.create_purchase_request", "stock_item.low_stock_watch.execute",
		}, true),
		integrationIdentityRole("hr_admin", "HR administrator", []string{
			"business.access", "report.summary.get", "runtime.workflows.approve_workflow_task", "runtime.workflows.reject_workflow_task", "runtime.workflows.return_workflow_task", "leave_request.read", "leave_request.create", "leave_request.update",
			"leave_balance.read", "leave_balance.update", "leave_balance_ledger.read", "leave_balance_ledger.create", "hr_position.read",
			"employee_profile.read", "employee_profile.create", "employee_profile.update",
			"leave_request.submit", "leave_request.withdraw", "leave_request.cancel", "leave_request.return_for_revision", "leave_request.approve", "leave_request.reject",
		}, true),
		integrationOwnedIdentityRole("sales_rep", "Sales representative", []string{
			"business.access", "customer.read", "customer.update", "contact.read", "contact.create",
			"lead.read", "lead.update", "opportunity.read", "opportunity.update",
			"activity.read", "activity.create", "activity.update", "contract.read", "lead.qualify", "lead.convert",
			"opportunity.advance_stage", "opportunity.mark_won", "activity.start", "activity.complete",
		}, []identitysdk.FieldPolicy{
			{Resource: "customer", Field: "health_score", Read: true},
			{Resource: "contract", Field: "value", Read: true, Masked: true},
		}),
		integrationIdentityRole("finance_reviewer", "Finance reviewer", []string{
			"business.access", "report.summary.get", "customer.read", "opportunity.read", "contract.read", "payment.read", "payment.update", "payment.export", "payment.mark_collected", "payment.record_overdue_escalation",
		}, true),
		integrationIdentityRole("restricted", "Restricted user", []string{"business.access", "customer.read"}, true),
		integrationOwnedIdentityRole("automation_business_tester", "Automation business tester", []string{
			"business.access", "customer.read", "customer.create", "customer.update", "customer.apply_automation_verification",
		}, []identitysdk.FieldPolicy{{Resource: "customer", Field: "*", Read: true, Write: true}}),
		integrationIdentityRole("automation_history_reviewer", "Automation history reviewer", []string{
			"admin_console.access", "runtime.automation.list_automation_executions",
		}, false),
		integrationIdentityRole("sales", "Sales", []string{
			"business.access", "workflow.sales_order.credit_discount_approval.run", "customer_account.read", "sales_order.read", "sales_order.create", "sales_order.update", "inventory_stock.read", "inventory_stock.update", "order_cash_ledger.read", "order_cash_ledger.create", "sales_order.initialize_risk", "sales_order.submit",
		}, true),
		integrationIdentityRole("credit_manager", "Credit manager", []string{
			"business.access", "runtime.workflows.approve_workflow_task", "runtime.workflows.reject_workflow_task", "runtime.workflows.return_workflow_task", "customer_account.read", "sales_order.read", "sales_order.update", "inventory_stock.read", "inventory_stock.update", "order_cash_ledger.create", "sales_order.approve_and_reserve", "sales_order.reject",
		}, true),
		integrationIdentityRole("finance", "Finance", []string{
			"business.access", "runtime.workflows.approve_workflow_task", "runtime.workflows.reject_workflow_task", "runtime.workflows.return_workflow_task", "identity.audit.view", "sales_order.read", "sales_order.update", "shipment.read", "inventory_stock.read", "inventory_stock.update", "invoice.read", "invoice.update", "payment.read", "payment.create", "payment.update", "return_request.read", "return_request.update", "order_cash_ledger.read", "order_cash_ledger.create",
			"sales_order.approve_and_reserve", "sales_order.reject",
		}, true),
	}
}

func integrationWorkflowTaskDecisionPermissions() []string {
	return []string{
		"runtime.workflows.approve_workflow_task",
		"runtime.workflows.reject_workflow_task",
		"runtime.workflows.return_workflow_task",
	}
}

func integrationIdentityRolePermissions(roleKey string) []string {
	for _, role := range integrationIdentityFixtureRoles() {
		if role.Key != strings.TrimSpace(roleKey) {
			continue
		}
		permissions := append([]string(nil), role.Permissions...)
		sort.Strings(permissions)
		return permissions
	}
	return nil
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
		WorkspaceID: identitysdk.WorkspaceID(workspaceID), ApplicationKey: identitysdk.ApplicationKey(applicationKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	return binding
}

func integrationOwnedIdentityRole(key, name string, permissions []string, fields []identitysdk.FieldPolicy) runtimetestkit.IdentityFixtureRole {
	predicate := identitysdk.Predicate{Fact: "owner_user_id", Operator: identitysdk.OperatorEqual, Value: "$subject.id"}
	role := integrationIdentityRole(key, name, permissions, false)
	role.DataPredicate = &predicate
	fieldPolicies := map[string]identitysdk.FieldPolicy{}
	for _, permission := range permissions {
		separator := strings.LastIndex(permission, ".")
		if separator <= 0 || separator == len(permission)-1 {
			continue
		}
		resource, action := permission[:separator], permission[separator+1:]
		policyKey := resource + "\x00*"
		policy := fieldPolicies[policyKey]
		policy.Resource, policy.Field = identitysdk.ResourceType(resource), "*"
		switch action {
		case "read":
			policy.Read = true
		case "create", "update", "delete":
			policy.Write = true
		case "export":
			policy.Export = true
		default:
			continue
		}
		fieldPolicies[policyKey] = policy
	}
	for _, policy := range fields {
		fieldPolicies[string(policy.Resource)+"\x00"+strings.TrimSpace(policy.Field)] = policy
	}
	keys := make([]string, 0, len(fieldPolicies))
	for policyKey := range fieldPolicies {
		keys = append(keys, policyKey)
	}
	sort.Strings(keys)
	role.FieldPolicies = make([]identitysdk.FieldPolicy, 0, len(keys))
	for _, policyKey := range keys {
		role.FieldPolicies = append(role.FieldPolicies, fieldPolicies[policyKey])
	}
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
		return roleForParticipantWorkflowRole(role)
	default:
		return "runtime_fixture_user"
	}
}

func roleForParticipantWorkflowRole(role string) string {
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

func integrationServiceIdentityRole(key, name string, permissions ...string) runtimetestkit.IdentityFixtureRole {
	return runtimetestkit.IdentityFixtureRole{
		Key: key, Name: name, Permissions: append([]string(nil), permissions...), Audience: "service", AssignmentMode: "system_managed", AllowAllBusinessData: true,
	}
}
