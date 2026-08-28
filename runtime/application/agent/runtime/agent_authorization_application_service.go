package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"go.opentelemetry.io/otel/attribute"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/telemetry"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentSchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot
}

type agentInternalSchemaProvider interface {
	Schema() metadatamodel.MetadataSchemaSnapshot
}

func fullAgentSchema(ctx context.Context, provider AgentSchemaProvider) metadatamodel.MetadataSchemaSnapshot {
	if internal, ok := provider.(agentInternalSchemaProvider); ok {
		return internal.Schema()
	}
	return provider.SchemaForPrincipal(ctx, principalmodel.Principal{})
}

type AgentRecordVisibility interface {
	CanReadAgentRecord(context.Context, string, string, principalmodel.Principal) (bool, error)
}

type AgentAuthorizationDependencies struct {
	Principals identitysdk.PrincipalResolver
	Schema     AgentSchemaProvider
	Records    AgentRecordVisibility
}

type AgentAuthorizationApplicationService struct {
	principals identitysdk.PrincipalResolver
	schema     AgentSchemaProvider
	records    AgentRecordVisibility
}

func NewAgentAuthorizationApplicationService(dependencies AgentAuthorizationDependencies) *AgentAuthorizationApplicationService {
	return &AgentAuthorizationApplicationService{principals: dependencies.Principals, schema: dependencies.Schema, records: dependencies.Records}
}

type AgentExecutionIdentityRequest struct {
	Initiator               principalmodel.Principal
	Identity                agentmodel.AgentTaskIdentity
	ExpectedRotationVersion int
}

type AgentTaskAuthorizationRequest struct {
	Initiator               principalmodel.Principal
	Identity                agentmodel.AgentTaskIdentity
	ExpectedRotationVersion int
	TaskKey                 string
	TaskVersion             string
	NodeAllowedObjects      []string
	NodeAllowedActions      []string
	NodeAllowedOutcomes     []string
}

type AgentTaskAuthorization struct {
	Principal       principalmodel.Principal
	Identity        agentmodel.AgentExecutionIdentity
	Task            agentmodel.AgentTaskDefinition
	AllowedObjects  []string
	AllowedActions  []string
	AllowedOutcomes []string
	AllowedTools    []string
	VisibleFields   map[string][]string
	Evidence        agentmodel.AgentAuthorizationEvidence
}

type GlobalAgentContextRequest struct {
	Principal             principalmodel.Principal
	EntrypointKey         string
	Surface               string
	RouteKey              string
	ObjectKey             string
	RecordID              string
	SelectedRecordIDs     []string
	Locale                string
	Timezone              string
	AvailableOperationIDs []string
}

func (s *AgentAuthorizationApplicationService) ResolveExecutionIdentity(ctx context.Context, request AgentExecutionIdentityRequest) (principalmodel.Principal, agentmodel.AgentExecutionIdentity, error) {
	initiator := request.Initiator
	initiatorWorkspace, workspaceErr := principalmodel.NewWorkspaceID(initiator.WorkspaceID)
	if workspaceErr != nil {
		return principalmodel.Principal{}, agentmodel.AgentExecutionIdentity{}, agentAuthorizationError("agent.authorization.initiator_inactive", initiator.AuthorizationRevision)
	}
	if !initiator.Known || strings.TrimSpace(initiator.UserID) == "" {
		return principalmodel.Principal{}, agentmodel.AgentExecutionIdentity{}, agentAuthorizationError("agent.authorization.initiator_inactive", initiator.AuthorizationRevision)
	}
	if s == nil || s.principals == nil || s.schema == nil {
		return principalmodel.Principal{}, agentmodel.AgentExecutionIdentity{}, apperror.New(apperror.KindUnavailable, "agent.authorization.resolver_unavailable", nil, nil)
	}
	var execution principalmodel.Principal
	identity := agentmodel.AgentExecutionIdentity{Mode: strings.TrimSpace(request.Identity.Mode), Initiator: agentPrincipalReference(initiator)}
	switch identity.Mode {
	case agentmodel.AgentTaskIdentityInherit:
		var err error
		execution, err = resolveIdentitySDKPrincipal(ctx, s.principals, initiator.UserID, initiator.RoleKey)
		if err != nil {
			return principalmodel.Principal{}, agentmodel.AgentExecutionIdentity{}, err
		}
	case agentmodel.AgentTaskIdentityService:
		binding, found := findAgentServicePrincipal(fullAgentSchema(ctx, s.schema), request.Identity.PrincipalKey)
		if !found || !binding.Enabled {
			return principalmodel.Principal{}, agentmodel.AgentExecutionIdentity{}, agentAuthorizationError("agent.authorization.service_principal_disabled", initiator.AuthorizationRevision)
		}
		if request.ExpectedRotationVersion > 0 && request.ExpectedRotationVersion != binding.RotationVersion {
			return principalmodel.Principal{}, agentmodel.AgentExecutionIdentity{}, agentAuthorizationError("agent.authorization.service_rotation_stale", initiator.AuthorizationRevision)
		}
		var err error
		execution, err = resolveIdentitySDKPrincipal(ctx, s.principals, binding.UserID, binding.RoleKey)
		if err != nil {
			return principalmodel.Principal{}, agentmodel.AgentExecutionIdentity{}, err
		}
		identity.ServicePrincipalKey, identity.ServiceRotationVersion = binding.Key, binding.RotationVersion
	default:
		return principalmodel.Principal{}, agentmodel.AgentExecutionIdentity{}, agentAuthorizationError("agent.authorization.identity_mode_denied", initiator.AuthorizationRevision)
	}
	if !execution.Known {
		return principalmodel.Principal{}, agentmodel.AgentExecutionIdentity{}, agentAuthorizationError("agent.authorization.execution_principal_inactive", initiator.AuthorizationRevision)
	}
	executionWorkspace, workspaceErr := principalmodel.NewWorkspaceID(execution.WorkspaceID)
	if workspaceErr != nil || executionWorkspace != initiatorWorkspace {
		return principalmodel.Principal{}, agentmodel.AgentExecutionIdentity{}, agentAuthorizationError("agent.authorization.workspace_removed", execution.AuthorizationRevision)
	}
	execution.RequestID, execution.CorrelationID, execution.CausationID = initiator.RequestID, initiator.CorrelationID, initiator.CausationID
	execution.SurfaceKey = initiator.SurfaceKey
	identity.Execution = agentPrincipalReference(execution)
	return execution, identity, nil
}

