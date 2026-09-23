package policy

import (
	"fmt"
	"strings"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func OperationsRetentionDuration(definition operationsmodel.OperationsDefinition, status operationsmodel.OperationsStatus) (time.Duration, error) {
	retention := definition.Retention
	if strings.TrimSpace(retention.PolicyKey) == "" || retention.MinimumRetentionSeconds <= 0 {
		return 0, fmt.Errorf("operations.retention_policy_required")
	}
	switch retention.Class {
	case operationsmodel.OperationsRetentionTechnical, operationsmodel.OperationsRetentionLegalAudit:
	default:
		return 0, fmt.Errorf("operations.retention_class_invalid")
	}
	seconds := retention.FailedRetentionSeconds
	if status == operationsmodel.OperationsStatusSucceeded {
		seconds = retention.SucceededRetentionSeconds
	} else if status != operationsmodel.OperationsStatusFailed {
		return 0, fmt.Errorf("operations.retention_terminal_status_required")
	}
	if seconds < retention.MinimumRetentionSeconds {
		return 0, fmt.Errorf("operations.retention_below_minimum")
	}
	return time.Duration(seconds) * time.Second, nil
}
