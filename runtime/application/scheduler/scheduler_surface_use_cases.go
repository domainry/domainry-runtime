package scheduler

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-scheduler-sdk/schedule"
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

// TenantAdminSchedulerAuthoringContract describes validation of scheduler
// definitions authored in source-controlled project JSON.
type TenantAdminSchedulerAuthoringContract struct {
	ResourceType          string   `json:"resource_type"`
	StatusField           string   `json:"status_field"`
	AllowedStatuses       []string `json:"allowed_statuses"`
	BusinessCalendarField string   `json:"business_calendar_field"`
	MutationOwner         string   `json:"mutation_owner"`
	ValidationEndpoint    string   `json:"validation_endpoint"`
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
			Value:     projectTenantAdminSchedulerDefinition(PublishedDefinition{Key: definitionID, Data: version.Data, CreatedAt: version.CreatedAt}),
		})
	}
	return out, nil
}

func (s *SchedulerApplicationService) TenantAdminAuthoringContract(_ context.Context, principal principalmodel.Principal) (TenantAdminSchedulerAuthoringContract, error) {
	if err := schedulerDefinitionWriteAllowed(principal); err != nil {
		return TenantAdminSchedulerAuthoringContract{}, err
	}
	return TenantAdminSchedulerAuthoringContract{
		ResourceType:          "scheduler",
		StatusField:           "status",
		AllowedStatuses:       []string{"enabled", "disabled", "paused", "archived"},
		BusinessCalendarField: "business_calendar_key",
		MutationOwner:         "source_controlled_json",
		ValidationEndpoint:    "/tenant-admin/scheduler/definitions/validate",
	}, nil
}

func (s *SchedulerApplicationService) AuthorizeOpsRead(ctx context.Context, principal principalmodel.Principal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return schedulerOpsReadAllowed(principal)
}

func projectTenantAdminSchedulerDefinition(definition PublishedDefinition) TenantAdminSchedulerDefinitionDTO {
	return TenantAdminSchedulerDefinitionDTO{
		Key:                 valueOrID(definition.Data, "key", definition.Key),
		Name:                schedulerDTOString(definition.Data, "name"),
		Status:              schedulerDTOString(definition.Data, "status"),
		ScheduleType:        schedulerDTOString(definition.Data, "schedule_type"),
		ScheduleExpression:  schedule.FirstNonEmpty(schedulerDTOString(definition.Data, "schedule_expression"), schedulerDTOString(definition.Data, "cron_expression")),
		IntervalSeconds:     schedulerDTOInt(definition.Data, "interval_seconds"),
		TimeOfDay:           schedulerDTOString(definition.Data, "time_of_day"),
		DayOfWeek:           schedulerDTOString(definition.Data, "day_of_week"),
		DayOfMonth:          schedulerDTOInt(definition.Data, "day_of_month"),
		Timezone:            schedulerDTOString(definition.Data, "timezone"),
		BusinessCalendarKey: schedulerDTOString(definition.Data, "business_calendar_key"),
		TargetType:          schedulerDTOString(definition.Data, "target_type"),
		TargetKey:           schedulerDTOString(definition.Data, "target_key"),
		TargetObject:        schedulerDTOString(definition.Data, "target_object"),
		RunAsRole:           schedulerDTOString(definition.Data, "run_as_role"),
		MaxAttempts:         schedulerDTOInt(definition.Data, "max_attempts"),
		TimeoutSeconds:      schedulerDTOInt(definition.Data, "timeout_seconds"),
		MissedWindowPolicy:  schedulerDTOString(definition.Data, "missed_window_policy"),
		MaxCatchupWindows:   schedulerDTOInt(definition.Data, "max_catchup_windows"),
		RetryBackoff:        schedulerDTOString(definition.Data, "retry_backoff"),
		RetryDelaySeconds:   schedulerDTOInt(definition.Data, "retry_delay_seconds"),
		RetryMaxDelay:       schedulerDTOInt(definition.Data, "retry_max_delay_seconds"),
		ConditionJSON:       schedulerDTOString(definition.Data, "condition_json"),
		PayloadJSON:         schedulerDTOString(definition.Data, "payload_json"),
		IdempotencyKeys:     schedulerDTOString(definition.Data, "idempotency_keys"),
		Description:         schedulerDTOString(definition.Data, "description"),
		NextRunAt:           schedulerDTOString(definition.Data, "next_run_at"),
		CreatedAt:           definition.CreatedAt,
		UpdatedAt:           definition.UpdatedAt,
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
	if principal.HasExactPermission("operations.read") || principal.HasExactPermission("scheduler.command") {
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
	return schedule.Int(data[key], 0)
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
