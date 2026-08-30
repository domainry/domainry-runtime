package runtime

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func agentAuthorizationError(code, revision string) error {
	return apperror.New(apperror.KindForbidden, code, nil, map[string]string{"authorization_revision": strings.TrimSpace(revision)})
}

func agentPrincipalReference(principal principalmodel.Principal) agentmodel.AgentPrincipalReference {
	return agentmodel.AgentPrincipalReference{UserID: principal.UserID, RoleKey: principal.RoleKey, WorkspaceID: principal.WorkspaceID, AuthorizationRevision: principal.AuthorizationRevision}
}

func findAgentServicePrincipal(snapshot appschemamodel.ApplicationSchemaSnapshot, key string) (agentmodel.AgentServicePrincipalBinding, bool) {
	key = strings.TrimSpace(key)
	for _, binding := range snapshot.AgentServicePrincipals {
		if strings.TrimSpace(binding.Key) == key {
			return binding, true
		}
	}
	return agentmodel.AgentServicePrincipalBinding{}, false
}

func findAgentTask(tasks []agentmodel.AgentTaskDefinition, key, version string) (agentmodel.AgentTaskDefinition, bool) {
	key, version = strings.TrimSpace(key), strings.TrimSpace(version)
	for _, task := range tasks {
		if strings.TrimSpace(task.Key) == key && strings.TrimSpace(task.Version) == version {
			return task, true
		}
	}
	return agentmodel.AgentTaskDefinition{}, false
}

func findAgentEntrypoint(entrypoints []agentmodel.AgentEntrypointAssignment, key string) (agentmodel.AgentEntrypointAssignment, bool) {
	key = strings.TrimSpace(key)
	for _, entrypoint := range entrypoints {
		if strings.TrimSpace(entrypoint.Key) == key {
			return entrypoint, true
		}
	}
	return agentmodel.AgentEntrypointAssignment{}, false
}

func findBusinessEntrypoint(entrypoints []definitionmodel.EntryPointSchema, key string) (definitionmodel.EntryPointSchema, bool) {
	key = strings.TrimSpace(key)
	for _, entrypoint := range entrypoints {
		if strings.TrimSpace(entrypoint.Key) == key {
			return entrypoint, true
		}
	}
	return definitionmodel.EntryPointSchema{}, false
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

func agentEntrypointSurface(entrypoint definitionmodel.EntryPointSchema) string {
	switch strings.TrimSpace(fmt.Sprint(entrypoint.Config["kind"])) {
	case "backoffice", "operator_console", "business_workspace":
		return "business_workspace"
	case "admin_console":
		return "admin_console"
	case "customer_portal", "consumer_portal":
		return "consumer_portal"
	default:
		return ""
	}
}
