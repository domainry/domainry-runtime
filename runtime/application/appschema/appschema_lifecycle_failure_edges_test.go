package appschema

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type metadataLifecycleFailureRepository struct {
	upsertMetadataRepository
	getErr, publishErr, completeErr, disableErr error
	versionsErr, rollbackErr, loadErr, syncErr  error
}

func (r *metadataLifecycleFailureRepository) GetDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) (appschemamodel.ApplicationDefinition, bool, error) {
	if r.getErr != nil {
		return appschemamodel.ApplicationDefinition{}, false, r.getErr
	}
	return r.upsertMetadataRepository.GetDefinition(ctx, scope, resourceType, resourceKey)
}
func (r *metadataLifecycleFailureRepository) PublishDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, request appschemamodel.ApplicationDefinitionUpsertRequest, audit auditmodel.AuditEvent) (appschemamodel.ApplicationDefinition, error) {
	if r.publishErr != nil {
		return appschemamodel.ApplicationDefinition{}, r.publishErr
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
func (r *metadataLifecycleFailureRepository) ListDefinitionVersions(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) ([]appschemamodel.ApplicationDefinitionVersion, error) {
	if r.versionsErr != nil {
		return nil, r.versionsErr
	}
	return r.upsertMetadataRepository.ListDefinitionVersions(ctx, scope, resourceType, resourceKey)
}
func (r *metadataLifecycleFailureRepository) RollbackDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, request appschemamodel.ApplicationDefinitionRollbackRequest, audit auditmodel.AuditEvent) (appschemamodel.ApplicationDefinition, error) {
	if r.rollbackErr != nil {
		return appschemamodel.ApplicationDefinition{}, r.rollbackErr
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

func metadataLifecycleService(repository *metadataLifecycleFailureRepository, audit bool, workflowErr error) *ApplicationSchemaApplicationService {
	dependencies := ApplicationSchemaDependencies{Repository: repository, Runtime: &upsertMetadataRuntime{}, Workflows: upsertWorkflowInitializer{err: workflowErr}}
	if audit {
		dependencies.Audit = auditEventFactoryStub{}
	}
	return NewApplicationSchemaApplicationService(dependencies)
}

func TestMetadataUpsertLifecycleFailureWindows(t *testing.T) {
	admin := metadataSchemaAdmin()
	nonAdmin := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: admin.UserID}}
	normalize := func(context.Context, string, string, appschemamodel.ApplicationDefinitionUpsertRequest) (appschemamodel.ApplicationDefinitionUpsertRequest, error) {
		return appschemamodel.ApplicationDefinitionUpsertRequest{}, nil
	}
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).upsertApplicationDefinitionWithOptions(t.Context(), "action", "key", appschemamodel.ApplicationDefinitionUpsertRequest{}, nonAdmin, ApplicationSchemaUpsertDefinitionOptions{Normalize: normalize}); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).upsertApplicationDefinitionWithOptions(t.Context(), "workflow", "key", appschemamodel.ApplicationDefinitionUpsertRequest{}, admin, ApplicationSchemaUpsertDefinitionOptions{Normalize: normalize}); apperror.CodeOf(err) != "backend.workflow.lifecycle_api_required" {
		t.Fatalf("workflow error=%v", err)
	}
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).upsertApplicationDefinitionWithOptions(t.Context(), "action", "key", appschemamodel.ApplicationDefinitionUpsertRequest{}, admin, ApplicationSchemaUpsertDefinitionOptions{}); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("normalize dependency error=%v", err)
	}
	edgeErr := errors.New("edge")
	for name, test := range map[string]struct {
		repository *metadataLifecycleFailureRepository
		audit      bool
		normalize  func(context.Context, string, string, appschemamodel.ApplicationDefinitionUpsertRequest) (appschemamodel.ApplicationDefinitionUpsertRequest, error)
	}{
		"normalize": {&metadataLifecycleFailureRepository{}, true, func(context.Context, string, string, appschemamodel.ApplicationDefinitionUpsertRequest) (appschemamodel.ApplicationDefinitionUpsertRequest, error) {
			return appschemamodel.ApplicationDefinitionUpsertRequest{}, edgeErr
		}},
		"get":      {&metadataLifecycleFailureRepository{getErr: edgeErr}, true, normalize},
		"audit":    {&metadataLifecycleFailureRepository{}, false, normalize},
		"publish":  {&metadataLifecycleFailureRepository{publishErr: edgeErr}, true, normalize},
		"complete": {&metadataLifecycleFailureRepository{completeErr: edgeErr, upsertMetadataRepository: upsertMetadataRepository{saved: appschemamodel.ApplicationDefinition{SchemaHash: "new", SchemaVersion: "1"}}}, true, normalize},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := metadataLifecycleService(test.repository, test.audit, nil).upsertApplicationDefinitionWithOptions(t.Context(), "action", "key", appschemamodel.ApplicationDefinitionUpsertRequest{}, admin, ApplicationSchemaUpsertDefinitionOptions{Normalize: test.normalize})
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
	if err := service.disableApplicationDefinitionWithOptions(t.Context(), "action", "key", nonAdmin, ApplicationSchemaDisableDefinitionOptions{ResolveImpact: resolve}); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	if err := service.disableApplicationDefinitionWithOptions(t.Context(), "workflow", "key", admin, ApplicationSchemaDisableDefinitionOptions{ResolveImpact: resolve}); apperror.CodeOf(err) != "backend.workflow.lifecycle_api_required" {
		t.Fatalf("workflow error=%v", err)
	}
	if err := service.disableApplicationDefinitionWithOptions(t.Context(), "action", "key", admin, ApplicationSchemaDisableDefinitionOptions{}); apperror.KindOf(err) != apperror.KindInternal {
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
			if err := metadataLifecycleService(test.repository, true, test.workflowErr).disableApplicationDefinitionWithOptions(t.Context(), "action", "key", admin, ApplicationSchemaDisableDefinitionOptions{ResolveImpact: test.resolve}); err == nil {
				t.Fatal("failure window unexpectedly succeeded")
			}
		})
	}
}

