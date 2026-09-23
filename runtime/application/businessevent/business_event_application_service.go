package businessevent

import (
	"context"
	"strings"
	"sync"
	"time"

	apperror "github.com/domainry/domainry-foundation/apperror"
	businesseventcontract "github.com/domainry/domainry-runtime/runtime/domain/businessevent/contract"
	businesseventmodel "github.com/domainry/domainry-runtime/runtime/domain/businessevent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type Limits struct {
	GlobalConnections    int
	WorkspaceConnections int
	PrincipalConnections int
}

type Stats struct {
	ActiveConnections  int
	OpenedTotal        uint64
	ClosedTotal        uint64
	RejectedTotal      uint64
	OpenFailedTotal    uint64
	PublishedTotal     uint64
	PublishFailedTotal uint64
	BackplaneMode      string
}

type BusinessEventApplicationService struct {
	backplane     businesseventcontract.Backplane
	limits        Limits
	mu            sync.Mutex
	global        int
	workspace     map[string]int
	principal     map[string]int
	opened        uint64
	closed        uint64
	rejected      uint64
	openFailed    uint64
	published     uint64
	publishFailed uint64
}

func NewBusinessEventApplicationService(backplane businesseventcontract.Backplane, limits Limits) *BusinessEventApplicationService {
	if limits.GlobalConnections < 1 {
		limits.GlobalConnections = 512
	}
	if limits.WorkspaceConnections < 1 {
		limits.WorkspaceConnections = 64
	}
	if limits.PrincipalConnections < 1 {
		limits.PrincipalConnections = 8
	}
	return &BusinessEventApplicationService{backplane: backplane, limits: limits, workspace: map[string]int{}, principal: map[string]int{}}
}

func (s *BusinessEventApplicationService) Publish(ctx context.Context, workspaceID, objectKey, reason string) (businesseventmodel.BusinessEvent, error) {
	workspaceScopeID := strings.TrimSpace(workspaceID)
	if len(workspaceScopeID) == 0 {
		return businesseventmodel.BusinessEvent{}, apperror.New(apperror.KindForbidden, "backend.event_stream.workspace_required", nil, nil)
	}
	if s == nil || s.backplane == nil {
		if s != nil {
			s.recordPublishFailure()
		}
		return businesseventmodel.BusinessEvent{}, apperror.New(apperror.KindUnavailable, "backend.event_stream.unavailable", nil, nil)
	}
	event, err := s.backplane.Publish(ctx, businesseventmodel.BusinessEvent{
		Type: businesseventmodel.EventTypeRefresh, WorkspaceID: workspaceScopeID,
		ObjectKey: strings.TrimSpace(objectKey), Reason: strings.TrimSpace(reason), OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		s.recordPublishFailure()
		return businesseventmodel.BusinessEvent{}, apperror.New(apperror.KindUnavailable, "backend.event_stream.unavailable", err, nil)
	}
	s.mu.Lock()
	s.published++
	s.mu.Unlock()
	return event, nil
}

func (s *BusinessEventApplicationService) Open(ctx context.Context, principal principalmodel.Principal, lastEventID string) (businesseventcontract.Subscription, error) {
	if s == nil || s.backplane == nil {
		if s != nil {
			s.recordOpenFailure()
		}
		return businesseventcontract.Subscription{}, apperror.New(apperror.KindUnavailable, "backend.event_stream.unavailable", nil, nil)
	}
	workspaceScopeID := strings.TrimSpace(principal.WorkspaceID)
	userID := strings.TrimSpace(principal.UserID)
	if len(workspaceScopeID) == 0 {
		return businesseventcontract.Subscription{}, apperror.New(apperror.KindForbidden, "backend.event_stream.workspace_required", nil, nil)
	}
	if !principal.Known || userID == "" {
		return businesseventcontract.Subscription{}, apperror.New(apperror.KindForbidden, "backend.event_stream.identity_required", nil, nil)
	}
	principalKey := workspaceScopeID + "\x00" + userID
	if !s.acquire(workspaceScopeID, principalKey) {
		return businesseventcontract.Subscription{}, apperror.New(apperror.KindRateLimited, "backend.event_stream.capacity_exceeded", nil, nil)
	}
	subscription, err := s.backplane.Open(ctx, workspaceScopeID, strings.TrimSpace(lastEventID))
	if err != nil {
		s.release(workspaceScopeID, principalKey, false)
		s.recordOpenFailure()
		return businesseventcontract.Subscription{}, apperror.New(apperror.KindUnavailable, "backend.event_stream.unavailable", err, nil)
	}
	s.recordOpened()
	closeBackplane := subscription.Close
	var once sync.Once
	subscription.Close = func() {
		once.Do(func() {
			if closeBackplane != nil {
				closeBackplane()
			}
			s.release(workspaceScopeID, principalKey, true)
		})
	}
	return subscription, nil
}

func (s *BusinessEventApplicationService) BackplaneMode(ctx context.Context) string {
	if s == nil || s.backplane == nil {
		return "unavailable"
	}
	return s.backplane.Mode(ctx)
}

func (s *BusinessEventApplicationService) Snapshot(ctx context.Context) Stats {
	if s == nil {
		return Stats{BackplaneMode: "unavailable"}
	}
	mode := s.BackplaneMode(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		ActiveConnections:  s.global,
		OpenedTotal:        s.opened,
		ClosedTotal:        s.closed,
		RejectedTotal:      s.rejected,
		OpenFailedTotal:    s.openFailed,
		PublishedTotal:     s.published,
		PublishFailedTotal: s.publishFailed,
		BackplaneMode:      mode,
	}
}

func (s *BusinessEventApplicationService) acquire(workspaceID, principalKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.global >= s.limits.GlobalConnections || s.workspace[workspaceID] >= s.limits.WorkspaceConnections || s.principal[principalKey] >= s.limits.PrincipalConnections {
		s.rejected++
		return false
	}
	s.global++
	s.workspace[workspaceID]++
	s.principal[principalKey]++
	return true
}

func (s *BusinessEventApplicationService) release(workspaceID, principalKey string, countClosed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.global > 0 {
		s.global--
		if countClosed {
			s.closed++
		}
	}
	if s.workspace[workspaceID] <= 1 {
		delete(s.workspace, workspaceID)
	} else {
		s.workspace[workspaceID]--
	}
	if s.principal[principalKey] <= 1 {
		delete(s.principal, principalKey)
	} else {
		s.principal[principalKey]--
	}
}

func (s *BusinessEventApplicationService) recordOpened() {
	s.mu.Lock()
	s.opened++
	s.mu.Unlock()
}

func (s *BusinessEventApplicationService) recordOpenFailure() {
	s.mu.Lock()
	s.openFailed++
	s.mu.Unlock()
}

func (s *BusinessEventApplicationService) recordPublishFailure() {
	s.mu.Lock()
	s.publishFailed++
	s.mu.Unlock()
}
