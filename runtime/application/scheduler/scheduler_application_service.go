package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulermodulehost "github.com/domainry/domainry-runtime/runtime/modulehost/scheduler"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/schedule"
)

type SchedulerDefinitionVersion struct {
	VersionID string         `json:"version_id"`
	Event     string         `json:"event"`
	Data      map[string]any `json:"data"`
	CreatedAt string         `json:"created_at"`
}

// PublishedDefinition is Runtime's source-controlled Scheduler metadata view.
// It is not a Scheduler run record and is never persisted through Record APIs.
type PublishedDefinition struct {
	Key       string
	Data      map[string]any
	CreatedAt string
	UpdatedAt string
}

const (
	ActionGetOpsSchedulerState                    = schedulersdk.ActionSchedulerStateGet
	ActionGetManagementSchedulerAuthoringContract = schedulersdk.ActionSchedulerAuthoringContractGet
	ActionListManagementSchedulerDefinitions      = schedulersdk.ActionSchedulerDefinitionsList
	ActionGetManagementSchedulerDefinition        = schedulersdk.ActionSchedulerDefinitionsGet
	ActionPreviewSchedulerJob                     = schedulersdk.ActionSchedulerDefinitionsValidate
	ActionPreviewSchedulerSchedule                = schedulersdk.ActionSchedulerSchedulesPreview
	ActionSimulateSchedulerJob                    = schedulersdk.ActionSchedulerDefinitionsSimulate
	ActionRunOpsSchedulerJob                      = schedulersdk.ActionSchedulerDefinitionsRun
	ActionRescheduleOpsSchedulerDefinition        = schedulersdk.ActionSchedulerDefinitionsReschedule
	ActionRetryOpsSchedulerRun                    = schedulersdk.ActionSchedulerRunsRetry
	ActionCancelOpsSchedulerRun                   = schedulersdk.ActionSchedulerRunsCancel
	ActionResolveOpsSchedulerDeadLetter           = schedulersdk.ActionSchedulerDeadLettersResolve
	ActionRequeueOpsSchedulerDeadLetter           = schedulersdk.ActionSchedulerDeadLettersRequeue
)

func (s *SchedulerApplicationService) GetDefinition(ctx context.Context, definitionID string, principal principalmodel.Principal) (PublishedDefinition, error) {
	if err := schedulerDefinitionReadAllowed(principal, schedulersdk.ActionSchedulerDefinitionsGet); err != nil {
		return PublishedDefinition{}, err
	}
	return s.schedulerDefinition(ctx, definitionID)
}

