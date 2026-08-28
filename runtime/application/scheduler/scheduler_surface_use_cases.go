package scheduler

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
)

// TenantAdminSchedulerDefinitionDTO is the governed business-facing scheduler
// definition projection. Runtime execution state is intentionally excluded.
type TenantAdminSchedulerDefinitionDTO struct {
	Key                 string `json:"key"`
	Name                string `json:"name,omitempty"`
	Status              string `json:"status"`
	ScheduleType        string `json:"schedule_type,omitempty"`
	ScheduleExpression  string `json:"schedule_expression,omitempty"`
	IntervalSeconds     int    `json:"interval_seconds,omitempty"`
	TimeOfDay           string `json:"time_of_day,omitempty"`
	DayOfWeek           string `json:"day_of_week,omitempty"`
	DayOfMonth          int    `json:"day_of_month,omitempty"`
	Timezone            string `json:"timezone,omitempty"`
	BusinessCalendarKey string `json:"business_calendar_key,omitempty"`
	TargetType          string `json:"target_type,omitempty"`
	TargetKey           string `json:"target_key,omitempty"`
	TargetObject        string `json:"target_object,omitempty"`
	RunAsRole           string `json:"run_as_role,omitempty"`
	MaxAttempts         int    `json:"max_attempts,omitempty"`
	TimeoutSeconds      int    `json:"timeout_seconds,omitempty"`
	MissedWindowPolicy  string `json:"missed_window_policy,omitempty"`
	MaxCatchupWindows   int    `json:"max_catchup_windows,omitempty"`
	RetryBackoff        string `json:"retry_backoff,omitempty"`
	RetryDelaySeconds   int    `json:"retry_delay_seconds,omitempty"`
	RetryMaxDelay       int    `json:"retry_max_delay_seconds,omitempty"`
	ConditionJSON       string `json:"condition_json,omitempty"`
	PayloadJSON         string `json:"payload_json,omitempty"`
	IdempotencyKeys     string `json:"idempotency_keys,omitempty"`
	Description         string `json:"description,omitempty"`
	NextRunAt           string `json:"next_run_at,omitempty"`
	CreatedAt           string `json:"created_at,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
}

type TenantAdminSchedulerDefinitionVersionDTO struct {
	VersionID string                            `json:"version_id"`
	Event     string                            `json:"event"`
	CreatedAt string                            `json:"created_at"`
	Value     TenantAdminSchedulerDefinitionDTO `json:"value"`
}

// TenantAdminSchedulerAuthoringContract makes the write owner explicit:
// definitions, status and business-calendar bindings are published through a
// reviewed domain change plan, not mutated by an operational scheduler API.
type TenantAdminSchedulerAuthoringContract struct {
	ResourceType            string   `json:"resource_type"`
	StatusField             string   `json:"status_field"`
	AllowedStatuses         []string `json:"allowed_statuses"`
	BusinessCalendarField   string   `json:"business_calendar_field"`
	MutationOwner           string   `json:"mutation_owner"`
	RequiresChangePlan      bool     `json:"requires_change_plan"`
	ValidationEndpoint      string   `json:"validation_endpoint"`
	ChangePlanApplyEndpoint string   `json:"change_plan_apply_endpoint"`
}

type OpsSchedulerRunDTO struct {
	ID             string `json:"id"`
	DefinitionKey  string `json:"definition_key,omitempty"`
	Status         string `json:"status"`
	Attempt        int    `json:"attempt,omitempty"`
	MaxAttempts    int    `json:"max_attempts,omitempty"`
	TriggeredBy    string `json:"triggered_by,omitempty"`
	ScheduledFor   string `json:"scheduled_for,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
	NextRetryAt    string `json:"next_retry_at,omitempty"`
	ErrorCategory  string `json:"error_category,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
	Recoverability string `json:"recoverability,omitempty"`
	LeaseOwner     string `json:"lease_owner,omitempty"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	FencingToken   int    `json:"fencing_token,omitempty"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type OpsSchedulerAttemptDTO struct {
	ID            string `json:"id"`
	RunID         string `json:"run_id,omitempty"`
	EventType     string `json:"event_type,omitempty"`
	Message       string `json:"message,omitempty"`
	Attempt       int    `json:"attempt,omitempty"`
	WorkerID      string `json:"worker_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
}

