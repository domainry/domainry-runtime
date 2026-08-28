package metadata

import (
	"bytes"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	metadatavalidation "github.com/domainry/domainry-runtime/runtime/domain/metadata/validation"
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

	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

	"strings"
	"sync"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

// MetadataApplicationService owns metadata authoring, lifecycle, dictionary and
// localization entrypoints. Cross-domain behavior is exposed through narrow
// runtime ports; the service does not retain the aggregate RuntimeServices.
// MetadataApplicationService owns metadata lifecycle behavior.
type MetadataApplicationService struct {
	repository        metadatarepository.MetadataRepository
	runtime           LifecycleRuntime
	workflows         WorkflowDefinitionInitializer
	dictionary        DictionaryRuntime
	audit             auditcontract.AuditEventFactory
	templateID        string
	version           string
	name              string
	records           recordrepository.RecordRepository
	integrations      integrationrepository.IntegrationConfigRepository
	references        MetadataReferenceGraphProvider
	changePlans       changeplanrepository.ChangePlanRepository
	operations        changeplanrepository.ChangePlanOperationRepository
	auditAppender     MetadataAuditAppender
	actionDefinitions func() []definitionmodel.ActionSchema
	reloadObserversMu sync.RWMutex
	reloadObservers   []func(metadatamodel.MetadataSchemaSnapshot)
}

// UseActionDefinitionSource binds the effective execution catalog used by
// read-only metadata projections. Persisted definition lifecycle and source
// identity remain owned by the metadata repository.
func (s *MetadataApplicationService) UseActionDefinitionSource(source func() []definitionmodel.ActionSchema) {
	if s != nil {
		s.actionDefinitions = source
	}
}

func (s *MetadataApplicationService) AddReloadObserver(observer func(metadatamodel.MetadataSchemaSnapshot)) {
	if s == nil || observer == nil {
		return
	}
	s.reloadObserversMu.Lock()
	defer s.reloadObserversMu.Unlock()
	s.reloadObservers = append(s.reloadObservers, observer)
}

func (s *MetadataApplicationService) notifyReloadObservers(snapshot metadatamodel.MetadataSchemaSnapshot) {
	s.reloadObserversMu.RLock()
	observers := append([]func(metadatamodel.MetadataSchemaSnapshot){}, s.reloadObservers...)
	s.reloadObserversMu.RUnlock()
	for _, observer := range observers {
		observer(snapshot)
	}
}

type MetadataReferenceGraphProvider interface {
	Graph(context.Context, principalmodel.Principal) (changeplanmodel.ReferenceGraph, error)
}

type MetadataAuditAppender func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)

type LifecycleRuntime interface {
	ApplyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ViewSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []metadatamodel.DictionarySchema, integrationmodel.IntegrationSchema, []reportmodel.ReportSchema, []definitionmodel.EntryPointSchema, []agentmodel.SkillSchema, []agentmodel.AgentSchema, []profilebindingmodel.Binding)
	Schema() metadatamodel.MetadataSchemaSnapshot
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
	Items(context.Context, metadatarepository.MetadataRepository, string, string, principalmodel.Principal) (metadatamodel.DictionaryItemsResult, bool, error)
}

type MetadataApplicationDependencies struct {
	Repository    metadatarepository.MetadataRepository
	Runtime       LifecycleRuntime
	Workflows     WorkflowDefinitionInitializer
	Dictionary    DictionaryRuntime
	Audit         auditcontract.AuditEventFactory
	TemplateID    string
	Version       string
	Name          string
	Records       recordrepository.RecordRepository
	Integrations  integrationrepository.IntegrationConfigRepository
	References    MetadataReferenceGraphProvider
	ChangePlans   changeplanrepository.ChangePlanRepository
	AuditAppender MetadataAuditAppender
}

