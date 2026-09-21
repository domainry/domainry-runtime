package workflow

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type projectAssigneeResolverCapabilities struct {
	dependencies WorkflowDependencies
	descriptor   runtimeext.AssigneeResolverDescriptor
	workspaceID  string
	principal    principalmodel.Principal
	mu           sync.Mutex
	reads        int
}

func (e *WorkflowProcessEngine) resolveProjectAssignees(ctx context.Context, process workflowmodel.WorkflowProcessInstance, nodeID string, resolver definitionmodel.WorkflowAssigneeResolver, resolverIndex int, principal principalmodel.Principal) ([]ResolvedAssignee, error) {
	if e.runtime.dependencies.ProjectExtensions == nil {
		return nil, badRequest("backend.workflow.resolver_not_registered", "resolver", resolver.ResolverKey)
	}
	binding, found := e.runtime.dependencies.ProjectExtensions.AssigneeResolverBinding(resolver.ResolverKey)
	if !found {
		return nil, badRequest("backend.workflow.resolver_not_registered", "resolver", resolver.ResolverKey)
	}
	config, err := runtimeext.NormalizeAssigneeResolverConfig(binding.Descriptor, resolver.Config)
	if err != nil {
		return nil, badRequest("backend.workflow.resolver_config_invalid", "resolver", resolver.ResolverKey)
	}
	timeout := time.Duration(binding.Descriptor.TimeoutMilliseconds) * time.Millisecond
	resolverCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	capabilities := &projectAssigneeResolverCapabilities{dependencies: e.runtime.dependencies, descriptor: binding.Descriptor, workspaceID: process.WorkspaceID, principal: principal}
	candidates, err := binding.Resolver.Resolve(resolverCtx, capabilities, runtimeext.AssigneeResolverContext{
		WorkspaceID: process.WorkspaceID, ProcessID: process.ID, WorkflowKey: process.WorkflowKey, DefinitionVersion: process.DefinitionVersion,
		NodeID: nodeID, ObjectKey: process.ObjectKey, RecordID: process.RecordID, InitiatorUserID: process.InitiatorID, InitiatorRoleKey: process.InitiatorRoleKey,
		Variables: cloneAssigneeMap(process.Variables), Config: cloneAssigneeMap(config),
	})
	if err != nil {
		return nil, internalError("resolve project workflow assignees", err)
	}
	if err := resolverCtx.Err(); err != nil {
		return nil, internalError("resolve project workflow assignees", runtimeext.ErrAssigneeResolverBudgetExceeded)
	}
	if len(candidates) > binding.Descriptor.MaxCandidates {
		return nil, internalError("resolve project workflow assignees", runtimeext.ErrAssigneeResolverBudgetExceeded)
	}
	allowedRoles := stringSet(binding.Descriptor.CandidateRoleKeys)
	roleMembers := map[string]map[string]bool{}
	result := make([]ResolvedAssignee, 0, len(candidates))
	for _, candidate := range candidates {
		userID, roleKey := strings.TrimSpace(candidate.UserID), strings.TrimSpace(candidate.RoleKey)
		if userID == "" || roleKey != "" && !allowedRoles[roleKey] || !validAssigneeEvidenceFacts(candidate.Evidence) {
			return nil, internalError("resolve project workflow assignees", runtimeext.ErrAssigneeResolverResultInvalid)
		}
		if e.runtime.dependencies.Identity == nil {
			return nil, internalError("resolve project workflow assignees", runtimeext.ErrAssigneeResolverResultInvalid)
		}
		user, exists, lookupErr := e.runtime.dependencies.Identity.FindUser(resolverCtx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(userID)})
		if lookupErr != nil {
			return nil, internalError("resolve project workflow assignees", lookupErr)
		}
		if !exists || user.Status != identitysdk.UserStatusActive {
			return nil, internalError("resolve project workflow assignees", runtimeext.ErrAssigneeResolverResultInvalid)
		}
		if roleKey != "" {
			members, loaded := roleMembers[roleKey]
			if !loaded {
				userIDs, roleErr := e.usersForApprovalRole(resolverCtx, roleKey)
				if roleErr != nil {
					return nil, internalError("resolve project workflow assignees", roleErr)
				}
				members = stringSet(userIDs)
				roleMembers[roleKey] = members
			}
			if !members[userID] {
				return nil, internalError("resolve project workflow assignees", runtimeext.ErrAssigneeResolverResultInvalid)
			}
		}
		facts := make([]workflowmodel.AssigneeEvidenceFact, len(candidate.Evidence))
		for index, fact := range candidate.Evidence {
			facts[index] = workflowmodel.AssigneeEvidenceFact{Key: strings.TrimSpace(fact.Key), Value: strings.TrimSpace(fact.Value)}
		}
		sort.Slice(facts, func(i, j int) bool { return facts[i].Key < facts[j].Key })
		match := workflowmodel.AssigneeEvidenceMatch{ResolverType: "project", ResolverKey: binding.Descriptor.ResolverKey, ResolverIndex: resolverIndex, RoleKey: roleKey, Facts: facts}
		result = mergeResolvedAssignees(result, []ResolvedAssignee{{UserID: userID, RoleKey: roleKey, ResolverKey: binding.Descriptor.ResolverKey, Evidence: workflowmodel.AssigneeEvidence{Matches: []workflowmodel.AssigneeEvidenceMatch{match}}}})
	}
	return result, nil
}

