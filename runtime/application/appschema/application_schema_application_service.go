package appschema

import (
	"bytes"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	appschemavalidation "github.com/domainry/domainry-runtime/runtime/domain/appschema/validation"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"context"
	"encoding/json"
	"fmt"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"

	"strings"
	"sync"

	"github.com/domainry/domainry-foundation/idempotency"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

// ApplicationSchemaApplicationService owns metadata authoring, lifecycle, dictionary and
// localization entrypoints. Cross-domain behavior is exposed through narrow
// runtime ports; the service does not retain the aggregate RuntimeServices.
// ApplicationSchemaApplicationService owns metadata lifecycle behavior.
type ApplicationSchemaApplicationService struct {
	repository        appschemarepository.ApplicationSchemaRepository
	runtime           LifecycleRuntime
	workflows         WorkflowDefinitionInitializer
	dictionary        DictionaryRuntime
	audit             auditcontract.AuditEventFactory
	templateID        string
	version           string
	name              string
	records           recordrepository.RecordRepository
	integrations      integrationrepository.IntegrationConfigRepository
	references        ApplicationSchemaReferenceGraphProvider
	changePlans       changeplanrepository.ChangePlanRepository
	operations        changeplanrepository.ChangePlanOperationRepository
	auditAppender     ApplicationSchemaAuditAppender
	actionDefinitions func() []definitionmodel.ActionSchema
	reloadObserversMu sync.RWMutex
	reloadObservers   []func(appschemamodel.ApplicationSchemaSnapshot)
}

// UseActionDefinitionSource binds the effective execution catalog used by
// read-only metadata projections. Persisted definition lifecycle and source
// identity remain owned by the metadata repository.
func (s *ApplicationSchemaApplicationService) UseActionDefinitionSource(source func() []definitionmodel.ActionSchema) {
	if s != nil {
		s.actionDefinitions = source
	}
}

func (s *ApplicationSchemaApplicationService) AddReloadObserver(observer func(appschemamodel.ApplicationSchemaSnapshot)) {
	if s == nil || observer == nil {
		return
	}
	s.reloadObserversMu.Lock()
	defer s.reloadObserversMu.Unlock()
	s.reloadObservers = append(s.reloadObservers, observer)
}

func (s *ApplicationSchemaApplicationService) notifyReloadObservers(snapshot appschemamodel.ApplicationSchemaSnapshot) {
	s.reloadObserversMu.RLock()
	observers := append([]func(appschemamodel.ApplicationSchemaSnapshot){}, s.reloadObservers...)
	s.reloadObserversMu.RUnlock()
	for _, observer := range observers {
		observer(snapshot)
	}
}

type ApplicationSchemaReferenceGraphProvider interface {
	Graph(context.Context, principalmodel.Principal) (changeplanmodel.ReferenceGraph, error)
}

type ApplicationSchemaAuditAppender func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)

type LifecycleRuntime interface {
	ApplyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ViewSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []appschemamodel.DictionarySchema, integrationmodel.IntegrationSchema, []reportmodel.ReportSchema, []definitionmodel.EntryPointSchema, []agentmodel.SkillSchema, []agentmodel.AgentSchema, []profilebindingmodel.Binding)
	Schema() appschemamodel.ApplicationSchemaSnapshot
}

type agentLifecycleRuntime interface {
	ApplyManifestAgentMetadata([]agentmodel.AgentTaskDefinition, []agentmodel.AgentEntrypointAssignment, []agentmodel.AgentServicePrincipalBinding)
}

func applyManifestAgentMetadata(runtime LifecycleRuntime, manifest manifestmodel.ManifestSchema) {
	if target, ok := runtime.(agentLifecycleRuntime); ok {
		target.ApplyManifestAgentMetadata(manifest.AgentTasks, manifest.AgentEntrypoints, manifest.AgentServicePrincipals)
	}
}

type WorkflowDefinitionInitializer interface {
	InitializePublishedWorkflowDefinitions(context.Context, []definitionmodel.WorkflowSchema, principalmodel.SystemScope) error
}

type DictionaryRuntime interface {
	Invalidate()
	Items(context.Context, appschemarepository.ApplicationSchemaRepository, string, string, principalmodel.Principal) (appschemamodel.DictionaryItemsResult, bool, error)
}