func NewMetadataApplicationService(dependencies MetadataApplicationDependencies) *MetadataApplicationService {
	operations, _ := dependencies.ChangePlans.(changeplanrepository.ChangePlanOperationRepository)
	return &MetadataApplicationService{
		repository: dependencies.Repository, runtime: dependencies.Runtime, workflows: dependencies.Workflows,
		dictionary: dependencies.Dictionary, audit: dependencies.Audit, templateID: dependencies.TemplateID,
		version: dependencies.Version, name: dependencies.Name, records: dependencies.Records,
		integrations: dependencies.Integrations, references: dependencies.References, changePlans: dependencies.ChangePlans, operations: operations,
		auditAppender: dependencies.AuditAppender,
	}
}

type MetadataUpsertDefinitionOptions struct {
	Normalize func(context.Context, string, string, metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error)
}

type DisableReferenceImpact struct {
	ResourceType      string
	GraphHash         string
	DirectConsumers   int
	IndirectConsumers int
	DeletionBlocked   bool
}

type MetadataDisableDefinitionOptions struct {
	ResolveImpact func(context.Context, string, string, principalmodel.Principal) (DisableReferenceImpact, error)
	Audit         func(context.Context, string, string, principalmodel.Principal, map[string]any, map[string]any, map[string]any)
}

type RollbackReferenceImpact struct {
	GraphHash         string
	DirectConsumers   int
	IndirectConsumers int
}

type MetadataRollbackDefinitionOptions struct {
	AuthoringContractVersion string
	AuthoringContractHash    string
	ResolveImpact            func(context.Context, string, string, principalmodel.Principal) (RollbackReferenceImpact, error)
}

func (s *MetadataApplicationService) rollbackMetadataDefinitionWithOptions(ctx context.Context, resourceType, resourceKey string, request metadatamodel.MetadataDefinitionRollbackRequest, principal principalmodel.Principal, options MetadataRollbackDefinitionOptions) (metadatamodel.MetadataDefinition, metadatamodel.MetadataSchemaSnapshot, error) {
	if err := metadataAuthorizeCommand(principal); err != nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, forbidden("auth.permission_denied")
	}
	resourceType, resourceKey = strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	if code := metadatavalidation.MetadataRollbackRequestErrorCode(request); code != "" {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, badRequest(code)
	}
	if request.AuthoringContractVersion != options.AuthoringContractVersion || request.AuthoringContractHash != options.AuthoringContractHash {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, conflict("backend.metadata.rollback_contract_stale", "expected_version", options.AuthoringContractVersion, "expected_hash", options.AuthoringContractHash)
	}
	current, found, err := s.repository.GetDefinition(ctx, metadataInstallationScope("load metadata definition for rollback"), resourceType, resourceKey)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, wrapMetadataError(err)
	}
	if !found {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, notFound("backend.metadata.definition_not_found")
	}
	if options.ResolveImpact == nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, metadataInternalError("resolve metadata rollback reference impact")
	}
	impact, err := options.ResolveImpact(ctx, resourceType, resourceKey, principal)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, err
	}
	if request.ExpectedReferenceGraphHash != impact.GraphHash {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, conflict("backend.metadata.rollback_reference_graph_stale", "expected", impact.GraphHash, "actual", request.ExpectedReferenceGraphHash)
	}
	versions, err := s.repository.ListDefinitionVersions(ctx, metadataInstallationScope("list metadata rollback versions"), resourceType, resourceKey)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, wrapMetadataError(err)
	}
	var target metadatamodel.MetadataDefinitionVersion
	for _, version := range versions {
		if version.SchemaVersion == request.TargetVersion {
			target = version
			break
		}
	}
	if target.SchemaVersion == "" {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, notFound("backend.metadata.rollback_target_not_found")
	}
	if s.audit == nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, metadataInternalError("build metadata rollback audit")
	}
	audit := buildRollbackAudit(ctx, s.audit, resourceType, resourceKey, current, target, request, impact, principal)
	definition, err := s.repository.RollbackDefinition(ctx, metadataInstallationScope("rollback metadata definition"), resourceType, resourceKey, request, audit)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, wrapMetadataError(err)
	}
	snapshot, err := s.ReloadMetadata(ctx, principal)
	return definition, snapshot, err
}