func (c *projectAssigneeResolverCapabilities) GetRecord(ctx context.Context, capabilityKey, recordID string) (runtimeext.AssigneeRecord, bool, error) {
	capability, found := c.recordCapability(capabilityKey)
	if !found {
		return runtimeext.AssigneeRecord{}, false, runtimeext.ErrAssigneeResolverGrantDenied
	}
	return c.getRecord(ctx, capability.ObjectKey, recordID, capability.Fields)
}

func (c *projectAssigneeResolverCapabilities) ListRecords(ctx context.Context, request runtimeext.AssigneeRecordListRequest) ([]runtimeext.AssigneeRecord, error) {
	capability, found := c.recordCapability(request.CapabilityKey)
	if !found || request.Limit < 1 || request.Limit > capability.MaxRows || c.dependencies.RecordReader == nil || c.dependencies.ObjectMap == nil {
		return nil, runtimeext.ErrAssigneeResolverGrantDenied
	}
	if err := c.consumeRead(ctx); err != nil {
		return nil, err
	}
	allowedFilters := stringSet(capability.FilterFields)
	filters := map[string]any{}
	seenFilters := map[string]bool{}
	for _, filter := range request.Filters {
		field, operator := strings.TrimSpace(filter.Field), strings.TrimSpace(filter.Operator)
		if !allowedFilters[field] || len(filter.Values) == 0 || seenFilters[field] {
			return nil, runtimeext.ErrAssigneeResolverGrantDenied
		}
		seenFilters[field] = true
		switch operator {
		case "eq":
			if len(filter.Values) != 1 {
				return nil, runtimeext.ErrAssigneeResolverGrantDenied
			}
			filters[field] = filter.Values[0]
		case "in":
			values := make([]any, len(filter.Values))
			for index, value := range filter.Values {
				values[index] = value
			}
			filters[field+"__in"] = values
		default:
			return nil, runtimeext.ErrAssigneeResolverGrantDenied
		}
	}
	object, exists := c.dependencies.ObjectMap(ctx)[capability.ObjectKey]
	if !exists {
		return nil, runtimeext.ErrAssigneeResolverGrantDenied
	}
	page, err := c.dependencies.RecordReader.ListWorkflowRecords(ctx, c.workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: request.Limit, SkipTotal: true, Filters: filters, SelectFields: append([]string(nil), capability.Fields...)}, c.principal)
	if err != nil {
		return nil, err
	}
	if len(page.Items) > request.Limit {
		return nil, runtimeext.ErrAssigneeResolverBudgetExceeded
	}
	result := make([]runtimeext.AssigneeRecord, 0, len(page.Items))
	for _, record := range page.Items {
		result = append(result, projectAssigneeRecord(capability.ObjectKey, record, capability.Fields))
	}
	return result, nil
}

