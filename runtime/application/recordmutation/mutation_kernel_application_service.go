package recordmutation

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

// MutationKernelApplicationService is the only application boundary that turns
// a canonical mutation description into a durable record change.
type MutationKernelApplicationService struct {
	planner   *MutationPlannerApplicationService
	committer *MutationCommitterApplicationService
}

func NewMutationKernelApplicationService(store MutationPlanStore, resolveRevision MutationMetadataRevisionResolver) *MutationKernelApplicationService {
	return &MutationKernelApplicationService{
		planner:   NewMutationPlannerApplicationService(resolveRevision),
		committer: NewMutationCommitterApplicationService(store),
	}
}

func (s *MutationKernelApplicationService) Plan(ctx context.Context, principal principalmodel.Principal, commit transactionmodel.RecordMutationCommit, before map[string]any) (transactionmodel.MutationPlan, error) {
	return s.planner.Plan(ctx, principal, commit, before)
}

func (s *MutationKernelApplicationService) Commit(ctx context.Context, principal principalmodel.Principal, commit transactionmodel.RecordMutationCommit, before map[string]any, receipt MutationReceiptCommit) (transactionmodel.MutationPlan, error) {
	plan, err := s.Plan(ctx, principal, commit, before)
	if err != nil {
		return transactionmodel.MutationPlan{}, err
	}
	if err := s.committer.CommitMutationPlanWithReceipt(ctx, plan, receipt); err != nil {
		return transactionmodel.MutationPlan{}, err
	}
	return plan, nil
}

func (s *MutationKernelApplicationService) CommitPlan(ctx context.Context, plan transactionmodel.MutationPlan, receipt MutationReceiptCommit) error {
	return s.committer.CommitMutationPlanWithReceipt(ctx, plan, receipt)
}

func (s *MutationKernelApplicationService) CommitBatch(ctx context.Context, plans []transactionmodel.MutationPlan, receipt MutationBatchReceiptCommit) error {
	return s.committer.CommitMutationBatch(ctx, plans, receipt)
}