func buildRollbackAudit(ctx context.Context, factory auditcontract.AuditEventFactory, resourceType, resourceKey string, current metadatamodel.MetadataDefinition, target metadatamodel.MetadataDefinitionVersion, request metadatamodel.MetadataDefinitionRollbackRequest, impact RollbackReferenceImpact, principal principalmodel.Principal) auditmodel.AuditEvent {
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

func (s *MetadataApplicationService) disableMetadataDefinitionWithOptions(ctx context.Context, resourceType, resourceKey string, principal principalmodel.Principal, options MetadataDisableDefinitionOptions) error {
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
	if _, err := s.ReloadMetadata(ctx, principal); err != nil {
		return err
	}
	if options.Audit != nil {
		options.Audit(ctx, resourceType, resourceKey, principal, DefinitionAuditValue(before, found), map[string]any{"disabled": true}, map[string]any{"reference_graph_hash": impact.GraphHash, "direct_consumers": impact.DirectConsumers})
	}
	return nil
}

func (s *MetadataApplicationService) upsertMetadataDefinitionWithOptions(ctx context.Context, resourceType, resourceKey string, request metadatamodel.MetadataDefinitionUpsertRequest, principal principalmodel.Principal, options MetadataUpsertDefinitionOptions) (metadatamodel.MetadataDefinition, metadatamodel.MetadataSchemaSnapshot, error) {
	if err := metadataAuthorizeCommand(principal); err != nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, forbidden("auth.permission_denied")
	}
	resourceType, resourceKey = strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	if resourceType == "workflow" {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, badRequest("backend.workflow.lifecycle_api_required")
	}
	if options.Normalize == nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, metadataInternalError("normalize metadata definition")
	}
	normalized, err := options.Normalize(ctx, resourceType, resourceKey, request)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, err
	}
	before, beforeFound, err := s.repository.GetDefinition(ctx, metadataInstallationScope("load metadata definition for publish"), resourceType, resourceKey)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, wrapMetadataError(err)
	}
	if err := metadataBuilderIdempotencyConflict(before, beforeFound, normalized); err != nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, err
	}
	if s.audit == nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, metadataInternalError("build metadata publication audit")
	}
	audit := s.audit.NewAuditEvent(ctx, auditcontract.AuditAppendRequest{
		Event: "metadata_definition.saved", ObjectKey: resourceType, RecordID: resourceKey, Principal: principal,
		Summary: "Saved " + resourceType + " " + resourceKey, Before: DefinitionAuditValue(before, beforeFound),
		Metadata: map[string]any{"source_kind": normalized.SourceKind, "source_id": normalized.SourceID},
	})
	definition, err := s.repository.PublishDefinition(ctx, metadataInstallationScope("publish metadata definition"), resourceType, resourceKey, normalized, audit)
	if err != nil {
		return metadatamodel.MetadataDefinition{}, metadatamodel.MetadataSchemaSnapshot{}, wrapMetadataError(err)
	}
	replayed := beforeFound && before.SchemaVersion == definition.SchemaVersion && before.SchemaHash == definition.SchemaHash
	snapshot, reloadErr := s.ReloadMetadata(ctx, principal)
	if !replayed {
		errorText := ""
		if reloadErr != nil {
			errorText = reloadErr.Error()
		}
		if completeErr := s.repository.CompleteDefinitionRefresh(ctx, metadataInstallationScope("complete metadata definition refresh"), resourceType, resourceKey, definition.SchemaHash, errorText); completeErr != nil {
			return definition, metadatamodel.MetadataSchemaSnapshot{}, metadataInternalErrorWithCause("complete metadata refresh intent", completeErr)
		}
	}
	if reloadErr != nil {
		return definition, metadatamodel.MetadataSchemaSnapshot{}, reloadErr
	}
	return definition, snapshot, nil
}

func metadataBuilderIdempotencyConflict(before metadatamodel.MetadataDefinition, found bool, request metadatamodel.MetadataDefinitionUpsertRequest) error {
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

func DefinitionAuditValue(definition metadatamodel.MetadataDefinition, found bool) map[string]any {
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
