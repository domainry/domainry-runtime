package integration

import (
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func integrationSDKPrincipal(userID, roleKey string, permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: strings.TrimSpace(userID)}},
		accessfixture.Bundle{Key: strings.TrimSpace(roleKey), Permissions: append([]string(nil), permissions...)},
	)
}

// integrationWorkspaceAdmin models what Identity would materialize after
// expanding a configured administrator role against Runtime's integration
// catalog. Runtime production code must never infer these grants from the role
// name or from workspace.admin alone.
func integrationWorkspaceAdmin(userID, workspaceID string) principalmodel.Principal {
	return accessfixture.Attach(
		principalmodel.Principal{Principal: identitysdk.Principal{
			Known: true, UserID: strings.TrimSpace(userID), WorkspaceID: strings.TrimSpace(workspaceID),
		}},
		accessfixture.Bundle{Key: "admin", Permissions: []string{
			"workspace.admin",
			PermissionCatalogView,
			PermissionConnectionManage,
			PermissionSecretManage,
			PermissionConnectionTest,
			PermissionInvoke,
			PermissionRetry,
			PermissionAuditView,
		}},
	)
}
