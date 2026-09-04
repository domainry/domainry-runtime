package record

import (
	"context"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	runtimeactioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func (s *RecordApplicationService) DeactivateBusinessProfile(
	ctx context.Context,
	objectKey, recordID, inactiveStatus, expectedUpdatedAt, idempotencyKey string,
	principal principalmodel.Principal,
) (recordmodel.Record, error) {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return recordmodel.Record{}, apperror.New(apperror.KindForbidden, "backend.workspace_scope_required", err, nil)
	}
	statusField, err := businessProfileDeactivationField(s, objectKey, inactiveStatus)
	if err != nil {
		return recordmodel.Record{}, err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return recordmodel.Record{}, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, nil)
	}
	ctx, err = businessProfileActionMutationContext(ctx, objectKey, statusField)
	if err != nil {
		return recordmodel.Record{}, err
	}
	patch := map[string]any{statusField: strings.TrimSpace(inactiveStatus)}
	if expectedUpdatedAt = strings.TrimSpace(expectedUpdatedAt); expectedUpdatedAt != "" {
		patch["expected_updated_at"] = expectedUpdatedAt
	}
	return s.UpdateRecordIdempotent(ctx, strings.TrimSpace(objectKey), strings.TrimSpace(recordID), patch, strings.TrimSpace(idempotencyKey), principal)
}

func (s *RecordApplicationService) ReactivateBusinessProfile(
	ctx context.Context,
	objectKey, recordID, activeStatus, expectedUpdatedAt, idempotencyKey string,
	principal principalmodel.Principal,
) (recordmodel.Record, error) {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return recordmodel.Record{}, apperror.New(apperror.KindForbidden, "backend.workspace_scope_required", err, nil)
	}
	statusField, err := businessProfileReactivationField(s, objectKey, activeStatus)
	if err != nil {
		return recordmodel.Record{}, err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return recordmodel.Record{}, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, nil)
	}
	ctx, err = businessProfileActionMutationContext(ctx, objectKey, statusField)
	if err != nil {
		return recordmodel.Record{}, err
	}
	patch := map[string]any{statusField: strings.TrimSpace(activeStatus)}
	if expectedUpdatedAt = strings.TrimSpace(expectedUpdatedAt); expectedUpdatedAt != "" {
		patch["expected_updated_at"] = expectedUpdatedAt
	}
	return s.UpdateRecordIdempotent(ctx, strings.TrimSpace(objectKey), strings.TrimSpace(recordID), patch, strings.TrimSpace(idempotencyKey), principal)
}

func businessProfileActionMutationContext(ctx context.Context, objectKey, statusField string) (context.Context, error) {
	definition, ok := runtimeactioncontract.AuthorizedActionFromContext(ctx)
	if !ok || definition.Authorization.Strategy != actioncontract.AuthorizationAuthenticated ||
		definition.Permission == nil || definition.Permission.Key != definition.Key {
		return ctx, apperror.New(apperror.KindInternal, "backend.action.authorization_context_required", nil, nil)
	}
	return recordmutation.WithMutationInvocation(ctx, recordmutation.MutationInvocation{
		Source: transactionmodel.MutationSourceAction, ActionKey: definition.Key,
		ActionResource: string(definition.Permission.ResourceKey), ActionOperation: string(definition.Permission.OperationKey),
		EffectAuthority: map[string][]string{strings.TrimSpace(objectKey): {strings.TrimSpace(statusField)}},
	}), nil
}

func businessProfileDeactivationField(s *RecordApplicationService, objectKey, inactiveStatus string) (string, error) {
	objectKey, inactiveStatus = strings.TrimSpace(objectKey), strings.TrimSpace(inactiveStatus)
	if s == nil || s.identityProfileExtensions == nil {
		return "", apperror.New(apperror.KindInternal, "backend.identity.profile_binding_unavailable", nil, nil)
	}
	for _, extension := range s.identityProfileExtensions() {
		if strings.TrimSpace(extension.ObjectKey) != objectKey {
			continue
		}
		statusField := strings.TrimSpace(extension.BusinessIdentity.StatusField)
		if statusField == "" || inactiveStatus == "" {
			return "", apperror.New(apperror.KindBadRequest, "backend.identity.profile_deactivation_status_required", nil, nil)
		}
		for _, active := range extension.BusinessIdentity.ActiveStatusValues {
			if inactiveStatus == strings.TrimSpace(active) {
				return "", apperror.New(apperror.KindBadRequest, "backend.identity.profile_deactivation_status_active", nil, nil)
			}
		}
		return statusField, nil
	}
	return "", apperror.New(apperror.KindNotFound, "backend.identity.profile_binding_definition_not_found", nil, nil)
}

func businessProfileReactivationField(s *RecordApplicationService, objectKey, activeStatus string) (string, error) {
	objectKey, activeStatus = strings.TrimSpace(objectKey), strings.TrimSpace(activeStatus)
	if s == nil || s.identityProfileExtensions == nil {
		return "", apperror.New(apperror.KindInternal, "backend.identity.profile_binding_unavailable", nil, nil)
	}
	for _, extension := range s.identityProfileExtensions() {
		if strings.TrimSpace(extension.ObjectKey) != objectKey {
			continue
		}
		statusField := strings.TrimSpace(extension.BusinessIdentity.StatusField)
		if statusField == "" || activeStatus == "" {
			return "", apperror.New(apperror.KindBadRequest, "backend.identity.profile_reactivation_status_required", nil, nil)
		}
		for _, active := range extension.BusinessIdentity.ActiveStatusValues {
			if activeStatus == strings.TrimSpace(active) {
				return statusField, nil
			}
		}
		return "", apperror.New(apperror.KindBadRequest, "backend.identity.profile_reactivation_status_inactive", nil, nil)
	}
	return "", apperror.New(apperror.KindNotFound, "backend.identity.profile_binding_definition_not_found", nil, nil)
}
