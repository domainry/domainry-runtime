package runtime

import (
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/persistence"
	lifecyclerepository "github.com/domainry/domainry-lifecycle/repository"
	auditrepository "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowrepository "github.com/domainry/domainry-runtime/runtime/domain/workflow/repository"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	publicationhandoffpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
)

// Keep Bootstrap dependency bindings compile-time checked. These assertions live
// outside service/storage so neither package needs to import the other and
// create an implementation-detail dependency cycle.
var (
	_ recordrepository.RecordRepository                        = recordpersistence.RecordStore{}
	_ recordrepository.RecordBusinessSeedRepository            = recordpersistence.RecordStore{}
	_ auditrepository.AuditRepository                          = (*auditpersistence.AuditStore)(nil)
	_ auditrepository.AuditEventWriterRepository               = (*auditpersistence.AuditStore)(nil)
	_ auditrepository.AuditEventRepository                     = (*auditpersistence.AuditStore)(nil)
	_ appschemarepository.ApplicationSchemaRepository          = appschemapersistence.ApplicationSchemaStore{}
	_ integrationrepository.RuntimePublicationRepository       = publicationhandoffpersistence.Store{}
	_ integrationrepository.RuntimePublicationWorkerRepository = publicationhandoffpersistence.WorkerStore{}
	_ lifecyclerepository.LifecycleRepository                  = lifecyclepersistence.LifecycleStore{}
	_ workflowcontract.WorkflowWorkerStore                     = workflowpersistence.WorkflowWorkerStore{}
	_ workflowcontract.WorkflowDefinitionStore                 = workflowpersistence.WorkflowDefinitionStore{}
	_ workflowcontract.WorkflowProcessStore                    = workflowpersistence.WorkflowProcessStore{}
	_ workflowcontract.WorkflowDecisionStore                   = workflowpersistence.WorkflowDecisionStore{}
	_ automationcontract.AutomationWorkerStore                 = automationpersistence.AutomationWorkerStore{}
	_ automationrepository.AutomationExecutionRepository       = automationpersistence.AutomationExecutionStore{}
	_ automationrepository.ExecutionSeedRepository             = automationpersistence.AutomationExecutionStore{}
	_ workflowrepository.WorkflowExecutionRepository           = workflowpersistence.WorkflowWorkerStore{}
	_ actioncontract.ActionExecutionStore                      = actionpersistence.ActionBusinessExecutionStore{}
	_ actioncontract.ActionExecutionTransactionStore           = actionpersistence.ActionBusinessExecutionStore{}
	_ actioncontract.ActionAssuranceStore                      = actionpersistence.ActionAssuranceStore{}
	_ deploymentrepository.DeploymentRuntimeStatusRepository   = deploymentpersistence.RuntimeStatusStore{}
)
