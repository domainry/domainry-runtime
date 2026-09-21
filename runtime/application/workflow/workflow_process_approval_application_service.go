package workflow

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"fmt"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func (e *WorkflowProcessEngine) createApprovalTasks(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, nodeInstance workflowmodel.WorkflowNodeInstance, principal principalmodel.Principal) (int, error) {
	ctx = requestcontext.WithWorkspaceID(ctx, process.WorkspaceID)
	assignees, err := e.resolveApprovalAssignees(ctx, process, node, principal)
	if err != nil {
		return 0, err
	}
	if len(assignees) == 0 {
		if valueOrDefault(strings.TrimSpace(workflowpolicy.WorkflowApprovalNodeContract(node).EmptyAssigneePolicy), "fail") == "skip" {
			return 0, nil
		}
		return 0, badRequest("backend.workflow.approval_assignee_not_found", "node", node.ID)
	}
	contract := workflowpolicy.WorkflowApprovalNodeContract(node)
	mode := valueOrDefault(strings.TrimSpace(contract.Mode), "any")
	nowTime := time.Now().UTC()
	now := nowTime.Format(time.RFC3339Nano)
	for index, assignee := range assignees {
		status := "open"
		if mode == "sequential" && index > 0 {
			status = "pending"
		}
		title := strings.TrimSpace(contract.Title)
		if title == "" || title == "<nil>" {
			title = node.Name
		}
		task := workflowmodel.WorkflowTask{
			ID:                    workflowProcessID(ctx, "task"),
			ProcessID:             process.ID,
			NodeInstanceID:        nodeInstance.ID,
			NodeID:                node.ID,
			Title:                 title,
			AssigneeUserID:        assignee.UserID,
			AssigneeRoleKey:       assignee.RoleKey,
			AssigneeResolverKey:   assignee.ResolverKey,
			AssigneeEvidence:      assignee.Evidence,
			ResolverSnapshot:      workflowpolicy.WorkflowOrderedApprovalResolvers(contract.Resolvers),
			CandidateSource:       workflowpolicy.WorkflowCandidateSource(workflowpolicy.WorkflowOrderedApprovalResolvers(contract.Resolvers)),
			NodeDefinitionVersion: workflowpolicy.WorkflowGraphContractVersion(process.DefinitionSnapshot),
			Sequence:              index + 1,
			Status:                status,
			DueAt:                 workflowpolicy.WorkflowApprovalDueAt(contract, node, nowTime),
			CreatedAt:             now,
			UpdatedAt:             now,
		}
		assigneeLocale := ""
		if user, ok, getErr := e.runtime.dependencies.Identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(assignee.UserID)}); getErr == nil && ok {
			task.AssigneeName = user.Name
			assigneeLocale = user.Locale
		}
		if err := e.insertApprovalTaskWithNotification(ctx, process, task, assigneeLocale); err != nil {
			return 0, internalError("insert workflow task", err)
		}
		if err := e.scheduleApprovalDeadlineTimers(ctx, process, task, contract, nowTime); err != nil {
			return 0, err
		}
		e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, task.ID, "task_created", "system", task.Title, map[string]any{"assignee_user_id": assignee.UserID, "assignee_role_key": assignee.RoleKey, "resolver_key": assignee.ResolverKey, "assignee_evidence": assignee.Evidence, "mode": mode, "sequence": task.Sequence})
	}
	return len(assignees), nil
}

