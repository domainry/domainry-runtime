package service

import (
	"context"
	"testing"
	"time"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type actionAssuranceMemoryStore struct {
	grants map[string]actionmodel.ActionAssuranceGrant
}

func (s *actionAssuranceMemoryStore) SaveActionAssuranceGrant(_ context.Context, grant actionmodel.ActionAssuranceGrant) error {
	if s.grants == nil {
		s.grants = map[string]actionmodel.ActionAssuranceGrant{}
	}
	s.grants[grant.ID] = grant
	return nil
}
func (s *actionAssuranceMemoryStore) GetActionAssuranceGrant(_ context.Context, id string) (actionmodel.ActionAssuranceGrant, bool, error) {
	grant, ok := s.grants[id]
	return grant, ok, nil
}
func (s *actionAssuranceMemoryStore) ConsumeActionAssuranceGrant(_ context.Context, id string, now time.Time) (bool, error) {
	grant, ok := s.grants[id]
	if !ok || grant.ConsumedAt != "" {
		return false, nil
	}
	grant.ConsumedAt = now.UTC().Format(time.RFC3339Nano)
	s.grants[id] = grant
	return true, nil
}

func TestActionAssuranceTokenIsBoundAndOneTime(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	store := &actionAssuranceMemoryStore{}
	service := NewActionAssuranceDomainService(store, func() time.Time { return now })
	service.random = func(buffer []byte) (int, error) {
		for index := range buffer {
			buffer[index] = byte(index + 1)
		}
		return len(buffer), nil
	}
	payload := map[string]any{"amount": "100.00", "reason": "refund"}
	token, grant, err := service.IssueVerifiedGrant(t.Context(), actionmodel.ActionAssuranceIssueRequest{WorkspaceID: "workspace-a", UserID: "user-a", ActionKey: "payment.refund", ObjectKey: "payment", RecordID: "payment-1", Payload: payload, VerifiedMethods: []string{"otp", "recent_reauth"}}, 5*time.Minute)
	if err != nil || token == "" || grant.TokenHash == "" {
		t.Fatalf("token=%q grant=%#v err=%v", token, grant, err)
	}
	action := definitionmodel.ActionSchema{Key: "payment.refund", AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"recent_reauth", "otp"}}}
	if _, err := service.ValidateAndConsume(t.Context(), action, "workspace-a", "user-a", "payment", "other-payment", payload, token); actionAssuranceCode(err) != "backend.action.assurance_binding_mismatch" {
		t.Fatalf("other record err=%v", err)
	}
	if _, err := service.ValidateAndConsume(t.Context(), action, "workspace-a", "user-a", "payment", "payment-1", map[string]any{"amount": "101.00", "reason": "refund"}, token); actionAssuranceCode(err) != "backend.action.assurance_payload_changed" {
		t.Fatalf("changed payload err=%v", err)
	}
	evidence, err := service.ValidateAndConsume(t.Context(), action, "workspace-a", "user-a", "payment", "payment-1", payload, token)
	if err != nil || evidence.GrantID != grant.ID || evidence.Facts["payload_digest"] == "" {
		t.Fatalf("evidence=%#v err=%v", evidence, err)
	}
	if _, err := service.ValidateAndConsume(t.Context(), action, "workspace-a", "user-a", "payment", "payment-1", payload, token); actionAssuranceCode(err) != "backend.action.assurance_token_replayed" {
		t.Fatalf("replay err=%v", err)
	}
}

func TestActionAssuranceRejectsExpiryMissingMethodAndApprovalEvidence(t *testing.T) {
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	store := &actionAssuranceMemoryStore{}
	service := NewActionAssuranceDomainService(store, func() time.Time { return now })
	service.random = func(buffer []byte) (int, error) {
		for index := range buffer {
			buffer[index] = byte(100 + index)
		}
		return len(buffer), nil
	}
	request := actionmodel.ActionAssuranceIssueRequest{WorkspaceID: "workspace-a", UserID: "user-a", ActionKey: "export.full", ObjectKey: "customer", Payload: map[string]any{"scope": "all"}, VerifiedMethods: []string{"otp"}}
	token, _, err := service.IssueVerifiedGrant(t.Context(), request, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	action := definitionmodel.ActionSchema{Key: request.ActionKey, AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"otp", "workflow_approval"}}}
	if _, err := service.ValidateAndConsume(t.Context(), action, request.WorkspaceID, request.UserID, request.ObjectKey, "", request.Payload, token); actionAssuranceCode(err) != "backend.action.assurance_method_missing" {
		t.Fatalf("missing method err=%v", err)
	}
	token, _, err = service.IssueVerifiedGrant(t.Context(), actionmodel.ActionAssuranceIssueRequest{WorkspaceID: request.WorkspaceID, UserID: request.UserID, ActionKey: request.ActionKey, ObjectKey: request.ObjectKey, Payload: request.Payload, VerifiedMethods: []string{"otp", "workflow_approval"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateAndConsume(t.Context(), action, request.WorkspaceID, request.UserID, request.ObjectKey, "", request.Payload, token); actionAssuranceCode(err) != "backend.action.assurance_approval_evidence_missing" {
		t.Fatalf("approval err=%v", err)
	}
	now = now.Add(2 * time.Minute)
	expiredToken, _, err := service.IssueVerifiedGrant(t.Context(), request, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Minute)
	if _, err := service.ValidateAndConsume(t.Context(), definitionmodel.ActionSchema{Key: request.ActionKey, AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"otp"}}}, request.WorkspaceID, request.UserID, request.ObjectKey, "", request.Payload, expiredToken); actionAssuranceCode(err) != "backend.action.assurance_token_expired" {
		t.Fatalf("expired err=%v", err)
	}
}

func actionAssuranceCode(err error) string {
	if typed, ok := err.(*ActionAssuranceError); ok {
		return typed.Code
	}
	return ""
}
