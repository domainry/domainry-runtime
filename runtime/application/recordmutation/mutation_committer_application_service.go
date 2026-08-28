package recordmutation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

type MutationPlanStore interface {
	CommitRecordMutation(context.Context, string, transactionmodel.RecordMutationCommit) error
	CommitRecordMutationBatch(context.Context, string, []transactionmodel.RecordMutationCommit) error
}

type MutationBatchReceiptCommit func(context.Context, []transactionmodel.RecordMutationCommit) error
type MutationReceiptCommit func(context.Context, transactionmodel.RecordMutationCommit) error

type MutationCommitterApplicationService struct {
	store MutationPlanStore
}

func NewMutationCommitterApplicationService(store MutationPlanStore) *MutationCommitterApplicationService {
	return &MutationCommitterApplicationService{store: store}
}

func (s *MutationCommitterApplicationService) CommitMutationPlan(ctx context.Context, plan transactionmodel.MutationPlan) error {
	return s.CommitMutationPlanWithReceipt(ctx, plan, nil)
}

func (s *MutationCommitterApplicationService) CommitMutationPlanWithReceipt(ctx context.Context, plan transactionmodel.MutationPlan, receiptCommit MutationReceiptCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	scopeID := strings.TrimSpace(plan.Context().WorkspaceID())
	if scopeID == "" {
		return mutationCommitterError("workspace_id")
	}
	if receiptCommit != nil {
		return retryCanonicalMutationCommit(ctx, func() error { return receiptCommit(ctx, plan.CanonicalCommit()) })
	}
	if s == nil || s.store == nil {
		return mutationCommitterError("store_unavailable")
	}
	return retryCanonicalMutationCommit(ctx, func() error { return s.store.CommitRecordMutation(ctx, scopeID, plan.CanonicalCommit()) })
}

func (s *MutationCommitterApplicationService) CommitMutationBatch(ctx context.Context, plans []transactionmodel.MutationPlan, receiptCommit MutationBatchReceiptCommit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(plans) == 0 {
		return mutationCommitterError("plans")
	}
	scopeID := strings.TrimSpace(plans[0].Context().WorkspaceID())
	commits := make([]transactionmodel.RecordMutationCommit, len(plans))
	for index := range plans {
		if strings.TrimSpace(plans[index].Context().WorkspaceID()) != scopeID {
			return mutationCommitterError("workspace_mismatch")
		}
		commits[index] = plans[index].CanonicalCommit()
	}
	if receiptCommit != nil {
		return retryCanonicalMutationCommit(ctx, func() error { return receiptCommit(ctx, commits) })
	}
	if s == nil || s.store == nil {
		return mutationCommitterError("store_unavailable")
	}
	return retryCanonicalMutationCommit(ctx, func() error { return s.store.CommitRecordMutationBatch(ctx, scopeID, commits) })
}

const canonicalMutationCommitMaxAttempts = 3

func retryCanonicalMutationCommit(ctx context.Context, commit func() error) error {
	var err error
	for attempt := 1; ; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = commit(); err == nil || !mutation.IsTransactionTransient(err, "") || attempt == canonicalMutationCommitMaxAttempts {
			return err
		}
		delay := time.Duration(1<<(attempt-1)) * 5 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
	}
}

type MutationCommitterError struct {
	Code  string
	Field string
}

func (e *MutationCommitterError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Field)
}

func mutationCommitterError(field string) error {
	return &MutationCommitterError{Code: "backend.mutation.commit_invalid", Field: field}
}
