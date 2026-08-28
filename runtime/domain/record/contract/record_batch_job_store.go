package contract

import (
	"context"
	"errors"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

var ErrRecordBatchJobIdempotencyConflict = errors.New("record batch job idempotency key reused with different request")

type RecordBatchJobStore interface {
	EnqueueRecordBatchJob(context.Context, recordmodel.RecordBatchJob) (recordmodel.RecordBatchJob, bool, error)
	GetRecordBatchJob(context.Context, string, string) (recordmodel.RecordBatchJob, bool, error)
	FindRecordBatchJobByIdempotency(context.Context, string, string, string, string) (recordmodel.RecordBatchJob, bool, error)
	ClaimRecordBatchJobs(context.Context, int, string, time.Duration, time.Time) ([]recordmodel.RecordBatchJob, error)
	SaveRecordBatchJobCheckpoint(context.Context, recordmodel.RecordBatchJob, time.Time) error
	HeartbeatRecordBatchJob(context.Context, recordmodel.RecordBatchJob, time.Duration, time.Time) error
	RetryRecordBatchJob(context.Context, recordmodel.RecordBatchJob, time.Time, time.Time) error
	CompleteRecordBatchJob(context.Context, recordmodel.RecordBatchJob, time.Time) error
	FailRecordBatchJob(context.Context, recordmodel.RecordBatchJob, time.Time) error
	QuarantineRecordBatchJob(context.Context, recordmodel.RecordBatchJob, time.Time) error
	RequeueRecordBatchJob(context.Context, string, string, time.Time) (recordmodel.RecordBatchJob, bool, error)
	CancelRecordBatchJob(context.Context, string, string) (recordmodel.RecordBatchJob, bool, error)
	ReplaceRecordBatchJobChunks(context.Context, recordmodel.RecordBatchJob, []recordmodel.RecordBatchJobChunk, time.Time) error
	ListRecordBatchJobChunks(context.Context, string, string) ([]recordmodel.RecordBatchJobChunk, error)
	RecordBatchJobQueueStats(context.Context, string) (int, time.Duration, error)
	GlobalRecordBatchJobQueueStats(context.Context, principalmodel.SystemScope) (int, time.Duration, error)
}

// RecordBatchJobPageCommitter is an optional store capability used by owned
// processors that checkpoint one immutable result page at a time. Keeping it
// separate preserves compatibility for stores that do not run paged jobs.
type RecordBatchJobPageCommitter interface {
	CommitRecordBatchJobPage(context.Context, recordmodel.RecordBatchJob, string, recordmodel.RecordBatchJobChunk, string, int, int, time.Time) error
}

type RecordBatchJobFingerprintReader interface {
	FindLatestRecordBatchJobByFingerprint(context.Context, string, string, string, string) (recordmodel.RecordBatchJob, bool, error)
}