func (e *WorkflowProcessEngine) scheduleApprovalDeadlineTimers(ctx context.Context, process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask, contract definitionmodel.WorkflowApprovalNodeContract, createdAt time.Time) error {
	if strings.TrimSpace(task.DueAt) == "" || strings.TrimSpace(contract.ReminderActionKey) == "" && contract.EscalationSeconds <= 0 {
		return nil
	}
	if e.runtime.dependencies.ApprovalDeadlineTimers == nil {
		return internalError("schedule workflow approval deadline", fmt.Errorf("workflow approval deadline timer service is required"))
	}
	dueAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(task.DueAt))
	if err != nil {
		return badRequest("backend.workflow.approval_due_at_invalid", "task", task.ID)
	}
	schedule := func(phase string, at time.Time) error {
		_, scheduleErr := e.runtime.dependencies.ApprovalDeadlineTimers.ScheduleWorkflowApprovalDeadlineTimer(ctx, WorkflowApprovalDeadlineTimerRequest{
			WorkspaceID: process.WorkspaceID, ProcessID: process.ID, NodeID: task.NodeID, TaskID: task.ID,
			Phase: phase, DueAt: at, CreatedAt: createdAt,
		})
		if scheduleErr != nil {
			return internalError("schedule workflow approval "+phase+" timer", scheduleErr)
		}
		return nil
	}
	if strings.TrimSpace(contract.ReminderActionKey) != "" {
		if err := schedule("reminder", dueAt); err != nil {
			return err
		}
	}
	if contract.EscalationSeconds > 0 {
		if err := schedule("escalation", dueAt.Add(time.Duration(contract.EscalationSeconds)*time.Second)); err != nil {
			return err
		}
	}
	return nil
}

// ResolvedAssignee preserves the primary resolver identity and every matching
// evidence item for one candidate. The first ordered resolver is authoritative
// for RoleKey and ResolverKey; later duplicate hits only append evidence.
type ResolvedAssignee struct {
	UserID      string                         `json:"user_id"`
	RoleKey     string                         `json:"role_key,omitempty"`
	ResolverKey string                         `json:"resolver_key"`
	Evidence    workflowmodel.AssigneeEvidence `json:"evidence"`
}

func (e *WorkflowProcessEngine) resolveApprovalAssignees(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, principal principalmodel.Principal) ([]ResolvedAssignee, error) {
	ctx = requestcontext.WithWorkspaceID(ctx, process.WorkspaceID)
	if e.runtime.dependencies.Identity == nil {
		return nil, badRequest("backend.workflow.approval_identity_unavailable")
	}
	contract := workflowpolicy.WorkflowApprovalNodeContract(node)
	resolved := []ResolvedAssignee{}
	for index, resolver := range workflowpolicy.WorkflowOrderedApprovalResolvers(contract.Resolvers) {
		candidates, err := e.resolveApprovalAssigneeStrategyAt(ctx, process, node.ID, resolver, index, principal)
		if err != nil {
			return nil, err
		}
		resolved = mergeResolvedAssignees(resolved, candidates)
		if strings.TrimSpace(contract.ResolverMode) == "first_match" && len(candidates) > 0 {
			break
		}
	}
	if len(resolved) == 0 && valueOrDefault(strings.TrimSpace(contract.EmptyAssigneePolicy), "fail") == "admin" {
		admins, err := e.usersForApprovalRole(ctx, "admin")
		if err != nil {
			return nil, err
		}
		resolved = resolvedAssigneesForUsers(admins, "admin", "role", 0, workflowmodel.AssigneeEvidenceMatch{ResolverType: "role", ResolverKey: "role", ResolverIndex: 0, RoleKey: "admin"})
	}
	if err := workflowpolicy.WorkflowValidateApprovalAssigneeCount(node, len(resolved)); err != nil {
		return nil, err
	}
	return resolved, nil
}

func (e *WorkflowProcessEngine) resolveApprovalAssigneeStrategy(ctx context.Context, process workflowmodel.WorkflowProcessInstance, resolver definitionmodel.WorkflowAssigneeResolver, principal principalmodel.Principal) ([]ResolvedAssignee, error) {
	return e.resolveApprovalAssigneeStrategyAt(ctx, process, "", resolver, 0, principal)
}

