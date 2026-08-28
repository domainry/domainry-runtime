package record

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

// recordFullAccessPrincipal models an Identity-issued, catalog-expanded test
// administrator. It is deliberately test-only: Runtime production code must
// never infer business permissions from Known, a role name, or workspace.admin.
func recordFullAccessPrincipal(principal principalmodel.Principal) principalmodel.Principal {
	return accessfixture.Attach(principal, accessfixture.Bundle{
		Key: "test-record-admin", Permissions: []string{"*"}, RecordScope: "all_records",
	})
}
