// Package auditmodule adapts Runtime identities and legacy Audit ports to the
// source-owned domainry-audit module. It contains no Audit business rules.
package auditmodule

import (
	"context"
	"fmt"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	sdkcontract "github.com/domainry/domainry-audit-sdk/contract"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type Appender struct{ binding auditsdk.Binding }

func NewAppender(binding auditsdk.Binding) *Appender { return &Appender{binding: binding} }

func ActorFromPrincipal(principal principalmodel.Principal) sdkcontract.Actor {
	kind := "user"
	if !principal.Known || principal.SystemScope.Valid() {
		kind = "system"
	}
	return sdkcontract.Actor{
		WorkspaceID: principal.WorkspaceID, SubjectID: principal.UserID,
		RoleKey: principal.RoleKey, Kind: kind, RequestID: principal.RequestID, CorrelationID: principal.CorrelationID,
		AuthorizationRevision: principal.EffectiveAuthorizationRevision(),
	}
}

func AppendRequest(request auditcontract.AuditAppendRequest) sdkcontract.AppendRequest {
	return sdkcontract.AppendRequest{
		IdempotencyKey: request.IdempotencyKey, Event: request.Event,
		ObjectKey: request.ObjectKey, RecordID: request.RecordID,
		Actor: ActorFromPrincipal(request.Principal), Summary: request.Summary,
		Before: request.Before, After: request.After, Metadata: request.Metadata,
	}
}

func (a *Appender) AppendAudit(ctx context.Context, request auditcontract.AuditAppendRequest) error {
	if a == nil || a.binding == nil {
		return fmt.Errorf("audit.binding_unavailable")
	}
	_, err := a.binding.Appender().Append(ctx, AppendRequest(request))
	return err
}

func (a *Appender) AppendAuditTelemetry(ctx context.Context, request auditcontract.AuditAppendRequest) {
	if a != nil && a.binding != nil {
		_, _ = a.binding.Appender().Append(ctx, AppendRequest(request))
	}
}

var _ auditcontract.AuditAppender = (*Appender)(nil)
var _ auditcontract.AuditTelemetryAppender = (*Appender)(nil)