func (e *WorkflowProcessEngine) resolveApprovalAssigneeStrategyAt(ctx context.Context, process workflowmodel.WorkflowProcessInstance, nodeID string, resolver definitionmodel.WorkflowAssigneeResolver, resolverIndex int, principal principalmodel.Principal) ([]ResolvedAssignee, error) {
	resolverType := strings.TrimSpace(resolver.Type)
	match := workflowmodel.AssigneeEvidenceMatch{ResolverType: resolverType, ResolverKey: resolverType, ResolverIndex: resolverIndex}
	switch resolverType {
	case "users":
		return resolvedAssigneesForUsers(uniqueSortedStrings(resolver.UserIDs), "", resolverType, resolverIndex, match), nil
	case "manager", "manager_of", "initiator_manager":
		userID := strings.TrimSpace(process.InitiatorID)
		if resolver.Type != "initiator_manager" {
			userID = workflowpolicy.WorkflowApprovalSubjectUserID(process.Variables, strings.TrimSpace(resolver.UserField))
		}
		if userID == "" {
			return nil, nil
		}
		user, userExists, err := e.runtime.dependencies.Identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(userID)})
		if err != nil {
			return nil, err
		}
		if !userExists {
			return nil, nil
		}
		managerID := strings.TrimSpace(user.ManagerUserID)
		if managerID == "" {
			return nil, nil
		}
		manager, managerExists, managerErr := e.runtime.dependencies.Identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(managerID)})
		if managerErr != nil {
			return nil, managerErr
		}
		if !managerExists || manager.Status != identitysdk.UserStatusActive {
			return nil, nil
		}
		match.SubjectUserID = userID
		match.FieldKey = strings.TrimSpace(resolver.UserField)
		return resolvedAssigneesForUsers([]string{managerID}, "", resolverType, resolverIndex, match), nil
	case "variable_user":
		field := strings.TrimSpace(resolver.Field)
		userID := workflowpolicy.WorkflowPayloadString(process.Variables, field)
		if userID == "" {
			return nil, nil
		}
		match.VariableKey = field
		return resolvedAssigneesForUsers([]string{userID}, "", resolverType, resolverIndex, match), nil
	case "record_user_field":
		field := strings.TrimSpace(resolver.Field)
		users, err := e.resolveRecordUserField(ctx, process, field, principal)
		if err != nil {
			return nil, err
		}
		match.ObjectKey, match.RecordID, match.FieldKey = strings.TrimSpace(process.ObjectKey), strings.TrimSpace(process.RecordID), field
		return resolvedAssigneesForUsers(users, "", resolverType, resolverIndex, match), nil
	case "relation_user":
		return e.resolveRelationUsers(ctx, process, resolver, resolverIndex, principal)
	case "relation_role":
		return e.resolveRelationRoles(ctx, process, resolver, resolverIndex, principal)
	case "manager_chain":
		return e.resolveManagerChain(ctx, process, resolver, resolverIndex, principal)
	case "role":
		roleKey := strings.TrimSpace(resolver.RoleKey)
		users, err := e.usersForApprovalRole(ctx, roleKey)
		match.RoleKey = roleKey
		return resolvedAssigneesForUsers(users, roleKey, resolverType, resolverIndex, match), err
	case "project":
		return e.resolveProjectAssignees(ctx, process, nodeID, resolver, resolverIndex, principal)
	default:
		return nil, badRequest("backend.workflow.approval_resolver_invalid", "resolver", resolver.Type)
	}
}

const workflowAssigneeRelationTargetLimit = 100

type workflowAssigneeRelatedRecord struct {
	ObjectKey string
	RecordID  string
	Data      map[string]any
}

func (e *WorkflowProcessEngine) resolveRelationUsers(ctx context.Context, process workflowmodel.WorkflowProcessInstance, resolver definitionmodel.WorkflowAssigneeResolver, resolverIndex int, principal principalmodel.Principal) ([]ResolvedAssignee, error) {
	path := normalizedWorkflowRelationPath(resolver.RelationPath)
	if len(path) == 0 {
		return nil, badRequest("backend.workflow.approval_resolver_invalid", "resolver", resolver.Type)
	}
	terminalField := path[len(path)-1]
	records, err := e.followWorkflowAssigneeRelations(ctx, process, path[:len(path)-1], principal)
	if err != nil {
		return nil, err
	}
	result := []ResolvedAssignee{}
	for _, record := range records {
		object, exists := e.runtime.dependencies.ObjectMap(ctx)[record.ObjectKey]
		field, found := workflowAssigneeObjectField(object, terminalField)
		if !exists || !found || !workflowResolverIdentityField(field) {
			return nil, badRequest("backend.workflow.resolver_field_type_invalid", "field", record.ObjectKey+"."+terminalField)
		}
		match := workflowmodel.AssigneeEvidenceMatch{
			ResolverType: "relation_user", ResolverKey: "relation_user", ResolverIndex: resolverIndex,
			ObjectKey: record.ObjectKey, RecordID: record.RecordID, FieldKey: terminalField,
			Facts: []workflowmodel.AssigneeEvidenceFact{{Key: "relation_path", Value: strings.Join(path, ".")}},
		}
		result = mergeResolvedAssignees(result, resolvedAssigneesForUsers(workflowAssigneeUserIDs(record.Data[terminalField]), "", "relation_user", resolverIndex, match))
	}
	return result, nil
}

