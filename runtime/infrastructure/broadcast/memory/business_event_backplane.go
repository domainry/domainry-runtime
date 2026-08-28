package memory

import (
	"context"
	"strconv"
	"strings"
	"sync"

	businesseventcontract "github.com/domainry/domainry-runtime/runtime/domain/businessevent/contract"
	businesseventmodel "github.com/domainry/domainry-runtime/runtime/domain/businessevent/model"
)

type workspaceState struct {
	sequence    uint64
	events      []businesseventmodel.BusinessEvent
	subscribers map[uint64]chan businesseventmodel.BusinessEvent
}

// BusinessEventBackplane is the default process-local backplane. The same
// instance may be injected into several routers in tests/embedders to exercise
// the shared-backplane contract, but it is not a distributed implementation.
type BusinessEventBackplane struct {
	mu             sync.Mutex
	replayLimit    int
	subscriberSize int
	nextSubscriber uint64
	workspaces     map[string]*workspaceState
}

func NewBusinessEventBackplane(replayLimit, subscriberSize int) *BusinessEventBackplane {
	if replayLimit < 1 {
		replayLimit = 128
	}
	if subscriberSize < 1 {
		subscriberSize = 32
	}
	return &BusinessEventBackplane{replayLimit: replayLimit, subscriberSize: subscriberSize, workspaces: map[string]*workspaceState{}}
}

func (b *BusinessEventBackplane) Mode(context.Context) string {
	return businesseventcontract.BackplaneModeLocal
}

func (b *BusinessEventBackplane) Publish(_ context.Context, event businesseventmodel.BusinessEvent) (businesseventmodel.BusinessEvent, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.workspace(strings.TrimSpace(event.WorkspaceID))
	state.sequence++
	event.ID = strconv.FormatUint(state.sequence, 10)
	state.events = append(state.events, event)
	if len(state.events) > b.replayLimit {
		state.events = append([]businesseventmodel.BusinessEvent(nil), state.events[len(state.events)-b.replayLimit:]...)
	}
	for id, subscriber := range state.subscribers {
		select {
		case subscriber <- event:
		default:
			delete(state.subscribers, id)
			close(subscriber)
		}
	}
	return event, nil
}

func (b *BusinessEventBackplane) Open(ctx context.Context, workspaceID, afterID string) (businesseventcontract.Subscription, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	afterID = strings.TrimSpace(afterID)
	b.mu.Lock()
	state := b.workspace(workspaceID)
	replay, needsResync := replayAfter(state.events, afterID)
	current := ""
	if len(state.events) > 0 {
		current = state.events[len(state.events)-1].ID
	}
	b.nextSubscriber++
	subscriberID := b.nextSubscriber
	events := make(chan businesseventmodel.BusinessEvent, b.subscriberSize)
	state.subscribers[subscriberID] = events
	b.mu.Unlock()

	var once sync.Once
	stopped := make(chan struct{})
	closeSubscription := func() {
		once.Do(func() {
			b.mu.Lock()
			if active, ok := b.workspaces[workspaceID]; ok {
				if subscriber, found := active.subscribers[subscriberID]; found {
					delete(active.subscribers, subscriberID)
					close(subscriber)
				}
			}
			b.mu.Unlock()
			close(stopped)
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			closeSubscription()
		case <-stopped:
		}
	}()
	return businesseventcontract.Subscription{Replay: replay, Events: events, NeedsResync: needsResync, CurrentEvent: current, Close: closeSubscription}, nil
}

func (b *BusinessEventBackplane) workspace(workspaceID string) *workspaceState {
	state := b.workspaces[workspaceID]
	if state == nil {
		state = &workspaceState{subscribers: map[uint64]chan businesseventmodel.BusinessEvent{}}
		b.workspaces[workspaceID] = state
	}
	return state
}

func replayAfter(events []businesseventmodel.BusinessEvent, afterID string) ([]businesseventmodel.BusinessEvent, bool) {
	if afterID == "" {
		return nil, false
	}
	for index, event := range events {
		if event.ID == afterID {
			return append([]businesseventmodel.BusinessEvent(nil), events[index+1:]...), false
		}
	}
	return nil, true
}
