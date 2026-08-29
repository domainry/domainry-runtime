package service

import (
	"context"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AuditEventFactory struct{}

func (AuditEventFactory) NewAuditEvent(_ context.Context, request auditcontract.AuditAppendRequest) auditmodel.AuditEvent {
	event, err := auditmodel.BuildEvent(auditmodel.AppendRequest{IdempotencyKey: request.IdempotencyKey, Event: request.Event, ObjectKey: request.ObjectKey, RecordID: request.RecordID, Actor: runtimeAuditActor(request.Principal), Summary: request.Summary, Before: request.Before, After: request.After, Metadata: request.Metadata}, time.Now())
	if err != nil {
		return auditmodel.AuditEvent{}
	}
	return event
}
func (s *AuditDomainService) NewAuditEvent(ctx context.Context, request auditcontract.AuditAppendRequest) auditmodel.AuditEvent {
	return AuditEventFactory{}.NewAuditEvent(ctx, request)
}
func runtimeAuditActor(p principalmodel.Principal) auditmodel.Actor {
	workspace := p.WorkspaceID
	if workspace == "" {
		workspace = "default"
	}
	return auditmodel.Actor{WorkspaceID: workspace, SubjectID: p.UserID, RoleKey: p.RoleKey, Kind: "user", RequestID: p.RequestID, CorrelationID: p.CorrelationID, AuthorizationRevision: p.AuthorizationRevision}
}
func principalWorkspaceID(p principalmodel.Principal) string {
	if p.WorkspaceID != "" {
		return p.WorkspaceID
	}
	return "default"
}
