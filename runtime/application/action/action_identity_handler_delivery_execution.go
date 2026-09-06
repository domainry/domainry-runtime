package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func (e *businessActionExecution) DeliverIdentity(ctx context.Context, request runtimeext.IdentityHandlerDeliveryRequest) (runtimeext.IdentityHandlerDeliveryResult, error) {
	if e == nil || e.identityGrant == nil || !e.identityOperationGranted(request.User.Operation) {
		return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindForbidden, "identity.handler_delivery_operation_denied", nil, nil)
	}
	if e.identityDeliveryCalls != 0 {
		return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindConflict, "identity.handler_delivery_already_used", nil, nil)
	}
	request.User.User.Name = strings.TrimSpace(request.User.User.Name)
	request.User.User.ID = strings.TrimSpace(request.User.User.ID)
	request.User.User.OrgID = strings.TrimSpace(request.User.User.OrgID)
	if request.User.ExpectedVersion < 0 {
		return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindBadRequest, "identity.handler_delivery_request_invalid", nil, nil)
	}
	if request.User.User.ID != "" || request.User.User.OrgID != "" {
		return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindForbidden, "identity.handler_delivery_runtime_owned_identity_fields", nil, nil)
	}
	targetID := strings.TrimSpace(e.targetOrganization.ID)
	if e.targetGrant == nil || !e.targetResolved || targetID == "" {
		return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindBadRequest, "backend.action.target_organization_unresolved", nil, nil)
	}
	if request.User.Operation == runtimeext.IdentityHandlerCreate {
		if request.User.ExpectedVersion != 0 || request.User.LoginMode != runtimeext.IdentityHandlerLoginNone && request.User.LoginMode != runtimeext.IdentityHandlerLoginPassword {
			return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindBadRequest, "identity.handler_delivery_login_mode_invalid", nil, nil)
		}
		request.User.User.ID = stableIdentityUserID(e.workspace.ID, e.identity.ExecutionID)
		request.User.User.OrgID = targetID
	} else if request.User.ExpectedVersion < 1 || request.User.LoginMode != "" {
		return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindBadRequest, "identity.handler_delivery_login_mode_invalid", nil, nil)
	}
	var relationField string
	var embeddedProfileRecord map[string]any
	var txCtx context.Context
	createProfile := false
	if request.User.Operation != runtimeext.IdentityHandlerCreate && request.ProfileBinding == nil {
		return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindBadRequest, "identity.handler_delivery_profile_binding_required", nil, nil)
	}
	if request.ProfileBinding != nil {
		request.ProfileBinding.BindingKey = strings.TrimSpace(request.ProfileBinding.BindingKey)
		request.ProfileBinding.ObjectKey = strings.TrimSpace(request.ProfileBinding.ObjectKey)
		request.ProfileBinding.ProfileID = strings.TrimSpace(request.ProfileBinding.ProfileID)
		if request.ProfileBinding.ExpectedVersion < 0 || !e.identityProfileBindingGranted(request.ProfileBinding.BindingKey, request.ProfileBinding.ObjectKey) {
			return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindForbidden, "identity.handler_delivery_profile_binding_denied", nil, nil)
		}
		createProfile = request.ProfileBinding.CreateProfileFields != nil
		if createProfile {
			if request.User.Operation != runtimeext.IdentityHandlerCreate || request.ProfileBinding.ProfileID != "" || request.ProfileBinding.ExpectedVersion != 0 {
				return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindBadRequest, "identity.handler_delivery_profile_create_invalid", nil, nil)
			}
			request.ProfileBinding.ProfileID = stableIdentityProfileID(e.workspace.ID, e.identity.ExecutionID, request.ProfileBinding.ObjectKey)
		} else if request.ProfileBinding.ProfileID == "" {
			return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindBadRequest, "identity.handler_delivery_profile_cas_required", nil, nil)
		}
		var found bool
		if e.dependencies.ResolveProfileBindingField != nil {
			relationField, found = e.dependencies.ResolveProfileBindingField(request.ProfileBinding.BindingKey, request.ProfileBinding.ObjectKey)
		}
		if !found || strings.TrimSpace(relationField) == "" {
			return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindInternal, "identity.handler_delivery_profile_metadata_unavailable", nil, nil)
		}
		if !createProfile {
			if e.dependencies.GetRecordForUpdate == nil {
				return runtimeext.IdentityHandlerDeliveryResult{}, missingExecutorPort("get_identity_profile_for_update")
			}
			var err error
			txCtx, err = e.unitOfWork.beginWriting(ctx)
			if err != nil {
				return runtimeext.IdentityHandlerDeliveryResult{}, err
			}
			authorizationPrincipal := actionReadEffectAuthorizationPrincipal(e.invocation.Principal, e.action.EffectSet, e.action, request.ProfileBinding.ObjectKey)
			profile, err := e.dependencies.GetRecordForUpdate(txCtx, request.ProfileBinding.ObjectKey, request.ProfileBinding.ProfileID, authorizationPrincipal)
			if err != nil {
				return runtimeext.IdentityHandlerDeliveryResult{}, err
			}
			if strings.TrimSpace(profile.ID) != request.ProfileBinding.ProfileID || strings.TrimSpace(profile.OwnerOrgID) != targetID || strings.TrimSpace(profile.UpdatedAt) == "" {
				return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindForbidden, "identity.handler_delivery_profile_target_mismatch", nil, nil)
			}
			if request.User.Operation != runtimeext.IdentityHandlerCreate {
				boundUserID := strings.TrimSpace(toStringOrEmpty(profile.Data[relationField]))
				if boundUserID == "" {
					return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindConflict, "identity.handler_delivery_profile_unbound", nil, nil)
				}
				request.User.User.ID = boundUserID
				request.User.User.OrgID = targetID
			}
		}
		if createProfile {
			if txCtx == nil {
				var err error
				txCtx, err = e.unitOfWork.beginWriting(ctx)
				if err != nil {
					return runtimeext.IdentityHandlerDeliveryResult{}, err
				}
			}
			planned, err := e.planIdentityProfileCreate(txCtx, request.ProfileBinding, relationField, request.User.User.ID)
			if err != nil {
				return runtimeext.IdentityHandlerDeliveryResult{}, err
			}
			embeddedProfileRecord = recordvalidation.RecordCloneData(planned.Data)
			delete(embeddedProfileRecord, relationField)
		}
	}
	if txCtx == nil {
		var err error
		txCtx, err = e.unitOfWork.beginWriting(ctx)
		if err != nil {
			return runtimeext.IdentityHandlerDeliveryResult{}, err
		}
	}
	delivery, err := e.bindIdentityHandlerDelivery(txCtx)
	if err != nil {
		return runtimeext.IdentityHandlerDeliveryResult{}, err
	}
	e.identityDeliveryCalls++
	sdkRequest := identitysdk.HandlerDeliveryRequest{
		ContractVersion: identitysdk.HandlerDeliveryContractVersionV1,
		AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken),
		IdempotencyKey:  e.identity.ExecutionID + ":identity_handler_delivery",
		User: identitysdk.HandlerUserMutation{
			Operation:       identitysdk.HandlerUserOperation(request.User.Operation),
			User:            toSDKIdentityUser(request.User.User),
			ExpectedVersion: request.User.ExpectedVersion,
			LoginMode:       identitysdk.HandlerLoginMode(request.User.LoginMode),
		},
		RoleKeys: append([]string(nil), request.RoleKeys...),
	}
	if request.ProfileBinding != nil {
		sdkRequest.ProfileBinding = &identitysdk.HandlerProfileBindingMutation{
			BindingKey: request.ProfileBinding.BindingKey, ObjectKey: request.ProfileBinding.ObjectKey,
			ProfileID: request.ProfileBinding.ProfileID, ExpectedVersion: request.ProfileBinding.ExpectedVersion,
			Reason: strings.TrimSpace(request.ProfileBinding.Reason), ApprovalID: strings.TrimSpace(request.ProfileBinding.ApprovalID),
			EmbeddedProfileRecord: embeddedProfileRecord,
		}
	}
	sdkResult, err := delivery.DeliverIdentity(txCtx, sdkRequest)
	if err != nil {
		return runtimeext.IdentityHandlerDeliveryResult{}, normalizeIdentityCapabilityError(err, "identity.handler_delivery_failed")
	}
	if strings.TrimSpace(sdkResult.User.ID) != request.User.User.ID || strings.TrimSpace(sdkResult.User.OrgID) != targetID {
		return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindInternal, "identity.handler_delivery_target_identity_mismatch", nil, nil)
	}
	if request.ProfileBinding != nil {
		binding := sdkResult.ProfileBinding
		if binding == nil || strings.TrimSpace(binding.BindingKey) != request.ProfileBinding.BindingKey || strings.TrimSpace(binding.ObjectKey) != request.ProfileBinding.ObjectKey ||
			strings.TrimSpace(binding.ProfileID) != request.ProfileBinding.ProfileID || strings.TrimSpace(binding.IdentityUserID) != request.User.User.ID {
			return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindInternal, "identity.handler_delivery_profile_binding_mismatch", nil, nil)
		}
	}
	if credential := sdkResult.InitialCredential; credential != nil {
		if request.User.Operation != runtimeext.IdentityHandlerCreate || request.User.LoginMode != runtimeext.IdentityHandlerLoginPassword ||
			strings.TrimSpace(credential.InitialPassword) == "" || !credential.MustChangePassword || !credential.NoStore {
			return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindInternal, "identity.handler_delivery_initial_credential_invalid", nil, nil)
		}
		if e.invocation.Source != actionmodel.ActionSourceHTTP {
			return runtimeext.IdentityHandlerDeliveryResult{}, apperror.New(apperror.KindForbidden, "identity.handler_delivery_initial_credential_http_required", nil, nil)
		}
		credentialCopy := *credential
		e.initialCredential = &credentialCopy
	}
	e.identityDeliveryOK = true
	return fromSDKIdentityHandlerDeliveryResult(sdkResult), nil
}

