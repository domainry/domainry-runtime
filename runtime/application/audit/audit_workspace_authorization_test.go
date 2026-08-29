package audit

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type workspaceGuardAuditRepository struct{ calls int }

func (repository *workspaceGuardAuditRepository) InsertAuditEvent(context.Context, string, auditmodel.AuditEvent) error {
	repository.calls++
	return nil
}
func (repository *workspaceGuardAuditRepository) ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	repository.calls++
	return nil, nil
}
func (repository *workspaceGuardAuditRepository) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	repository.calls++
	return nil, nil
}
func (repository *workspaceGuardAuditRepository) ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	repository.calls++
	return nil, nil
}

func TestAuditApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	repository := &workspaceGuardAuditRepository{}
	service := NewAuditApplicationService(repository)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"identity.audit.view"}})
	service.Append(t.Context(), "event", "object", "record", principal, "summary", nil, nil)
	if _, err := service.Events(t.Context(), auditmodel.AuditEventQuery{}, principal); err == nil || apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("missing query workspace was not rejected: %v", err)
	}
	if _, err := service.Options(t.Context(), auditmodel.AuditOptionQuery{Field: "event"}, principal); err == nil || apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("missing option workspace was not rejected: %v", err)
	}
	if repository.calls != 0 {
		t.Fatalf("audit repository was called %d times before workspace authorization", repository.calls)
	}
}
