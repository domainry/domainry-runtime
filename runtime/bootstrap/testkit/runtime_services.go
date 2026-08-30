package testkit

import (
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
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
	Integrations                integrationmodel.IntegrationSchema
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
	ConnectorProviders          *connector.Registry
	DataExchange                dataexchange.Binding
}
