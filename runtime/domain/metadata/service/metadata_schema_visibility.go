package service

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func SnapshotForPrincipal(snapshot metadatamodel.ApplicationSchemaSnapshot, principal principalmodel.Principal) metadatamodel.ApplicationSchemaSnapshot {
	if !principal.Known {
		snapshot.Reports = nil
		snapshot.AgentServicePrincipals = nil
		snapshot.SchemaHash = SchemaSnapshotHash(snapshot)
		snapshot.SnapshotVersion = snapshot.SchemaHash
		return snapshot
	}
	if principal.Allows("workspace", "admin") {
		snapshot.SchemaHash = SchemaSnapshotHash(snapshot)
		snapshot.SnapshotVersion = snapshot.SchemaHash
		return snapshot
	}
	visibleObjects := make([]definitionmodel.ObjectSchema, 0, len(snapshot.Objects))
	visibleObjectKeys := map[string]bool{}
	for _, object := range snapshot.Objects {
		if !principalCanUseObject(principal, object.Key) {
			continue
		}
		fields := make([]definitionmodel.FieldSchema, 0, len(object.Fields))
		for _, field := range object.Fields {
			read, readHandled := metadataSDKFieldAllowed(principal, object.Key, field.Key, "read")
			write, writeHandled := metadataSDKFieldAllowed(principal, object.Key, field.Key, "update")
			if readHandled || writeHandled {
				if read || write {
					fields = append(fields, field)
				}
				continue
			}
			if principal.SystemScope.Valid() && (principal.Allows(object.Key, "read") || principal.Allows(object.Key, "update")) {
				fields = append(fields, field)
			}
		}
		object.Fields = fields
		visibleObjects = append(visibleObjects, object)
		visibleObjectKeys[object.Key] = true
	}
	visibleActions := make([]definitionmodel.ActionSchema, 0, len(snapshot.Actions))
	visibleActionKeys := map[string]bool{}
	for _, action := range snapshot.Actions {
		if visibleObjectKeys[action.ObjectKey] && actionAllowed(principal, action) {
			visibleActions = append(visibleActions, action)
			visibleActionKeys[action.Key] = true
		}
	}
	visibleGuardedWrites := make([]metadatamodel.MetadataGuardedWriteContract, 0, len(snapshot.GuardedWrites))
	for _, contract := range snapshot.GuardedWrites {
		if visibleObjectKeys[contract.ObjectKey] && visibleActionKeys[contract.ActionKey] {
			visibleGuardedWrites = append(visibleGuardedWrites, contract)
		}
	}
	visibleViews := make([]definitionmodel.ViewSchema, 0, len(snapshot.Views))
	for _, view := range snapshot.Views {
		if visibleObjectKeys[view.ObjectKey] {
			visibleViews = append(visibleViews, view)
		}
	}
	visibleReports := make([]reportmodel.ReportSchema, 0, len(snapshot.Reports))
	for _, report := range snapshot.Reports {
		if reportVisibleForPrincipal(report, principal, visibleObjectKeys) {
			visibleReports = append(visibleReports, report)
		}
	}
	visibleEntryPoints := make([]definitionmodel.EntryPointSchema, 0, len(snapshot.EntryPoints))
	for _, entrypoint := range snapshot.EntryPoints {
		if principal.HasAllPermissions(entrypoint.RequiredPermissions) {
			visibleEntryPoints = append(visibleEntryPoints, entrypoint)
		}
	}
	snapshot.Objects = visibleObjects
	snapshot.Views = visibleViews
	snapshot.Actions = visibleActions
	snapshot.GuardedWrites = visibleGuardedWrites
	snapshot.Reports = visibleReports
	snapshot.EntryPoints = visibleEntryPoints
	visibleProfileExtensions := make([]profilebindingmodel.Binding, 0, len(snapshot.IdentityProfileExtensions))
	for _, extension := range snapshot.IdentityProfileExtensions {
		if visibleObjectKeys[extension.ObjectKey] {
			visibleProfileExtensions = append(visibleProfileExtensions, extension)
		}
	}
	snapshot.IdentityProfileExtensions = visibleProfileExtensions
	snapshot.Skills, snapshot.Agents = visibleAgentRegistryForPrincipal(snapshot.Skills, snapshot.Agents, principal, visibleObjectKeys)
	snapshot.AgentTasks, snapshot.AgentEntrypoints = visibleAgentContractsForPrincipal(snapshot, principal, visibleObjectKeys, visibleActionKeys)
	snapshot.AgentServicePrincipals = nil
	snapshot.SchemaHash = SchemaSnapshotHash(snapshot)
	snapshot.SnapshotVersion = snapshot.SchemaHash
	return snapshot
}

