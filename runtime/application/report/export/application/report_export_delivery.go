package application

import (
	"context"
	"encoding/json"
	"strings"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// PrepareResolvedExportDelivery keeps PrepareResolvedExport as the explicit
// asynchronous API while adding automatic delivery for an exact bounded result.
// Both paths create and reuse the same durable Data Exchange job.
func (s *ReportExportApplicationService) PrepareResolvedExportDelivery(ctx context.Context, request reportmodel.ReportExportPrepareRequest, report reportmodel.ReportSchema, control reportmodel.ReportExportControlSchema, principal principalmodel.Principal) (reportmodel.ReportExportPreparation, error) {
	job, err := s.PrepareResolvedExport(ctx, request, report, control, principal)
	if err != nil {
		return reportmodel.ReportExportPreparation{}, err
	}
	result := reportmodel.ReportExportPreparation{Job: job}
	exchangeJob, err := s.dataExchange.Job(ctx, dataexchange.JobRequest{
		Scope: reportexport.Scope(principal), JobID: job.ID, Provider: reportexport.DataExchangeProviderKey, Operation: "export",
	})
	if err != nil {
		return reportmodel.ReportExportPreparation{}, reportExportDataExchangeError(err)
	}
	var payload reportexport.ExportPayload
	if err = json.Unmarshal(exchangeJob.Options, &payload); err != nil || strings.TrimSpace(payload.ReportKey) != strings.TrimSpace(report.Key) || strings.TrimSpace(payload.ObjectKey) != strings.TrimSpace(request.ObjectKey) {
		return reportmodel.ReportExportPreparation{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_job_payload_invalid", Err: err}
	}
	if !reportExportCanDeliverInline(payload) {
		return result, nil
	}
	inline, ok := s.dataExchange.(dataexchange.InlineExportBinding)
	if !ok {
		return result, nil
	}
	exchangeJob, err = inline.MaterializeExport(ctx, dataexchange.JobRequest{
		Scope: reportexport.Scope(principal), JobID: job.ID, Provider: reportexport.DataExchangeProviderKey, Operation: "export",
	})
	if err != nil {
		return reportmodel.ReportExportPreparation{}, reportExportDataExchangeError(err)
	}
	result.Job, err = s.dataExchangeProvider.ProjectJob(ctx, exchangeJob, principal)
	if err != nil {
		return reportmodel.ReportExportPreparation{}, err
	}
	if exchangeJob.Status != "completed" {
		return result, nil
	}
	artifact, err := s.dataExchangeProvider.OpenDataExchangeArtifact(ctx, exchangeJob, reportexport.Scope(principal))
	if err != nil {
		return reportmodel.ReportExportPreparation{}, err
	}
	result.Artifact = &reportmodel.ReportExportArtifact{
		ID: artifact.ID, Filename: artifact.Filename, ContentType: artifact.ContentType, ContentSHA256: artifact.SHA256,
		Size: artifact.Size, ExpiresAt: artifact.ExpiresAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), Content: artifact.Content,
	}
	return result, nil
}

func reportExportCanDeliverInline(payload reportexport.ExportPayload) bool {
	return payload.ExactTotal >= 0 && payload.ExactTotal <= reportExportProbeRowLimit
}