type OpsSchedulerDeadLetterDTO struct {
	ID            string `json:"id"`
	RunID         string `json:"run_id,omitempty"`
	DefinitionKey string `json:"definition_key,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	FailedAt      string `json:"failed_at,omitempty"`
	ResolvedAt    string `json:"resolved_at,omitempty"`
	ResolvedBy    string `json:"resolved_by,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type OpsSchedulerStateDTO struct {
	Provisioned bool                        `json:"provisioned"`
	Runs        []OpsSchedulerRunDTO        `json:"runs"`
	Attempts    []OpsSchedulerAttemptDTO    `json:"attempts"`
	DeadLetters []OpsSchedulerDeadLetterDTO `json:"dead_letters"`
}

type OpsSchedulerOperationDTO struct {
	Status  string              `json:"status"`
	Message string              `json:"message,omitempty"`
	Run     *OpsSchedulerRunDTO `json:"run,omitempty"`
}

func ProjectOpsSchedulerOperation(result SchedulerOperationResult) OpsSchedulerOperationDTO {
	out := OpsSchedulerOperationDTO{Status: result.Status, Message: result.Message}
	if result.Run.ID != "" {
		run := projectOpsSchedulerRun(result.Run)
		out.Run = &run
	}
	return out
}

func (s *SchedulerApplicationService) TenantAdminDefinitions(ctx context.Context, principal principalmodel.Principal) ([]TenantAdminSchedulerDefinitionDTO, error) {
	if err := schedulerDefinitionReadAllowed(principal); err != nil {
		return nil, err
	}
	if s.definitions == nil {
		return nil, unavailableDefinitionSource()
	}
	definitions, err := s.definitions.ListSchedulerDefinitions(ctx)
	if err != nil {
		return nil, internalError("list tenant admin scheduler definitions", err)
	}
	out := make([]TenantAdminSchedulerDefinitionDTO, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, projectTenantAdminSchedulerDefinition(definition))
	}
	return out, nil
}

func (s *SchedulerApplicationService) TenantAdminDefinition(ctx context.Context, definitionID string, principal principalmodel.Principal) (TenantAdminSchedulerDefinitionDTO, error) {
	definition, err := s.GetDefinition(ctx, definitionID, principal)
	if err != nil {
		return TenantAdminSchedulerDefinitionDTO{}, err
	}
	return projectTenantAdminSchedulerDefinition(definition), nil
}

func (s *SchedulerApplicationService) TenantAdminDefinitionVersions(ctx context.Context, definitionID string, principal principalmodel.Principal) ([]TenantAdminSchedulerDefinitionVersionDTO, error) {
	versions, err := s.DefinitionVersions(ctx, definitionID, principal)
	if err != nil {
		return nil, err
	}
	out := make([]TenantAdminSchedulerDefinitionVersionDTO, 0, len(versions))
	for _, version := range versions {
		out = append(out, TenantAdminSchedulerDefinitionVersionDTO{
			VersionID: version.VersionID,
			Event:     version.Event,
			CreatedAt: version.CreatedAt,
			Value:     projectTenantAdminSchedulerDefinition(recordmodel.Record{ID: definitionID, Data: version.Data, CreatedAt: version.CreatedAt}),
		})
	}
	return out, nil
}

func (s *SchedulerApplicationService) TenantAdminAuthoringContract(_ context.Context, principal principalmodel.Principal) (TenantAdminSchedulerAuthoringContract, error) {
	if err := schedulerDefinitionWriteAllowed(principal); err != nil {
		return TenantAdminSchedulerAuthoringContract{}, err
	}
	return TenantAdminSchedulerAuthoringContract{
		ResourceType:            "scheduler",
		StatusField:             "status",
		AllowedStatuses:         []string{"enabled", "disabled", "paused", "archived"},
		BusinessCalendarField:   "business_calendar_key",
		MutationOwner:           "domain_change_plan",
		RequiresChangePlan:      true,
		ValidationEndpoint:      "/tenant-admin/scheduler/definitions/validate",
		ChangePlanApplyEndpoint: "/tenant-admin/change-plans/apply",
	}, nil
}

