package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type breakGlassRepositoryProbe struct {
	grants        map[string]operationsmodel.OperationsBreakGlassGrant
	createErr     error
	getErr        error
	listErr       error
	revokeErr     error
	createChanged *bool
	revokeChanged *bool
}

func (p *breakGlassRepositoryProbe) CreateOperationsBreakGlass(_ context.Context, grant operationsmodel.OperationsBreakGlassGrant) (bool, error) {
	if p.createErr != nil {
		return false, p.createErr
	}
	if p.createChanged != nil {
		return *p.createChanged, nil
	}
	for _, existing := range p.grants {
		if existing.WorkspaceID == grant.WorkspaceID && existing.Active(grant.CreatedAt) {
			return false, nil
		}
	}
	p.grants[grant.ID] = grant
	return true, nil
}
func (p *breakGlassRepositoryProbe) GetOperationsBreakGlass(_ context.Context, id string) (operationsmodel.OperationsBreakGlassGrant, bool, error) {
	if p.getErr != nil {
		return operationsmodel.OperationsBreakGlassGrant{}, false, p.getErr
	}
	grant, ok := p.grants[id]
	return grant, ok, nil
}
func (p *breakGlassRepositoryProbe) ListOperationsBreakGlass(_ context.Context, workspace string, _ int) ([]operationsmodel.OperationsBreakGlassGrant, error) {
	if p.listErr != nil {
		return nil, p.listErr
	}
	result := []operationsmodel.OperationsBreakGlassGrant{}
	for _, grant := range p.grants {
		if grant.WorkspaceID == workspace {
			result = append(result, grant)
		}
	}
	return result, nil
}
func (p *breakGlassRepositoryProbe) RevokeOperationsBreakGlass(_ context.Context, grant operationsmodel.OperationsBreakGlassGrant, revision int64) (bool, error) {
	if p.revokeErr != nil {
		return false, p.revokeErr
	}
	if p.revokeChanged != nil {
		return *p.revokeChanged, nil
	}
	existing, ok := p.grants[grant.ID]
	if !ok || existing.Revision != revision || existing.State != operationsmodel.OperationsBreakGlassActive {
		return false, nil
	}
	p.grants[grant.ID] = grant
	return true, nil
}

var errBreakGlassProbe = errors.New("break glass probe failed")

type breakGlassAlertProbe struct {
	events []string
	fail   bool
}

func (p *breakGlassAlertProbe) BreakGlassAlert(_ context.Context, event string, _ operationsmodel.OperationsBreakGlassGrant, _ principalmodel.Principal) error {
	p.events = append(p.events, event)
	if p.fail {
		return apperror.New(apperror.KindInternal, "alert.failed", nil, nil)
	}
	return nil
}

func TestOperationsBreakGlassIsTimeLimitedDualApprovedAuditedAndRevisionFenced(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	sequence := 0
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, func() time.Time { return now }, func() string { sequence++; return "id" + string(rune('0'+sequence)) })
	repository := &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{}}
	alerts := &breakGlassAlertProbe{}
	if err := service.RegisterBreakGlass(repository, alerts); err != nil {
		t.Fatal(err)
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "actor"}}, accessfixture.Bundle{Permissions: []string{"runtime.break_glass"}})
	command := OperationsBreakGlassEnableCommand{DurationSeconds: 900, ApproverIDs: []string{"approver-a", "approver-b"}, Reason: "production incident", IncidentRef: "INC-42", AlertTarget: "pager-duty-runtime"}
	enabled, err := service.EnableBreakGlass(t.Context(), command, "enable-key", principal)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Grant.Active(now) || enabled.Grant.ExpiresAt.Sub(now) != 15*time.Minute || len(alerts.events) != 1 || enabled.Receipt.Correlation != "INC-42" || len(enabled.Receipt.Evidence) != 2 {
		t.Fatalf("enabled=%#v alerts=%#v", enabled, alerts.events)
	}
	replay, err := service.EnableBreakGlass(t.Context(), command, "enable-key", principal)
	if err != nil || replay.Grant.ID != enabled.Grant.ID || len(alerts.events) != 1 {
		t.Fatalf("replay=%#v alerts=%#v err=%v", replay, alerts.events, err)
	}
	if _, err := service.DisableBreakGlass(t.Context(), enabled.Grant.ID, OperationsBreakGlassDisableCommand{ExpectedRevision: 2, Reason: "wrong revision", IncidentRef: "INC-42"}, "disable-bad", principal); apperror.CodeOf(err) != "backend.operations.break_glass_revision_conflict" {
		t.Fatalf("revision err=%v", err)
	}
	disabled, err := service.DisableBreakGlass(t.Context(), enabled.Grant.ID, OperationsBreakGlassDisableCommand{ExpectedRevision: 1, Reason: "incident ended", IncidentRef: "INC-42"}, "disable-key", principal)
	if err != nil || disabled.Grant.State != operationsmodel.OperationsBreakGlassRevoked || len(alerts.events) != 2 {
		t.Fatalf("disabled=%#v alerts=%#v err=%v", disabled, alerts.events, err)
	}
}

func TestOperationsBreakGlassRejectsSingleApprovalAndCompensatesAlertFailure(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, func() time.Time { return now }, func() string { return "fixed" })
	repository := &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{}}
	alerts := &breakGlassAlertProbe{}
	_ = service.RegisterBreakGlass(repository, alerts)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "actor"}}, accessfixture.Bundle{Permissions: []string{"runtime.break_glass"}})
	base := OperationsBreakGlassEnableCommand{DurationSeconds: 60, ApproverIDs: []string{"approver-a"}, Reason: "incident", IncidentRef: "INC-1", AlertTarget: "pager"}
	if _, err := service.EnableBreakGlass(t.Context(), base, "single", principal); apperror.CodeOf(err) != "backend.operations.break_glass_approval_invalid" {
		t.Fatalf("approval err=%v", err)
	}
	alerts.fail = true
	base.ApproverIDs = []string{"approver-a", "approver-b"}
	if _, err := service.EnableBreakGlass(t.Context(), base, "alert", principal); apperror.CodeOf(err) != "backend.operations.break_glass_alert_failed" {
		t.Fatalf("alert err=%v", err)
	}
	for _, grant := range repository.grants {
		if grant.State != operationsmodel.OperationsBreakGlassRevoked {
			t.Fatalf("uncompensated grant=%#v", grant)
		}
	}
}
