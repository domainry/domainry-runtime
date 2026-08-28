package metadata

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type metadataLifecycleFailureRepository struct {
	upsertMetadataRepository
	getErr, publishErr, completeErr, disableErr error
	versionsErr, rollbackErr, loadErr, syncErr  error
}

func (r *metadataLifecycleFailureRepository) GetDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) (metadatamodel.MetadataDefinition, bool, error) {
	if r.getErr != nil {
		return metadatamodel.MetadataDefinition{}, false, r.getErr
	}
	return r.upsertMetadataRepository.GetDefinition(ctx, scope, resourceType, resourceKey)
}
func (r *metadataLifecycleFailureRepository) PublishDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, request metadatamodel.MetadataDefinitionUpsertRequest, audit auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	if r.publishErr != nil {
		return metadatamodel.MetadataDefinition{}, r.publishErr
	}
	return r.upsertMetadataRepository.PublishDefinition(ctx, scope, resourceType, resourceKey, request, audit)
}
func (r *metadataLifecycleFailureRepository) CompleteDefinitionRefresh(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey, schemaHash, errorText string) error {
	if r.completeErr != nil {
		return r.completeErr
	}
	return r.upsertMetadataRepository.CompleteDefinitionRefresh(ctx, scope, resourceType, resourceKey, schemaHash, errorText)
}
func (r *metadataLifecycleFailureRepository) DisableDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) error {
	if r.disableErr != nil {
		return r.disableErr
	}
	return r.upsertMetadataRepository.DisableDefinition(ctx, scope, resourceType, resourceKey)
}
func (r *metadataLifecycleFailureRepository) ListDefinitionVersions(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) ([]metadatamodel.MetadataDefinitionVersion, error) {
	if r.versionsErr != nil {
		return nil, r.versionsErr
	}
	return r.upsertMetadataRepository.ListDefinitionVersions(ctx, scope, resourceType, resourceKey)
}
func (r *metadataLifecycleFailureRepository) RollbackDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, request metadatamodel.MetadataDefinitionRollbackRequest, audit auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	if r.rollbackErr != nil {
		return metadatamodel.MetadataDefinition{}, r.rollbackErr
	}
	return r.upsertMetadataRepository.RollbackDefinition(ctx, scope, resourceType, resourceKey, request, audit)
}
func (r *metadataLifecycleFailureRepository) LoadManifest(ctx context.Context, scope principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	if r.loadErr != nil {
		return manifestmodel.ManifestSchema{}, r.loadErr
	}
	return r.upsertMetadataRepository.LoadManifest(ctx, scope)
}
func (r *metadataLifecycleFailureRepository) SyncManifest(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) error {
	if r.syncErr != nil {
		return r.syncErr
	}
	return r.upsertMetadataRepository.SyncManifest(ctx, scope, manifest)
}

func metadataLifecycleService(repository *metadataLifecycleFailureRepository, audit bool, workflowErr error) *MetadataApplicationService {
	dependencies := MetadataApplicationDependencies{Repository: repository, Runtime: &upsertMetadataRuntime{}, Workflows: upsertWorkflowInitializer{err: workflowErr}}
	if audit {
		dependencies.Audit = auditEventFactoryStub{}
	}
	return NewMetadataApplicationService(dependencies)
}

