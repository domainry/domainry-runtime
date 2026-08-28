package businesseventmodel

import "time"

const (
	EventTypeRefresh = "refresh"
	EventTypeResync  = "resync"
)

// BusinessEvent is a tenant-scoped invalidation signal. It intentionally does
// not carry record data: clients must re-read durable state through the normal
// authorized Runtime APIs after receiving it.
type BusinessEvent struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	WorkspaceID string    `json:"-"`
	ObjectKey   string    `json:"object_key,omitempty"`
	Reason      string    `json:"reason"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type Filter struct {
	ObjectKeys map[string]struct{}
	EventTypes map[string]struct{}
}

func (f Filter) Matches(event BusinessEvent) bool {
	if event.Type == EventTypeResync {
		return true
	}
	if len(f.EventTypes) > 0 {
		if _, ok := f.EventTypes[event.Type]; !ok {
			return false
		}
	}
	if len(f.ObjectKeys) > 0 {
		if _, ok := f.ObjectKeys[event.ObjectKey]; !ok {
			return false
		}
	}
	return true
}
