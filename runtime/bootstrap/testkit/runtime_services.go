package testkit

import (
	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// RuntimeServicesConfig is the typed fixture contract for cross-package Runtime
// tests. Store is only a test convenience: NewRuntimeServices expands it into
// focused owner stores before entering the production composition root.
type RuntimeServicesConfig struct {
	TemplateID                  string
	TemplateVersion             string
	Name                        string
	Objects                     []definitionmodel.ObjectSchema
	Actions                     []definitionmodel.ActionSchema
	Workflows                   []definitionmodel.WorkflowSchema
	AutomationRules             []automationmodel.AutomationRuleSchema
	Dictionaries                []appschemamodel.DictionarySchema
	Integrations                connectormodel.IntegrationSchema
	Reports                     []reportmodel.ReportSchema
	Skills                      []agentsdk.SkillSchema
	Agents                      []agentsdk.AgentSchema
	IdentityProfileExtensions   []profilebindingmodel.Binding
	Store                       *database.RuntimeStore
	ApplicationSchemaRepository appschemarepository.ApplicationSchemaRepository
	BusinessEvidence            changeplanrepository.ChangePlanEvidenceRepository
	WorkflowWorker              workflowcontract.WorkflowWorkerStore
	WorkflowProcesses           workflowcontract.WorkflowProcessStore
	WorkflowDefinitions         workflowcontract.WorkflowDefinitionStore
	WorkflowDecisions           workflowcontract.WorkflowDecisionStore
	IdentityDirectory           identitysdk.Directory
	IdentityPrincipals          identitysdk.PrincipalResolver
	DataExchange                dataexchange.Binding
}
