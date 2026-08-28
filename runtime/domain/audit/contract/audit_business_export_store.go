package contract

import (
	"context"
	"errors"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
)

var ErrBusinessAuditExportIdempotencyConflict = errors.New("business audit export idempotency key reused with different filters or authorization scope")

type AuditBusinessExportStore interface {
	CreateOrGetBusinessAuditExport(context.Context, auditmodel.AuditBusinessExportArtifact) (auditmodel.AuditBusinessExportArtifact, bool, error)
	BusinessAuditExportByTokenHash(context.Context, string, string) (auditmodel.AuditBusinessExportArtifact, bool, error)
	RecordBusinessAuditExportDownload(context.Context, string, string, string) (bool, error)
}