func (e *businessActionExecution) ResolveBoundIdentity(ctx context.Context, userID string) (runtimeext.IdentityBoundIdentity, error) {
	return e.resolveBoundIdentity(ctx, userID, nil)
}

func (e *businessActionExecution) ResolveBoundIdentityProfile(ctx context.Context, userID string, selector runtimeext.IdentityHandlerProfileBindingSelector) (runtimeext.IdentityBoundIdentity, error) {
	if e == nil {
		return runtimeext.IdentityBoundIdentity{}, apperror.New(apperror.KindForbidden, "identity.handler_delivery_operation_denied", nil, nil)
	}
	selector.BindingKey = strings.TrimSpace(selector.BindingKey)
	selector.ObjectKey = strings.TrimSpace(selector.ObjectKey)
	selector.ProfileID = strings.TrimSpace(selector.ProfileID)
	if selector.ProfileID == "" {
		return runtimeext.IdentityBoundIdentity{}, apperror.New(apperror.KindBadRequest, "identity.handler_delivery_profile_selector_invalid", nil, nil)
	}
	if !e.identityProfileBindingGranted(selector.BindingKey, selector.ObjectKey) {
		return runtimeext.IdentityBoundIdentity{}, apperror.New(apperror.KindForbidden, "identity.handler_delivery_profile_binding_denied", nil, nil)
	}
	return e.resolveBoundIdentity(ctx, userID, &selector)
}

