package action

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ActionAssuranceChallengeRequest struct {
	ActionKey string         `json:"action_key"`
	ObjectKey string         `json:"object_key"`
	RecordID  string         `json:"record_id,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type ActionAssuranceVerificationRequest struct {
	ActionAssuranceChallengeRequest
	Provider string `json:"provider"`
	State    string `json:"state"`
	Code     string `json:"code"`
}

type ActionAssuranceGrantResult struct {
	AssuranceToken string   `json:"assurance_token"`
	GrantID        string   `json:"grant_id"`
	Methods        []string `json:"methods"`
	ExpiresAt      string   `json:"expires_at"`
}

type ActionAssuranceApplicationService struct {
	actions  *ActionApplicationService
	grants   *actionservice.ActionAssuranceDomainService
	identity identitysdk.ActionAssurance
}

func NewActionAssuranceApplicationService(actions *ActionApplicationService, grants *actionservice.ActionAssuranceDomainService, identity identitysdk.ActionAssurance) *ActionAssuranceApplicationService {
	return &ActionAssuranceApplicationService{actions: actions, grants: grants, identity: identity}
}

func (service *ActionAssuranceApplicationService) Begin(ctx context.Context, request ActionAssuranceChallengeRequest, accessToken string, principal principalmodel.Principal) (identitysdk.ProviderChallenge, error) {
	_, _, err := service.prepare(request, principal)
	if err != nil {
		return identitysdk.ProviderChallenge{}, err
	}
	if service.identity == nil {
		return identitysdk.ProviderChallenge{}, actionAssuranceApplicationError(apperror.KindUnavailable, "backend.action.assurance_identity_unavailable")
	}
	return service.identity.BeginActionAssurance(ctx, identitysdk.BeginActionAssuranceRequest{WorkspaceID: identitysdk.WorkspaceID(principal.WorkspaceID), AccessToken: strings.TrimSpace(accessToken)})
}

func (service *ActionAssuranceApplicationService) Verify(ctx context.Context, request ActionAssuranceVerificationRequest, accessToken string, principal principalmodel.Principal) (ActionAssuranceGrantResult, error) {
	action, payload, err := service.prepare(request.ActionAssuranceChallengeRequest, principal)
	if err != nil {
		return ActionAssuranceGrantResult{}, err
	}
	if service.identity == nil || service.grants == nil {
		return ActionAssuranceGrantResult{}, actionAssuranceApplicationError(apperror.KindUnavailable, "backend.action.assurance_identity_unavailable")
	}
	receipt, err := service.identity.VerifyActionAssurance(ctx, identitysdk.VerifyActionAssuranceRequest{
		WorkspaceID: identitysdk.WorkspaceID(principal.WorkspaceID), AccessToken: strings.TrimSpace(accessToken),
		Provider: strings.TrimSpace(request.Provider), State: strings.TrimSpace(request.State), Code: strings.TrimSpace(request.Code),
	})
	if err != nil {
		return ActionAssuranceGrantResult{}, err
	}
	verified, err := service.identity.ValidateActionAssuranceReceipt(ctx, identitysdk.ValidateActionAssuranceReceiptRequest{Token: receipt.Token, WorkspaceID: identitysdk.WorkspaceID(principal.WorkspaceID), SubjectID: identitysdk.SubjectID(principal.UserID), AccessToken: strings.TrimSpace(accessToken)})
	if err != nil {
		return ActionAssuranceGrantResult{}, err
	}
	if verified.WorkspaceID != identitysdk.WorkspaceID(principal.WorkspaceID) || verified.SubjectID != identitysdk.SubjectID(principal.UserID) || !containsAssuranceMethod(verified.Methods, definitionmodel.ActionAssuranceOTP) {
		return ActionAssuranceGrantResult{}, actionAssuranceApplicationError(apperror.KindForbidden, "backend.action.assurance_receipt_invalid")
	}
	grantTTL, err := actionAssuranceGrantTTL(verified.ExpiresAt, time.Now().UTC())
	if err != nil {
		return ActionAssuranceGrantResult{}, err
	}
	methods := []string{definitionmodel.ActionAssuranceOTP}
	if containsAssuranceMethod(action.AssurancePolicy.RequiredMethods, definitionmodel.ActionAssuranceRecentReauth) {
		methods = append(methods, definitionmodel.ActionAssuranceRecentReauth)
	}
	token, grant, err := service.grants.IssueVerifiedGrant(ctx, actionmodel.ActionAssuranceIssueRequest{
		WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, ActionKey: action.Key, ObjectKey: action.ObjectKey,
		RecordID: strings.TrimSpace(request.RecordID), Payload: payload, VerifiedMethods: methods,
	}, grantTTL)
	if err != nil {
		return ActionAssuranceGrantResult{}, err
	}
	return ActionAssuranceGrantResult{AssuranceToken: token, GrantID: grant.ID, Methods: append([]string(nil), grant.Methods...), ExpiresAt: grant.ExpiresAt}, nil
}

func actionAssuranceGrantTTL(receiptExpiresAt string, now time.Time) (time.Duration, error) {
	expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(receiptExpiresAt))
	if err != nil || !expiresAt.After(now) {
		return 0, actionAssuranceApplicationError(apperror.KindForbidden, "backend.action.assurance_receipt_invalid")
	}
	ttl := expiresAt.Sub(now)
	if ttl > 5*time.Minute {
		ttl = 5 * time.Minute
	}
	return ttl, nil
}

func (service *ActionAssuranceApplicationService) prepare(request ActionAssuranceChallengeRequest, principal principalmodel.Principal) (definitionmodel.ActionSchema, map[string]any, error) {
	if service == nil || service.actions == nil || service.actions.dependencies.Catalog == nil {
		return definitionmodel.ActionSchema{}, nil, actionAssuranceApplicationError(apperror.KindUnavailable, "backend.action.assurance_unavailable")
	}
	if err := actionAuthorizeCommand(principal); err != nil {
		return definitionmodel.ActionSchema{}, nil, err
	}
	actionKey, objectKey, recordID := strings.TrimSpace(request.ActionKey), strings.TrimSpace(request.ObjectKey), strings.TrimSpace(request.RecordID)
	entry, ok := service.actions.dependencies.Catalog.Entry(actionKey)
	if !ok {
		return definitionmodel.ActionSchema{}, nil, actionAssuranceApplicationError(apperror.KindNotFound, "backend.action.not_found")
	}
	if entry.ResolutionError != nil {
		return definitionmodel.ActionSchema{}, nil, actionOwnerResolutionError(entry)
	}
	action := entry.Definition
	if objectKey == "" {
		objectKey, request.ObjectKey = action.ObjectKey, action.ObjectKey
	}
	if action.ObjectKey != objectKey {
		return definitionmodel.ActionSchema{}, nil, actionAssuranceApplicationError(apperror.KindBadRequest, "backend.action.object_mismatch")
	}
	if recordID == "" && !actionpolicy.ActionIsObjectKind(action.Kind) {
		return definitionmodel.ActionSchema{}, nil, actionAssuranceApplicationError(apperror.KindBadRequest, "backend.action.object_action_required")
	}
	if recordID != "" && !actionpolicy.ActionIsRecordKind(action.Kind) {
		return definitionmodel.ActionSchema{}, nil, actionAssuranceApplicationError(apperror.KindBadRequest, "backend.action.record_action_required")
	}
	if err := service.actions.dependencies.Authorization.Validate(principal, action); err != nil {
		return definitionmodel.ActionSchema{}, nil, err
	}
	if action.AssurancePolicy == nil || len(action.AssurancePolicy.RequiredMethods) == 0 || !containsOTPAssuranceRequirement(action.AssurancePolicy.RequiredMethods) {
		return definitionmodel.ActionSchema{}, nil, actionAssuranceApplicationError(apperror.KindBadRequest, "backend.action.assurance_otp_not_required")
	}
	if containsAssuranceMethod(action.AssurancePolicy.RequiredMethods, definitionmodel.ActionAssuranceMakerChecker) || containsAssuranceMethod(action.AssurancePolicy.RequiredMethods, definitionmodel.ActionAssuranceWorkflowApproval) {
		return definitionmodel.ActionSchema{}, nil, actionAssuranceApplicationError(apperror.KindConflict, "backend.action.assurance_additional_evidence_required")
	}
	payload, err := ActionNormalizePayload(action, request.Payload)
	if err != nil {
		return definitionmodel.ActionSchema{}, nil, err
	}
	return action, payload, nil
}

func containsOTPAssuranceRequirement(methods []string) bool {
	return containsAssuranceMethod(methods, definitionmodel.ActionAssuranceOTP) || containsAssuranceMethod(methods, definitionmodel.ActionAssuranceRecentReauth)
}

func containsAssuranceMethod(methods []string, expected string) bool {
	for _, method := range methods {
		if strings.TrimSpace(method) == expected {
			return true
		}
	}
	return false
}

func actionAssuranceApplicationError(kind apperror.ErrorKind, code string) error {
	return apperror.New(kind, code, nil, nil)
}