func (s *SchedulerApplicationService) schedulerDefinition(ctx context.Context, definitionID string) (PublishedDefinition, error) {
	if s.definitions == nil {
		return PublishedDefinition{}, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	definition, found, err := s.definitions.GetSchedulerDefinition(ctx, strings.TrimSpace(definitionID))
	if err != nil {
		return PublishedDefinition{}, internalError("get scheduler definition", err)
	}
	if !found {
		return PublishedDefinition{}, notFound("backend.scheduler.definition_not_found")
	}
	return definition, nil
}

func (s *SchedulerApplicationService) PublishedDefinitions(ctx context.Context, principal principalmodel.Principal) ([]PublishedDefinition, error) {
	if err := schedulerDefinitionReadAllowed(principal, schedulersdk.ActionSchedulerDefinitionsList); err != nil {
		return nil, err
	}
	if s.definitions == nil {
		return nil, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	return s.definitions.ListSchedulerDefinitions(ctx)
}

func (s *SchedulerApplicationService) DefinitionVersions(ctx context.Context, definitionID string, principal principalmodel.Principal) ([]SchedulerDefinitionVersion, error) {
	if _, err := s.GetDefinition(ctx, definitionID, principal); err != nil {
		return nil, err
	}
	if s.definitions == nil {
		return nil, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	return s.definitions.ListSchedulerDefinitionVersions(ctx, strings.TrimSpace(definitionID))
}

func schedulerDefinitionReadAllowed(principal principalmodel.Principal, actionKey string) error {
	if principal.Known {
		if _, err := principalmodel.NewSystemQueryScope(principal.SystemScope); err == nil {
			return nil
		}
	}
	return schedulerExactQueryAllowed(principal, actionKey)
}

type SchedulerDefinitionPreview struct {
	NextRuns []string `json:"next_runs"`
}

// SchedulerDefinitionSource is the read boundary for published, versioned
// scheduler metadata. Operational records must never implement this port.
type SchedulerDefinitionSource interface {
	ListSchedulerDefinitions(context.Context) ([]PublishedDefinition, error)
	GetSchedulerDefinition(context.Context, string) (PublishedDefinition, bool, error)
	ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error)
}

// SchedulerApplicationService is the Runtime-side Scheduler integration. It
// owns published-definition projection and downstream Runtime dispatch only;
// clock execution belongs to domainry-scheduler.
type SchedulerApplicationService struct {
	definitions SchedulerDefinitionSource
	clock       workerplatform.Clock
	downstream  *schedulermodulehost.DownstreamDispatcher
}

func (s *SchedulerApplicationService) UseDefinitionSource(source SchedulerDefinitionSource) {
	if s != nil {
		s.definitions = source
	}
}

func schedulerExactQueryAllowed(principal principalmodel.Principal, actionKey string) error {
	if err := schedulerAuthorizeQuery(principal); err != nil {
		return err
	}
	if principal.HasExactPermission(strings.TrimSpace(actionKey)) {
		return nil
	}
	return forbidden("backend.scheduler.permission_required")
}

func schedulerAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func definitionString(definition PublishedDefinition, key string) string {
	value := strings.TrimSpace(fmt.Sprint(definition.Data[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}

func (s *SchedulerApplicationService) UseReportSnapshotRuntime(runtime ReportSnapshotRuntime) {
	if s != nil && s.downstream != nil {
		s.downstream.UseReportSnapshotRuntime(runtime)
	}
}

func NewSchedulerApplicationService(runtime ScheduledWorkflowRuntime) *SchedulerApplicationService {
	return NewSchedulerApplicationServiceWithClock(runtime, workerplatform.SystemClock{})
}

func NewSchedulerApplicationServiceWithClock(runtime ScheduledWorkflowRuntime, clock workerplatform.Clock) *SchedulerApplicationService {
	if clock == nil {
		clock = workerplatform.SystemClock{}
	}
	return &SchedulerApplicationService{clock: clock, downstream: schedulermodulehost.NewDownstreamDispatcher(runtime)}
}

func (s *SchedulerApplicationService) PreviewDefinition(ctx context.Context, data map[string]any, principal principalmodel.Principal) (SchedulerDefinitionPreview, error) {
	if err := ctx.Err(); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	if err := schedulerExactQueryAllowed(principal, ActionPreviewSchedulerJob); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	return s.previewDefinition(ctx, data, true)
}

// PreviewSchedule validates and previews a leaf schedule payload without a
// synthetic Scheduler job definition.
func (s *SchedulerApplicationService) PreviewSchedule(ctx context.Context, data map[string]any, principal principalmodel.Principal) (SchedulerDefinitionPreview, error) {
	if err := ctx.Err(); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	if err := schedulerExactQueryAllowed(principal, ActionPreviewSchedulerSchedule); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	return s.previewDefinition(ctx, data, false)
}

func (s *SchedulerApplicationService) previewDefinition(ctx context.Context, data map[string]any, complete bool) (SchedulerDefinitionPreview, error) {
	var nextRuns []time.Time
	var err error
	if complete {
		nextRuns, err = schedule.PreviewDefinitionData(ctx, data, s.clock.Now(), 3)
	} else {
		nextRuns, err = schedule.PreviewData(ctx, data, s.clock.Now(), 3)
	}
	if err != nil {
		return SchedulerDefinitionPreview{}, apperror.FromError(apperror.KindBadRequest, err)
	}
	preview := SchedulerDefinitionPreview{NextRuns: make([]string, 0, len(nextRuns))}
	for _, next := range nextRuns {
		preview.NextRuns = append(preview.NextRuns, next.UTC().Format(time.RFC3339))
	}
	return preview, nil
}