func (e *WorkflowProcessEngine) resolveRelationRoles(ctx context.Context, process workflowmodel.WorkflowProcessInstance, resolver definitionmodel.WorkflowAssigneeResolver, resolverIndex int, principal principalmodel.Principal) ([]ResolvedAssignee, error) {
	path := normalizedWorkflowRelationPath(resolver.RelationPath)
	roleField := strings.TrimSpace(resolver.RoleField)
	if len(path) == 0 || roleField == "" {
		return nil, badRequest("backend.workflow.approval_resolver_invalid", "resolver", resolver.Type)
	}
	records, err := e.followWorkflowAssigneeRelations(ctx, process, path, principal)
	if err != nil {
		return nil, err
	}
	result := []ResolvedAssignee{}
	for _, record := range records {
		object, exists := e.runtime.dependencies.ObjectMap(ctx)[record.ObjectKey]
		field, found := workflowAssigneeObjectField(object, roleField)
		if !exists || !found || !workflowResolverRoleField(field) {
			return nil, badRequest("backend.workflow.resolver_field_type_invalid", "field", record.ObjectKey+"."+roleField)
		}
		for _, roleKey := range workflowAssigneeUserIDs(record.Data[roleField]) {
			users, roleErr := e.usersForApprovalRole(ctx, roleKey)
			if roleErr != nil {
				return nil, roleErr
			}
			match := workflowmodel.AssigneeEvidenceMatch{
				ResolverType: "relation_role", ResolverKey: "relation_role", ResolverIndex: resolverIndex, RoleKey: roleKey,
				ObjectKey: record.ObjectKey, RecordID: record.RecordID, FieldKey: roleField,
				Facts: []workflowmodel.AssigneeEvidenceFact{{Key: "relation_path", Value: strings.Join(path, ".")}},
			}
			result = mergeResolvedAssignees(result, resolvedAssigneesForUsers(users, roleKey, "relation_role", resolverIndex, match))
		}
	}
	return result, nil
}

func (e *WorkflowProcessEngine) resolveManagerChain(ctx context.Context, process workflowmodel.WorkflowProcessInstance, resolver definitionmodel.WorkflowAssigneeResolver, resolverIndex int, principal principalmodel.Principal) ([]ResolvedAssignee, error) {
	var sourceUsers []string
	match := workflowmodel.AssigneeEvidenceMatch{ResolverType: "manager_chain", ResolverKey: "manager_chain", ResolverIndex: resolverIndex}
	switch strings.TrimSpace(resolver.Source) {
	case "initiator":
		sourceUsers = workflowAssigneeUserIDs(process.InitiatorID)
	case "variable":
		match.VariableKey = strings.TrimSpace(resolver.Field)
		sourceUsers = workflowAssigneeUserIDs(process.Variables[match.VariableKey])
	case "record":
		match.ObjectKey, match.RecordID, match.FieldKey = strings.TrimSpace(process.ObjectKey), strings.TrimSpace(process.RecordID), strings.TrimSpace(resolver.Field)
		users, err := e.resolveRecordUserField(ctx, process, match.FieldKey, principal)
		if err != nil {
			return nil, err
		}
		sourceUsers = users
	default:
		return nil, badRequest("backend.workflow.approval_resolver_invalid", "resolver", resolver.Type)
	}
	if resolver.MaxDepth < 1 || resolver.MaxDepth > 20 {
		return nil, badRequest("backend.workflow.approval_resolver_invalid", "resolver", resolver.Type)
	}
	result := []ResolvedAssignee{}
	for _, sourceUserID := range sourceUsers {
		currentID := sourceUserID
		visited := map[string]bool{currentID: true}
		for depth := 1; depth <= resolver.MaxDepth; depth++ {
			current, exists, err := e.runtime.dependencies.Identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(currentID)})
			if err != nil {
				return nil, err
			}
			if !exists || strings.TrimSpace(current.ManagerUserID) == "" {
				break
			}
			managerID := strings.TrimSpace(current.ManagerUserID)
			if visited[managerID] {
				return nil, badRequest("backend.workflow.resolver_manager_cycle", "user", managerID)
			}
			visited[managerID] = true
			manager, managerExists, managerErr := e.runtime.dependencies.Identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(managerID)})
			if managerErr != nil {
				return nil, managerErr
			}
			if !managerExists || manager.Status != identitysdk.UserStatusActive {
				break
			}
			candidateMatch := match
			candidateMatch.SubjectUserID = sourceUserID
			candidateMatch.Facts = []workflowmodel.AssigneeEvidenceFact{{Key: "depth", Value: fmt.Sprint(depth)}, {Key: "manager_of", Value: currentID}}
			result = mergeResolvedAssignees(result, resolvedAssigneesForUsers([]string{managerID}, "", "manager_chain", resolverIndex, candidateMatch))
			currentID = managerID
		}
	}
	return result, nil
}

