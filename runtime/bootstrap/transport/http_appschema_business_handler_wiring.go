package transport

import (
	"net/http"

	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	changeplancontract "github.com/domainry/domainry-runtime/runtime/domain/changeplan/contract"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	appschemahttp "github.com/domainry/domainry-runtime/runtime/transport/http/appschema"
	businessreferencehttp "github.com/domainry/domainry-runtime/runtime/transport/http/businessreferences"
	businesssystemhttp "github.com/domainry/domainry-runtime/runtime/transport/http/businesssystem"
	capabilityhttp "github.com/domainry/domainry-runtime/runtime/transport/http/capabilities"
	discoveryhttp "github.com/domainry/domainry-runtime/runtime/transport/http/discovery"
	lifecyclehttp "github.com/domainry/domainry-runtime/runtime/transport/http/lifecycle"
	openapihttp "github.com/domainry/domainry-runtime/runtime/transport/http/openapi"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
	"github.com/domainry/domainry-runtime/runtime/transport/provision"
)

func (a *httpServerAssembly) wireOperationsApplication() {
	records := a.dependencies.Records
	operationsStore := operationspersistence.NewOperationsStore(a.dependencies.Store)
	operationsService := operationsapplication.NewOperationsApplicationService(operationsStore, records.Applications().RuntimeStatus, nil, nil)
	useDirectAuthoringProjection(operationsService, records.Applications().AuthoringCapabilities)
	_ = operationsService.RegisterDiagnostics(operationsStore, a.dependencies.RuntimeInstanceID)
	_ = operationsService.RegisterBreakGlass(operationsStore, operationsBreakGlassAuditAlert{audit: records.Applications().Audit})
	registerOperationsDeadLetterOwners(operationsService, records.Applications().PublicationHandoff, records.Applications().Workflows, a.dependencies.SchedulerBinding, records.Applications().RecordTimers)
	a.operations = operationsService
}

func (a *httpServerAssembly) wireMetadataAndBusinessHandlers() {
	records := a.dependencies.Records
	operationsService := a.operations
	operationsStore := operationspersistence.NewOperationsStore(a.dependencies.Store)
	a.handlers.Discovery = discoveryhttp.NewDiscoveryHandler(discoveryhttp.DiscoveryDependencies{
		Schema: records.Applications().Schema, Principal: a.callbacks.Principal,
		WriteJSON: a.callbacks.WriteJSON, WriteError: a.callbacks.WriteError,
	})
	a.handlers.OpenAPI = openapihttp.NewOpenAPIHandler(openapihttp.OpenAPIDependencies{
		Schema: records.Applications().Schema, WriteJSON: a.callbacks.WriteJSON,
		ProductBrandName: a.dependencies.Config.EffectiveProductBrandName(),
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
		DecodeJSON: a.callbacks.DecodeJSON, SecurityAudit: a.callbacks.SecurityAuditForPrincipal, Admin: a.identityHTTP.PermissionFunc("workspace.admin"),
		Authenticated: a.identityHTTP.AuthenticatedFunc,
	})
	a.handlers.Lifecycle = lifecyclehttp.NewLifecycleHandler(lifecyclehttp.LifecycleDependencies{
		Service: records.Applications().Lifecycle, Operations: operationsService,
		Principal: a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON,
		Authenticated: a.identityHTTP.AuthenticatedFunc,
	})
	a.handlers.ApplicationSchema = appschemahttp.NewApplicationSchemaHandler(appschemahttp.ApplicationSchemaDependencies{
		Definitions: a.metadata, LocalizedTexts: a.metadata, RuntimeCatalog: a.metadata,
		Capabilities: records.Applications().AuthoringCapabilities,
		Audit:        records.Applications().Audit, Principal: a.callbacks.Principal,
		WriteJSON: a.callbacks.WriteJSON, WriteError: a.callbacks.WriteError,
		WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON,
		Admin: a.identityHTTP.PermissionFunc("workspace.admin"), Authenticated: a.identityHTTP.AuthenticatedFunc, LegacyHeaders: capabilityhttp.WriteLegacyProjectionHeaders,
		ProvisionRequired: a.callbacks.ProvisionRequired,
	})
	a.handlers.Capabilities = capabilityhttp.NewCapabilitiesHandler(capabilityhttp.CapabilitiesDependencies{
		Service: records.Applications().AuthoringCapabilities, Principal: a.callbacks.Principal,
		WriteJSON: a.callbacks.WriteJSON, WriteServiceError: a.callbacks.WriteServiceError,
	})
	a.handlers.BusinessReferences = businessreferencehttp.NewBusinessReferencesHandler(businessreferencehttp.BusinessReferencesDependencies{
		Service:   records.Applications().BusinessReferences,
		Principal: a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError,
	})
	a.handlers.BusinessSystem = businesssystemhttp.NewBusinessSystemHandler(businesssystemhttp.BusinessSystemDependencies{
		Service: records.Applications().BusinessSystem,
		Validation: businesssystemapplication.NewRuntimeAuthoringValidationApplicationService(businesssystemapplication.RuntimeAuthoringValidationDependencies{
			CurrentManifest: a.metadata.CurrentManifest, BaseManifest: a.dependencies.Manifest,
			ValidateDefinitions: a.metadata.ValidateCurrentRuntimeDefinitions,
			CurrentSnapshot:     records.Applications().BusinessSystem.Snapshot,
			StorageReadiness:    records.Applications().RuntimeStatus.StorageReadiness, MigrationReadiness: records.Applications().RuntimeStatus.MigrationReadiness,
		}),
		Principal:       a.callbacks.Principal,
		RuntimeMetadata: a.server.BusinessSystemRuntimeMetadata, WriteJSON: a.callbacks.WriteJSON, WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON,
		BeginValidation:    beginAuthoringValidationCallback(a.dependencies.Config.ManifestPath),
		CompleteValidation: completeAuthoringValidationCallback(a.dependencies.Config.ManifestPath),
		CompleteDelivery:   completeAuthoringDeliveryCallback(a.dependencies.Config.ManifestPath),
	})
}