func (s *AgentAuthorizationApplicationService) AuthorizeTask(ctx context.Context, request AgentTaskAuthorizationRequest) (AgentTaskAuthorization, error) {
	execution, identity, err := s.ResolveExecutionIdentity(ctx, AgentExecutionIdentityRequest{Initiator: request.Initiator, Identity: request.Identity, ExpectedRotationVersion: request.ExpectedRotationVersion})
	if err != nil {
		return AgentTaskAuthorization{}, err
	}
	full := fullAgentSchema(ctx, s.schema)
	task, found := findAgentTask(full.AgentTasks, request.TaskKey, request.TaskVersion)
	if !found || !task.Enabled {
		return AgentTaskAuthorization{}, agentAuthorizationError("agent.authorization.task_unpublished", execution.AuthorizationRevision)
	}
	if !agentSubset(request.NodeAllowedObjects, task.AllowedObjects) || !agentSubset(request.NodeAllowedActions, task.AllowedActions) || !agentSubset(request.NodeAllowedOutcomes, task.AllowedOutcomes) {
		return AgentTaskAuthorization{}, agentAuthorizationError("agent.authorization.node_allowlist_widening", execution.AuthorizationRevision)
	}
	visible := s.schema.SchemaForPrincipal(ctx, execution)
	visibleObjects := agentObjectKeys(visible.Objects)
	visibleActions := agentActionKeys(visible.Actions)
	tools := agentToolsForTask(visible, task)
	objects := agentIntersect(agentEffectiveNodeLimit(task.AllowedObjects, request.NodeAllowedObjects), visibleObjects)
	actions := agentIntersect(agentEffectiveNodeLimit(task.AllowedActions, request.NodeAllowedActions), visibleActions)
	outcomes := agentEffectiveNodeLimit(task.AllowedOutcomes, request.NodeAllowedOutcomes)
	if len(task.AllowedObjects) > 0 && len(objects) == 0 || len(task.AllowedActions) > 0 && task.SideEffectMode != agentmodel.AgentTaskSideEffectAnalysisOnly && len(actions) == 0 {
		return AgentTaskAuthorization{}, agentAuthorizationError("agent.authorization.capability_revoked", execution.AuthorizationRevision)
	}
	fields := map[string][]string{}
	for _, object := range visible.Objects {
		if !agentContains(objects, object.Key) {
			continue
		}
		for _, field := range object.Fields {
			fields[object.Key] = append(fields[object.Key], field.Key)
		}
		sort.Strings(fields[object.Key])
	}
	policyRevision := agentStableHash(map[string]any{"schema_hash": visible.SchemaHash, "task_key": task.Key, "task_version": task.Version, "authorization_revision": execution.AuthorizationRevision, "objects": objects, "actions": actions, "outcomes": outcomes, "tools": tools})
	evidence := agentmodel.AgentAuthorizationEvidence{Decision: "allow", Code: "agent.authorization.allowed", PolicyRevision: policyRevision, AuthorizationRevision: execution.AuthorizationRevision, AllowedObjects: objects, AllowedActions: actions, AllowedOutcomes: outcomes, AllowedTools: tools}
	return AgentTaskAuthorization{Principal: execution, Identity: identity, Task: task, AllowedObjects: objects, AllowedActions: actions, AllowedOutcomes: outcomes, AllowedTools: tools, VisibleFields: fields, Evidence: evidence}, nil
}