func (e *WorkflowProcessEngine) followWorkflowAssigneeRelations(ctx context.Context, process workflowmodel.WorkflowProcessInstance, path []string, principal principalmodel.Principal) ([]workflowAssigneeRelatedRecord, error) {
	if e.runtime.dependencies.RecordReader == nil || e.runtime.dependencies.ObjectMap == nil {
		return nil, internalError("resolve workflow assignee relation", fmt.Errorf("workflow record reader is required"))
	}
	root, err := e.workflowAssigneeRecord(ctx, process.WorkspaceID, strings.TrimSpace(process.ObjectKey), strings.TrimSpace(process.RecordID), principal)
	if err != nil {
		return nil, err
	}
	records := []workflowAssigneeRelatedRecord{root}
	for _, fieldKey := range path {
		next := []workflowAssigneeRelatedRecord{}
		for _, record := range records {
			object := e.runtime.dependencies.ObjectMap(ctx)[record.ObjectKey]
			field, found := workflowAssigneeObjectField(object, fieldKey)
			targetObjectKey := workflowAssigneeRelationTarget(field)
			if !found || strings.TrimSpace(field.Type) != "relation" || targetObjectKey == "" || targetObjectKey == "identity_user" || targetObjectKey == "identity_role" {
				return nil, badRequest("backend.workflow.resolver_field_type_invalid", "field", record.ObjectKey+"."+fieldKey)
			}
			for _, recordID := range workflowAssigneeUserIDs(record.Data[fieldKey]) {
				target, readErr := e.workflowAssigneeRecord(ctx, process.WorkspaceID, targetObjectKey, recordID, principal)
				if readErr != nil {
					return nil, readErr
				}
				next = append(next, target)
				if len(next) > workflowAssigneeRelationTargetLimit {
					return nil, badRequest("backend.workflow.resolver_relation_limit_exceeded", "limit", fmt.Sprint(workflowAssigneeRelationTargetLimit))
				}
			}
		}
		records = uniqueWorkflowAssigneeRecords(next)
		if len(records) == 0 {
			break
		}
	}
	return records, nil
}

func (e *WorkflowProcessEngine) workflowAssigneeRecord(ctx context.Context, workspaceID, objectKey, recordID string, principal principalmodel.Principal) (workflowAssigneeRelatedRecord, error) {
	object, exists := e.runtime.dependencies.ObjectMap(ctx)[objectKey]
	if !exists || objectKey == "" || recordID == "" {
		return workflowAssigneeRelatedRecord{}, badRequest("backend.workflow.resolver_record_not_found", "object", objectKey, "record", recordID)
	}
	record, found, err := e.runtime.dependencies.RecordReader.GetWorkflowRecord(ctx, workspaceID, object, recordID, principal)
	if err != nil {
		return workflowAssigneeRelatedRecord{}, err
	}
	if !found {
		return workflowAssigneeRelatedRecord{}, badRequest("backend.workflow.resolver_record_not_found", "object", objectKey, "record", recordID)
	}
	return workflowAssigneeRelatedRecord{ObjectKey: objectKey, RecordID: record.ID, Data: record.Data}, nil
}