func visibleAgentContractsForPrincipal(snapshot metadatamodel.ApplicationSchemaSnapshot, principal principalmodel.Principal, visibleObjects, visibleActions map[string]bool) ([]agentmodel.AgentTaskDefinition, []agentmodel.AgentEntrypointAssignment) {
	agents := map[string]bool{}
	for _, agent := range snapshot.Agents {
		agents[strings.TrimSpace(agent.Key)] = true
	}
	tasks := []agentmodel.AgentTaskDefinition{}
	taskKeys := map[string]bool{}
	for _, task := range snapshot.AgentTasks {
		if !task.Enabled {
			continue
		}
		if !agents[strings.TrimSpace(task.AgentKey)] {
			continue
		}
		declaredObjects := len(task.AllowedObjects)
		declaredActions := len(task.AllowedActions)
		task.AllowedObjects = visibleAgentStringIntersection(task.AllowedObjects, visibleObjects)
		task.AllowedActions = visibleAgentStringIntersection(task.AllowedActions, visibleActions)
		if declaredObjects > 0 {
			if len(task.AllowedObjects) == 0 {
				continue
			}
		}
		if declaredActions > 0 {
			if len(task.AllowedActions) == 0 {
				continue
			}
		}
		tasks = append(tasks, task)
		taskKeys[strings.TrimSpace(task.Key)] = true
	}
	workflows := map[string]bool{}
	for _, workflow := range snapshot.Workflows {
		if workflow.Enabled {
			workflows[strings.TrimSpace(workflow.Key)] = true
		}
	}
	entrypoints := []agentmodel.AgentEntrypointAssignment{}
	for _, entrypoint := range snapshot.AgentEntrypoints {
		if !entrypoint.Enabled {
			continue
		}
		if !agents[strings.TrimSpace(entrypoint.AgentKey)] {
			continue
		}
		if !principal.HasAllPermissions(entrypoint.RequiredPermissions) {
			continue
		}
		if surface := strings.TrimSpace(principal.SurfaceKey); surface != "" {
			if surface != strings.TrimSpace(entrypoint.Surface) {
				continue
			}
		}
		entrypoint.AllowedTaskKeys = visibleAgentStringIntersection(entrypoint.AllowedTaskKeys, taskKeys)
		entrypoint.AllowedWorkflowKeys = visibleAgentStringIntersection(entrypoint.AllowedWorkflowKeys, workflows)
		entrypoints = append(entrypoints, entrypoint)
	}
	return tasks, entrypoints
}

func visibleAgentStringIntersection(values []string, allowed map[string]bool) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !allowed[value] {
			continue
		}
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func agentStringContains(values []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func principalCanUseObject(principal principalmodel.Principal, objectKey string) bool {
	for _, action := range []string{"read", "create", "update", "delete", "import", "export"} {
		if allowed, handled := metadataSDKAllowsObjectAction(principal, objectKey, action); handled {
			if allowed {
				return true
			}
			continue
		}
		if principal.SystemScope.Valid() && principal.Allows(objectKey, action) {
			return true
		}
	}
	return false
}

func actionAllowed(principal principalmodel.Principal, action definitionmodel.ActionSchema) bool {
	if !principal.Known {
		return false
	}
	permission := strings.TrimSpace(action.RequiresPermission)
	if permission == "" {
		permission = strings.TrimSpace(action.Key)
	}
	objectKey, actionName := splitPermission(permission)
	if objectKey == "" {
		objectKey = action.ObjectKey
	}
	return principal.Allows(objectKey, actionName)
}

func visibleAgentRegistryForPrincipal(skills []agentmodel.SkillSchema, agents []agentmodel.AgentSchema, principal principalmodel.Principal, visibleObjects map[string]bool) ([]agentmodel.SkillSchema, []agentmodel.AgentSchema) {
	visibleSkills := make([]agentmodel.SkillSchema, 0, len(skills))
	skillByKey := map[string]agentmodel.SkillSchema{}
	for _, skill := range skills {
		allowedTools := visibleAgentToolsForPrincipal(skill.AllowedTools, principal, visibleObjects)
		if len(skill.AllowedTools) > 0 && len(allowedTools) == 0 {
			continue
		}
		skill.AllowedTools = allowedTools
		visibleSkills = append(visibleSkills, skill)
		skillByKey[skill.Key] = skill
	}
	visibleAgents := make([]agentmodel.AgentSchema, 0, len(agents))
	for _, agent := range agents {
		tools := visibleAgentToolsForPrincipal(agent.Tools, principal, visibleObjects)
		if len(agent.Tools) > 0 && len(tools) == 0 {
			continue
		}
		skillKeys := make([]string, 0, len(agent.SkillKeys))
		for _, key := range agent.SkillKeys {
			if _, ok := skillByKey[key]; ok {
				skillKeys = append(skillKeys, key)
			}
		}
		if len(agent.SkillKeys) > 0 && len(skillKeys) == 0 {
			continue
		}
		agent.Tools = tools
		agent.SkillKeys = skillKeys
		visibleAgents = append(visibleAgents, agent)
	}
	return visibleSkills, visibleAgents
}

func visibleAgentToolsForPrincipal(tools []string, principal principalmodel.Principal, visibleObjects map[string]bool) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool != "" && !seen[tool] && agentToolAllowedForPrincipal(tool, principal, visibleObjects) {
			seen[tool] = true
			out = append(out, tool)
		}
	}
	return out
}

