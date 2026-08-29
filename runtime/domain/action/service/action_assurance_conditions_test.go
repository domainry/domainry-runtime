package service

import (
	"context"
	"errors"
	"testing"
	"time"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type actionAssuranceFaultStore struct {
	grant      actionmodel.ActionAssuranceGrant
	found      bool
	saveErr    error
	getErr     error
	consumeErr error
	consumed   bool
}

func (s *actionAssuranceFaultStore) SaveActionAssuranceGrant(_ context.Context, grant actionmodel.ActionAssuranceGrant) error {
	s.grant = grant
	return s.saveErr
}

func (s *actionAssuranceFaultStore) GetActionAssuranceGrant(context.Context, string) (actionmodel.ActionAssuranceGrant, bool, error) {
	return s.grant, s.found, s.getErr
}

func (s *actionAssuranceFaultStore) ConsumeActionAssuranceGrant(context.Context, string, string, time.Time) (bool, error) {
	return s.consumed, s.consumeErr
}

func TestIssueVerifiedGrantCoversBindingMethodsPayloadTTLRandomAndStoreFailures(t *testing.T) {
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	valid := actionmodel.ActionAssuranceIssueRequest{WorkspaceID: " workspace ", UserID: " user ", ActionKey: " action.run ", ObjectKey: " order ", RecordID: " order-1 ", Payload: map[string]any{"amount": "1.00"}, VerifiedMethods: []string{"otp"}}
	if _, _, err := (*ActionAssuranceDomainService)(nil).IssueVerifiedGrant(t.Context(), valid, time.Minute); actionAssuranceCode(err) != "backend.action.assurance_store_unavailable" {
		t.Fatalf("nil service err=%v", err)
	}
	if _, _, err := NewActionAssuranceDomainService(nil, nil).IssueVerifiedGrant(t.Context(), valid, time.Minute); actionAssuranceCode(err) != "backend.action.assurance_store_unavailable" {
		t.Fatalf("nil store err=%v", err)
	}

	bindings := []struct {
		name string
		edit func(*actionmodel.ActionAssuranceIssueRequest)
	}{
		{name: "workspace", edit: func(r *actionmodel.ActionAssuranceIssueRequest) { r.WorkspaceID = " " }},
		{name: "user", edit: func(r *actionmodel.ActionAssuranceIssueRequest) { r.UserID = " " }},
		{name: "action", edit: func(r *actionmodel.ActionAssuranceIssueRequest) { r.ActionKey = " " }},
		{name: "object", edit: func(r *actionmodel.ActionAssuranceIssueRequest) { r.ObjectKey = " " }},
	}
	for _, tc := range bindings {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			tc.edit(&r)
			if _, _, err := NewActionAssuranceDomainService(&actionAssuranceFaultStore{}, func() time.Time { return now }).IssueVerifiedGrant(t.Context(), r, time.Minute); actionAssuranceCode(err) != "backend.action.assurance_binding_invalid" {
				t.Fatalf("err=%v", err)
			}
		})
	}

	for _, methods := range [][]string{nil, {"unknown"}, {"otp", " otp "}} {
		r := valid
		r.VerifiedMethods = methods
		if _, _, err := NewActionAssuranceDomainService(&actionAssuranceFaultStore{}, func() time.Time { return now }).IssueVerifiedGrant(t.Context(), r, time.Minute); actionAssuranceCode(err) != "backend.action.assurance_method_invalid" {
			t.Fatalf("methods=%v err=%v", methods, err)
		}
	}
	r := valid
	r.Payload = map[string]any{"invalid": make(chan int)}
	if _, _, err := NewActionAssuranceDomainService(&actionAssuranceFaultStore{}, func() time.Time { return now }).IssueVerifiedGrant(t.Context(), r, time.Minute); actionAssuranceCode(err) != "backend.action.assurance_payload_invalid" {
		t.Fatalf("payload err=%v", err)
	}

	store := &actionAssuranceFaultStore{}
	service := NewActionAssuranceDomainService(store, func() time.Time { return now })
	service.random = func([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
	if _, _, err := service.IssueVerifiedGrant(t.Context(), valid, time.Minute); actionAssuranceCode(err) != "backend.action.assurance_token_unavailable" {
		t.Fatalf("random err=%v", err)
	}
	store.saveErr = errors.New("store unavailable")
	service.random = func(value []byte) (int, error) {
		for i := range value {
			value[i] = byte(i + 1)
		}
		return len(value), nil
	}
	if _, _, err := service.IssueVerifiedGrant(t.Context(), valid, 0); !errors.Is(err, store.saveErr) {
		t.Fatalf("save err=%v", err)
	}
	store.saveErr = nil
	token, grant, err := service.IssueVerifiedGrant(t.Context(), valid, 0)
	if err != nil || token == "" || grant.WorkspaceID != "workspace" || grant.ExpiresAt != now.Add(5*time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("token=%q grant=%#v err=%v", token, grant, err)
	}
}

func TestValidateAndConsumeCoversAllTokenEvidenceAndStoreBoundaries(t *testing.T) {
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	payload := map[string]any{"amount": "1.00"}
	digest, err := ActionAssurancePayloadDigest("action.run", "order", "order-1", payload)
	if err != nil {
		t.Fatal(err)
	}
	secret := "secret"
	base := actionmodel.ActionAssuranceGrant{ID: "assurance_id", TokenHash: actionAssuranceTokenHash(secret), WorkspaceID: "workspace", UserID: "user", ActionKey: "action.run", ObjectKey: "order", RecordID: "order-1", PayloadDigest: digest, Methods: []string{"otp"}, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Minute).Format(time.RFC3339Nano)}
	action := definitionmodel.ActionSchema{Key: "action.run", AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"otp"}}}
	call := func(service *ActionAssuranceDomainService, action definitionmodel.ActionSchema, payload map[string]any, token string) error {
		_, err := service.ValidateAndConsume(t.Context(), action, "workspace", "user", "order", "order-1", payload, token)
		return err
	}
	if err := call(nil, definitionmodel.ActionSchema{}, payload, ""); err != nil {
		t.Fatalf("nil policy err=%v", err)
	}
	if err := call(nil, definitionmodel.ActionSchema{AssurancePolicy: &definitionmodel.ActionAssurancePolicy{}}, payload, ""); err != nil {
		t.Fatalf("empty policy err=%v", err)
	}
	if err := call(nil, action, payload, ""); actionAssuranceCode(err) != "backend.action.assurance_store_unavailable" {
		t.Fatalf("nil service err=%v", err)
	}
	if err := call(NewActionAssuranceDomainService(nil, nil), action, payload, ""); actionAssuranceCode(err) != "backend.action.assurance_store_unavailable" {
		t.Fatalf("nil store err=%v", err)
	}

	store := &actionAssuranceFaultStore{grant: base, found: true, consumed: true}
	service := NewActionAssuranceDomainService(store, func() time.Time { return now })
	invalidTokens := []string{"", "one", "wrong.secret", "assurance_id."}
	for _, token := range invalidTokens {
		if err := call(service, action, payload, token); actionAssuranceCode(err) != "backend.action.assurance_token_invalid" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	}
	store.getErr = errors.New("read failed")
	if err := call(service, action, payload, "assurance_id."+secret); !errors.Is(err, store.getErr) {
		t.Fatalf("get err=%v", err)
	}
	store.getErr = nil
	store.found = false
	if err := call(service, action, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_token_invalid" {
		t.Fatalf("not found err=%v", err)
	}
	store.found = true
	store.grant.TokenHash = "wrong"
	if err := call(service, action, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_token_invalid" {
		t.Fatalf("hash err=%v", err)
	}
	store.grant = base
	store.grant.ConsumedAt = now.Format(time.RFC3339Nano)
	if err := call(service, action, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_token_replayed" {
		t.Fatalf("consumed err=%v", err)
	}
	store.grant = base
	store.grant.ExpiresAt = "bad"
	if err := call(service, action, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_token_expired" {
		t.Fatalf("parse expiry err=%v", err)
	}
	store.grant = base
	store.grant.ExpiresAt = now.Format(time.RFC3339Nano)
	if err := call(service, action, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_token_expired" {
		t.Fatalf("expired err=%v", err)
	}

	bindings := []struct {
		name string
		edit func(*actionmodel.ActionAssuranceGrant)
	}{
		{name: "workspace", edit: func(g *actionmodel.ActionAssuranceGrant) { g.WorkspaceID = "other" }},
		{name: "user", edit: func(g *actionmodel.ActionAssuranceGrant) { g.UserID = "other" }},
		{name: "action", edit: func(g *actionmodel.ActionAssuranceGrant) { g.ActionKey = "other" }},
		{name: "object", edit: func(g *actionmodel.ActionAssuranceGrant) { g.ObjectKey = "other" }},
		{name: "record", edit: func(g *actionmodel.ActionAssuranceGrant) { g.RecordID = "other" }},
	}
	for _, tc := range bindings {
		t.Run(tc.name, func(t *testing.T) {
			store.grant = base
			tc.edit(&store.grant)
			if err := call(service, action, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_binding_mismatch" {
				t.Fatalf("err=%v", err)
			}
		})
	}
	store.grant = base
	if err := call(service, action, map[string]any{"invalid": make(chan int)}, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_payload_changed" {
		t.Fatalf("invalid payload err=%v", err)
	}
	store.grant.PayloadDigest = "other"
	if err := call(service, action, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_payload_changed" {
		t.Fatalf("changed payload err=%v", err)
	}

	approval := action
	approval.AssurancePolicy = &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"workflow_approval"}}
	store.grant = base
	store.grant.Methods = []string{"workflow_approval"}
	store.grant.ApprovalVersion = "v1"
	if err := call(service, approval, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_approval_evidence_missing" {
		t.Fatalf("approval hash err=%v", err)
	}
	store.grant.ApprovalHash = "approval-hash"
	store.consumed = true
	if err := call(service, approval, payload, "assurance_id."+secret); err != nil {
		t.Fatalf("approval evidence err=%v", err)
	}

	reauth := action
	reauth.AssurancePolicy = &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"recent_reauth"}, RecentReauthMaxAgeSeconds: 60}
	store.grant = base
	store.grant.Methods = []string{"recent_reauth"}
	store.grant.IssuedAt = "bad"
	if err := call(service, reauth, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_reauth_expired" {
		t.Fatalf("reauth parse err=%v", err)
	}
	store.grant.IssuedAt = now.Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if err := call(service, reauth, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_reauth_expired" {
		t.Fatalf("reauth age err=%v", err)
	}

	store.grant = base
	store.consumeErr = errors.New("consume failed")
	if err := call(service, action, payload, "assurance_id."+secret); !errors.Is(err, store.consumeErr) {
		t.Fatalf("consume err=%v", err)
	}
	store.consumeErr = nil
	store.consumed = false
	if err := call(service, action, payload, "assurance_id."+secret); actionAssuranceCode(err) != "backend.action.assurance_token_replayed" {
		t.Fatalf("consume race err=%v", err)
	}
	store.consumed = true
	if evidence, err := service.ValidateAndConsume(t.Context(), action, " workspace ", " user ", " order ", " order-1 ", payload, " assurance_id."+secret+" "); err != nil || evidence.GrantID != base.ID || evidence.Facts["payload_digest"] != digest {
		t.Fatalf("evidence=%#v err=%v", evidence, err)
	}
}

func TestActionAssuranceMethodHelpersCoverSupportedDuplicateAndMissingValues(t *testing.T) {
	values := []string{definitionmodel.ActionAssuranceNormalLogin, definitionmodel.ActionAssuranceRecentReauth, definitionmodel.ActionAssuranceOTP, definitionmodel.ActionAssuranceMakerChecker, definitionmodel.ActionAssuranceWorkflowApproval}
	methods, err := actionAssuranceMethods(values)
	if err != nil || len(methods) != len(values) {
		t.Fatalf("methods=%v err=%v", methods, err)
	}
	if !containsActionAssuranceMethod([]string{" otp "}, "otp") || containsActionAssuranceMethod([]string{"otp"}, "recent_reauth") || isActionAssuranceMethod("unknown") {
		t.Fatal("method helpers returned invalid result")
	}
	assuranceErr := actionAssuranceError("code")
	typed, ok := assuranceErr.(*ActionAssuranceError)
	if !ok || typed.Error() != "code" || typed.ErrorCode() != "code" || typed.ErrorParams() != nil {
		t.Fatalf("error=%#v", assuranceErr)
	}
}
