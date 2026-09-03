package record

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type RecordDataExchangeDependencies struct {
	Importer     *RecordImportApplicationService
	Exporter     *RecordExportApplicationService
	DataExchange dataexchange.Binding
}

type RecordDataExchangeApplicationService struct {
	dependencies RecordDataExchangeDependencies
}

const (
	RecordExportDeliveryDirect     = "direct"
	RecordExportDeliveryBackground = "background"
)

type RecordExportDispatch struct {
	Delivery string
	Content  []byte
	Filename string
	Job      recordmodel.RecordBatchJob
	Replayed bool
}

type recordDataExchangeExportPayload struct {
	Options           RecordExportOptions `json:"options"`
	AssuranceEvidence map[string]string   `json:"assurance_evidence,omitempty"`
	ExactTotal        *int                `json:"exact_total,omitempty"`
}

func NewRecordDataExchangeApplicationService(dependencies RecordDataExchangeDependencies) *RecordDataExchangeApplicationService {
	return &RecordDataExchangeApplicationService{dependencies: dependencies}
}

func (s *RecordDataExchangeApplicationService) EnqueueImport(ctx context.Context, objectKey string, rawCSV []byte, key string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	return s.EnqueueImportStream(ctx, objectKey, bytes.NewReader(rawCSV), objectKey+".csv", "text/csv", int64(len(rawCSV)), key, principal)
}

func (s *RecordDataExchangeApplicationService) EnqueueImportStream(ctx context.Context, objectKey string, source io.Reader, filename, contentType string, maxBytes int64, key string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	if strings.TrimSpace(key) == "" {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.import.async"})
	}
	if s == nil || s.dependencies.DataExchange == nil {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindInternal, "backend.data_exchange.unavailable", nil, nil)
	}
	job, replayed, err := s.dependencies.DataExchange.SubmitImport(ctx, dataexchange.ImportRequest{Scope: recordDataExchangeScope(principal), Provider: "records", ObjectKey: strings.TrimSpace(objectKey), IdempotencyKey: key, Filename: filename, ContentType: contentType, Source: source, MaxBytes: maxBytes})
	if errors.Is(err, dataexchange.ErrSourceTooLarge) {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindBadRequest, "backend.import.payload_too_large", err, nil)
	}
	if errors.Is(err, dataexchange.ErrSourceUnreadable) {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindBadRequest, "backend.import.read_csv_failed", err, nil)
	}
	return recordBatchJobFromDataExchange(job), replayed, err
}

func (s *RecordDataExchangeApplicationService) DispatchExportIdempotent(ctx context.Context, objectKey, key string, options RecordExportOptions, principal principalmodel.Principal) (RecordExportDispatch, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return RecordExportDispatch{}, err
	}
	if strings.TrimSpace(key) == "" {
		return RecordExportDispatch{}, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.export"})
	}
	if s == nil || s.dependencies.Exporter == nil {
		return RecordExportDispatch{}, apperror.New(apperror.KindInternal, "backend.data_exchange.unavailable", nil, nil)
	}
	prepared, total, err := s.dependencies.Exporter.prepareDispatch(ctx, objectKey, principal, options)
	if err != nil {
		return RecordExportDispatch{}, err
	}
	if total <= recordExportDirectMaxRows {
		content, filename, exportErr := s.dependencies.Exporter.exportPrepared(ctx, prepared, recordExportDirectMaxRows)
		if exportErr == nil {
			return RecordExportDispatch{Delivery: RecordExportDeliveryDirect, Content: content, Filename: filename}, nil
		}
		// A concurrent insert can move the result over the direct limit after the
		// count probe. In that case, transparently promote the request to a job.
		if apperror.CodeOf(exportErr) != "backend.export.too_many_records" {
			return RecordExportDispatch{}, exportErr
		}
	}
	job, replayed, err := s.enqueueAuthorizedExport(ctx, prepared.object.Key, key, prepared.options, prepared.evidence, &total, principal)
	if err != nil {
		return RecordExportDispatch{}, err
	}
	return RecordExportDispatch{Delivery: RecordExportDeliveryBackground, Job: job, Replayed: replayed}, nil
}

