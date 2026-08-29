package contract

import (
	"context"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
)

// AuditEventFactory creates the canonical event for an owner that must persist the
// audit fact in the same transaction as its business mutation.
type AuditEventFactory interface {
	NewAuditEvent(context.Context, AuditAppendRequest) auditmodel.AuditEvent
}