func normalizedWorkflowRelationPath(path []string) []string {
	result := make([]string, 0, len(path))
	for _, field := range path {
		if field = strings.TrimSpace(field); field != "" {
			result = append(result, field)
		}
	}
	return result
}

func uniqueWorkflowAssigneeRecords(records []workflowAssigneeRelatedRecord) []workflowAssigneeRelatedRecord {
	seen := map[string]bool{}
	result := make([]workflowAssigneeRelatedRecord, 0, len(records))
	for _, record := range records {
		key := record.ObjectKey + "\x00" + record.RecordID
		if !seen[key] {
			seen[key] = true
			result = append(result, record)
		}
	}
	return result
}

func workflowAssigneeObjectField(object definitionmodel.ObjectSchema, fieldKey string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if field.Key == strings.TrimSpace(fieldKey) && field.DisabledAt == "" {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func workflowAssigneeRelationTarget(field definitionmodel.FieldSchema) string {
	for _, target := range []string{strings.TrimSpace(fmt.Sprint(field.Config["object_key"])), strings.TrimSpace(fmt.Sprint(field.Config["target"])), strings.TrimSpace(field.Validation.Target)} {
		if target != "" && target != "<nil>" {
			return target
		}
	}
	return ""
}

func workflowResolverRoleField(field definitionmodel.FieldSchema) bool {
	switch strings.TrimSpace(field.Type) {
	case "text", "select", "multi_select", "role", "identity_role":
		return true
	case "relation":
		return workflowAssigneeRelationTarget(field) == "identity_role"
	default:
		return false
	}
}

func (e *WorkflowProcessEngine) resolveRecordUserField(ctx context.Context, process workflowmodel.WorkflowProcessInstance, field string, principal principalmodel.Principal) ([]string, error) {
	if e.runtime.dependencies.RecordReader == nil || e.runtime.dependencies.ObjectMap == nil {
		return nil, internalError("resolve workflow record user field", fmt.Errorf("workflow record reader is required"))
	}
	objectKey, recordID := strings.TrimSpace(process.ObjectKey), strings.TrimSpace(process.RecordID)
	object, exists := e.runtime.dependencies.ObjectMap(ctx)[objectKey]
	if objectKey == "" || recordID == "" || !exists {
		return nil, badRequest("backend.workflow.resolver_record_not_found", "object", objectKey, "record", recordID)
	}
	record, found, err := e.runtime.dependencies.RecordReader.GetWorkflowRecord(ctx, process.WorkspaceID, object, recordID, principal)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, badRequest("backend.workflow.resolver_record_not_found", "object", objectKey, "record", recordID)
	}
	return workflowAssigneeUserIDs(record.Data[field]), nil
}

func workflowAssigneeUserIDs(value any) []string {
	switch typed := value.(type) {
	case string:
		return uniqueSortedStrings([]string{typed})
	case []string:
		return uniqueSortedStrings(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, current := range typed {
			if value, ok := current.(string); ok {
				values = append(values, value)
			}
		}
		return uniqueSortedStrings(values)
	default:
		return nil
	}
}

func resolvedAssigneesForUsers(userIDs []string, roleKey, resolverKey string, resolverIndex int, match workflowmodel.AssigneeEvidenceMatch) []ResolvedAssignee {
	result := make([]ResolvedAssignee, 0, len(userIDs))
	for _, userID := range uniqueSortedStrings(userIDs) {
		current := match
		current.ResolverIndex = resolverIndex
		result = append(result, ResolvedAssignee{UserID: userID, RoleKey: roleKey, ResolverKey: resolverKey, Evidence: workflowmodel.AssigneeEvidence{Matches: []workflowmodel.AssigneeEvidenceMatch{current}}})
	}
	return result
}

func mergeResolvedAssignees(existing, candidates []ResolvedAssignee) []ResolvedAssignee {
	positions := make(map[string]int, len(existing)+len(candidates))
	for index := range existing {
		positions[existing[index].UserID] = index
	}
	for _, candidate := range candidates {
		candidate.UserID = strings.TrimSpace(candidate.UserID)
		if candidate.UserID == "" {
			continue
		}
		if index, exists := positions[candidate.UserID]; exists {
			existing[index].Evidence.Matches = append(existing[index].Evidence.Matches, candidate.Evidence.Matches...)
			continue
		}
		positions[candidate.UserID] = len(existing)
		existing = append(existing, candidate)
	}
	return existing
}

func (e *WorkflowProcessEngine) usersForApprovalRole(ctx context.Context, roleKey string) ([]string, error) {
	roles, err := e.runtime.dependencies.Identity.ListRoles(ctx, identitysdk.ProjectionQuery{})
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
		return nil, badRequest("backend.workflow.approval_role_not_found", "role", roleKey)
	}
	users, err := e.runtime.dependencies.Identity.ListUsers(ctx, identitysdk.ProjectionQuery{})
	if err != nil {
		return nil, err
	}
	assignments, err := e.runtime.dependencies.Identity.ListUserRoleAssignments(ctx, identitysdk.UserRoleAssignmentQuery{})
	if err != nil {
		return nil, err
	}
	usersWithRole := make(map[string]struct{})
	for _, assignment := range assignments {
		if assignment.RoleID == roleID {
			usersWithRole[assignment.UserID] = struct{}{}
		}
	}
	out := []string{}
	for _, user := range users {
		if user.Status != identitysdk.UserStatusActive {
			continue
		}
		if _, ok := usersWithRole[user.ID]; ok {
			out = append(out, user.ID)
		}
	}
	return uniqueSortedStrings(out), nil
}

func (e *WorkflowProcessEngine) aggregateApproval(ctx context.Context, process workflowmodel.WorkflowProcessInstance, nodeID string, principal principalmodel.Principal) (string, bool, error) {
	tasks, err := e.runtime.dependencies.Processes.ListTasks(ctx, process.WorkspaceID, process.ID, "", "", 500)
	if err != nil {
		return "", false, internalError("list approval tasks", err)
	}
	nodeTasks := []workflowmodel.WorkflowTask{}
	for _, task := range tasks {
		if task.NodeID == nodeID {
			nodeTasks = append(nodeTasks, task)
		}
	}
	if len(nodeTasks) == 0 {
		return "", false, badRequest("backend.workflow.approval_tasks_missing")
	}
	sort.Slice(nodeTasks, func(i, j int) bool { return nodeTasks[i].Sequence < nodeTasks[j].Sequence })
	for _, task := range nodeTasks {
		if task.Status == "returned" {
			e.cancelUnfinishedApprovalTasks(ctx, nodeTasks, task.ID, principal.UserID)
			return "returned", true, nil
		}
		if task.Status == "rejected" {
			e.cancelUnfinishedApprovalTasks(ctx, nodeTasks, task.ID, principal.UserID)
			return "rejected", true, nil
		}
	}
	node, _ := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, nodeID)
	mode := valueOrDefault(strings.TrimSpace(workflowpolicy.WorkflowApprovalNodeContract(node).Mode), "any")
	approved := 0
	for _, task := range nodeTasks {
		if task.Status == "approved" {
			approved++
		}
	}
	switch mode {
	case "quorum":
		if required := workflowpolicy.WorkflowApprovalNodeContract(node).RequiredApprovals; required > 0 && approved >= required {
			e.cancelUnfinishedApprovalTasks(ctx, nodeTasks, "", principal.UserID)
			return "approved", true, nil
		}
	case "any":
		if approved > 0 {
			e.cancelUnfinishedApprovalTasks(ctx, nodeTasks, "", principal.UserID)
			return "approved", true, nil
		}
	case "all":
		return "approved", approved == len(nodeTasks), nil
	case "sequential":
		if approved == len(nodeTasks) {
			return "approved", true, nil
		}
		for _, task := range nodeTasks {
			if task.Status == "pending" {
				task.Status = "open"
				task.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
				if err := e.openSequentialApprovalTask(ctx, process, task); err != nil {
					return "", false, internalError("open sequential approval task", err)
				}
				e.appendEvent(ctx, process.WorkspaceID, process.ID, nodeID, task.ID, "task_opened", "system", task.Title, nil)
				break
			}
		}
	}
	return "", false, nil
}

