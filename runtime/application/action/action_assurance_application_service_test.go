package action

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type actionAssuranceIdentityProbe struct {
	beginRequest     identitysdk.BeginActionAssuranceRequest
	verifyRequest    identitysdk.VerifyActionAssuranceRequest
	validateRequest  identitysdk.ValidateActionAssuranceReceiptRequest
	validateErr      error
	receiptExpiresAt string
}

func (p *actionAssuranceIdentityProbe) BeginActionAssurance(_ context.Context, request identitysdk.BeginActionAssuranceRequest) (identitysdk.ProviderChallenge, error) {
	p.beginRequest = request
	return identitysdk.ProviderChallenge{Provider: "sms", State: "challenge-a", Purpose: "action_assurance", Status: identitysdk.ChallengeStatusActive, ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}, nil
}

func (p *actionAssuranceIdentityProbe) VerifyActionAssurance(_ context.Context, request identitysdk.VerifyActionAssuranceRequest) (identitysdk.ActionAssuranceReceipt, error) {
	p.verifyRequest = request
	return identitysdk.ActionAssuranceReceipt{Token: "identity-signed-receipt", WorkspaceID: request.WorkspaceID, SubjectID: "user-a", Methods: []string{"otp"}, ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}, nil
}

func (p *actionAssuranceIdentityProbe) ValidateActionAssuranceReceipt(_ context.Context, request identitysdk.ValidateActionAssuranceReceiptRequest) (identitysdk.ActionAssuranceReceipt, error) {
	p.validateRequest = request
	if p.validateErr != nil {
		return identitysdk.ActionAssuranceReceipt{}, p.validateErr
	}
	expiresAt := p.receiptExpiresAt
	if expiresAt == "" {
		expiresAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	}
	return identitysdk.ActionAssuranceReceipt{Token: request.Token, WorkspaceID: request.WorkspaceID, SubjectID: request.SubjectID, Methods: []string{"otp"}, ExpiresAt: expiresAt}, nil
}

type actionAssuranceGrantStore struct {
	grants map[string]actionmodel.ActionAssuranceGrant
}

var _ actioncontract.ActionAssuranceStore = (*actionAssuranceGrantStore)(nil)

func (s *actionAssuranceGrantStore) SaveActionAssuranceGrant(_ context.Context, grant actionmodel.ActionAssuranceGrant) error {
	if s.grants == nil {
		s.grants = map[string]actionmodel.ActionAssuranceGrant{}
	}
	s.grants[grant.ID] = grant
	return nil
}

func (s *actionAssuranceGrantStore) GetActionAssuranceGrant(_ context.Context, id string) (actionmodel.ActionAssuranceGrant, bool, error) {
	grant, ok := s.grants[id]
	return grant, ok, nil
}

func (s *actionAssuranceGrantStore) ConsumeActionAssuranceGrant(_ context.Context, workspaceID, id string, now time.Time) (bool, error) {
	grant, ok := s.grants[id]
	if !ok || grant.WorkspaceID != workspaceID || grant.ConsumedAt != "" {
		return false, nil
	}
	grant.ConsumedAt = now.UTC().Format(time.RFC3339Nano)
	s.grants[id] = grant
	return true, nil
}