func TestMetadataRollbackLifecycleFailureWindows(t *testing.T) {
	admin := metadataSchemaAdmin()
	nonAdmin := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: admin.UserID}}
	request := appschemamodel.ApplicationDefinitionRollbackRequest{TargetVersion: "1", ExpectedSchemaHash: "current", ExpectedReferenceGraphHash: "graph", BusinessReason: "reason", ChangePlanID: "plan", AuthoringContractVersion: "v1", AuthoringContractHash: "hash"}
	options := ApplicationSchemaRollbackDefinitionOptions{AuthoringContractVersion: "v1", AuthoringContractHash: "hash", ResolveImpact: func(context.Context, string, string, principalmodel.Principal) (RollbackReferenceImpact, error) {
		return RollbackReferenceImpact{GraphHash: "graph"}, nil
	}}
	base := upsertMetadataRepository{before: appschemamodel.ApplicationDefinition{ResourceKey: "key"}, versions: []appschemamodel.ApplicationDefinitionVersion{{SchemaVersion: "1"}}, rollback: appschemamodel.ApplicationDefinition{SchemaVersion: "1"}}
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).rollbackApplicationDefinitionWithOptions(t.Context(), "action", "key", request, principalmodel.Principal{}, options); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).rollbackApplicationDefinitionWithOptions(t.Context(), "action", "key", request, nonAdmin, options); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	invalid := request
	invalid.BusinessReason = ""
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).rollbackApplicationDefinitionWithOptions(t.Context(), "action", "key", invalid, admin, options); apperror.CodeOf(err) != "backend.metadata.rollback_contract_required" {
		t.Fatalf("contract error=%v", err)
	}
	staleOptions := options
	staleOptions.AuthoringContractHash = "other"
	if _, _, err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).rollbackApplicationDefinitionWithOptions(t.Context(), "action", "key", request, admin, staleOptions); apperror.CodeOf(err) != "backend.metadata.rollback_contract_stale" {
		t.Fatalf("stale contract error=%v", err)
	}
	edgeErr := errors.New("edge")
	tests := []struct {
		name        string
		repository  *metadataLifecycleFailureRepository
		audit       bool
		options     ApplicationSchemaRollbackDefinitionOptions
		request     appschemamodel.ApplicationDefinitionRollbackRequest
		workflowErr error
	}{
		{"get", &metadataLifecycleFailureRepository{getErr: edgeErr}, true, options, request, nil},
		{"not found", &metadataLifecycleFailureRepository{}, true, options, request, nil},
		{"impact dependency", &metadataLifecycleFailureRepository{upsertMetadataRepository: base}, true, ApplicationSchemaRollbackDefinitionOptions{AuthoringContractVersion: "v1", AuthoringContractHash: "hash"}, request, nil},
		{"impact", &metadataLifecycleFailureRepository{upsertMetadataRepository: base}, true, ApplicationSchemaRollbackDefinitionOptions{AuthoringContractVersion: "v1", AuthoringContractHash: "hash", ResolveImpact: func(context.Context, string, string, principalmodel.Principal) (RollbackReferenceImpact, error) {
			return RollbackReferenceImpact{}, edgeErr
		}}, request, nil},
		{"graph", &metadataLifecycleFailureRepository{upsertMetadataRepository: base}, true, options, func() appschemamodel.ApplicationDefinitionRollbackRequest {
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
			if _, _, err := metadataLifecycleService(test.repository, test.audit, test.workflowErr).rollbackApplicationDefinitionWithOptions(t.Context(), "action", "key", test.request, admin, test.options); err == nil {
				t.Fatal("failure window unexpectedly succeeded")
			}
		})
	}
}

func TestMetadataDisableLifecycleWorkspaceAuthorization(t *testing.T) {
	if err := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil).disableApplicationDefinitionWithOptions(t.Context(), "action", "key", principalmodel.Principal{}, ApplicationSchemaDisableDefinitionOptions{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
}