func (s *SchedulerApplicationService) OpsState(ctx context.Context, principal principalmodel.Principal) (OpsSchedulerStateDTO, error) {
	if err := schedulerOpsReadAllowed(principal); err != nil {
		return OpsSchedulerStateDTO{}, err
	}
	if s.repository == nil {
		return OpsSchedulerStateDTO{}, schedulerErrorUnavailable("backend.scheduler.repository_unavailable")
	}
	if !s.RuntimeAvailable(ctx, principal) {
		return OpsSchedulerStateDTO{
			Provisioned: false,
			Runs:        []OpsSchedulerRunDTO{},
			Attempts:    []OpsSchedulerAttemptDTO{},
			DeadLetters: []OpsSchedulerDeadLetterDTO{},
		}, nil
	}
	runs, err := s.opsRecords(ctx, principal, "job_run")
	if err != nil {
		return OpsSchedulerStateDTO{}, err
	}
	attempts, err := s.opsRecords(ctx, principal, "job_run_event")
	if err != nil {
		return OpsSchedulerStateDTO{}, err
	}
	deadLetters, err := s.opsRecords(ctx, principal, "job_dead_letter")
	if err != nil {
		return OpsSchedulerStateDTO{}, err
	}
	result := OpsSchedulerStateDTO{
		Provisioned: true,
		Runs:        make([]OpsSchedulerRunDTO, 0, len(runs)),
		Attempts:    make([]OpsSchedulerAttemptDTO, 0, len(attempts)),
		DeadLetters: make([]OpsSchedulerDeadLetterDTO, 0, len(deadLetters)),
	}
	for _, record := range runs {
		result.Runs = append(result.Runs, projectOpsSchedulerRun(record))
	}
	for _, record := range attempts {
		result.Attempts = append(result.Attempts, projectOpsSchedulerAttempt(record))
	}
	for _, record := range deadLetters {
		result.DeadLetters = append(result.DeadLetters, projectOpsSchedulerDeadLetter(record))
	}
	return result, nil
}

func (s *SchedulerApplicationService) opsRecords(ctx context.Context, principal principalmodel.Principal, objectKey string) ([]recordmodel.Record, error) {
	object, err := s.ownerObject(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	page, err := s.repository.ListRecords(ctx, principal.WorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 500})
	if err != nil {
		return nil, internalError("list scheduler "+objectKey, err)
	}
	return page.Items, nil
}

func projectTenantAdminSchedulerDefinition(record recordmodel.Record) TenantAdminSchedulerDefinitionDTO {
	return TenantAdminSchedulerDefinitionDTO{
		Key:                 valueOrID(record.Data, "key", record.ID),
		Name:                schedulerDTOString(record.Data, "name"),
		Status:              schedulerDTOString(record.Data, "status"),
		ScheduleType:        schedulerDTOString(record.Data, "schedule_type"),
		ScheduleExpression:  schedulerpolicy.SchedulerFirstNonEmptyString(schedulerDTOString(record.Data, "schedule_expression"), schedulerDTOString(record.Data, "cron_expression")),
		IntervalSeconds:     schedulerDTOInt(record.Data, "interval_seconds"),
		TimeOfDay:           schedulerDTOString(record.Data, "time_of_day"),
		DayOfWeek:           schedulerDTOString(record.Data, "day_of_week"),
		DayOfMonth:          schedulerDTOInt(record.Data, "day_of_month"),
		Timezone:            schedulerDTOString(record.Data, "timezone"),
		BusinessCalendarKey: schedulerDTOString(record.Data, "business_calendar_key"),
		TargetType:          schedulerDTOString(record.Data, "target_type"),
		TargetKey:           schedulerDTOString(record.Data, "target_key"),
		TargetObject:        schedulerDTOString(record.Data, "target_object"),
		RunAsRole:           schedulerDTOString(record.Data, "run_as_role"),
		MaxAttempts:         schedulerDTOInt(record.Data, "max_attempts"),
		TimeoutSeconds:      schedulerDTOInt(record.Data, "timeout_seconds"),
		MissedWindowPolicy:  schedulerDTOString(record.Data, "missed_window_policy"),
		MaxCatchupWindows:   schedulerDTOInt(record.Data, "max_catchup_windows"),
		RetryBackoff:        schedulerDTOString(record.Data, "retry_backoff"),
		RetryDelaySeconds:   schedulerDTOInt(record.Data, "retry_delay_seconds"),
		RetryMaxDelay:       schedulerDTOInt(record.Data, "retry_max_delay_seconds"),
		ConditionJSON:       schedulerDTOString(record.Data, "condition_json"),
		PayloadJSON:         schedulerDTOString(record.Data, "payload_json"),
		IdempotencyKeys:     schedulerDTOString(record.Data, "idempotency_keys"),
		Description:         schedulerDTOString(record.Data, "description"),
		NextRunAt:           schedulerDTOString(record.Data, "next_run_at"),
		CreatedAt:           record.CreatedAt,
		UpdatedAt:           record.UpdatedAt,
	}
}

