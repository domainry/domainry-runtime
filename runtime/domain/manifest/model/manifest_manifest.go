package manifestmodel

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type ManifestSchema struct {
	SchemaVersion             string                                          `json:"schema_version"`
	TemplateID                string                                          `json:"template_id"`
	Version                   string                                          `json:"version"`
	ManifestHash              string                                          `json:"manifest_hash,omitempty"`
	SourceBlueprintID         string                                          `json:"source_blueprint_id,omitempty"`
	TargetAPIContractVersion  string                                          `json:"target_api_contract_version,omitempty"`
	TargetAPIContractHash     string                                          `json:"target_api_contract_hash,omitempty"`
	AuthoringContractVersion  string                                          `json:"authoring_contract_version,omitempty"`
	AuthoringContractHash     string                                          `json:"authoring_contract_hash,omitempty"`
	GeneratedDomainSDK        *GeneratedDomainSDKIdentity                     `json:"generated_domain_sdk,omitempty"`
	SourceIntentCoverage      *ManifestSourceIntentCoverage                   `json:"source_intent_coverage,omitempty"`
	DefaultLocale             string                                          `json:"default_locale,omitempty"`
	Name                      string                                          `json:"name,omitempty"`
	Description               string                                          `json:"description,omitempty"`
	I18n                      localizationmodel.LocalizedTextMap              `json:"i18n,omitempty"`
	Objects                   []definitionmodel.ObjectSchema                  `json:"objects"`
	Views                     []definitionmodel.ViewSchema                    `json:"views"`
	Actions                   []definitionmodel.ActionSchema                  `json:"actions,omitempty"`
	Workflows                 []definitionmodel.WorkflowSchema                `json:"workflows,omitempty"`
	SchedulerDefinitions      []map[string]any                                `json:"scheduler_definitions,omitempty"`
	AutomationRules           []automationmodel.AutomationRuleSchema          `json:"automation_rules,omitempty"`
	NotificationTemplates     []notificationmodel.NotificationTemplate        `json:"notification_templates,omitempty"`
	NotificationEventTypes    []notificationmodel.NotificationEventType       `json:"notification_event_types,omitempty"`
	NotificationRules         []notificationmodel.NotificationRule            `json:"notification_rules,omitempty"`
	Dictionaries              []metadatamodel.DictionarySchema                `json:"dictionaries,omitempty"`
	Integrations              integrationmodel.IntegrationSchema              `json:"integrations,omitempty"`
	Reports                   []reportmodel.ReportSchema                      `json:"reports,omitempty"`
	IdentityProfileExtensions []profilebindingmodel.Binding                   `json:"identity_profile_extensions,omitempty"`
	OperationStateExamples    []reportmodel.ReportOperationStateExampleSchema `json:"operation_state_examples,omitempty"`
	SensitiveFieldPolicies    []reportmodel.ReportSensitiveFieldPolicySchema  `json:"sensitive_field_policies,omitempty"`
	ReportExportControls      []reportmodel.ReportExportControlSchema         `json:"report_export_controls,omitempty"`
	EntryPoints               []definitionmodel.EntryPointSchema              `json:"entrypoints,omitempty"`
	Skills                    []agentmodel.SkillSchema                        `json:"skills,omitempty"`
	Agents                    []agentmodel.AgentSchema                        `json:"agents,omitempty"`
	AgentTasks                []agentmodel.AgentTaskDefinition                `json:"agent_tasks,omitempty"`
	AgentEntrypoints          []agentmodel.AgentEntrypointAssignment          `json:"agent_entrypoints,omitempty"`
	AgentServicePrincipals    []agentmodel.AgentServicePrincipalBinding       `json:"agent_service_principals,omitempty"`
	SeedRecords               []businessseedmodel.SeedRecordSchema            `json:"seed_records,omitempty"`
	AutomationExecutionSeeds  []automationmodel.AutomationRuleExecution       `json:"automation_execution_seeds,omitempty"`
	BusinessLoops             []map[string]any                                `json:"business_loops,omitempty"`
	StateMachines             []map[string]any                                `json:"state_machines,omitempty"`
	ValidationPlan            []map[string]any                                `json:"validation_plan,omitempty"`
}