func agentToolAllowedForPrincipal(tool string, principal principalmodel.Principal, visibleObjects map[string]bool) bool {
	switch strings.TrimSpace(tool) {
	case "listObjects", "listFields", "searchRecords", "getRecord", "list_objects", "list_fields", "search_records", "query_records", "get_record":
		return len(visibleObjects) > 0
	case "createRecord", "create_record":
		return principalCanUseAnyObjectAction(principal, visibleObjects, "create")
	case "updateRecord", "update_record":
		return principalCanUseAnyObjectAction(principal, visibleObjects, "update")
	default:
		return principal.HasPermission("integration.tool."+strings.TrimSpace(tool)) || principal.HasPermission("integration.tool.*")
	}
}

func principalCanUseAnyObjectAction(principal principalmodel.Principal, visibleObjects map[string]bool, action string) bool {
	for objectKey := range visibleObjects {
		if allowed, handled := metadataSDKAllowsObjectAction(principal, objectKey, action); handled {
			if allowed {
				return true
			}
			continue
		}
		if principal.SystemScope.Valid() && principal.Allows(objectKey, action) {
			return true
		}
	}
	return false
}

func metadataSDKAllowsObjectAction(principal principalmodel.Principal, objectKey, action string) (allowed, handled bool) {
	if principal.AccessBundle == nil {
		return false, false
	}
	filter, err := identityevaluator.CompileRecordFilter(
		*principal.AccessBundle,
		identitysdk.ResourceType(strings.TrimSpace(objectKey)),
		identitysdk.Action(strings.TrimSpace(action)),
		time.Now().UTC(),
	)
	return err == nil && len(filter.Allow) > 0, true
}

func metadataSDKFieldAllowed(principal principalmodel.Principal, objectKey, fieldKey, action string) (allowed, handled bool) {
	if principal.AccessBundle == nil {
		return false, false
	}
	decision, err := identityevaluator.EvaluateField(*principal.AccessBundle, identityevaluator.FieldRequest{
		Resource: identitysdk.ResourceType(strings.TrimSpace(objectKey)),
		Field:    strings.TrimSpace(fieldKey),
		Action:   identitysdk.Action(strings.TrimSpace(action)),
	}, nil)
	if err != nil {
		return false, true
	}
	return decision.Effect == identitysdk.FieldEffectAllow || decision.Effect == identitysdk.FieldEffectMask, true
}

func reportVisibleForPrincipal(report reportmodel.ReportSchema, principal principalmodel.Principal, visibleObjects map[string]bool) bool {
	if len(report.RequiredPermissions) > 0 {
		for _, permission := range report.RequiredPermissions {
			if !principal.HasPermission(permission) {
				return false
			}
		}
		return true
	}
	objectKeys := reportmodel.ReportDatasetObjectKeys(report.Dataset)
	if report.ObjectSQLV1 != nil {
		objectKeys = reportmodel.ReportObjectSQLObjectKeys(report.ObjectSQLV1)
	}
	for _, objectKey := range objectKeys {
		if visibleObjects[objectKey] {
			return true
		}
	}
	return len(objectKeys) == 0
}

func splitPermission(value string) (string, string) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 2 {
		return "", strings.TrimSpace(value)
	}
	return strings.TrimSpace(parts[len(parts)-2]), strings.TrimSpace(parts[len(parts)-1])
}
