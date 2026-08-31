// Package repository defines ports for Runtime's durable publication handoff.
package repository

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
)

type Repository interface {
	ListOutbox(context.Context, string, string, string, int) ([]publicationmodel.Message, error)
	InsertOutbox(context.Context, string, publicationmodel.Message) (publicationmodel.Message, error)
	UpdateOutboxStatus(context.Context, string, string, string, string, string) (publicationmodel.Message, error)
	ScheduleOutboxRetry(context.Context, string, string, int, string) (publicationmodel.Message, error)
}

type Reader interface {
	GetOutbox(context.Context, string, string) (publicationmodel.Message, bool, error)
}

type WorkerRepository interface {
	ListDueOutbox(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]publicationmodel.Message, error)
	ClaimOutbox(ctx context.Context, workspaceID, messageID, owner, now string) (publicationmodel.Message, bool, error)
	HeartbeatOutbox(ctx context.Context, workspaceID, messageID, expectedLeaseOwner string, expectedFencingToken int64, now string) (publicationmodel.Message, error)
	UpdateOutboxStatus(ctx context.Context, workspaceID, messageID, expectedLeaseOwner string, expectedFencingToken int64, status, responseRef, errorText, now string) (publicationmodel.Message, error)
	ScheduleOutboxRetry(ctx context.Context, workspaceID, messageID, expectedLeaseOwner string, expectedFencingToken int64, delaySeconds int, errorText, now string) (publicationmodel.Message, error)
}