func (e *businessActionExecution) resolveBoundIdentity(ctx context.Context, userID string, selector *runtimeext.IdentityHandlerProfileBindingSelector) (runtimeext.IdentityBoundIdentity, error) {
	if e == nil || e.identityGrant == nil || !e.identityOperationGranted(runtimeext.IdentityHandlerResolve) {
		return runtimeext.IdentityBoundIdentity{}, apperror.New(apperror.KindForbidden, "identity.handler_delivery_operation_denied", nil, nil)
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return runtimeext.IdentityBoundIdentity{}, apperror.New(apperror.KindBadRequest, "identity.handler_delivery_user_required", nil, nil)
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.IdentityBoundIdentity{}, err
	}
	delivery, err := e.bindIdentityHandlerDelivery(txCtx)
	if err != nil {
		return runtimeext.IdentityBoundIdentity{}, err
	}
	e.boundIdentityCalls++
	sdkRequest := identitysdk.HandlerBoundIdentityRequest{
		ContractVersion: identitysdk.HandlerDeliveryContractVersionV1,
		AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken),
		UserID:          userID,
	}
	if selector != nil {
		sdkRequest.ProfileBinding = &identitysdk.HandlerProfileBindingSelector{BindingKey: selector.BindingKey, ObjectKey: selector.ObjectKey, ProfileID: selector.ProfileID}
	}
	resolved, err := delivery.ResolveBoundIdentity(txCtx, sdkRequest)
	if err != nil {
		return runtimeext.IdentityBoundIdentity{}, normalizeIdentityCapabilityError(err, "identity.handler_delivery_resolve_failed")
	}
	result := runtimeext.IdentityBoundIdentity{
		UserID: resolved.UserID, DisplayName: resolved.DisplayName, Status: resolved.Status, Active: resolved.Active, Version: resolved.Version,
		OrganizationID: resolved.OrganizationID, OrganizationPath: resolved.OrganizationPath, OrganizationScopeIDs: append([]string(nil), resolved.OrganizationScopeIDs...),
		SupportOrganizationID: resolved.SupportOrganizationID, SupportOrgScopeIDs: append([]string(nil), resolved.SupportOrgScopeIDs...),
		ManagerUserID: resolved.ManagerUserID, ReportingPath: resolved.ReportingPath, ReportingScopeUserIDs: append([]string(nil), resolved.ReportingScopeUserIDs...), RoleKeys: append([]string(nil), resolved.RoleKeys...),
	}
	if selector != nil && resolved.ProfileBinding != nil {
		result.ProfileBinding = &runtimeext.IdentityHandlerProfileBinding{
			BindingKey: resolved.ProfileBinding.BindingKey, ObjectKey: resolved.ProfileBinding.ObjectKey, ProfileID: resolved.ProfileBinding.ProfileID,
			IdentityUserID: resolved.ProfileBinding.IdentityUserID, Status: resolved.ProfileBinding.Status, Version: resolved.ProfileBinding.Version,
		}
	}
	if selector != nil && (result.ProfileBinding == nil || result.ProfileBinding.BindingKey != selector.BindingKey || result.ProfileBinding.ObjectKey != selector.ObjectKey ||
		result.ProfileBinding.ProfileID != selector.ProfileID || result.ProfileBinding.IdentityUserID != userID || result.ProfileBinding.Version < 1) {
		return runtimeext.IdentityBoundIdentity{}, apperror.New(apperror.KindInternal, "identity.handler_delivery_profile_resolution_mismatch", nil, nil)
	}
	return result, nil
}

