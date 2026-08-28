package repository

import (
	"context"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type LifecycleRepository interface {
	SavePolicy(context.Context, lifecyclemodel.PolicyVersion) error
	LatestPolicy(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error)
	ListPolicies(context.Context, string) ([]lifecyclemodel.PolicyVersion, error)
	SaveLegalHold(context.Context, lifecyclemodel.LegalHold) error
	GetLegalHold(context.Context, string, string) (lifecyclemodel.LegalHold, bool, error)
	ActiveLegalHolds(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error)
	SaveCleanupJob(context.Context, lifecyclemodel.CleanupJob) error
	GetCleanupJob(context.Context, string, string) (lifecyclemodel.CleanupJob, bool, error)
	ListRunnableCleanupJobs(context.Context, principalmodel.SystemScope, int, time.Time) ([]lifecyclemodel.CleanupJob, error)
	ClaimCleanupJob(context.Context, string, string, string, time.Duration, time.Time) (lifecyclemodel.CleanupJob, bool, error)
	UpdateCleanupJob(context.Context, lifecyclemodel.CleanupJob) error
	SaveSubjectRequest(context.Context, lifecyclemodel.SubjectRequest) error
	GetSubjectRequest(context.Context, string, string) (lifecyclemodel.SubjectRequest, bool, error)
	ExpireSubjectExportReferences(context.Context, principalmodel.SystemScope, time.Time) ([]lifecyclemodel.SubjectRequest, error)
	SaveExternalErasures(context.Context, []lifecyclemodel.ExternalErasure) error
	ListExternalErasures(context.Context, string, string) ([]lifecyclemodel.ExternalErasure, error)
	ReconcileExternalErasure(context.Context, string, string, string, time.Time) (lifecyclemodel.ExternalErasure, bool, error)
	SaveDeletionRegistration(context.Context, lifecyclemodel.DeletionRegistration) error
	ListPendingDeletionRegistrations(context.Context, string, int) ([]lifecyclemodel.DeletionRegistration, error)
	ListArchiveEntries(context.Context, string, string, int) ([]lifecyclemodel.ArchiveEntry, error)
	AppendAuditEvidence(context.Context, lifecyclemodel.AuditEvidence) error
	Metrics(context.Context, string, time.Time) (lifecyclemodel.Metrics, error)
	GlobalMetrics(context.Context, principalmodel.SystemScope, time.Time) (lifecyclemodel.Metrics, error)
}
