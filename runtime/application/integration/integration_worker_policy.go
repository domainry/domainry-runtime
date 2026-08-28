package integration

import (
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func SyncPolicyKey(req SyncCallRequest, connection integrationmodel.IntegrationConnection, principal principalmodel.Principal) string {
	workspaceID := strings.TrimSpace(connection.WorkspaceID)
	return strings.Join([]string{
		workspaceID,
		strings.TrimSpace(req.ConnectorKey),
		strings.TrimSpace(req.ConnectionKey),
		strings.TrimSpace(req.ActionKey),
		strings.TrimSpace(req.InvocationKey),
		strings.TrimSpace(req.Operation),
	}, ":")
}
