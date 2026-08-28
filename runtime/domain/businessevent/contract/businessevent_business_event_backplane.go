package contract

import (
	"context"

	businesseventmodel "github.com/domainry/domainry-runtime/runtime/domain/businessevent/model"
)

const (
	BackplaneModeLocal  = "local"
	BackplaneModeShared = "shared"
)

type Subscription struct {
	Replay       []businesseventmodel.BusinessEvent
	Events       <-chan businesseventmodel.BusinessEvent
	NeedsResync  bool
	CurrentEvent string
	Close        func()
}

// Backplane is the multi-instance boundary. A shared implementation must own
// cursor assignment, bounded replay and fan-out across Runtime replicas.
type Backplane interface {
	Mode(context.Context) string
	Publish(context.Context, businesseventmodel.BusinessEvent) (businesseventmodel.BusinessEvent, error)
	Open(context.Context, string, string) (Subscription, error)
}
