package record

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

// recordFullAccessPrincipal models an Identity-issued test role with explicit
// object Action grants. It is deliberately test-only: Runtime production code
// must never infer business permissions from Known, a role name, runtime.appschema.validate_application_definition,
// or a wildcard grant.
func recordFullAccessPrincipal(principal principalmodel.Principal) principalmodel.Principal {
	return accessfixture.Attach(principal, recordFullAccessBundle())
}

func recordFullAccessBundle(additionalPermissions ...string) accessfixture.Bundle {
	objectKeys := []string{
		"case", "child", "counter", "customer", "emergency_contact", "employee_document", "employee_profile",
		"empty", "governed_document", "identity", "invoice", "line_item", "member_profile", "note", "order",
		"pipeline_item", "product", "profile", "record_timer", "root", "sales_order", "settings", "task", "ticket",
	}
	actions := []string{"create", "read", "update", "delete", "restore", "import", "export"}
	permissions := append([]string(nil), additionalPermissions...)
	for _, objectKey := range objectKeys {
		for _, action := range actions {
			permissions = append(permissions, objectKey+"."+action)
		}
	}
	return accessfixture.Bundle{Key: "test-record-admin", Permissions: permissions, RecordScope: "all_records"}
}