func (e *WorkflowProcessEngine) completeApprovalNode(ctx context.Context, workspaceID, processID, nodeID, outcome, actorID string) error {
	nodes, err := e.runtime.dependencies.Processes.ListNodes(ctx, workspaceID, processID)
	if err != nil {
		return internalError("list workflow nodes", err)
	}
	for _, node := range nodes {
		if node.NodeID != nodeID || node.Status != "waiting" {
			continue
		}
		node.Status = outcome
		node.Output = map[string]any{"decision": outcome}
		node.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := e.runtime.dependencies.Processes.UpdateNode(ctx, workspaceID, node); err != nil {
			return internalError("complete approval node", err)
		}
		e.appendEvent(ctx, workspaceID, processID, nodeID, "", "approval_"+outcome, actorID, "workflow.event.approval."+outcome, nil)
		return nil
	}
	return notFound("backend.workflow.approval_node_instance_not_found")
}

func runCommittedWorkflowContinuation(ctx context.Context, records *WorkflowProcessRuntime, execution workflowmodel.WorkflowExecution, principal principalmodel.Principal, processSnapshot workflowmodel.WorkflowProcessInstance, nodeSnapshots []workflowmodel.WorkflowNodeInstance, taskSnapshots []workflowmodel.WorkflowTask, decidedTask workflowmodel.WorkflowTask) (workflowmodel.WorkflowProcessInstance, bool, error) {
	claimed := execution
	claimed.Status, claimed.UpdatedAt = "running", time.Now().UTC().Format(time.RFC3339Nano)
	updated, err := records.dependencies.Workers.UpdateExecutionWhere(ctx, execution.WorkspaceID, claimed, map[string]any{"status": "pending", "updated_at": execution.UpdatedAt})
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, false, internalError("claim workflow continuation", err)
	}
	if !updated {
		return workflowmodel.WorkflowProcessInstance{}, false, nil
	}
	process, found, err := records.dependencies.Workers.GetProcess(ctx, execution.WorkspaceID, execution.ProcessID)
	if err != nil || !found {
		return workflowmodel.WorkflowProcessInstance{}, true, internalError("load workflow continuation process", err)
	}
	nodeIDs := workflowNodeIDsFromAny(execution.Result["resume_node_ids"])
	if len(nodeIDs) == 0 {
		nodeIDs = append([]string(nil), process.CurrentNodeIDs...)
	}
	continued, runErr := records.processEngine.runWithContext(ctx, process, nodeIDs, nil, principal)
	claimed.Attempt++
	claimed.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	claimed.Result = workflowpolicy.WorkflowCloneMap(claimed.Result)
	if runErr != nil {
		workflow, _ := records.dependencies.WorkflowRegistry.Get(execution.WorkflowKey)
		workflowpolicy.WorkflowMarkFailed(&claimed, workflow, runErr, time.Now().UTC())
		recoveredExecution := execution
		recoveredExecution.Status, recoveredExecution.Message, recoveredExecution.LastError = "waiting", "workflow.message.approvalWaiting", ""
		recoveredExecution.UpdatedAt, recoveredExecution.NextRunAt = time.Now().UTC().Format(time.RFC3339Nano), ""
		recoveredExecution.Result = workflowpolicy.WorkflowCloneMap(recoveredExecution.Result)
		recoveredExecution.Result["process_status"], recoveredExecution.Result["current_node_ids"] = processSnapshot.Status, processSnapshot.CurrentNodeIDs
		delete(recoveredExecution.Result, "resume_node_ids")
		recovered, recoveryErr := records.processEngine.recoverWorkflowDecision(ctx, processSnapshot, nodeSnapshots, taskSnapshots, decidedTask, runErr, principal.UserID, &recoveredExecution)
		return recovered, true, recoveryErr
	}
	claimed.Status, claimed.Message, claimed.LastError, claimed.NextRunAt = continued.Status, "workflow.message.continuationCompleted", "", ""
	claimed.NodeID = ""
	if len(continued.CurrentNodeIDs) > 0 {
		claimed.NodeID = continued.CurrentNodeIDs[0]
	}
	claimed.Result["process_status"], claimed.Result["current_node_ids"] = continued.Status, continued.CurrentNodeIDs
	delete(claimed.Result, "resume_node_ids")
	if err := records.dependencies.Workers.UpdateExecution(ctx, execution.WorkspaceID, claimed); err != nil {
		return continued, true, internalError("complete workflow continuation", err)
	}
	return continued, true, nil
}