func (c *projectAssigneeResolverCapabilities) FollowRelation(ctx context.Context, request runtimeext.AssigneeRelationRequest) ([]runtimeext.AssigneeRecord, error) {
	capability, found := c.relationCapability(request.CapabilityKey)
	if !found || c.dependencies.ObjectMap == nil || c.dependencies.RecordReader == nil {
		return nil, runtimeext.ErrAssigneeResolverGrantDenied
	}
	source, found, err := c.getRecord(ctx, capability.SourceObjectKey, request.SourceRecordID, []string{capability.RelationFieldKey})
	if err != nil || !found {
		return nil, err
	}
	targetIDs := workflowAssigneeUserIDs(source.Fields[capability.RelationFieldKey])
	if len(targetIDs) > capability.MaxTargets {
		return nil, runtimeext.ErrAssigneeResolverBudgetExceeded
	}
	result := make([]runtimeext.AssigneeRecord, 0, len(targetIDs))
	for _, recordID := range targetIDs {
		record, targetFound, getErr := c.getRecord(ctx, capability.TargetObjectKey, recordID, capability.TargetFields)
		if getErr != nil {
			return nil, getErr
		}
		if targetFound {
			result = append(result, record)
		}
	}
	return result, nil
}

func (c *projectAssigneeResolverCapabilities) FindUser(ctx context.Context, userID string) (runtimeext.AssigneeIdentityUser, bool, error) {
	if !c.identityProjectionAllowed(runtimeext.AssigneeIdentityProjectionFindUser) || c.dependencies.Identity == nil {
		return runtimeext.AssigneeIdentityUser{}, false, runtimeext.ErrAssigneeResolverGrantDenied
	}
	if err := c.consumeRead(ctx); err != nil {
		return runtimeext.AssigneeIdentityUser{}, false, err
	}
	user, found, err := c.dependencies.Identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(strings.TrimSpace(userID))})
	if err != nil || !found {
		return runtimeext.AssigneeIdentityUser{}, found, err
	}
	return projectAssigneeIdentityUser(user), true, nil
}

func (c *projectAssigneeResolverCapabilities) UsersForRole(ctx context.Context, roleKey string) ([]runtimeext.AssigneeIdentityUser, error) {
	if !c.identityProjectionAllowed(runtimeext.AssigneeIdentityProjectionUsersForRole) || c.dependencies.Identity == nil {
		return nil, runtimeext.ErrAssigneeResolverGrantDenied
	}
	if err := c.consumeRead(ctx); err != nil {
		return nil, err
	}
	roles, err := c.dependencies.Identity.ListRoles(ctx, identitysdk.ProjectionQuery{})
	if err != nil {
		return nil, err
	}
	roleID := ""
	for _, role := range roles {
		if role.ID == roleKey || role.Key == roleKey {
			roleID = role.ID
			break
		}
	}
	if roleID == "" {
		return nil, runtimeext.ErrAssigneeResolverResultInvalid
	}
	users, err := c.dependencies.Identity.ListUsers(ctx, identitysdk.ProjectionQuery{})
	if err != nil {
		return nil, err
	}
	assignments, err := c.dependencies.Identity.ListUserRoleAssignments(ctx, identitysdk.UserRoleAssignmentQuery{})
	if err != nil {
		return nil, err
	}
	assigned := map[string]bool{}
	for _, assignment := range assignments {
		if assignment.RoleID == roleID {
			assigned[assignment.UserID] = true
		}
	}
	result := []runtimeext.AssigneeIdentityUser{}
	for _, user := range users {
		if assigned[user.ID] && user.Status == identitysdk.UserStatusActive {
			result = append(result, projectAssigneeIdentityUser(user))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UserID < result[j].UserID })
	return result, nil
}