type ApplicationSchemaDependencies struct {
	Repository    appschemarepository.ApplicationSchemaRepository
	Runtime       LifecycleRuntime
	Workflows     WorkflowDefinitionInitializer
	Dictionary    DictionaryRuntime
	Audit         auditcontract.AuditEventFactory
	TemplateID    string
	Version       string
	Name          string
	Records       recordrepository.RecordRepository
	Integrations  integrationrepository.IntegrationConfigRepository
	References    ApplicationSchemaReferenceGraphProvider
	ChangePlans   changeplanrepository.ChangePlanRepository
	AuditAppender ApplicationSchemaAuditAppender
}

func NewApplicationSchemaApplicationService(dependencies ApplicationSchemaDependencies) *ApplicationSchemaApplicationService {
	operations, _ := dependencies.ChangePlans.(changeplanrepository.ChangePlanOperationRepository)
	return &ApplicationSchemaApplicationService{
		repository: dependencies.Repository, runtime: dependencies.Runtime, workflows: dependencies.Workflows,
		dictionary: dependencies.Dictionary, audit: dependencies.Audit, templateID: dependencies.TemplateID,
		version: dependencies.Version, name: dependencies.Name, records: dependencies.Records,
		integrations: dependencies.Integrations, references: dependencies.References, changePlans: dependencies.ChangePlans, operations: operations,
		auditAppender: dependencies.AuditAppender,
	}
}

type ApplicationSchemaUpsertDefinitionOptions struct {
	Normalize func(context.Context, string, string, appschemamodel.ApplicationDefinitionUpsertRequest) (appschemamodel.ApplicationDefinitionUpsertRequest, error)
}

type DisableReferenceImpact struct {
	ResourceType      string
	GraphHash         string
	DirectConsumers   int
	IndirectConsumers int
	DeletionBlocked   bool
}

type ApplicationSchemaDisableDefinitionOptions struct {
	ResolveImpact func(context.Context, string, string, principalmodel.Principal) (DisableReferenceImpact, error)
	Audit         func(context.Context, string, string, principalmodel.Principal, map[string]any, map[string]any, map[string]any)
}

type RollbackReferenceImpact struct {
	GraphHash         string
	DirectConsumers   int
	IndirectConsumers int
}

type ApplicationSchemaRollbackDefinitionOptions struct {
	AuthoringContractVersion string
	AuthoringContractHash    string
	ResolveImpact            func(context.Context, string, string, principalmodel.Principal) (RollbackReferenceImpact, error)
}

func (s *ApplicationSchemaApplicationService) rollbackApplicationDefinitionWithOptions(ctx context.Context, resourceType, resourceKey string, request appschemamodel.ApplicationDefinitionRollbackRequest, principal principalmodel.Principal, options ApplicationSchemaRollbackDefinitionOptions) (appschemamodel.ApplicationDefinition, appschemamodel.ApplicationSchemaSnapshot, error) {
	if err := metadataAuthorizeCommand(principal); err != nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, forbidden("auth.permission_denied")
	}
	resourceType, resourceKey = strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	if code := appschemavalidation.ApplicationSchemaRollbackRequestErrorCode(request); code != "" {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, badRequest(code)
	}
	if request.AuthoringContractVersion != options.AuthoringContractVersion || request.AuthoringContractHash != options.AuthoringContractHash {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, conflict("backend.metadata.rollback_contract_stale", "expected_version", options.AuthoringContractVersion, "expected_hash", options.AuthoringContractHash)
	}
	current, found, err := s.repository.GetDefinition(ctx, metadataInstallationScope("load metadata definition for rollback"), resourceType, resourceKey)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, wrapMetadataError(err)
	}
	if !found {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, notFound("backend.metadata.definition_not_found")
	}
	if options.ResolveImpact == nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, metadataInternalError("resolve metadata rollback reference impact")
	}
	impact, err := options.ResolveImpact(ctx, resourceType, resourceKey, principal)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, err
	}
	if request.ExpectedReferenceGraphHash != impact.GraphHash {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, conflict("backend.metadata.rollback_reference_graph_stale", "expected", impact.GraphHash, "actual", request.ExpectedReferenceGraphHash)
	}
	versions, err := s.repository.ListDefinitionVersions(ctx, metadataInstallationScope("list metadata rollback versions"), resourceType, resourceKey)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, wrapMetadataError(err)
	}
	var target appschemamodel.ApplicationDefinitionVersion
	for _, version := range versions {
		if version.SchemaVersion == request.TargetVersion {
			target = version
			break
		}
	}
	if target.SchemaVersion == "" {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, notFound("backend.metadata.rollback_target_not_found")
	}
	if s.audit == nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, metadataInternalError("build metadata rollback audit")
	}
	audit := buildRollbackAudit(ctx, s.audit, resourceType, resourceKey, current, target, request, impact, principal)
	definition, err := s.repository.RollbackDefinition(ctx, metadataInstallationScope("rollback metadata definition"), resourceType, resourceKey, request, audit)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, wrapMetadataError(err)
	}
	snapshot, err := s.ReloadApplicationSchema(ctx, principal)
	return definition, snapshot, err
}