func TestActionAssuranceApplicationVerifiesIdentityReceiptAndIssuesBoundOneTimeGrant(t *testing.T) {
	events := []string{}
	handler := newGovernedHandlerProbe(&events)
	store := &governedExecutionStoreProbe{events: &events}
	application := newGovernedHandlerApplication(t, handler, ActionAuthorization{}, ActionAssurance{}, store)
	entry, ok := application.dependencies.Catalog.Entry("booking.reserve")
	if !ok {
		t.Fatal("missing action entry")
	}
	entry.Definition.AssurancePolicy = &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceOTP}}
	application.dependencies.Catalog.entries[entry.Definition.Key] = entry

	now := time.Now().UTC()
	grantStore := &actionAssuranceGrantStore{}
	grants := actionservice.NewActionAssuranceDomainService(grantStore, func() time.Time { return now })
	receiptExpiresAt := now.Add(30 * time.Second)
	identity := &actionAssuranceIdentityProbe{receiptExpiresAt: receiptExpiresAt.Format(time.RFC3339Nano)}
	service := NewActionAssuranceApplicationService(application, grants, identity)
	principal := actionTestPrincipal("booking.reserve")
	principal.UserID = "user-a"
	request := ActionAssuranceChallengeRequest{ActionKey: "booking.reserve", ObjectKey: "booking", Payload: map[string]any{"amount": 12.5}}

	challenge, err := service.Begin(t.Context(), request, "access-token-a", principal)
	if err != nil || challenge.State == "" || identity.beginRequest.AccessToken != "access-token-a" || identity.beginRequest.WorkspaceID != "workspace-a" {
		t.Fatalf("challenge=%#v begin=%#v err=%v", challenge, identity.beginRequest, err)
	}
	grant, err := service.Verify(t.Context(), ActionAssuranceVerificationRequest{
		ActionAssuranceChallengeRequest: request, Provider: "sms", State: challenge.State, Code: "123456",
	}, "access-token-a", principal)
	if err != nil || grant.AssuranceToken == "" || grant.GrantID == "" || identity.verifyRequest.AccessToken != "access-token-a" {
		t.Fatalf("grant=%#v verify=%#v err=%v", grant, identity.verifyRequest, err)
	}
	if identity.validateRequest.Token != "identity-signed-receipt" || identity.validateRequest.SubjectID != "user-a" || identity.validateRequest.AccessToken != "access-token-a" || len(grantStore.grants) != 1 {
		t.Fatalf("validate=%#v grants=%#v", identity.validateRequest, grantStore.grants)
	}
	persistedGrant := grantStore.grants[grant.GrantID]
	persistedExpiry, parseErr := time.Parse(time.RFC3339Nano, persistedGrant.ExpiresAt)
	if parseErr != nil || persistedExpiry.After(receiptExpiresAt) {
		t.Fatalf("runtime grant outlived identity receipt: grant=%s receipt=%s err=%v", persistedGrant.ExpiresAt, receiptExpiresAt, parseErr)
	}

	application.dependencies.Assurance.Validate = func(ctx context.Context, invocation actionmodel.ActionInvocation) (map[string]string, error) {
		entry, _ := application.dependencies.Catalog.Entry(invocation.ActionKey)
		evidence, err := grants.ValidateAndConsume(ctx, entry.Definition, invocation.Principal.WorkspaceID, invocation.Principal.UserID, invocation.ObjectKey, invocation.RecordID, invocation.Input, invocation.AssuranceToken)
		if err != nil {
			return nil, err
		}
		return map[string]string{"grant_id": evidence.GrantID}, nil
	}
	invocation := governedHandlerInvocation(map[string]any{"amount": 12.5})
	invocation.Principal = principal
	invocation.AssuranceToken = grant.AssuranceToken
	if result, err := application.Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation); err != nil || result.Object == nil || handler.invoked != 1 {
		t.Fatalf("result=%#v invoked=%d err=%v", result, handler.invoked, err)
	}
	invocation.IdempotencyKey = "command-2"
	if _, err := application.Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation); err == nil || handler.invoked != 1 {
		t.Fatalf("replayed assurance err=%v invoked=%d", err, handler.invoked)
	}
}

func TestActionAssuranceApplicationRejectsExpiredOrMalformedIdentityReceipt(t *testing.T) {
	events := []string{}
	application := newGovernedHandlerApplication(t, newGovernedHandlerProbe(&events), ActionAuthorization{}, ActionAssurance{}, &governedExecutionStoreProbe{events: &events})
	entry, _ := application.dependencies.Catalog.Entry("booking.reserve")
	entry.Definition.AssurancePolicy = &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceOTP}}
	application.dependencies.Catalog.entries[entry.Definition.Key] = entry
	principal := actionTestPrincipal("booking.reserve")
	principal.UserID = "user-a"
	request := ActionAssuranceVerificationRequest{ActionAssuranceChallengeRequest: ActionAssuranceChallengeRequest{ActionKey: "booking.reserve", ObjectKey: "booking", Payload: map[string]any{"amount": 12.5}}, Provider: "sms", State: "challenge-a", Code: "123456"}
	for _, expiresAt := range []string{"not-a-time", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)} {
		grantStore := &actionAssuranceGrantStore{}
		identity := &actionAssuranceIdentityProbe{receiptExpiresAt: expiresAt}
		service := NewActionAssuranceApplicationService(application, actionservice.NewActionAssuranceDomainService(grantStore, time.Now), identity)
		if _, err := service.Verify(t.Context(), request, "access-token-a", principal); err == nil || len(grantStore.grants) != 0 {
			t.Fatalf("expires_at=%q err=%v grants=%#v", expiresAt, err, grantStore.grants)
		}
	}
}

func TestActionAssuranceApplicationDoesNotMintGrantWhenIdentityValidationFails(t *testing.T) {
	events := []string{}
	application := newGovernedHandlerApplication(t, newGovernedHandlerProbe(&events), ActionAuthorization{}, ActionAssurance{}, &governedExecutionStoreProbe{events: &events})
	entry, _ := application.dependencies.Catalog.Entry("booking.reserve")
	entry.Definition.AssurancePolicy = &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceOTP}}
	application.dependencies.Catalog.entries[entry.Definition.Key] = entry
	grantStore := &actionAssuranceGrantStore{}
	failure := errors.New("identity receipt rejected")
	identity := &actionAssuranceIdentityProbe{validateErr: failure}
	service := NewActionAssuranceApplicationService(application, actionservice.NewActionAssuranceDomainService(grantStore, time.Now), identity)
	principal := actionTestPrincipal("booking.reserve")
	principal.UserID = "user-a"
	_, err := service.Verify(t.Context(), ActionAssuranceVerificationRequest{
		ActionAssuranceChallengeRequest: ActionAssuranceChallengeRequest{ActionKey: "booking.reserve", ObjectKey: "booking", Payload: map[string]any{"amount": 12.5}},
		Provider:                        "sms", State: "challenge-a", Code: "123456",
	}, "access-token-a", principal)
	if !errors.Is(err, failure) || len(grantStore.grants) != 0 {
		t.Fatalf("err=%v grants=%#v", err, grantStore.grants)
	}
}
