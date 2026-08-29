package scheduler

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// Status reports only Runtime-owned Scheduler integration state. Execution
// health, runs and dead letters are reported by domainry-scheduler itself.
func (s *SchedulerApplicationService) Status(ctx context.Context, scope principalmodel.SystemScope) (map[string]any, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	status := map[string]any{"execution_owner": "domainry-scheduler", "definitions_available": s != nil && s.definitions != nil}
	if s == nil || s.definitions == nil {
		return status, nil
	}
	definitions, err := s.definitions.ListSchedulerDefinitions(ctx)
	if err != nil {
		status["definition_error"] = err.Error()
		return status, nil
	}
	enabled := 0
	for _, definition := range definitions {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(definition.Data["status"])), "enabled") {
			enabled++
		}
	}
	status["definitions"] = len(definitions)
	status["enabled_definitions"] = enabled
	return status, nil
}
