package repository

import (
	"context"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

type ChangePlanRepository interface {
	GetDraft(context.Context, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error)
	SaveDraft(context.Context, string, changeplanmodel.BusinessChangePlanDraft, int) (changeplanmodel.BusinessChangePlanDraft, bool, error)
	PublishDraft(context.Context, string, string, int, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error)
}

// ChangePlanLifecycleRepository is the compare-and-swap state boundary for a
// reviewed system draft. It is separate from the basic repository contract so
// read-only/test adapters do not accidentally claim lifecycle support.
type ChangePlanLifecycleRepository interface {
	TransitionDraft(context.Context, string, string, int, string, string, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error)
}

type ChangePlanOperationRepository interface {
	TryBeginOperation(context.Context, string, changeplanmodel.ChangePlanOperationClaimRequest) (changeplanmodel.ChangePlanOperationClaimResult, error)
	CompleteOperation(context.Context, string, changeplanmodel.ChangePlanOperationCompletion) (changeplanmodel.ChangePlanOperationExecution, error)
	FailOperation(context.Context, string, changeplanmodel.ChangePlanOperationFailure) (changeplanmodel.ChangePlanOperationExecution, error)
}

type ChangePlanEvidenceRepository interface {
	ListSeedProvenance(context.Context) ([]businessseedmodel.BusinessSeedProvenance, error)
}