func (s *RecordDataExchangeApplicationService) DownloadExport(ctx context.Context, jobID string, principal principalmodel.Principal) (dataexchange.Artifact, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return dataexchange.Artifact{}, err
	}
	if s == nil || s.dependencies.DataExchange == nil {
		return dataexchange.Artifact{}, apperror.New(apperror.KindInternal, "backend.data_exchange.unavailable", nil, nil)
	}
	scope := recordDataExchangeScope(principal)
	job, err := s.dependencies.DataExchange.Job(ctx, dataexchange.JobRequest{Scope: scope, JobID: strings.TrimSpace(jobID)})
	if err != nil {
		if errors.Is(err, dataexchange.ErrJobNotFound) {
			return dataexchange.Artifact{}, apperror.New(apperror.KindNotFound, "backend.export.job_not_found", err, nil)
		}
		return dataexchange.Artifact{}, err
	}
	if job.Provider != "records" || job.Operation != "export" || job.WorkspaceID != principal.WorkspaceID || job.ActorID != principal.UserID {
		return dataexchange.Artifact{}, apperror.New(apperror.KindNotFound, "backend.export.job_not_found", nil, nil)
	}
	if job.Status != "completed" || strings.TrimSpace(job.ArtifactID) == "" {
		return dataexchange.Artifact{}, apperror.New(apperror.KindConflict, "backend.export.download_not_ready", nil, nil)
	}
	artifact, err := s.dependencies.DataExchange.Download(ctx, dataexchange.JobRequest{Scope: scope, JobID: job.ID})
	if err != nil {
		return dataexchange.Artifact{}, err
	}
	if artifact.Content == nil {
		return dataexchange.Artifact{}, apperror.New(apperror.KindInternal, "backend.export.download_unavailable", nil, nil)
	}
	return artifact, nil
}

func (s *RecordDataExchangeApplicationService) enqueueAuthorizedExport(ctx context.Context, objectKey, key string, options RecordExportOptions, evidence map[string]string, exactTotal *int, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	if s == nil || s.dependencies.DataExchange == nil {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindInternal, "backend.data_exchange.unavailable", nil, nil)
	}
	options.AssuranceToken = ""
	payload, err := json.Marshal(recordDataExchangeExportPayload{Options: options, AssuranceEvidence: evidence, ExactTotal: exactTotal})
	if err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	job, replayed, err := s.dependencies.DataExchange.SubmitExport(ctx, dataexchange.ExportRequest{Scope: recordDataExchangeScope(principal), Provider: "records", ObjectKey: strings.TrimSpace(objectKey), IdempotencyKey: key, Options: payload})
	return recordBatchJobFromDataExchange(job), replayed, err
}

func (s *RecordDataExchangeApplicationService) StartWorker(ctx context.Context, interval time.Duration, limit int) <-chan struct{} {
	if s == nil || s.dependencies.DataExchange == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return s.dependencies.DataExchange.Start(ctx, dataexchange.WorkerConfig{Enabled: true, PollInterval: interval, BatchSize: limit, LeaseTTL: time.Minute})
}

func recordDataExchangeScope(principal principalmodel.Principal) dataexchange.Scope {
	return dataexchange.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, RoleKey: principal.RoleKey, RequestID: principal.RequestID}
}

func recordBatchJobFromDataExchange(job dataexchange.Job) recordmodel.RecordBatchJob {
	return recordmodel.RecordBatchJob{ID: job.ID, WorkspaceID: job.WorkspaceID, Kind: job.Operation, ObjectKey: job.ObjectKey, ActorID: job.ActorID, RoleKey: job.RoleKey, Status: job.Status, Checkpoint: job.Checkpoint, Total: job.Total, ResultArtifactID: job.ArtifactID, ErrorCode: job.ErrorCode, CreatedAt: job.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: job.UpdatedAt.Format(time.RFC3339Nano)}
}