func agentToolsForTask(snapshot metadatamodel.MetadataSchemaSnapshot, task agentmodel.AgentTaskDefinition) []string {
	skillKeys := map[string]bool{}
	tools := []string{}
	for _, agent := range snapshot.Agents {
		if strings.TrimSpace(agent.Key) != strings.TrimSpace(task.AgentKey) {
			continue
		}
		tools = append(tools, agent.Tools...)
		for _, key := range agent.SkillKeys {
			skillKeys[strings.TrimSpace(key)] = true
		}
		break
	}
	for _, skill := range snapshot.Skills {
		if skillKeys[strings.TrimSpace(skill.Key)] {
			tools = append(tools, skill.AllowedTools...)
		}
	}
	return agentUniqueStrings(tools)
}

func (s *AgentAuthorizationApplicationService) ResolveGlobalContext(ctx context.Context, request GlobalAgentContextRequest) (result agentmodel.GlobalAgentContext, err error) {
	ctx, span := telemetry.StartUseCase(ctx, "agent.global_context.resolve", attribute.String("workspace.id", request.Principal.WorkspaceID), attribute.String("agent.entrypoint", request.EntrypointKey), attribute.String("product.surface", request.Surface), attribute.String("route.key", request.RouteKey))
	defer func() { telemetry.EndUseCase(span, err, "") }()
	principal, _, err := s.ResolveExecutionIdentity(ctx, AgentExecutionIdentityRequest{Initiator: request.Principal, Identity: agentmodel.AgentTaskIdentity{Mode: agentmodel.AgentTaskIdentityInherit}})
	if err != nil {
		return agentmodel.GlobalAgentContext{}, err
	}
	full := fullAgentSchema(ctx, s.schema)
	assignment, found := findAgentEntrypoint(full.AgentEntrypoints, request.EntrypointKey)
	if !found || !assignment.Enabled || !principal.HasAllPermissions(assignment.RequiredPermissions) {
		return agentmodel.GlobalAgentContext{}, agentAuthorizationError("agent.authorization.entrypoint_denied", principal.AuthorizationRevision)
	}
	if strings.TrimSpace(request.Surface) != strings.TrimSpace(assignment.Surface) {
		return agentmodel.GlobalAgentContext{}, agentAuthorizationError("agent.authorization.surface_denied", principal.AuthorizationRevision)
	}
	entrypoint, routeFound := findBusinessEntrypoint(full.EntryPoints, request.RouteKey)
	if !routeFound || !agentRouteAllowed(assignment.RoutePatterns, request.RouteKey) || agentEntrypointSurface(entrypoint) != assignment.Surface {
		return agentmodel.GlobalAgentContext{}, agentAuthorizationError("agent.authorization.route_denied", principal.AuthorizationRevision)
	}
	visible := s.schema.SchemaForPrincipal(ctx, principal)
	if _, visibleRoute := findBusinessEntrypoint(visible.EntryPoints, request.RouteKey); !visibleRoute {
		return agentmodel.GlobalAgentContext{}, agentAuthorizationError("agent.authorization.route_denied", principal.AuthorizationRevision)
	}
	objectKey := strings.TrimSpace(request.ObjectKey)
	if objectKey != "" && (!agentEntrypointAllowsObject(entrypoint, objectKey) || !agentObjectKeys(visible.Objects)[objectKey]) {
		return agentmodel.GlobalAgentContext{}, agentAuthorizationError("agent.authorization.object_denied", principal.AuthorizationRevision)
	}
	selected := agentUniqueStrings(request.SelectedRecordIDs)
	if len(selected) > assignment.ContextContract.MaxSelectedRecord {
		return agentmodel.GlobalAgentContext{}, agentAuthorizationError("agent.authorization.selection_limit", principal.AuthorizationRevision)
	}
	for _, recordID := range append([]string{strings.TrimSpace(request.RecordID)}, selected...) {
		if recordID == "" {
			continue
		}
		if objectKey == "" || s.records == nil {
			return agentmodel.GlobalAgentContext{}, agentAuthorizationError("agent.authorization.record_denied", principal.AuthorizationRevision)
		}
		allowed, visibilityErr := s.records.CanReadAgentRecord(ctx, objectKey, recordID, principal)
		if visibilityErr != nil {
			return agentmodel.GlobalAgentContext{}, visibilityErr
		}
		if !allowed {
			return agentmodel.GlobalAgentContext{}, agentAuthorizationError("agent.authorization.record_denied", principal.AuthorizationRevision)
		}
	}
	taskKeys := agentIntersect(assignment.AllowedTaskKeys, agentTaskKeys(visible.AgentTasks))
	workflowKeys := agentIntersect(assignment.AllowedWorkflowKeys, agentWorkflowKeys(visible.Workflows))
	operations := []string{}
	for _, key := range taskKeys {
		operations = append(operations, "task:"+key)
	}
	for _, key := range workflowKeys {
		operations = append(operations, "workflow:"+key)
	}
	for _, action := range visible.Actions {
		if objectKey == "" || action.ObjectKey == objectKey {
			operations = append(operations, "action:"+action.Key)
		}
	}
	operations = agentUniqueStrings(operations)
	if len(request.AvailableOperationIDs) > 0 {
		operations = agentIntersect(request.AvailableOperationIDs, agentStringSet(operations))
	}
	contextValue := agentmodel.GlobalAgentContext{
		ContractVersion: agentmodel.GlobalAgentContextContractVersion, EntrypointKey: assignment.Key, AgentKey: assignment.AgentKey, Surface: assignment.Surface, RouteKey: strings.TrimSpace(request.RouteKey),
		ObjectKey: objectKey, RecordID: strings.TrimSpace(request.RecordID), SelectedRecordIDs: selected, Locale: strings.TrimSpace(request.Locale), Timezone: strings.TrimSpace(request.Timezone),
		Principal: agentPrincipalReference(principal), AllowedTaskKeys: taskKeys, AllowedWorkflowKeys: workflowKeys, AvailableOperations: operations,
	}
	encoded, _ := json.Marshal(contextValue)
	if len(encoded) > assignment.ContextContract.MaxContextBytes {
		return agentmodel.GlobalAgentContext{}, agentAuthorizationError("agent.authorization.context_limit", principal.AuthorizationRevision)
	}
	contextValue.ContextRevision = agentStableHash(map[string]any{"context": contextValue, "schema_hash": visible.SchemaHash, "authorization_revision": principal.AuthorizationRevision})
	return contextValue, nil
}

