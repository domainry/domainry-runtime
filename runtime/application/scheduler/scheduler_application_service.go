package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
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

func (s *SchedulerApplicationService) GetDefinition(ctx context.Context, definitionID string, principal principalmodel.Principal) (PublishedDefinition, error) {
	if err := schedulerDefinitionReadAllowed(principal); err != nil {
		return PublishedDefinition{}, err
	}
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
	if err := schedulerDefinitionReadAllowed(principal); err != nil {
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

func schedulerDefinitionReadAllowed(principal principalmodel.Principal) error {
	if err := schedulerAuthorizeQuery(principal); err != nil {
		return err
	}
	if principal.HasPermission("workspace.admin") || principal.HasExactPermission("metadata.read") || principal.HasExactPermission("scheduler.definition.read") {
		return nil
	}
	return forbidden("backend.scheduler.permission_required")
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
	runtime         ScheduledWorkflowRuntime
	definitions     SchedulerDefinitionSource
	clock           workerplatform.Clock
	reportSnapshots ReportSnapshotRuntime
}

func (s *SchedulerApplicationService) UseDefinitionSource(source SchedulerDefinitionSource) {
	if s != nil {
		s.definitions = source
	}
}

func schedulerOperationAllowed(principal principalmodel.Principal) error {
	if err := schedulerAuthorizeCommand(principal); err != nil {
		return err
	}
	if principal.HasPermission("workspace.admin") || principal.HasExactPermission("scheduler.command") {
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

func schedulerAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
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
	s.reportSnapshots = runtime
}

func NewSchedulerApplicationService(runtime ScheduledWorkflowRuntime) *SchedulerApplicationService {
	return NewSchedulerApplicationServiceWithClock(runtime, workerplatform.SystemClock{})
}

func NewSchedulerApplicationServiceWithClock(runtime ScheduledWorkflowRuntime, clock workerplatform.Clock) *SchedulerApplicationService {
	if clock == nil {
		clock = workerplatform.SystemClock{}
	}
	return &SchedulerApplicationService{runtime: runtime, clock: clock}
}

func (s *SchedulerApplicationService) PreviewDefinition(ctx context.Context, data map[string]any, principal principalmodel.Principal) (SchedulerDefinitionPreview, error) {
	if err := ctx.Err(); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	if err := schedulerDefinitionWriteAllowed(principal); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	if err := validateSchedulerDefinitionContract(ctx, data); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	return s.previewSchedule(data), nil
}

// PreviewSchedule validates and previews a leaf schedule payload without a
// synthetic Scheduler job definition.
func (s *SchedulerApplicationService) PreviewSchedule(ctx context.Context, data map[string]any, principal principalmodel.Principal) (SchedulerDefinitionPreview, error) {
	if err := ctx.Err(); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	if err := schedulerDefinitionWriteAllowed(principal); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	if err := validateSchedulerScheduleFragment(ctx, data); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	return s.previewSchedule(data), nil
}

func (s *SchedulerApplicationService) previewSchedule(data map[string]any) SchedulerDefinitionPreview {
	definition := schedulerScheduleFromData(cloneSchedulerDefinitionData(data))
	cursor := s.clock.Now()
	preview := SchedulerDefinitionPreview{NextRuns: make([]string, 0, 3)}
	for range 3 {
		next := schedule.NextSchedule(definition, cursor)
		preview.NextRuns = append(preview.NextRuns, next.UTC().Format(time.RFC3339))
		cursor = next
	}
	return preview
}

func cloneSchedulerDefinitionData(data map[string]any) map[string]any {
	clone := make(map[string]any, len(data))
	for key, value := range data {
		clone[key] = value
	}
	return clone
}

func schedulerScheduleFromData(data map[string]any) schedulersdk.Schedule {
	return schedulersdk.Schedule{
		Type:            strings.TrimSpace(fmt.Sprint(data["schedule_type"])),
		Expression:      strings.TrimSpace(fmt.Sprint(data["schedule_expression"])),
		Timezone:        strings.TrimSpace(fmt.Sprint(data["timezone"])),
		IntervalSeconds: schedule.Int(data["interval_seconds"], 0),
		TimeOfDay:       strings.TrimSpace(fmt.Sprint(data["time_of_day"])),
		DayOfWeek:       strings.TrimSpace(fmt.Sprint(data["day_of_week"])),
		DayOfMonth:      schedule.Int(data["day_of_month"], 0),
	}
}