func projectOpsSchedulerRun(record recordmodel.Record) OpsSchedulerRunDTO {
	return OpsSchedulerRunDTO{
		ID:             record.ID,
		DefinitionKey:  schedulerDTOString(record.Data, "scheduler_definition_key"),
		Status:         schedulerDTOString(record.Data, "status"),
		Attempt:        schedulerDTOInt(record.Data, "attempt"),
		MaxAttempts:    schedulerDTOInt(record.Data, "max_attempts"),
		TriggeredBy:    schedulerDTOString(record.Data, "triggered_by"),
		ScheduledFor:   schedulerDTOString(record.Data, "scheduled_for"),
		StartedAt:      schedulerDTOString(record.Data, "started_at"),
		FinishedAt:     schedulerDTOString(record.Data, "finished_at"),
		NextRetryAt:    schedulerDTOString(record.Data, "next_retry_at"),
		ErrorCategory:  schedulerDTOString(record.Data, "error_category"),
		ErrorMessage:   schedulerDTOString(record.Data, "error_message"),
		Recoverability: schedulerDTOString(record.Data, "recoverability"),
		LeaseOwner:     schedulerDTOString(record.Data, "lease_owner"),
		LeaseExpiresAt: schedulerDTOString(record.Data, "lease_expires_at"),
		FencingToken:   schedulerDTOInt(record.Data, "fencing_token"),
		CorrelationID:  schedulerDTOString(record.Data, "correlation_id"),
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
	}
}

func projectOpsSchedulerAttempt(record recordmodel.Record) OpsSchedulerAttemptDTO {
	return OpsSchedulerAttemptDTO{
		ID:            record.ID,
		RunID:         schedulerDTOString(record.Data, "job_run_id"),
		EventType:     schedulerDTOString(record.Data, "event_type"),
		Message:       schedulerDTOString(record.Data, "message"),
		Attempt:       schedulerDTOInt(record.Data, "attempt"),
		WorkerID:      schedulerDTOString(record.Data, "worker_id"),
		CorrelationID: schedulerDTOString(record.Data, "correlation_id"),
		CreatedAt:     record.CreatedAt,
	}
}

func projectOpsSchedulerDeadLetter(record recordmodel.Record) OpsSchedulerDeadLetterDTO {
	return OpsSchedulerDeadLetterDTO{
		ID:            record.ID,
		RunID:         schedulerDTOString(record.Data, "job_run_id"),
		DefinitionKey: schedulerDTOString(record.Data, "scheduler_definition_key"),
		Status:        schedulerDTOString(record.Data, "status"),
		Reason:        schedulerDTOString(record.Data, "reason"),
		LastError:     schedulerDTOString(record.Data, "last_error"),
		FailedAt:      schedulerDTOString(record.Data, "failed_at"),
		ResolvedAt:    schedulerDTOString(record.Data, "resolved_at"),
		ResolvedBy:    schedulerDTOString(record.Data, "resolved_by"),
		CreatedAt:     record.CreatedAt,
		UpdatedAt:     record.UpdatedAt,
	}
}

func schedulerDefinitionWriteAllowed(principal principalmodel.Principal) error {
	if err := schedulerAuthorizeCommand(principal); err != nil {
		return err
	}
	if principal.HasPermission("workspace.admin") || principal.HasExactPermission("metadata.write") || principal.HasExactPermission("scheduler.definition.write") {
		return nil
	}
	return forbidden("backend.scheduler.permission_required")
}

func schedulerOpsReadAllowed(principal principalmodel.Principal) error {
	if err := schedulerAuthorizeQuery(principal); err != nil {
		return err
	}
	if principal.HasExactPermission("operations.read") || principal.HasExactPermission("job_run.read") || principal.HasExactPermission("job_run.update") || principal.HasExactPermission("scheduler.command") {
		return nil
	}
	return forbidden("backend.scheduler.permission_required")
}

func schedulerOpsCommandAllowed(principal principalmodel.Principal) error {
	if err := schedulerAuthorizeCommand(principal); err != nil {
		return err
	}
	if principal.HasExactPermission("scheduler.definition.run") || principal.HasExactPermission("job_run.update") || principal.HasExactPermission("scheduler.command") {
		return nil
	}
	return forbidden("backend.scheduler.permission_required")
}

func schedulerDTOString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value := strings.TrimSpace(fmt.Sprint(data[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}

func schedulerDTOInt(data map[string]any, key string) int {
	return schedulerpolicy.SchedulerInt(data[key], 0)
}

func valueOrID(data map[string]any, key, fallback string) string {
	if value := schedulerDTOString(data, key); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func unavailableDefinitionSource() error {
	return schedulerErrorUnavailable("backend.scheduler.definition_source_unavailable")
}

func schedulerErrorUnavailable(code string) error {
	return schedulerError(apperror.KindUnavailable, code, nil)
}
