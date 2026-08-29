package contract

import (
	"context"
	"time"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
)

type ActionAssuranceStore interface {
	SaveActionAssuranceGrant(context.Context, actionmodel.ActionAssuranceGrant) error
	GetActionAssuranceGrant(context.Context, string) (actionmodel.ActionAssuranceGrant, bool, error)
	ConsumeActionAssuranceGrant(context.Context, string, string, time.Time) (bool, error)
}
