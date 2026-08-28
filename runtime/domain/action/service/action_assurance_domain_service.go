package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type ActionAssuranceError struct{ Code string }

func (e *ActionAssuranceError) Error() string                  { return e.Code }
func (e *ActionAssuranceError) ErrorCode() string              { return e.Code }
func (e *ActionAssuranceError) ErrorParams() map[string]string { return nil }

type ActionAssuranceDomainService struct {
	store  actioncontract.ActionAssuranceStore
	now    func() time.Time
	random func([]byte) (int, error)
}

func NewActionAssuranceDomainService(store actioncontract.ActionAssuranceStore, now func() time.Time) *ActionAssuranceDomainService {
	if now == nil {
		now = time.Now
	}
	return &ActionAssuranceDomainService{store: store, now: now, random: rand.Read}
}

// IssueVerifiedGrant is an internal boundary. Its methods must already have
// been verified by the owning auth/workflow/maker-checker adapters.
func (s *ActionAssuranceDomainService) IssueVerifiedGrant(ctx context.Context, request actionmodel.ActionAssuranceIssueRequest, ttl time.Duration) (string, actionmodel.ActionAssuranceGrant, error) {
	if s == nil || s.store == nil {
		return "", actionmodel.ActionAssuranceGrant{}, actionAssuranceError("backend.action.assurance_store_unavailable")
	}
	request.WorkspaceID, request.UserID, request.ActionKey, request.ObjectKey, request.RecordID = strings.TrimSpace(request.WorkspaceID), strings.TrimSpace(request.UserID), strings.TrimSpace(request.ActionKey), strings.TrimSpace(request.ObjectKey), strings.TrimSpace(request.RecordID)
	if request.WorkspaceID == "" || request.UserID == "" || request.ActionKey == "" || request.ObjectKey == "" {
		return "", actionmodel.ActionAssuranceGrant{}, actionAssuranceError("backend.action.assurance_binding_invalid")
	}
	methods, err := actionAssuranceMethods(request.VerifiedMethods)
	if err != nil {
		return "", actionmodel.ActionAssuranceGrant{}, err
	}
	digest, err := ActionAssurancePayloadDigest(request.ActionKey, request.ObjectKey, request.RecordID, request.Payload)
	if err != nil {
		return "", actionmodel.ActionAssuranceGrant{}, err
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	secret := make([]byte, 32)
	if _, err := s.random(secret); err != nil {
		return "", actionmodel.ActionAssuranceGrant{}, actionAssuranceError("backend.action.assurance_token_unavailable")
	}
	now := s.now().UTC()
	secretText := hex.EncodeToString(secret)
	grant := actionmodel.ActionAssuranceGrant{
		ID: "assurance_" + secretText[:24], TokenHash: actionAssuranceTokenHash(secretText), WorkspaceID: request.WorkspaceID, UserID: request.UserID,
		ActionKey: request.ActionKey, ObjectKey: request.ObjectKey, RecordID: request.RecordID, PayloadDigest: digest, Methods: methods,
		ApprovalVersion: strings.TrimSpace(request.ApprovalVersion), ApprovalHash: strings.TrimSpace(request.ApprovalHash), IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(ttl).Format(time.RFC3339Nano),
	}
	if err := s.store.SaveActionAssuranceGrant(ctx, grant); err != nil {
		return "", actionmodel.ActionAssuranceGrant{}, err
	}
	return grant.ID + "." + secretText, grant, nil
}

func (s *ActionAssuranceDomainService) ValidateAndConsume(ctx context.Context, action definitionmodel.ActionSchema, workspaceID, userID, objectKey, recordID string, payload map[string]any, token string) (actionmodel.ActionAssuranceEvidence, error) {
	if action.AssurancePolicy == nil || len(action.AssurancePolicy.RequiredMethods) == 0 {
		return actionmodel.ActionAssuranceEvidence{}, nil
	}
	if s == nil || s.store == nil {
		return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_store_unavailable")
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "assurance_") || parts[1] == "" {
		return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_token_invalid")
	}
	grant, found, err := s.store.GetActionAssuranceGrant(ctx, parts[0])
	if err != nil {
		return actionmodel.ActionAssuranceEvidence{}, err
	}
	if !found || grant.TokenHash != actionAssuranceTokenHash(parts[1]) {
		return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_token_invalid")
	}
	now := s.now().UTC()
	if grant.ConsumedAt != "" {
		return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_token_replayed")
	}
	expiresAt, parseErr := time.Parse(time.RFC3339Nano, grant.ExpiresAt)
	if parseErr != nil || !now.Before(expiresAt) {
		return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_token_expired")
	}
	if grant.WorkspaceID != strings.TrimSpace(workspaceID) || grant.UserID != strings.TrimSpace(userID) || grant.ActionKey != strings.TrimSpace(action.Key) || grant.ObjectKey != strings.TrimSpace(objectKey) || grant.RecordID != strings.TrimSpace(recordID) {
		return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_binding_mismatch")
	}
	digest, err := ActionAssurancePayloadDigest(action.Key, objectKey, recordID, payload)
	if err != nil || grant.PayloadDigest != digest {
		return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_payload_changed")
	}
	verified := map[string]bool{}
	for _, method := range grant.Methods {
		verified[method] = true
	}
	verified[definitionmodel.ActionAssuranceNormalLogin] = true
	for _, required := range action.AssurancePolicy.RequiredMethods {
		if !verified[strings.TrimSpace(required)] {
			return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_method_missing")
		}
	}
	if containsActionAssuranceMethod(action.AssurancePolicy.RequiredMethods, definitionmodel.ActionAssuranceWorkflowApproval) && (grant.ApprovalVersion == "" || grant.ApprovalHash == "") {
		return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_approval_evidence_missing")
	}
	if containsActionAssuranceMethod(action.AssurancePolicy.RequiredMethods, definitionmodel.ActionAssuranceRecentReauth) {
		issuedAt, parseErr := time.Parse(time.RFC3339Nano, grant.IssuedAt)
		if parseErr != nil || now.Sub(issuedAt) > time.Duration(action.AssurancePolicy.RecentReauthMaxAgeSeconds)*time.Second {
			return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_reauth_expired")
		}
	}
	consumed, err := s.store.ConsumeActionAssuranceGrant(ctx, grant.ID, now)
	if err != nil {
		return actionmodel.ActionAssuranceEvidence{}, err
	}
	if !consumed {
		return actionmodel.ActionAssuranceEvidence{}, actionAssuranceError("backend.action.assurance_token_replayed")
	}
	return actionmodel.ActionAssuranceEvidence{GrantID: grant.ID, Methods: append([]string(nil), grant.Methods...), ApprovalVersion: grant.ApprovalVersion, ApprovalHash: grant.ApprovalHash, Facts: map[string]string{"payload_digest": grant.PayloadDigest, "expires_at": grant.ExpiresAt}}, nil
}

func ActionAssurancePayloadDigest(actionKey, objectKey, recordID string, payload map[string]any) (string, error) {
	value, err := idempotency.Fingerprint(idempotency.FingerprintInput{UseCase: "action.assurance", ResourceType: "action_invocation", TargetID: strings.TrimSpace(actionKey) + "/" + strings.TrimSpace(objectKey) + "/" + strings.TrimSpace(recordID), Payload: payload})
	if err != nil {
		return "", actionAssuranceError("backend.action.assurance_payload_invalid")
	}
	return value, nil
}

func actionAssuranceMethods(values []string) ([]string, error) {
	seen := map[string]bool{}
	methods := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !isActionAssuranceMethod(value) || seen[value] {
			return nil, actionAssuranceError("backend.action.assurance_method_invalid")
		}
		seen[value], methods = true, append(methods, value)
	}
	if len(methods) == 0 {
		return nil, actionAssuranceError("backend.action.assurance_method_invalid")
	}
	sort.Strings(methods)
	return methods, nil
}

func isActionAssuranceMethod(value string) bool {
	switch value {
	case definitionmodel.ActionAssuranceNormalLogin, definitionmodel.ActionAssuranceRecentReauth, definitionmodel.ActionAssuranceOTP, definitionmodel.ActionAssuranceMakerChecker, definitionmodel.ActionAssuranceWorkflowApproval:
		return true
	}
	return false
}

func containsActionAssuranceMethod(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}
func actionAssuranceTokenHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
func actionAssuranceError(code string) error { return &ActionAssuranceError{Code: fmt.Sprint(code)} }