func (e *businessActionExecution) bindIdentityHandlerDelivery(ctx context.Context) (identitysdk.HandlerDelivery, error) {
	if e.dependencies.BindIdentityHandlerDelivery == nil {
		return nil, apperror.New(apperror.KindInternal, "identity.handler_delivery_transaction_required", nil, nil)
	}
	if strings.TrimSpace(e.requestIdentity.AccessToken) == "" || !e.requestIdentity.Principal.Known ||
		strings.TrimSpace(e.requestIdentity.Principal.WorkspaceID) != strings.TrimSpace(e.invocation.Principal.WorkspaceID) ||
		strings.TrimSpace(e.requestIdentity.Principal.UserID) != strings.TrimSpace(e.invocation.Principal.UserID) {
		return nil, apperror.New(apperror.KindForbidden, "identity.handler_delivery_request_identity_required", nil, nil)
	}
	delivery, err := e.dependencies.BindIdentityHandlerDelivery(ctx)
	if err != nil {
		return nil, normalizeIdentityCapabilityError(err, "identity.handler_delivery_transaction_required")
	}
	if delivery == nil {
		return nil, apperror.New(apperror.KindInternal, "identity.handler_delivery_transaction_required", nil, nil)
	}
	return delivery, nil
}

func (e *businessActionExecution) identityOperationGranted(operation runtimeext.IdentityHandlerOperation) bool {
	if e == nil || e.identityGrant == nil || !operation.Valid() {
		return false
	}
	for _, granted := range e.identityGrant.Operations {
		if granted == operation {
			return true
		}
	}
	return false
}

func (e *businessActionExecution) identityProfileBindingGranted(bindingKey, objectKey string) bool {
	for _, granted := range e.identityGrant.ProfileBindings {
		if strings.TrimSpace(granted.BindingKey) == strings.TrimSpace(bindingKey) && strings.TrimSpace(granted.ObjectKey) == strings.TrimSpace(objectKey) {
			return true
		}
	}
	return false
}