func buildRollbackAudit(ctx context.Context, factory auditcontract.AuditEventFactory, resourceType, resourceKey string, current appschemamodel.ApplicationDefinition, target appschemamodel.ApplicationDefinitionVersion, request appschemamodel.ApplicationDefinitionRollbackRequest, impact RollbackReferenceImpact, principal principalmodel.Principal) auditmodel.AuditEvent {
	metadata := map[string]any{
		"business_reason": request.BusinessReason, "change_plan_id": request.ChangePlanID, "builder_task_id": request.BuilderTaskID,
		"from_version": current.SchemaVersion, "from_hash": current.SchemaHash, "target_version": target.SchemaVersion, "target_hash": target.SchemaHash,
		"reference_graph_hash": impact.GraphHash, "direct_consumers": impact.DirectConsumers, "indirect_consumers": impact.IndirectConsumers,
		"authoring_contract_version": request.AuthoringContractVersion, "authoring_contract_hash": request.AuthoringContractHash,
	}
	return factory.NewAuditEvent(ctx, auditcontract.AuditAppendRequest{
		Event: "metadata_definition.rolled_back", ObjectKey: resourceType, RecordID: resourceKey, Principal: principal,
		Summary: "Rolled back " + resourceType + " " + resourceKey + " to version " + target.SchemaVersion,
		Before:  rollbackJSONMap(current.Payload), After: rollbackJSONMap(target.Payload), Metadata: metadata,
	})
}

func rollbackJSONMap(payload json.RawMessage) map[string]any {
	value := map[string]any{}
	_ = json.Unmarshal(payload, &value)
	return value
}

func (s *ApplicationSchemaApplicationService) disableApplicationDefinitionWithOptions(ctx context.Context, resourceType, resourceKey string, principal principalmodel.Principal, options ApplicationSchemaDisableDefinitionOptions) error {
	if err := metadataAuthorizeCommand(principal); err != nil {
		return err
	}
	if !principal.HasPermission("workspace.admin") {
		return forbidden("auth.permission_denied")
	}
	resourceType, resourceKey = strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	if resourceType == "workflow" {
		return badRequest("backend.workflow.lifecycle_api_required")
	}
	if options.ResolveImpact == nil {
		return metadataInternalError("resolve metadata definition reference impact")
	}
	impact, err := options.ResolveImpact(ctx, resourceType, resourceKey, principal)
	if err != nil {
		return err
	}
	if impact.DeletionBlocked {
		return conflict("backend.reference.delete_blocked", "resource_type", impact.ResourceType, "resource_key", resourceKey, "direct_consumers", fmt.Sprint(impact.DirectConsumers), "indirect_consumers", fmt.Sprint(impact.IndirectConsumers), "graph_hash", impact.GraphHash)
	}
	before, found, err := s.repository.GetDefinition(ctx, metadataInstallationScope("load metadata definition for disable"), resourceType, resourceKey)
	if err != nil {
		return wrapMetadataError(err)
	}
	if err := s.repository.DisableDefinition(ctx, metadataInstallationScope("disable metadata definition"), resourceType, resourceKey); err != nil {
		return wrapMetadataError(err)
	}
	if _, err := s.ReloadApplicationSchema(ctx, principal); err != nil {
		return err
	}
	if options.Audit != nil {
		options.Audit(ctx, resourceType, resourceKey, principal, DefinitionAuditValue(before, found), map[string]any{"disabled": true}, map[string]any{"reference_graph_hash": impact.GraphHash, "direct_consumers": impact.DirectConsumers})
	}
	return nil
}