func (c *projectAssigneeResolverCapabilities) getRecord(ctx context.Context, objectKey, recordID string, fields []string) (runtimeext.AssigneeRecord, bool, error) {
	if c.dependencies.RecordReader == nil || c.dependencies.ObjectMap == nil || strings.TrimSpace(recordID) == "" {
		return runtimeext.AssigneeRecord{}, false, runtimeext.ErrAssigneeResolverGrantDenied
	}
	if err := c.consumeRead(ctx); err != nil {
		return runtimeext.AssigneeRecord{}, false, err
	}
	object, exists := c.dependencies.ObjectMap(ctx)[strings.TrimSpace(objectKey)]
	if !exists {
		return runtimeext.AssigneeRecord{}, false, runtimeext.ErrAssigneeResolverGrantDenied
	}
	record, found, err := c.dependencies.RecordReader.GetWorkflowRecord(ctx, c.workspaceID, object, strings.TrimSpace(recordID), c.principal)
	if err != nil || !found {
		return runtimeext.AssigneeRecord{}, found, err
	}
	return projectAssigneeRecord(objectKey, record, fields), true, nil
}

func (c *projectAssigneeResolverCapabilities) consumeRead(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return runtimeext.ErrAssigneeResolverBudgetExceeded
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reads >= c.descriptor.MaxReadOperations {
		return runtimeext.ErrAssigneeResolverBudgetExceeded
	}
	c.reads++
	return nil
}

func (c *projectAssigneeResolverCapabilities) recordCapability(key string) (runtimeext.AssigneeResolverRecordCapability, bool) {
	for _, capability := range c.descriptor.RecordCapabilities {
		if capability.Key == strings.TrimSpace(key) {
			return capability, true
		}
	}
	return runtimeext.AssigneeResolverRecordCapability{}, false
}

func (c *projectAssigneeResolverCapabilities) relationCapability(key string) (runtimeext.AssigneeResolverRelationCapability, bool) {
	for _, capability := range c.descriptor.RelationCapabilities {
		if capability.Key == strings.TrimSpace(key) {
			return capability, true
		}
	}
	return runtimeext.AssigneeResolverRelationCapability{}, false
}

func (c *projectAssigneeResolverCapabilities) identityProjectionAllowed(expected string) bool {
	for _, projection := range c.descriptor.IdentityProjections {
		if projection == expected {
			return true
		}
	}
	return false
}

func projectAssigneeRecord(objectKey string, record recordmodel.Record, fields []string) runtimeext.AssigneeRecord {
	projection := make(map[string]any, len(fields))
	for _, field := range fields {
		if value, exists := record.Data[field]; exists {
			projection[field] = cloneAssigneeValue(value)
		}
	}
	return runtimeext.AssigneeRecord{ObjectKey: strings.TrimSpace(objectKey), RecordID: record.ID, Fields: projection}
}

func projectAssigneeIdentityUser(user identitysdk.User) runtimeext.AssigneeIdentityUser {
	return runtimeext.AssigneeIdentityUser{UserID: user.ID, DisplayName: user.Name, ManagerUserID: user.ManagerUserID, Status: string(user.Status)}
}

func validAssigneeEvidenceFacts(facts []runtimeext.AssigneeEvidenceFact) bool {
	if len(facts) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, fact := range facts {
		key, value := strings.TrimSpace(fact.Key), strings.TrimSpace(fact.Value)
		if key == "" || value == "" || len(key) > 128 || len(value) > 1024 || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = true
	}
	return result
}

func cloneAssigneeMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	result := map[string]any{}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return map[string]any{}
	}
	return result
}

func cloneAssigneeValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var result any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil
	}
	return result
}

var _ runtimeext.AssigneeResolverCapabilities = (*projectAssigneeResolverCapabilities)(nil)
