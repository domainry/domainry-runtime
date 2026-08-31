package agenthost

import (
	"context"
	"strconv"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentInteractiveAuthorization struct {
	Context                      agentsdk.GlobalContext
	Principal                    principalmodel.Principal
	Agent                        agentsdk.AgentSchema
	Candidates                   []agentsdk.RouteCandidate
	AllowedTools, AllowedActions []string
	VisibleFields                map[string][]string
}

func (s *AgentAuthorizationApplicationService) AuthorizeInteractive(ctx context.Context, stored agentsdk.GlobalContext, principal principalmodel.Principal) (AgentInteractiveAuthorization, error) {
	if s == nil || s.schema == nil {
		return AgentInteractiveAuthorization{}, apperror.New(apperror.KindUnavailable, "agent.authorization.schema_unavailable", nil, nil)
	}
	resolved, err := s.ResolveGlobalContext(ctx, GlobalAgentContextRequest{
		Principal: principal, EntrypointKey: stored.EntrypointKey, RouteKey: stored.RouteKey,
		ObjectKey: stored.ObjectKey, RecordID: stored.RecordID, SelectedRecordIDs: stored.SelectedRecordIDs, Locale: stored.Locale, Timezone: stored.Timezone,
		AvailableOperationIDs: stored.AvailableOperations,
	})
	if err != nil {
		return AgentInteractiveAuthorization{}, err
	}
	if strings.TrimSpace(stored.ContextRevision) != "" && stored.ContextRevision != resolved.ContextRevision {
		return AgentInteractiveAuthorization{}, agentAuthorizationError("agent.authorization.context_stale", resolved.Principal.AuthorizationRevision)
	}
	livePrincipal, err := resolveIdentitySDKPrincipal(ctx, s.principals, resolved.Principal.UserID, resolved.Principal.RoleKey)
	if err != nil {
		return AgentInteractiveAuthorization{}, err
	}
	if !livePrincipal.Known || livePrincipal.WorkspaceID != resolved.Principal.WorkspaceID {
		return AgentInteractiveAuthorization{}, agentAuthorizationError("agent.authorization.execution_principal_inactive", livePrincipal.AuthorizationRevision)
	}
	full, visible := fullAgentSchema(ctx, s.schema), s.schema.SchemaForPrincipal(ctx, livePrincipal)
	assignment, found := findAgentEntrypoint(full.AgentEntrypoints, resolved.EntrypointKey)
	if !found || assignment.AgentKey != resolved.AgentKey {
		return AgentInteractiveAuthorization{}, agentAuthorizationError("agent.authorization.entrypoint_denied", livePrincipal.AuthorizationRevision)
	}
	agent, found := findInteractiveAgent(visible.Agents, resolved.AgentKey)
	if !found {
		return AgentInteractiveAuthorization{}, agentAuthorizationError("agent.authorization.agent_unpublished", livePrincipal.AuthorizationRevision)
	}
	allowedRouteTypes := agentStringSet(assignment.RoutingContract.AllowedRouteTypes)
	candidates := []agentsdk.RouteCandidate{}
	if allowedRouteTypes[agentsdk.AgentRouteInteractiveQuery] {
		candidates = append(candidates, agentsdk.RouteCandidate{RouteType: agentsdk.AgentRouteInteractiveQuery, TargetKey: agent.Key, Version: agent.Version})
	}
	if allowedRouteTypes[agentsdk.AgentRouteTask] {
		for _, task := range visible.AgentTasks {
			if task.Enabled && agentContains(resolved.AllowedTaskKeys, task.Key) {
				candidates = append(candidates, agentsdk.RouteCandidate{RouteType: agentsdk.AgentRouteTask, TargetKey: task.Key, Version: task.Version})
			}
		}
	}
	if allowedRouteTypes[agentsdk.AgentRouteWorkflow] {
		for _, workflow := range visible.Workflows {
			if workflow.Enabled && agentContains(resolved.AllowedWorkflowKeys, workflow.Key) {
				version := strings.TrimSpace(workflow.DefinitionVersionID)
				if version == "" && workflow.PublishedVersion > 0 {
					version = strconv.Itoa(workflow.PublishedVersion)
				}
				candidates = append(candidates, agentsdk.RouteCandidate{RouteType: agentsdk.AgentRouteWorkflow, TargetKey: workflow.Key, Version: version})
			}
		}
	}
	if allowedRouteTypes[agentsdk.AgentRouteProposal] {
		for _, operation := range resolved.AvailableOperations {
			if strings.HasPrefix(operation, "action:") {
				candidates = append(candidates, agentsdk.RouteCandidate{RouteType: agentsdk.AgentRouteProposal, TargetKey: strings.TrimPrefix(operation, "action:")})
			}
		}
	}
	allowedActions := []string{}
	for _, operation := range resolved.AvailableOperations {
		if strings.HasPrefix(operation, "action:") {
			allowedActions = append(allowedActions, strings.TrimPrefix(operation, "action:"))
		}
	}
	visibleFields := map[string][]string{}
	for _, object := range visible.Objects {
		if object.Key != resolved.ObjectKey {
			continue
		}
		for _, field := range object.Fields {
			visibleFields[object.Key] = append(visibleFields[object.Key], field.Key)
		}
	}
	return AgentInteractiveAuthorization{Context: resolved, Principal: livePrincipal, Agent: agent, Candidates: candidates, AllowedTools: agentToolsForInteractiveAgent(visible, agent), AllowedActions: allowedActions, VisibleFields: visibleFields}, nil
}

func findInteractiveAgent(agents []agentsdk.AgentSchema, key string) (agentsdk.AgentSchema, bool) {
	for _, agent := range agents {
		if strings.TrimSpace(agent.Key) == strings.TrimSpace(key) {
			return agent, true
		}
	}
	return agentsdk.AgentSchema{}, false
}

func agentToolsForInteractiveAgent(snapshot appschemamodel.ApplicationSchemaSnapshot, agent agentsdk.AgentSchema) []string {
	tools := append([]string(nil), agent.Tools...)
	skills := agentStringSet(agent.SkillKeys)
	for _, skill := range snapshot.Skills {
		if skills[strings.TrimSpace(skill.Key)] {
			tools = append(tools, skill.AllowedTools...)
		}
	}
	return agentUniqueStrings(tools)
}