func TestMetadataUpsertLifecycleFailureWindows(t *testing.T) {
	admin := metadataSchemaAdmin()
	nonAdmin := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: admin.UserID}}
	normalize := func(context.Context, string, string, metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
		return metadatamodel.MetadataDefinitionUpsertRequest{}, nil
	}
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).upsertMetadataDefinitionWithOptions(t.Context(), "action", "key", metadatamodel.MetadataDefinitionUpsertRequest{}, nonAdmin, MetadataUpsertDefinitionOptions{Normalize: normalize}); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).upsertMetadataDefinitionWithOptions(t.Context(), "workflow", "key", metadatamodel.MetadataDefinitionUpsertRequest{}, admin, MetadataUpsertDefinitionOptions{Normalize: normalize}); apperror.CodeOf(err) != "backend.workflow.lifecycle_api_required" {
		t.Fatalf("workflow error=%v", err)
	}
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).upsertMetadataDefinitionWithOptions(t.Context(), "action", "key", metadatamodel.MetadataDefinitionUpsertRequest{}, admin, MetadataUpsertDefinitionOptions{}); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("normalize dependency error=%v", err)
	}
	edgeErr := errors.New("edge")
	for name, test := range map[string]struct {
		repository *metadataLifecycleFailureRepository
		audit      bool
		normalize  func(context.Context, string, string, metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error)
	}{
		"normalize": {&metadataLifecycleFailureRepository{}, true, func(context.Context, string, string, metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
			return metadatamodel.MetadataDefinitionUpsertRequest{}, edgeErr
		}},
		"get":      {&metadataLifecycleFailureRepository{getErr: edgeErr}, true, normalize},
		"audit":    {&metadataLifecycleFailureRepository{}, false, normalize},
		"publish":  {&metadataLifecycleFailureRepository{publishErr: edgeErr}, true, normalize},
		"complete": {&metadataLifecycleFailureRepository{completeErr: edgeErr, upsertMetadataRepository: upsertMetadataRepository{saved: metadatamodel.MetadataDefinition{SchemaHash: "new", SchemaVersion: "1"}}}, true, normalize},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := metadataLifecycleService(test.repository, test.audit, nil).upsertMetadataDefinitionWithOptions(t.Context(), "action", "key", metadatamodel.MetadataDefinitionUpsertRequest{}, admin, MetadataUpsertDefinitionOptions{Normalize: test.normalize})
			if err == nil {
				t.Fatal("failure window unexpectedly succeeded")
			}
		})
	}
}

func TestMetadataDisableLifecycleFailureWindows(t *testing.T) {
	admin := metadataSchemaAdmin()
	nonAdmin := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: admin.UserID}}
	resolve := func(context.Context, string, string, principalmodel.Principal) (DisableReferenceImpact, error) {
		return DisableReferenceImpact{}, nil
	}
	service := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil)
	if err := service.disableMetadataDefinitionWithOptions(t.Context(), "action", "key", nonAdmin, MetadataDisableDefinitionOptions{ResolveImpact: resolve}); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	if err := service.disableMetadataDefinitionWithOptions(t.Context(), "workflow", "key", admin, MetadataDisableDefinitionOptions{ResolveImpact: resolve}); apperror.CodeOf(err) != "backend.workflow.lifecycle_api_required" {
		t.Fatalf("workflow error=%v", err)
	}
	if err := service.disableMetadataDefinitionWithOptions(t.Context(), "action", "key", admin, MetadataDisableDefinitionOptions{}); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("impact dependency error=%v", err)
	}
	edgeErr := errors.New("edge")
	tests := []struct {
		name        string
		repository  *metadataLifecycleFailureRepository
		resolve     func(context.Context, string, string, principalmodel.Principal) (DisableReferenceImpact, error)
		workflowErr error
	}{
		{"impact", &metadataLifecycleFailureRepository{}, func(context.Context, string, string, principalmodel.Principal) (DisableReferenceImpact, error) {
			return DisableReferenceImpact{}, edgeErr
		}, nil},
		{"get", &metadataLifecycleFailureRepository{getErr: edgeErr}, resolve, nil},
		{"disable", &metadataLifecycleFailureRepository{disableErr: edgeErr}, resolve, nil},
		{"reload", &metadataLifecycleFailureRepository{}, resolve, edgeErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := metadataLifecycleService(test.repository, true, test.workflowErr).disableMetadataDefinitionWithOptions(t.Context(), "action", "key", admin, MetadataDisableDefinitionOptions{ResolveImpact: test.resolve}); err == nil {
				t.Fatal("failure window unexpectedly succeeded")
			}
		})
	}
}

