package appschemamodel

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

type ApplicationSchemaSnapshot struct {
	TemplateID                string                                  `json:"template_id"`
	TemplateVersion           string                                  `json:"template_version"`
	Name                      string                                  `json:"name,omitempty"`
	SchemaHash                string                                  `json:"schema_hash"`
	SnapshotVersion           string                                  `json:"snapshot_version"`
	Objects                   []definitionmodel.ObjectSchema          `json:"objects"`
	Actions                   []definitionmodel.ActionSchema          `json:"actions"`
	GuardedWrites             []ApplicationSchemaGuardedWriteContract `json:"guarded_writes,omitempty"`
	Workflows                 []definitionmodel.WorkflowSchema        `json:"workflows"`
	AutomationRules           []automationmodel.AutomationRuleSchema  `json:"automation_rules,omitempty"`
	Dictionaries              []DictionarySchema                      `json:"dictionaries,omitempty"`
	Integrations              integrationmodel.IntegrationSchema      `json:"integrations,omitempty"`
	Reports                   []reportmodel.ReportSchema              `json:"reports,omitempty"`
	Skills                    []agentsdk.SkillSchema                  `json:"skills,omitempty"`
	Agents                    []agentsdk.AgentSchema                  `json:"agents,omitempty"`
	AgentTasks                []agentsdk.AgentTaskDefinition          `json:"agent_tasks,omitempty"`
	AgentEntrypoints          []agentsdk.AgentEntrypointAssignment    `json:"agent_entrypoints,omitempty"`
	AgentServicePrincipals    []agentsdk.AgentServicePrincipalBinding `json:"agent_service_principals,omitempty"`
	IdentityProfileExtensions []profilebindingmodel.Binding           `json:"identity_profile_extensions,omitempty"`
}

type ApplicationSchemaGuardedWriteContract struct {
	ObjectKey       string   `json:"object_key"`
	Operation       string   `json:"operation"`
	ActionKey       string   `json:"action_key"`
	ActionKind      string   `json:"action_kind"`
	Label           string   `json:"label,omitempty"`
	Endpoint        string   `json:"endpoint"`
	RequiresRecord  bool     `json:"requires_record,omitempty"`
	BlocksRawCRUD   bool     `json:"blocks_raw_crud"`
	IdempotencyKeys []string `json:"idempotency_keys,omitempty"`
}
