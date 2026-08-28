package agentmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
)

const (
	AgentTaskContractVersion             = "agent-task-v1"
	AgentEntrypointContractVersion       = "agent-entrypoint-v1"
	AgentServicePrincipalContractVersion = "agent-service-principal-v1"
	GlobalAgentContextContractVersion    = "global-agent-context-v1"
	AgentRoutingContractVersion          = "agent-routing-v1"
	InteractiveHandoffContractVersion    = "interactive-handoff-v1"
	AgentTaskSideEffectAnalysisOnly      = "analysis_only"
	AgentTaskSideEffectProposalOnly      = "proposal_only"
	AgentTaskSideEffectActionAllowed     = "action_allowed"
	AgentTaskIdentityInherit             = "inherit"
	AgentTaskIdentityService             = "service"
	AgentRouteInteractiveQuery           = "interactive_query"
	AgentRouteTask                       = "agent_task"
	AgentRouteWorkflow                   = "workflow"
	AgentRouteProposal                   = "proposal"
)

var AgentTaskOutcomes = []string{"success", "manual_review", "rejected", "no_result", "error"}

type SkillSchema struct {
	Key            string                             `json:"key"`
	Version        string                             `json:"version,omitempty"`
	Name           string                             `json:"name"`
	Description    string                             `json:"description,omitempty"`
	I18n           localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Instructions   string                             `json:"instructions,omitempty"`
	AllowedTools   []string                           `json:"allowed_tools,omitempty"`
	AllowedObjects []string                           `json:"allowed_objects,omitempty"`
	RunMode        string                             `json:"run_mode,omitempty"`
	Config         map[string]any                     `json:"config,omitempty"`
}

type AgentSchema struct {
	Key             string                             `json:"key"`
	Version         string                             `json:"version,omitempty"`
	Name            string                             `json:"name"`
	Description     string                             `json:"description,omitempty"`
	I18n            localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Tools           []string                           `json:"tools,omitempty"`
	SkillKeys       []string                           `json:"skill_keys,omitempty"`
	ExecutionLimits AgentExecutionLimits               `json:"execution_limits,omitempty"`
	Config          map[string]any                     `json:"config,omitempty"`
}

