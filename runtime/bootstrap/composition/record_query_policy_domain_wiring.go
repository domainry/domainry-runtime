package composition

import (
	"context"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	pipelineapplication "github.com/domainry/domainry-runtime/runtime/application/pipeline"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func (a recordQueryPolicyAdapter) normalizeListQuery(object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) recordmodel.RecordListQuery {
	return a.service.NormalizeListQuery(object, query, principal)
}

func (a recordQueryPolicyAdapter) canAccessRecord(principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return a.service.CanAccessRecord(principal, object, record)
}

func (a recordQueryPolicyAdapter) canAccessPersistedRecord(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) (bool, error) {
	authorized, err := relationReadEffectPrincipal(ctx, principal, object.Key)
	if err != nil {
		return false, err
	}
	return a.service.CanAccessPersistedRecordScope(ctx, authorized, object, record, false)
}

func (a recordQueryPolicyAdapter) canWriteRecordScope(principal principalmodel.Principal, object definitionmodel.ObjectSchema, data map[string]any) bool {
	return a.service.CanWriteRecordScope(principal, object, data)
}

func (a recordReadPolicyAdapter) ObjectForAction(principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
	return a.policy.objectForAction(principal, objectKey, action)
}

func (a recordReadPolicyAdapter) EnsureReportSnapshotAccess(object definitionmodel.ObjectSchema, action string, principal principalmodel.Principal) error {
	return a.policy.ensureReportSnapshotAccess(object, action, principal)
}

func (a recordReadPolicyAdapter) NormalizeListQuery(object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) recordmodel.RecordListQuery {
	return a.policy.normalizeListQuery(object, query, principal)
}

func (a recordReadPolicyAdapter) CanAccessRecord(principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return a.policy.canAccessRecord(principal, object, record)
}

type recordMutationPolicy interface {
	validatePipelineDefaults(context.Context, definitionmodel.ObjectSchema, string, map[string]any, principalmodel.Principal) error
	applyPipelineItemDefaults(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal, bool) error
	validateRelationReferences(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error
	validateDomainPolicies(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error
	validateUnique(context.Context, string, string, definitionmodel.ObjectSchema, string, map[string]any) error
	validateDuplicateIdentity(context.Context, string, definitionmodel.ObjectSchema, string, map[string]any) error
}

type recordMutationPolicyAdapter struct {
	pipeline   *pipelineapplication.PipelineApplicationService
	validation *recordservice.RecordValidationDomainService
}

func (a recordMutationPolicyAdapter) validatePipelineDefaults(ctx context.Context, object definitionmodel.ObjectSchema, recordID string, data map[string]any, principal principalmodel.Principal) error {
	return a.pipeline.ValidateDefaults(ctx, object, recordID, data, principal)
}

func (a recordMutationPolicyAdapter) applyPipelineItemDefaults(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal, overwriteSLA bool) error {
	return a.pipeline.ApplyItemDefaults(ctx, object, data, principal, overwriteSLA)
}

func (a recordMutationPolicyAdapter) validateRelationReferences(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal) error {
	return a.validation.ValidateRelations(ctx, object, data, principal)
}

func (a recordMutationPolicyAdapter) validateDomainPolicies(ctx context.Context, object definitionmodel.ObjectSchema, before, next map[string]any, recordID, operation string, principal principalmodel.Principal) error {
	return a.validation.ValidateDomainPolicies(ctx, object, before, next, recordID, operation, principal)
}

func (a recordMutationPolicyAdapter) validateUnique(ctx context.Context, workspaceID, objectKey string, object definitionmodel.ObjectSchema, currentID string, data map[string]any) error {
	return a.validation.ValidateUnique(ctx, workspaceID, objectKey, object, currentID, data)
}

func (a recordMutationPolicyAdapter) validateDuplicateIdentity(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, currentID string, data map[string]any) error {
	return a.validation.ValidateDuplicateIdentity(ctx, workspaceID, object, currentID, data)
}

type recordApplicationRuntimeAdapter struct{ records *runtimeAssembly }

// relationReadEffectPrincipal authorizes relation validation with the read
// effect explicitly delegated by the source Action. The derived principal is
// local to this check, so ordinary record browsing keeps the caller's scope.
func relationReadEffectPrincipal(ctx context.Context, principal principalmodel.Principal, objectKey string) (principalmodel.Principal, error) {
	invocation, ok := recordmutation.MutationInvocationFromContext(ctx)
	if !ok || invocation.Source != transactionmodel.MutationSourceAction || principal.AccessBundle == nil {
		return principal, nil
	}
	objectKey = strings.TrimSpace(objectKey)
	if !invocation.ReadEffectAuthority[objectKey] {
		return principal, nil
	}
	bundle, err := identitysdk.DeriveExecutionAccess(*principal.AccessBundle, identitysdk.ExecutionGrant{
		Resource: identitysdk.ResourceType(objectKey), Action: identitysdk.Action("read"),
		SourceResource: identitysdk.ResourceType(invocation.ActionResource), SourceAction: identitysdk.Action(invocation.ActionOperation),
	}, time.Now().UTC())
	if err != nil {
		return principal, err
	}
	authorized := principal
	authorized.AccessBundle = &bundle
	return authorized, nil
}
