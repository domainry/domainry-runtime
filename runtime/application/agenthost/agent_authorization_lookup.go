package agenthost

import (
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func agentAuthorizationError(code, revision string) error {
	return apperror.New(apperror.KindForbidden, code, nil, map[string]string{"authorization_revision": strings.TrimSpace(revision)})
}

func agentPrincipalReference(principal principalmodel.Principal) agentsdk.PrincipalReference {
	return agentsdk.PrincipalReference{UserID: principal.UserID, RoleKey: principal.RoleKey, WorkspaceID: principal.WorkspaceID, AuthorizationRevision: principal.AuthorizationRevision}
}

func findAgentServicePrincipal(snapshot appschemamodel.ApplicationSchemaSnapshot, key string) (agentsdk.AgentServicePrincipalBinding, bool) {
	key = strings.TrimSpace(key)
	for _, binding := range snapshot.AgentServicePrincipals {
		if strings.TrimSpace(binding.Key) == key {
			return binding, true
		}
	}
	return agentsdk.AgentServicePrincipalBinding{}, false
}

func findAgentTask(tasks []agentsdk.AgentTaskDefinition, key, version string) (agentsdk.AgentTaskDefinition, bool) {
	key, version = strings.TrimSpace(key), strings.TrimSpace(version)
	for _, task := range tasks {
		if strings.TrimSpace(task.Key) == key && strings.TrimSpace(task.Version) == version {
			return task, true
		}
	}
	return agentsdk.AgentTaskDefinition{}, false
}

func findAgentEntrypoint(entrypoints []agentsdk.AgentEntrypointAssignment, key string) (agentsdk.AgentEntrypointAssignment, bool) {
	key = strings.TrimSpace(key)
	for _, entrypoint := range entrypoints {
		if strings.TrimSpace(entrypoint.Key) == key {
			return entrypoint, true
		}
	}
	return agentsdk.AgentEntrypointAssignment{}, false
}

func agentRouteAllowed(patterns []string, route string) bool {
	route = strings.TrimSpace(route)
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == route || strings.HasSuffix(pattern, "*") && strings.HasPrefix(route, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}
