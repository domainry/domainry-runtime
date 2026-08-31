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

type recordDataExchangeExportPayload struct {
	Options           RecordExportOptions `json:"options"`
	AssuranceEvidence map[string]string   `json:"assurance_evidence,omitempty"`
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

func (s *RecordDataExchangeApplicationService) EnqueueExport(ctx context.Context, objectKey, key string, options RecordExportOptions, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	if strings.TrimSpace(key) == "" {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.export.async"})
	}
	if s == nil || s.dependencies.DataExchange == nil || s.dependencies.Exporter == nil {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindInternal, "backend.data_exchange.unavailable", nil, nil)
	}
	evidence, err := s.dependencies.Exporter.authorizeForBatch(ctx, objectKey, principal, options)
	if err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	options.AssuranceToken = ""
	payload, err := json.Marshal(recordDataExchangeExportPayload{Options: options, AssuranceEvidence: evidence})
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