func runtimeObjectSchemas(records *composition.RuntimeServices) func() []definitionmodel.ObjectSchema {
	return func() []definitionmodel.ObjectSchema {
		return records.Schema().Objects
	}
}

func useDirectAuthoringProjection(service *operationsapplication.OperationsApplicationService, capabilities *capabilityapplication.CapabilityAuthoringApplicationService) {
	if capabilities != nil {
		service.UseDirectAuthoringProjection(capabilities.DirectAuthoringSuccessProjection)
	}
}

func withProvisionLifecycle(manifestPath string, transition func() error) error {
	if _, found, err := provision.ReadLifecycle(manifestPath); err != nil || !found {
		return err
	}
	return transition()
}

func beginAuthoringValidationCallback(manifestPath string) func(string) error {
	return func(builderTaskID string) error {
		return withProvisionLifecycle(manifestPath, func() error {
			_, err := provision.BeginAuthoringValidation(manifestPath, builderTaskID)
			return err
		})
	}
}

func completeAuthoringValidationCallback(manifestPath string) func(string, string, bool) error {
	return func(builderTaskID, snapshotHash string, valid bool) error {
		return withProvisionLifecycle(manifestPath, func() error {
			_, err := provision.CompleteAuthoringValidation(manifestPath, builderTaskID, snapshotHash, valid)
			return err
		})
	}
}

func completeAuthoringDeliveryCallback(manifestPath string) func(string, string, bool) error {
	return func(builderTaskID, snapshotHash string, valid bool) error {
		return withProvisionLifecycle(manifestPath, func() error {
			_, err := provision.CompleteAuthoringDelivery(manifestPath, builderTaskID, snapshotHash, valid)
			return err
		})
	}
}

type businessSystemSnapshotReader interface {
	Snapshot(*http.Request) (changeplanprojection.BusinessSystemSnapshot, error)
}

type businessReferenceGraphReader interface {
	Graph(*http.Request) (changeplanmodel.ReferenceGraph, error)
}

type changePlanSnapshotSource struct{ handler businessSystemSnapshotReader }

func (s changePlanSnapshotSource) Snapshot(request *http.Request) (changeplancontract.SnapshotSource, error) {
	return s.handler.Snapshot(request)
}

type changePlanGraphSource struct{ handler businessReferenceGraphReader }

func (s changePlanGraphSource) Graph(request *http.Request) (changeplancontract.ReferenceGraphSource, error) {
	return s.handler.Graph(request)
}
