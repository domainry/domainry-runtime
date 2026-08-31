package validation

import (
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	invocationcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/invocation"
)

func (state *validationState) validateAgents() {
	skills := map[string]agentsdk.SkillSchema{}
	for index, skill := range state.manifest.Skills {
		path := fmt.Sprintf("skills[%d]", index)
		key := strings.TrimSpace(skill.Key)
		if !storageValuePattern.MatchString(key) {
			state.add(path+".key", "must be a stable non-empty key")
		} else if _, exists := skills[key]; exists {
			state.add(path+".key", "duplicate skill %q", key)
		}
		if strings.TrimSpace(skill.Name) == "" {
			state.add(path+".name", "is required")
		}
		validateAgentStringSet(state, path+".allowed_tools", skill.AllowedTools)
		validateAgentTools(state, path+".allowed_tools", skill.AllowedTools)
		validateAgentStringSet(state, path+".allowed_objects", skill.AllowedObjects)
		for _, toolKey := range skill.AllowedTools {
			tool, exists := agentsdk.LookupAgentTool(toolKey)
			if exists && tool.RequiresAllowedObjects && len(skill.AllowedObjects) == 0 {
				state.add(path+".allowed_objects", "must declare at least one object when tool %q is allowed", tool.Key)
			}
		}
		for objectIndex, objectKey := range skill.AllowedObjects {
			if _, exists := state.objects[strings.TrimSpace(objectKey)]; !exists {
				state.add(fmt.Sprintf("%s.allowed_objects[%d]", path, objectIndex), "references unknown object %q", objectKey)
			}
		}
		skills[key] = skill
	}

	agents := map[string]agentsdk.AgentSchema{}
	for index, agent := range state.manifest.Agents {
		path := fmt.Sprintf("agents[%d]", index)
		key := strings.TrimSpace(agent.Key)
		if !storageValuePattern.MatchString(key) {
			state.add(path+".key", "must be a stable non-empty key")
		} else if _, exists := agents[key]; exists {
			state.add(path+".key", "duplicate agent %q", key)
		}
		if strings.TrimSpace(agent.Name) == "" {
			state.add(path+".name", "is required")
		}
		validateAgentExecutionLimits(state, path+".execution_limits", agent.ExecutionLimits)
		validateAgentStringSet(state, path+".tools", agent.Tools)
		validateAgentTools(state, path+".tools", agent.Tools)
		validateAgentStringSet(state, path+".skill_keys", agent.SkillKeys)
		for skillIndex, skillKey := range agent.SkillKeys {
			if _, exists := skills[strings.TrimSpace(skillKey)]; !exists {
				state.add(fmt.Sprintf("%s.skill_keys[%d]", path, skillIndex), "references unknown skill %q", skillKey)
			}
		}
		if agentUsesObjectTool(agent, skills) && len(agentAllowedObjects(agent, skills)) == 0 {
			state.add(path+".tools", "object tools require at least one allowed object from the Agent's Skills")
		}
		agents[key] = agent
	}

	servicePrincipals := map[string]agentsdk.AgentServicePrincipalBinding{}
	for index, binding := range state.manifest.AgentServicePrincipals {
		path := fmt.Sprintf("agent_service_principals[%d]", index)
		key := strings.TrimSpace(binding.Key)
		if binding.ContractVersion != agentsdk.AgentServicePrincipalContractVersion {
			state.add(path+".contract_version", "must be %q", agentsdk.AgentServicePrincipalContractVersion)
		}
		if !storageValuePattern.MatchString(key) {
			state.add(path+".key", "must be a stable non-empty key")
		} else if _, exists := servicePrincipals[key]; exists {
			state.add(path+".key", "duplicate service principal %q", key)
		}
		if !storageValuePattern.MatchString(strings.TrimSpace(binding.UserID)) {
			state.add(path+".user_id", "must be a stable non-empty service identity")
		}
		if !storageValuePattern.MatchString(strings.TrimSpace(binding.RoleKey)) {
			state.add(path+".role_key", "must be a stable non-empty Identity role key")
		}
		if binding.RotationVersion < 1 {
			state.add(path+".rotation_version", "must be at least 1")
		}
		servicePrincipals[key] = binding
	}

	tasks := map[string]agentsdk.AgentTaskDefinition{}
	for index, task := range state.manifest.AgentTasks {
		path := fmt.Sprintf("agent_tasks[%d]", index)
		key := strings.TrimSpace(task.Key)
		if task.ContractVersion != agentsdk.AgentTaskContractVersion {
			state.add(path+".contract_version", "must be %q", agentsdk.AgentTaskContractVersion)
		}
		if !storageValuePattern.MatchString(key) {
			state.add(path+".key", "must be a stable non-empty key")
		} else if _, exists := tasks[key]; exists {
			state.add(path+".key", "duplicate task %q", key)
		}
		if strings.TrimSpace(task.Version) == "" {
			state.add(path+".version", "is required")
		}
		agent, agentExists := agents[strings.TrimSpace(task.AgentKey)]
		if !agentExists {
			state.add(path+".agent_key", "references unknown agent %q", task.AgentKey)
		} else {
			if strings.TrimSpace(agent.Version) == "" {
				state.add(path+".agent_key", "references unversioned agent %q", task.AgentKey)
			}
			for _, skillKey := range agent.SkillKeys {
				if strings.TrimSpace(skills[strings.TrimSpace(skillKey)].Version) == "" {
					state.add(path+".agent_key", "agent %q references unversioned skill %q", task.AgentKey, skillKey)
				}
			}
		}
		if strings.TrimSpace(task.Instruction) == "" {
			state.add(path+".instruction", "is required")
		}
		validateAgentJSONSchema(state, path+".input_schema", task.InputSchema)
		validateAgentJSONSchema(state, path+".output_schema", task.OutputSchema)
		allowedObjects := agentAllowedObjects(agent, skills)
		validateAgentStringSet(state, path+".allowed_objects", task.AllowedObjects)
		validateAgentStringSet(state, path+".allowed_actions", task.AllowedActions)
		for objectIndex, objectKey := range task.AllowedObjects {
			objectKey = strings.TrimSpace(objectKey)
			if _, exists := state.objects[objectKey]; !exists {
				state.add(fmt.Sprintf("%s.allowed_objects[%d]", path, objectIndex), "references unknown object %q", objectKey)
			} else if agentExists && !allowedObjects[objectKey] {
				state.add(fmt.Sprintf("%s.allowed_objects[%d]", path, objectIndex), "object %q is outside the Agent Skill allowlist", objectKey)
			}
		}
		for actionIndex, actionKey := range task.AllowedActions {
			action, exists := state.actions[strings.TrimSpace(actionKey)]
			if !exists {
				state.add(fmt.Sprintf("%s.allowed_actions[%d]", path, actionIndex), "references unknown Action %q", actionKey)
				continue
			}
			for _, issue := range invocationcontract.ValidateActionTarget(action, "") {
				state.add(fmt.Sprintf("%s.allowed_actions[%d]", path, actionIndex), "%s", issue.Code)
			}
			if len(task.AllowedObjects) > 0 && !stringSet(task.AllowedObjects)[strings.TrimSpace(action.ObjectKey)] {
				state.add(fmt.Sprintf("%s.allowed_actions[%d]", path, actionIndex), "Action %q owns object %q outside task allowed_objects", actionKey, action.ObjectKey)
			}
		}
		validateAgentTaskOutcomes(state, path+".allowed_outcomes", task.AllowedOutcomes)
		switch task.SideEffectMode {
		case agentsdk.AgentTaskSideEffectAnalysisOnly:
			if len(task.AllowedActions) > 0 {
				state.add(path+".allowed_actions", "must be empty for analysis_only")
			}
		case agentsdk.AgentTaskSideEffectProposalOnly, agentsdk.AgentTaskSideEffectActionAllowed:
			if len(task.AllowedActions) == 0 {
				state.add(path+".allowed_actions", "must declare at least one Business Action for %s", task.SideEffectMode)
			}
			if agentExists {
				if !agentAllowsCapability(agent, skills, "invoke_action") {
					state.add(path+".allowed_actions", "Agent %q does not allow invoke_action", task.AgentKey)
				}
			}
		default:
			state.add(path+".side_effect_mode", "unsupported mode %q", task.SideEffectMode)
		}
		validateAgentExecutionLimits(state, path+".execution_limits", task.ExecutionLimits)
		tasks[key] = task
	}

	workflows := map[string]definitionmodel.WorkflowSchema{}
	for _, workflow := range state.manifest.Workflows {
		workflows[strings.TrimSpace(workflow.Key)] = workflow
	}
	agentEntrypointKeys := map[string]bool{}
	for index, assignment := range state.manifest.AgentEntrypoints {
		path := fmt.Sprintf("agent_entrypoints[%d]", index)
		key := strings.TrimSpace(assignment.Key)
		if assignment.ContractVersion != agentsdk.AgentEntrypointContractVersion {
			state.add(path+".contract_version", "must be %q", agentsdk.AgentEntrypointContractVersion)
		}
		if !storageValuePattern.MatchString(key) {
			state.add(path+".key", "must be a stable non-empty key")
		} else if agentEntrypointKeys[key] {
			state.add(path+".key", "duplicate Agent entrypoint %q", key)
		}
		agentEntrypointKeys[key] = true
		assignedAgent, assignedAgentExists := agents[strings.TrimSpace(assignment.AgentKey)]
		if !assignedAgentExists {
			state.add(path+".agent_key", "references unknown agent %q", assignment.AgentKey)
		} else if strings.TrimSpace(assignedAgent.Version) == "" {
			state.add(path+".agent_key", "references unversioned agent %q", assignment.AgentKey)
		}
		if len(assignment.RequiredPermissions) == 0 {
			state.add(path+".required_permissions", "must declare at least one permission")
		}
		validateAgentStringSet(state, path+".required_permissions", assignment.RequiredPermissions)
		if len(assignment.RoutePatterns) == 0 {
			state.add(path+".route_patterns", "must declare at least one route pattern")
		}
		validateAgentStringSet(state, path+".route_patterns", assignment.RoutePatterns)
		validateAgentStringSet(state, path+".allowed_task_keys", assignment.AllowedTaskKeys)
		for taskIndex, taskKey := range assignment.AllowedTaskKeys {
			task, exists := tasks[strings.TrimSpace(taskKey)]
			if !exists {
				state.add(fmt.Sprintf("%s.allowed_task_keys[%d]", path, taskIndex), "references unknown Agent Task %q", taskKey)
				continue
			}
			if assignment.Enabled {
				if !task.Enabled {
					state.add(fmt.Sprintf("%s.allowed_task_keys[%d]", path, taskIndex), "references disabled Agent Task %q", taskKey)
					continue
				}
			}
			if strings.TrimSpace(task.AgentKey) != strings.TrimSpace(assignment.AgentKey) {
				state.add(fmt.Sprintf("%s.allowed_task_keys[%d]", path, taskIndex), "Agent Task %q belongs to Agent %q, not entrypoint Agent %q", taskKey, task.AgentKey, assignment.AgentKey)
			}
		}
		for workflowIndex, workflowKey := range assignment.AllowedWorkflowKeys {
			workflow, exists := workflows[strings.TrimSpace(workflowKey)]
			if !exists {
				state.add(fmt.Sprintf("%s.allowed_workflow_keys[%d]", path, workflowIndex), "references unknown Workflow %q", workflowKey)
				continue
			}
			if assignment.Enabled {
				for _, issue := range invocationcontract.ValidateWorkflowTarget(workflow, invocationcontract.WorkflowEntryAgent) {
					if issue.Code == "invocation.workflow_disabled" {
						state.add(fmt.Sprintf("%s.allowed_workflow_keys[%d]", path, workflowIndex), "references disabled Workflow %q", workflowKey)
					} else {
						state.add(fmt.Sprintf("%s.allowed_workflow_keys[%d]", path, workflowIndex), "%s", issue.Code)
					}
				}
			}
		}
		validateAgentStringSet(state, path+".allowed_workflow_keys", assignment.AllowedWorkflowKeys)
		validateGlobalAgentContextContract(state, path+".context_contract", assignment.ContextContract)
		validateAgentRoutingContract(state, path+".routing_contract", assignment.RoutingContract)
	}

	for workflowIndex, workflow := range state.manifest.Workflows {
		if workflow.Graph == nil {
			continue
		}
		nodeTasks := map[string]agentsdk.AgentTaskDefinition{}
		for nodeIndex, node := range workflow.Graph.Nodes {
			if strings.TrimSpace(node.Type) != "agent_task" {
				continue
			}
			path := fmt.Sprintf("workflows[%d].graph.nodes[%d].contract.agent_task", workflowIndex, nodeIndex)
			if node.Contract == nil {
				state.add(path, "is required")
				continue
			}
			if node.Contract.AgentTask == nil {
				state.add(path, "is required")
				continue
			}
			contract := *node.Contract.AgentTask
			task, exists := tasks[strings.TrimSpace(contract.TaskKey)]
			if !exists {
				state.add(path+".task_key", "references unknown Agent Task %q", contract.TaskKey)
			} else {
				nodeTasks[strings.TrimSpace(node.ID)] = task
				if strings.TrimSpace(contract.TaskVersion) != strings.TrimSpace(task.Version) {
					state.add(path+".task_version", "must match Agent Task %q version %q", task.Key, task.Version)
				}
				if !task.Enabled {
					state.add(path+".task_key", "references disabled Agent Task %q", contract.TaskKey)
				}
			}
			if contract.Identity.Mode == agentsdk.AgentTaskIdentityService {
				binding, found := servicePrincipals[strings.TrimSpace(contract.Identity.PrincipalKey)]
				if !found {
					state.add(path+".identity.principal_key", "references unknown service principal %q", contract.Identity.PrincipalKey)
				} else if !binding.Enabled {
					state.add(path+".identity.principal_key", "references disabled service principal %q", contract.Identity.PrincipalKey)
				}
			}
			validateAgentNodeAllowlist(state, path, contract, task, exists)
		}
		for edgeIndex, edge := range workflow.Graph.Edges {
			task, exists := nodeTasks[strings.TrimSpace(edge.Source)]
			if !exists {
				continue
			}
			branch := strings.TrimSpace(edge.Branch)
			if branch == "" {
				branch = strings.TrimSpace(edge.Label)
			}
			if !stringSet(task.AllowedOutcomes)[branch] {
				state.add(fmt.Sprintf("workflows[%d].graph.edges[%d].branch", workflowIndex, edgeIndex), "branch %q is not declared by Agent Task %q", branch, task.Key)
			}
		}
	}
}

func validateAgentTools(state *validationState, path string, values []string) {
	for index, value := range values {
		if _, exists := agentsdk.LookupAgentTool(value); !exists {
			state.add(fmt.Sprintf("%s[%d]", path, index), "references unsupported Agent tool %q; allowed values are %s", value, strings.Join(agentsdk.AgentToolKeys(), ","))
		}
	}
}

func agentUsesObjectTool(agent agentsdk.AgentSchema, skills map[string]agentsdk.SkillSchema) bool {
	for _, toolKey := range agent.Tools {
		if tool, exists := agentsdk.LookupAgentTool(toolKey); exists && tool.RequiresAllowedObjects {
			return true
		}
	}
	for _, skillKey := range agent.SkillKeys {
		for _, toolKey := range skills[strings.TrimSpace(skillKey)].AllowedTools {
			if tool, exists := agentsdk.LookupAgentTool(toolKey); exists && tool.RequiresAllowedObjects {
				return true
			}
		}
	}
	return false
}