func (s *ApplicationSchemaApplicationService) upsertApplicationDefinitionWithOptions(ctx context.Context, resourceType, resourceKey string, request appschemamodel.ApplicationDefinitionUpsertRequest, principal principalmodel.Principal, options ApplicationSchemaUpsertDefinitionOptions) (appschemamodel.ApplicationDefinition, appschemamodel.ApplicationSchemaSnapshot, error) {
	if err := metadataAuthorizeCommand(principal); err != nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, forbidden("auth.permission_denied")
	}
	resourceType, resourceKey = strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	if resourceType == "workflow" {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, badRequest("backend.workflow.lifecycle_api_required")
	}
	if options.Normalize == nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, metadataInternalError("normalize metadata definition")
	}
	normalized, err := options.Normalize(ctx, resourceType, resourceKey, request)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, err
	}
	before, beforeFound, err := s.repository.GetDefinition(ctx, metadataInstallationScope("load metadata definition for publish"), resourceType, resourceKey)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, wrapMetadataError(err)
	}
	if err := metadataBuilderIdempotencyConflict(before, beforeFound, normalized); err != nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, err
	}
	if s.audit == nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, metadataInternalError("build metadata publication audit")
	}
	audit := s.audit.NewAuditEvent(ctx, auditcontract.AuditAppendRequest{
		Event: "metadata_definition.saved", ObjectKey: resourceType, RecordID: resourceKey, Principal: principal,
		Summary: "Saved " + resourceType + " " + resourceKey, Before: DefinitionAuditValue(before, beforeFound),
		Metadata: map[string]any{"source_kind": normalized.SourceKind, "source_id": normalized.SourceID},
	})
	definition, err := s.repository.PublishDefinition(ctx, metadataInstallationScope("publish metadata definition"), resourceType, resourceKey, normalized, audit)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, appschemamodel.ApplicationSchemaSnapshot{}, wrapMetadataError(err)
	}
	replayed := beforeFound && before.SchemaVersion == definition.SchemaVersion && before.SchemaHash == definition.SchemaHash
	snapshot, reloadErr := s.ReloadApplicationSchema(ctx, principal)
	if !replayed {
		errorText := ""
		if reloadErr != nil {
			errorText = reloadErr.Error()
		}
		if completeErr := s.repository.CompleteDefinitionRefresh(ctx, metadataInstallationScope("complete metadata definition refresh"), resourceType, resourceKey, definition.SchemaHash, errorText); completeErr != nil {
			return definition, appschemamodel.ApplicationSchemaSnapshot{}, metadataInternalErrorWithCause("complete metadata refresh intent", completeErr)
		}
	}
	if reloadErr != nil {
		return definition, appschemamodel.ApplicationSchemaSnapshot{}, reloadErr
	}
	return definition, snapshot, nil
}

func metadataBuilderIdempotencyConflict(before appschemamodel.ApplicationDefinition, found bool, request appschemamodel.ApplicationDefinitionUpsertRequest) error {
	if !found || strings.TrimSpace(request.SourceKind) != "builder_v4" || strings.TrimSpace(request.SourceID) == "" || before.SourceID != request.SourceID {
		return nil
	}
	compact := func(value []byte) []byte {
		var decoded any
		if json.Unmarshal(value, &decoded) != nil {
			return value
		}
		// Generic values decoded by encoding/json are always marshalable.
		canonical, _ := json.Marshal(decoded)
		return canonical
	}
	if bytes.Equal(compact(before.Payload), compact(request.Payload)) {
		return nil
	}
	return conflict(idempotency.ErrorCodeKeyReused, "source_id", request.SourceID)
}

func DefinitionAuditValue(definition appschemamodel.ApplicationDefinition, found bool) map[string]any {
	if !found {
		return nil
	}
	value := map[string]any{
		"resource_type": definition.ResourceType, "resource_key": definition.ResourceKey,
		"schema_version": definition.SchemaVersion, "schema_hash": definition.SchemaHash,
		"source_kind": definition.SourceKind, "source_id": definition.SourceID,
	}
	var payload any
	if json.Unmarshal(definition.Payload, &payload) == nil {
		value["payload"] = payload
	}
	return value
}