func TestMetadataRollbackLifecycleFailureWindows(t *testing.T) {
	admin := metadataSchemaAdmin()
	nonAdmin := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: admin.UserID}}
	request := metadatamodel.MetadataDefinitionRollbackRequest{TargetVersion: "1", ExpectedSchemaHash: "current", ExpectedReferenceGraphHash: "graph", BusinessReason: "reason", ChangePlanID: "plan", AuthoringContractVersion: "v1", AuthoringContractHash: "hash"}
	options := MetadataRollbackDefinitionOptions{AuthoringContractVersion: "v1", AuthoringContractHash: "hash", ResolveImpact: func(context.Context, string, string, principalmodel.Principal) (RollbackReferenceImpact, error) {
		return RollbackReferenceImpact{GraphHash: "graph"}, nil
	}}
	base := upsertMetadataRepository{before: metadatamodel.MetadataDefinition{ResourceKey: "key"}, versions: []metadatamodel.MetadataDefinitionVersion{{SchemaVersion: "1"}}, rollback: metadatamodel.MetadataDefinition{SchemaVersion: "1"}}
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).rollbackMetadataDefinitionWithOptions(t.Context(), "action", "key", request, principalmodel.Principal{}, options); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).rollbackMetadataDefinitionWithOptions(t.Context(), "action", "key", request, nonAdmin, options); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	invalid := request
	invalid.BusinessReason = ""
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).rollbackMetadataDefinitionWithOptions(t.Context(), "action", "key", invalid, admin, options); apperror.CodeOf(err) != "backend.metadata.rollback_contract_required" {
		t.Fatalf("contract error=%v", err)
	}
	staleOptions := options
	staleOptions.AuthoringContractHash = "other"
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).rollbackMetadataDefinitionWithOptions(t.Context(), "action", "key", request, admin, staleOptions); apperror.CodeOf(err) != "backend.metadata.rollback_contract_stale" {
		t.Fatalf("stale contract error=%v", err)
	}
	edgeErr := errors.New("edge")
	tests := []struct {
		name        string
		repository  *metadataLifecycleFailureRepository
		audit       bool
		options     MetadataRollbackDefinitionOptions
		request     metadatamodel.MetadataDefinitionRollbackRequest
		workflowErr error
	}{
		{"get", &metadataLifecycleFailureRepository{getErr: edgeErr}, true, options, request, nil},
		{"not found", &metadataLifecycleFailureRepository{}, true, options, request, nil},
		{"impact dependency", &metadataLifecycleFailureRepository{upsertMetadataRepository: base}, true, MetadataRollbackDefinitionOptions{AuthoringContractVersion: "v1", AuthoringContractHash: "hash"}, request, nil},
		{"impact", &metadataLifecycleFailureRepository{upsertMetadataRepository: base}, true, MetadataRollbackDefinitionOptions{AuthoringContractVersion: "v1", AuthoringContractHash: "hash", ResolveImpact: func(context.Context, string, string, principalmodel.Principal) (RollbackReferenceImpact, error) {
			return RollbackReferenceImpact{}, edgeErr
		}}, request, nil},
		{"graph", &metadataLifecycleFailureRepository{upsertMetadataRepository: base}, true, options, func() metadatamodel.MetadataDefinitionRollbackRequest {
			value := request
			value.ExpectedReferenceGraphHash = "stale"
			return value
		}(), nil},
		{"versions", &metadataLifecycleFailureRepository{upsertMetadataRepository: base, versionsErr: edgeErr}, true, options, request, nil},
		{"target", &metadataLifecycleFailureRepository{upsertMetadataRepository: upsertMetadataRepository{before: base.before}}, true, options, request, nil},
		{"audit", &metadataLifecycleFailureRepository{upsertMetadataRepository: base}, false, options, request, nil},
		{"rollback", &metadataLifecycleFailureRepository{upsertMetadataRepository: base, rollbackErr: edgeErr}, true, options, request, nil},
		{"reload", &metadataLifecycleFailureRepository{upsertMetadataRepository: base}, true, options, request, edgeErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := metadataLifecycleService(test.repository, test.audit, test.workflowErr).rollbackMetadataDefinitionWithOptions(t.Context(), "action", "key", test.request, admin, test.options); err == nil {
				t.Fatal("failure window unexpectedly succeeded")
			}
		})
	}
}

func TestMetadataDisableLifecycleWorkspaceAuthorization(t *testing.T) {
	if err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).disableMetadataDefinitionWithOptions(t.Context(), "action", "key", principalmodel.Principal{}, MetadataDisableDefinitionOptions{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
}
