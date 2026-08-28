package repository

import (
	"context"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type DeploymentRuntimeStatusRepository interface {
	Ping(context.Context) error
	MigrationStatus(context.Context) (deploymentmodel.MigrationStatus, error)
}

type IdempotencyOperationsRepository interface {
	IdempotencyOperationalStatus(context.Context, string, time.Time) (deploymentmodel.IdempotencyOperationalStatus, error)
	IdempotencyOperationalStatusForSystem(context.Context, principalmodel.SystemScope, time.Time) (deploymentmodel.IdempotencyOperationalStatus, error)
	ListIdempotencyReceipts(context.Context, string, string, int) ([]idempotency.ReceiptSummary, error)
	RetryIdempotencyReceipt(context.Context, string, string, string) (bool, error)
	ResetIdempotencyReceipt(context.Context, string, string, string) (bool, error)
	RunIdempotencyCleanup(context.Context, deploymentmodel.IdempotencyCleanupRequest) (deploymentmodel.IdempotencyCleanupResult, error)
}
