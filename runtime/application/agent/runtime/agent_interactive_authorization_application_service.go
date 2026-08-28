package runtime

import (
	"context"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentInteractiveAuthorization struct {
	Context                      agentmodel.GlobalAgentContext
	Principal                    principalmodel.Principal
	Agent                        agentmodel.AgentSchema
	Candidates                   []AgentRouteCandidate
	AllowedTools, AllowedActions []string
	VisibleFields                map[string][]string
}

func (s *AgentAuthorizationApplicationService) AuthorizeInteractive(ctx context.Context, stored agentmodel.GlobalAgentContext, principal principalmodel.Principal) (AgentInteractiveAuthorization, error) {
	if s == nil || s.schema == nil {
		return AgentInteractiveAuthorization{}, apperror.New(apperror.KindUnavailable, "agent.authorization.schema_unavailable", nil, nil)
	}
	resolved, err := s.ResolveGlobalContext(ctx, GlobalAgentContextRequest{
		Principal: principal, EntrypointKey: stored.EntrypointKey, Surface: stored.Surface, RouteKey: stored.RouteKey,
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
	candidates := []AgentRouteCandidate{}
	if allowedRouteTypes[agentmodel.AgentRouteInteractiveQuery] {
		candidates = append(candidates, AgentRouteCandidate{RouteType: agentmodel.AgentRouteInteractiveQuery, TargetKey: agent.Key, Version: agent.Version})
	}
	if allowedRouteTypes[agentmodel.AgentRouteTask] {
		for _, task := range visible.AgentTasks {
			if task.Enabled && agentContains(resolved.AllowedTaskKeys, task.Key) {
				candidates = append(candidates, AgentRouteCandidate{RouteType: agentmodel.AgentRouteTask, TargetKey: task.Key, Version: task.Version})
			}
		}
	}
	if allowedRouteTypes[agentmodel.AgentRouteWorkflow] {
		for _, workflow := range visible.Workflows {
			if workflow.Enabled && agentContains(resolved.AllowedWorkflowKeys, workflow.Key) {
				version := strings.TrimSpace(workflow.DefinitionVersionID)
				if version == "" && workflow.PublishedVersion > 0 {
					version = strconv.Itoa(workflow.PublishedVersion)
				}
				candidates = append(candidates, AgentRouteCandidate{RouteType: agentmodel.AgentRouteWorkflow, TargetKey: workflow.Key, Version: version})
			}
		}
	}
	if allowedRouteTypes[agentmodel.AgentRouteProposal] {
		for _, operation := range resolved.AvailableOperations {
			if strings.HasPrefix(operation, "action:") {
				candidates = append(candidates, AgentRouteCandidate{RouteType: agentmodel.AgentRouteProposal, TargetKey: strings.TrimPrefix(operation, "action:")})
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

func findInteractiveAgent(agents []agentmodel.AgentSchema, key string) (agentmodel.AgentSchema, bool) {
	for _, agent := range agents {
		if strings.TrimSpace(agent.Key) == strings.TrimSpace(key) {
			return agent, true
		}
	}
	return agentmodel.AgentSchema{}, false
}

func agentToolsForInteractiveAgent(snapshot metadatamodel.MetadataSchemaSnapshot, agent agentmodel.AgentSchema) []string {
	tools := append([]string(nil), agent.Tools...)
	skills := agentStringSet(agent.SkillKeys)
	for _, skill := range snapshot.Skills {
		if skills[strings.TrimSpace(skill.Key)] {
			tools = append(tools, skill.AllowedTools...)
		}
	}
	return agentUniqueStrings(tools)
}
