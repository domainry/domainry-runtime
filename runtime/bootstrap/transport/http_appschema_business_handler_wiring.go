package transport

import (
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsprojection "github.com/domainry/domainry-runtime/runtime/domain/operations/projection"
	artifactpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/artifact"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	appschemahttp "github.com/domainry/domainry-runtime/runtime/transport/http/appschema"
	discoveryhttp "github.com/domainry/domainry-runtime/runtime/transport/http/discovery"
	lifecyclehttp "github.com/domainry/domainry-runtime/runtime/transport/http/lifecycle"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

func (a *httpServerAssembly) wireOperationsApplication() {
	records := a.dependencies.Records
	operationsStore := operationspersistence.NewOperationsStore(a.dependencies.Store)
	operationsService := operationsapplication.NewOperationsApplicationService(operationsStore, records.Applications().RuntimeStatus, nil, nil, a.selectedOperationsDefinitions())
	if a.dependencies.BlobStore != nil {
		_ = operationsService.RegisterResultArtifacts(operationspersistence.NewOperationsResultArtifactStore(artifactpersistence.NewStore(a.dependencies.Store), a.dependencies.BlobStore))
	}
	_ = operationsService.RegisterDiagnostics(operationsStore, a.dependencies.RuntimeInstanceID)
	_ = operationsService.RegisterBreakGlass(operationsStore, operationsBreakGlassAuditAlert{audit: records.Applications().Audit})
	var workflows *workflowapplication.WorkflowApplicationService
	if a.schemaCapabilities().Workflow {
		workflows = records.Applications().Workflows
	}
	registerOperationsDeadLetterOwners(operationsService, records.Applications().PublicationHandoff, workflows, records.Applications().RecordTimers)
	a.operations = operationsService
}

func (a *httpServerAssembly) selectedOperationsDefinitions() []operationsmodel.OperationsDefinition {
	capabilities := a.schemaCapabilities()
	definitions := operationsprojection.OperationsDefinitions()
	selected := make([]operationsmodel.OperationsDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Owner == "workflow" && !capabilities.Workflow {
			continue
		}
		if definition.Owner == "automation" && !capabilities.Automation {
			continue
		}
		if definition.Owner == "lifecycle" && !capabilities.Lifecycle {
			continue
		}
		selected = append(selected, definition)
	}
	return selected
}

func (a *httpServerAssembly) wireMetadataAndProjectExtensions() {
	records := a.dependencies.Records
	operationsService := a.operations
	operationsStore := operationspersistence.NewOperationsStore(a.dependencies.Store)
	a.handlers.Discovery = discoveryhttp.NewDiscoveryHandler(discoveryhttp.DiscoveryDependencies{
		Schema:    records.Applications().Schema,
		Principal: a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteError: a.callbacks.WriteError,
	})
	a.handlers.Operations = operationshttp.NewOperationsHandler(operationshttp.OperationsDependencies{
		Service: operationsService, Controls: operationsapplication.NewOperationsControlApplicationService(operationsStore, operationsService, operationsStore, nil), Leases: operationsapplication.NewOperationsLeaseApplicationService(operationsStore, operationsService, nil), Principal: a.callbacks.Principal,
		DatabaseRetirement: operationsapplication.NewDatabaseRetirementApplicationService(
			operationspersistence.NewOperationsStore(a.dependencies.Store),
			operationspersistence.NewDatabaseRetirementSQLExecutor(a.dependencies.Store, nil, nil),
			nil,
			nil,
		),
		WriteJSON: a.callbacks.WriteJSON, WriteServiceError: a.callbacks.WriteServiceError,
		DecodeJSON: a.callbacks.DecodeJSON, SecurityAudit: a.callbacks.SecurityAuditForPrincipal,
		Authenticated: a.identityHTTP.AuthenticatedFunc,
	})
	if a.dependencies.LifecycleBinding != nil {
		a.handlers.Lifecycle = lifecyclehttp.NewLifecycleHandler(lifecyclehttp.LifecycleDependencies{
			Service: a.dependencies.LifecycleBinding.Governance(), Operations: operationsService,
			Principal: a.callbacks.Principal,
			CleanupProcessorPrincipal: lifecycleaccess.NewSystemPrincipal("runtime-lifecycle-http", lifecycleaccess.NewSystemScope(
				lifecycleaccess.SystemScopeGlobal,
				"process cleanup after Runtime operations authorization",
			)),
			WriteJSON:         a.callbacks.WriteJSON,
			WriteServiceError: a.callbacks.WriteServiceError,
			Authenticated:     a.identityHTTP.AuthenticatedFunc,
		})
	}
	a.handlers.ApplicationSchema = appschemahttp.NewApplicationSchemaHandler(appschemahttp.ApplicationSchemaDependencies{
		RuntimeCatalog: a.metadata,
		Principal:      a.callbacks.Principal,
		WriteJSON:      a.callbacks.WriteJSON, WriteError: a.callbacks.WriteError,
		WriteServiceError: a.callbacks.WriteServiceError,
		Authenticated:     a.identityHTTP.AuthenticatedFunc,
	})
}

func runtimeObjectSchemas(records *composition.RuntimeServices) func() []definitionmodel.ObjectSchema {
	return func() []definitionmodel.ObjectSchema {
		return records.Schema().Objects
	}
}