func agentEntrypointAllowsObject(entrypoint definitionmodel.EntryPointSchema, objectKey string) bool {
	for _, field := range []string{"create_objects", "read_objects", "update_objects"} {
		for _, value := range agentStringsFromAny(entrypoint.Config[field]) {
			if value == objectKey {
				return true
			}
		}
	}
	return strings.TrimSpace(fmt.Sprint(entrypoint.Config["primary_object"])) == objectKey
}

func agentStringsFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return agentUniqueStrings(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, fmt.Sprint(item))
		}
		return agentUniqueStrings(values)
	default:
		return nil
	}
}

func agentSubset(subset, superset []string) bool {
	allowed := agentStringSet(superset)
	for _, value := range subset {
		if !allowed[strings.TrimSpace(value)] {
			return false
		}
	}
	return true
}

func agentEffectiveNodeLimit(task, node []string) []string {
	if len(node) == 0 {
		return agentUniqueStrings(task)
	}
	return agentUniqueStrings(node)
}

func agentIntersect(values []string, allowed map[string]bool) []string {
	out := []string{}
	for _, value := range agentUniqueStrings(values) {
		if allowed[value] {
			out = append(out, value)
		}
	}
	return out
}

func agentUniqueStrings(values []string) []string {
	set := agentStringSet(values)
	out := make([]string, 0, len(set))
	for value := range set {
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func agentStringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[strings.TrimSpace(value)] = true
	}
	return out
}

func agentContains(values []string, expected string) bool {
	return agentStringSet(values)[strings.TrimSpace(expected)]
}

func agentObjectKeys(objects []definitionmodel.ObjectSchema) map[string]bool {
	out := map[string]bool{}
	for _, object := range objects {
		out[strings.TrimSpace(object.Key)] = true
	}
	return out
}

func agentActionKeys(actions []definitionmodel.ActionSchema) map[string]bool {
	out := map[string]bool{}
	for _, action := range actions {
		out[strings.TrimSpace(action.Key)] = true
	}
	return out
}

func agentTaskKeys(tasks []agentmodel.AgentTaskDefinition) map[string]bool {
	out := map[string]bool{}
	for _, task := range tasks {
		if task.Enabled {
			out[strings.TrimSpace(task.Key)] = true
		}
	}
	return out
}

func agentWorkflowKeys(workflows []definitionmodel.WorkflowSchema) map[string]bool {
	out := map[string]bool{}
	for _, workflow := range workflows {
		if workflow.Enabled {
			out[strings.TrimSpace(workflow.Key)] = true
		}
	}
	return out
}

func agentStableHash(value any) string {
	raw, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