func stableIdentityProfileID(workspaceID, executionID, objectKey string) string {
	digest := sha256.Sum256([]byte("runtime-action-identity-profile-v1\x00" + strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(executionID) + "\x00" + strings.TrimSpace(objectKey)))
	return "profile_" + hex.EncodeToString(digest[:16])
}

func stableIdentityUserID(workspaceID, executionID string) string {
	digest := sha256.Sum256([]byte("runtime-action-identity-user-v1\x00" + strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(executionID)))
	return "user_" + hex.EncodeToString(digest[:16])
}

func toStringOrEmpty(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func (e *businessActionExecution) planIdentityProfileCreate(ctx context.Context, binding *runtimeext.IdentityHandlerProfileBindingMutation, fieldKey, userID string) (recordmodel.Record, error) {
	if e.dependencies.PlanCreateMutation == nil {
		return recordmodel.Record{}, missingExecutorPort("plan_identity_profile_create")
	}
	fields := recordvalidation.RecordCloneData(binding.CreateProfileFields)
	if _, supplied := fields[fieldKey]; supplied {
		return recordmodel.Record{}, apperror.New(apperror.KindForbidden, "identity.handler_delivery_profile_relation_field_forbidden", nil, nil)
	}
	fields[fieldKey] = userID
	effectAuthority := actionEffectAuthority(e.action.EffectSet)
	authorizedFields := append([]string(nil), effectAuthority[binding.ObjectKey]...)
	if !containsTrimmed(authorizedFields, fieldKey) {
		authorizedFields = append(authorizedFields, fieldKey)
	}
	effectAuthority[binding.ObjectKey] = authorizedFields
	resource, operation := definitionmodel.ActionPermissionSubject(e.action)
	ctx = recordmutation.WithMutationInvocation(ctx, recordmutation.MutationInvocation{
		Source: transactionmodel.MutationSourceAction, ActionKey: e.action.Key, IdempotencyKey: e.invocation.IdempotencyKey,
		ActionResource: resource, ActionOperation: operation, EffectAuthority: effectAuthority, AssuranceEvidence: e.invocation.AssuranceEvidence,
		WorkflowTriggers: []string{"action_executed:" + e.action.Key}, TargetOrganizationID: e.targetOrganization.ID,
		ProfileBindingAuthorities: []recordmutation.ProfileBindingAuthority{{ObjectKey: binding.ObjectKey, ProfileID: binding.ProfileID, FieldKey: fieldKey, IdentityUserID: userID}},
	})
	ctx = recordservice.RecordWithPlannedIdentityUsers(ctx, userID)
	ctx = e.withPlannedRelationRecords(ctx)
	plan, record, err := e.dependencies.PlanCreateMutation(ctx, binding.ObjectKey, fields, binding.ProfileID, e.actionMutationPrincipal())
	if err != nil {
		return recordmodel.Record{}, err
	}
	if err := e.validateMutationTarget([]transactionmodel.MutationPlan{plan}); err != nil {
		return recordmodel.Record{}, err
	}
	e.plans = append(e.plans, plan)
	if canonical, ok := canonicalMutationResultRecord(runtimeext.RecordMutation{Operation: runtimeext.MutationCreate, ObjectKey: binding.ObjectKey}, []transactionmodel.MutationPlan{plan}); ok {
		record = canonical
	}
	if e.mutatedRecords == nil {
		e.mutatedRecords = map[string]recordmodel.Record{}
	}
	e.mutatedRecords[strings.TrimSpace(binding.ObjectKey)+"\x00"+strings.TrimSpace(record.ID)] = record
	e.trackRecordMutation(runtimeext.MutationCreate, binding.ObjectKey, record.ID)
	return record, nil
}

func containsTrimmed(values []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func (e *businessActionExecution) validateCapabilityCompletion(output map[string]any) error {
	if e.targetGrant != nil && !e.targetResolved {
		return apperror.New(apperror.KindBadRequest, "backend.action.target_organization_unresolved", nil, nil)
	}
	if e.identityDeliveryCalls != 0 && !e.identityDeliveryOK {
		return apperror.New(apperror.KindInternal, "identity.handler_delivery_incomplete", nil, nil)
	}
	if e.initialCredential == nil {
		return nil
	}
	field := ""
	if e.identityGrant != nil {
		field = strings.TrimSpace(e.identityGrant.InitialCredentialOutputField)
	}
	if field == "" {
		return apperror.New(apperror.KindInternal, "identity.handler_delivery_initial_credential_output_required", nil, nil)
	}
	if existing, found := output[field]; found && existing != nil && strings.TrimSpace(toString(existing)) != "" {
		return apperror.New(apperror.KindInternal, "identity.handler_delivery_initial_credential_output_occupied", nil, nil)
	}
	return nil
}

func (e *businessActionExecution) postCommit(result *actionmodel.ActionInvocationResult) {
	if e == nil || e.initialCredential == nil || result == nil || e.identityGrant == nil {
		return
	}
	field := strings.TrimSpace(e.identityGrant.InitialCredentialOutputField)
	if field == "" {
		return
	}
	value := map[string]any{
		"initial_password":     e.initialCredential.InitialPassword,
		"must_change_password": e.initialCredential.MustChangePassword,
	}
	if result.Record != nil {
		if result.Record.Output == nil {
			result.Record.Output = map[string]any{}
		}
		result.Record.Output[field] = value
		result.Record.NoStore = true
	}
	if result.Object != nil {
		if result.Object.Output == nil {
			result.Object.Output = map[string]any{}
		}
		result.Object.Output[field] = value
		result.Object.NoStore = true
	}
	result.NoStore = true
	e.initialCredential = nil
}

func toString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return "occupied"
}

func toSDKIdentityUser(value runtimeext.IdentityUser) identitysdk.User {
	return identitysdk.User{
		ID: value.ID, Name: value.Name, GivenName: value.GivenName, MiddleName: value.MiddleName, FamilyName: value.FamilyName,
		NamePrefix: value.NamePrefix, NameSuffix: value.NameSuffix, NativeName: value.NativeName, NameLocale: value.NameLocale,
		Email: value.Email, Phone: value.Phone, AccountType: value.AccountType, Locale: value.Locale, Timezone: value.Timezone,
		OrgID: value.OrgID, SupportOrgID: value.SupportOrgID, ManagerUserID: value.ManagerUserID, ReportingPath: value.ReportingPath,
		WorkerNo: value.WorkerNo, WorkerType: value.WorkerType, WorkStatus: value.WorkStatus, StartDate: value.StartDate, EndDate: value.EndDate,
		Status: value.Status, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func fromSDKIdentityUser(value identitysdk.User) runtimeext.IdentityUser {
	return runtimeext.IdentityUser{
		ID: value.ID, Name: value.Name, GivenName: value.GivenName, MiddleName: value.MiddleName, FamilyName: value.FamilyName,
		NamePrefix: value.NamePrefix, NameSuffix: value.NameSuffix, NativeName: value.NativeName, NameLocale: value.NameLocale,
		Email: value.Email, Phone: value.Phone, AccountType: value.AccountType, Locale: value.Locale, Timezone: value.Timezone,
		OrgID: value.OrgID, SupportOrgID: value.SupportOrgID, ManagerUserID: value.ManagerUserID, ReportingPath: value.ReportingPath,
		WorkerNo: value.WorkerNo, WorkerType: value.WorkerType, WorkStatus: value.WorkStatus, StartDate: value.StartDate, EndDate: value.EndDate,
		Status: value.Status, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func fromSDKIdentityHandlerDeliveryResult(value identitysdk.HandlerDeliveryResult) runtimeext.IdentityHandlerDeliveryResult {
	result := runtimeext.IdentityHandlerDeliveryResult{
		DeliveryID: value.DeliveryID, User: fromSDKIdentityUser(value.User), RoleKeys: append([]string(nil), value.RoleKeys...),
		RevokedSessions: value.RevokedSessions, Replayed: value.Replayed, InitialCredentialPending: value.InitialCredential != nil,
	}
	if value.ProfileBinding != nil {
		result.ProfileBinding = &runtimeext.IdentityHandlerProfileBinding{
			BindingKey: value.ProfileBinding.BindingKey, ObjectKey: value.ProfileBinding.ObjectKey, ProfileID: value.ProfileBinding.ProfileID,
			IdentityUserID: value.ProfileBinding.IdentityUserID, Status: value.ProfileBinding.Status, Version: value.ProfileBinding.Version,
		}
	}
	return result
}
