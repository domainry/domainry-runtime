package runtime

import (
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"
	businessseedrepository "github.com/domainry/domainry-runtime/runtime/domain/businessseed/repository"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	lifecyclerepository "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/repository"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowrepository "github.com/domainry/domainry-runtime/runtime/domain/workflow/repository"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	changeplanpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/changeplan"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/lifecycle"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
)

// Keep Bootstrap dependency bindings compile-time checked. These assertions live
// outside service/storage so neither package needs to import the other and
// create an implementation-detail dependency cycle.
var (
	_ recordrepository.RecordRepository                      = recordpersistence.RecordStore{}
	_ recordrepository.RecordBusinessSeedRepository          = recordpersistence.RecordStore{}
	_ auditrepository.AuditRepository                        = (*auditpersistence.Repository)(nil)
	_ auditrepository.AuditEventWriterRepository             = (*auditpersistence.Repository)(nil)
	_ auditrepository.AuditEventRepository                   = (*auditpersistence.Repository)(nil)
	_ appschemarepository.ApplicationSchemaRepository        = appschemapersistence.ApplicationSchemaStore{}
	_ appschemarepository.DefinitionMutationRepository       = appschemapersistence.ApplicationSchemaStore{}
	_ integrationrepository.IntegrationConfigRepository      = integrationpersistence.IntegrationConfigStore{}
	_ integrationrepository.IntegrationConnectionRepository  = integrationpersistence.IntegrationConfigStore{}
	_ integrationrepository.IntegrationEventRepository       = integrationpersistence.IntegrationEventStore{}
	_ integrationrepository.IntegrationDeliveryRepository    = integrationpersistence.IntegrationDeliveryStore{}
	_ integrationrepository.IntegrationWorkerRepository      = integrationpersistence.IntegrationWorkerStore{}
	_ lifecyclerepository.LifecycleRepository                = lifecyclepersistence.LifecycleStore{}
	_ workflowcontract.WorkflowWorkerStore                   = workflowpersistence.WorkflowWorkerStore{}
	_ workflowcontract.WorkflowDefinitionStore               = workflowpersistence.WorkflowDefinitionStore{}
	_ workflowcontract.WorkflowProcessStore                  = workflowpersistence.WorkflowProcessStore{}
	_ workflowcontract.WorkflowDecisionStore                 = workflowpersistence.WorkflowDecisionStore{}
	_ automationcontract.AutomationWorkerStore               = automationpersistence.AutomationWorkerStore{}
	_ automationrepository.AutomationExecutionRepository     = automationpersistence.AutomationExecutionStore{}
	_ automationrepository.ExecutionSeedRepository           = automationpersistence.AutomationExecutionStore{}
	_ changeplanrepository.ChangePlanRepository              = changeplanpersistence.BusinessChangePlanStore{}
	_ changeplanrepository.ChangePlanEvidenceRepository      = changeplanpersistence.BusinessEvidenceStore{}
	_ businessseedrepository.ProvenanceRepository            = changeplanpersistence.BusinessEvidenceStore{}
	_ workflowrepository.WorkflowExecutionRepository         = workflowpersistence.WorkflowWorkerStore{}
	_ actioncontract.ActionExecutionStore                    = actionpersistence.ActionBusinessExecutionStore{}
	_ actioncontract.ActionExecutionTransactionStore         = actionpersistence.ActionBusinessExecutionStore{}
	_ actioncontract.ActionAssuranceStore                    = actionpersistence.ActionAssuranceStore{}
	_ deploymentrepository.DeploymentRuntimeStatusRepository = deploymentpersistence.RuntimeStatusStore{}
)