type AgentExecutionLimits struct {
	MaxSteps       int    `json:"max_steps,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	MaxToolCalls   int    `json:"max_tool_calls,omitempty"`
	MaxInputBytes  int    `json:"max_input_bytes,omitempty"`
	MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
	CostBudget     string `json:"cost_budget,omitempty"`
}

var AgentCostBudgets = []string{"low", "standard", "high"}

type AgentTaskDefinition struct {
	ContractVersion string                             `json:"contract_version"`
	Key             string                             `json:"key"`
	Version         string                             `json:"version"`
	Name            string                             `json:"name,omitempty"`
	Description     string                             `json:"description,omitempty"`
	I18n            localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	AgentKey        string                             `json:"agent_key"`
	Instruction     string                             `json:"instruction"`
	InputSchema     map[string]any                     `json:"input_schema"`
	OutputSchema    map[string]any                     `json:"output_schema"`
	AllowedObjects  []string                           `json:"allowed_objects,omitempty"`
	AllowedActions  []string                           `json:"allowed_actions,omitempty"`
	AllowedOutcomes []string                           `json:"allowed_outcomes"`
	SideEffectMode  string                             `json:"side_effect_mode"`
	ExecutionLimits AgentExecutionLimits               `json:"execution_limits,omitempty"`
	Enabled         bool                               `json:"enabled"`
}

func (definition *AgentTaskDefinition) UnmarshalJSON(raw []byte) error {
	type alias AgentTaskDefinition
	var decoded alias
	if err := decodeStrictAgentContract(raw, &decoded); err != nil {
		return err
	}
	switch decoded.SideEffectMode {
	case AgentTaskSideEffectAnalysisOnly, AgentTaskSideEffectProposalOnly, AgentTaskSideEffectActionAllowed:
	default:
		return fmt.Errorf("unsupported Agent Task side_effect_mode %q", decoded.SideEffectMode)
	}
	allowed := stringSet(AgentTaskOutcomes)
	for _, outcome := range decoded.AllowedOutcomes {
		if !allowed[strings.TrimSpace(outcome)] {
			return fmt.Errorf("unsupported Agent Task outcome %q", outcome)
		}
	}
	*definition = AgentTaskDefinition(decoded)
	return nil
}

type GlobalAgentContextContract struct {
	ContractVersion   string   `json:"contract_version"`
	AllowedHintFields []string `json:"allowed_hint_fields,omitempty"`
	MaxSelectedRecord int      `json:"max_selected_records,omitempty"`
	MaxContextBytes   int      `json:"max_context_bytes,omitempty"`
}

func (contract *GlobalAgentContextContract) UnmarshalJSON(raw []byte) error {
	type alias GlobalAgentContextContract
	var decoded alias
	if err := decodeStrictAgentContract(raw, &decoded); err != nil {
		return err
	}
	allowed := stringSet([]string{"route_key", "object_key", "record_id", "selected_record_ids", "locale", "timezone"})
	for _, field := range decoded.AllowedHintFields {
		if !allowed[strings.TrimSpace(field)] {
			return fmt.Errorf("unsupported Global Agent context hint %q", field)
		}
	}
	*contract = GlobalAgentContextContract(decoded)
	return nil
}

type AgentRoutingContract struct {
	ContractVersion   string   `json:"contract_version"`
	AllowedRouteTypes []string `json:"allowed_route_types"`
	AllowRecursive    bool     `json:"allow_recursive,omitempty"`
}

func (contract *AgentRoutingContract) UnmarshalJSON(raw []byte) error {
	type alias AgentRoutingContract
	var decoded alias
	if err := decodeStrictAgentContract(raw, &decoded); err != nil {
		return err
	}
	allowed := stringSet([]string{AgentRouteInteractiveQuery, AgentRouteTask, AgentRouteWorkflow, AgentRouteProposal})
	for _, routeType := range decoded.AllowedRouteTypes {
		if !allowed[strings.TrimSpace(routeType)] {
			return fmt.Errorf("unsupported Agent route type %q", routeType)
		}
	}
	*contract = AgentRoutingContract(decoded)
	return nil
}

type AgentEntrypointAssignment struct {
	ContractVersion     string                     `json:"contract_version"`
	Key                 string                     `json:"key"`
	AgentKey            string                     `json:"agent_key"`
	Surface             string                     `json:"surface"`
	DefaultForSurface   bool                       `json:"default_for_surface,omitempty"`
	RequiredPermissions []string                   `json:"required_permissions"`
	RoutePatterns       []string                   `json:"route_patterns"`
	AllowedTaskKeys     []string                   `json:"allowed_task_keys,omitempty"`
	AllowedWorkflowKeys []string                   `json:"allowed_workflow_keys,omitempty"`
	ContextContract     GlobalAgentContextContract `json:"context_contract"`
	RoutingContract     AgentRoutingContract       `json:"routing_contract"`
	Enabled             bool                       `json:"enabled"`
}

type AgentTaskIdentity struct {
	Mode         string `json:"mode"`
	PrincipalKey string `json:"principal_key,omitempty"`
}

func (identity *AgentTaskIdentity) UnmarshalJSON(raw []byte) error {
	type alias AgentTaskIdentity
	var decoded alias
	if err := decodeStrictAgentContract(raw, &decoded); err != nil {
		return err
	}
	switch decoded.Mode {
	case AgentTaskIdentityInherit, AgentTaskIdentityService:
	default:
		return fmt.Errorf("unsupported Agent Task identity mode %q", decoded.Mode)
	}
	*identity = AgentTaskIdentity(decoded)
	return nil
}

type AgentServicePrincipalBinding struct {
	ContractVersion string `json:"contract_version"`
	Key             string `json:"key"`
	UserID          string `json:"user_id"`
	RoleKey         string `json:"role_key"`
	Enabled         bool   `json:"enabled"`
	RotationVersion int    `json:"rotation_version"`
}

type InteractiveAgentHandoff struct {
	ContractVersion string         `json:"contract_version"`
	RouteType       string         `json:"route_type"`
	TargetKey       string         `json:"target_key"`
	Input           map[string]any `json:"input"`
	IdempotencyKey  string         `json:"idempotency_key"`
	ProcessID       string         `json:"process_id,omitempty"`
	TaskRunID       string         `json:"task_run_id,omitempty"`
}

type AgentPrincipalReference struct {
	UserID                string `json:"user_id"`
	RoleKey               string `json:"role_key"`
	WorkspaceID           string `json:"workspace_id"`
	AuthorizationRevision string `json:"authorization_revision"`
}

type AgentExecutionIdentity struct {
	Mode                   string                  `json:"mode"`
	Initiator              AgentPrincipalReference `json:"initiator"`
	Execution              AgentPrincipalReference `json:"execution"`
	ServicePrincipalKey    string                  `json:"service_principal_key,omitempty"`
	ServiceRotationVersion int                     `json:"service_rotation_version,omitempty"`
}

type GlobalAgentContext struct {
	ContractVersion     string                  `json:"contract_version"`
	ContextRevision     string                  `json:"context_revision"`
	EntrypointKey       string                  `json:"entrypoint_key"`
	AgentKey            string                  `json:"agent_key"`
	Surface             string                  `json:"surface"`
	RouteKey            string                  `json:"route_key"`
	ObjectKey           string                  `json:"object_key,omitempty"`
	RecordID            string                  `json:"record_id,omitempty"`
	SelectedRecordIDs   []string                `json:"selected_record_ids,omitempty"`
	Locale              string                  `json:"locale,omitempty"`
	Timezone            string                  `json:"timezone,omitempty"`
	Principal           AgentPrincipalReference `json:"principal"`
	AllowedTaskKeys     []string                `json:"allowed_task_keys"`
	AllowedWorkflowKeys []string                `json:"allowed_workflow_keys"`
	AvailableOperations []string                `json:"available_operation_ids"`
}

type AgentAuthorizationEvidence struct {
	Decision              string   `json:"decision"`
	Code                  string   `json:"code"`
	PolicyRevision        string   `json:"policy_revision"`
	ContextRevision       string   `json:"context_revision,omitempty"`
	AuthorizationRevision string   `json:"authorization_revision"`
	AllowedObjects        []string `json:"allowed_objects"`
	AllowedActions        []string `json:"allowed_actions"`
	AllowedOutcomes       []string `json:"allowed_outcomes"`
	AllowedTools          []string `json:"allowed_tools"`
}

func (handoff *InteractiveAgentHandoff) UnmarshalJSON(raw []byte) error {
	type alias InteractiveAgentHandoff
	var decoded alias
	if err := decodeStrictAgentContract(raw, &decoded); err != nil {
		return err
	}
	switch decoded.RouteType {
	case AgentRouteTask, AgentRouteWorkflow:
	default:
		return fmt.Errorf("unsupported durable handoff route_type %q", decoded.RouteType)
	}
	*handoff = InteractiveAgentHandoff(decoded)
	return nil
}

func decodeStrictAgentContract(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode Agent contract: %w", err)
	}
	return nil
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = true
	}
	return result
}
