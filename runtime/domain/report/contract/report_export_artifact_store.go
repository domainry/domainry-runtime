package contract

import (
	"context"
	"errors"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

var ErrReportExportIdempotencyConflict = errors.New("report export idempotency key reused with a different immutable scope")

type ReportExportArtifactStore interface {
	CreateOrGetReportExportArtifact(context.Context, reportmodel.ReportExportArtifact) (reportmodel.ReportExportArtifact, bool, error)
	LinkReportExportBusinessDownload(context.Context, string, string, string) error
	ReportExportArtifactByToken(context.Context, string, string) (reportmodel.ReportExportArtifact, bool, error)
}

type ReportExportArtifactRequestReader interface {
	ReportExportArtifactByIdempotency(context.Context, string, string, string, string) (reportmodel.ReportExportArtifact, bool, error)
}
