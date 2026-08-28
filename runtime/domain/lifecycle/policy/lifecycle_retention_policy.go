package policy

import (
	"fmt"
	"strings"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func ValidateRetentionPolicy(policy lifecyclemodel.RetentionPolicy) error {
	if strings.TrimSpace(policy.Key) == "" || strings.TrimSpace(policy.Version) == "" || strings.TrimSpace(policy.Owner) == "" {
		return fmt.Errorf("retention policy key, version, and owner are required")
	}
	if !validRetentionClass(policy.Class) {
		return fmt.Errorf("unsupported retention class %q", policy.Class)
	}
	if policy.DefaultRetention <= 0 || policy.MinimumRetention <= 0 {
		return fmt.Errorf("retention durations must be positive")
	}
	if policy.DefaultRetention < policy.MinimumRetention {
		return fmt.Errorf("default retention cannot be shorter than minimum retention")
	}
	if policy.ReplayWindow > 0 && (policy.DefaultRetention < policy.ReplayWindow || policy.MinimumRetention < policy.ReplayWindow) {
		return fmt.Errorf("retention cannot be shorter than the declared replay window")
	}
	for status, retention := range policy.StatusRetention {
		if strings.TrimSpace(status) == "" || retention < policy.MinimumRetention {
			return fmt.Errorf("status retention must name a status group and meet the policy minimum")
		}
	}
	if policy.WorkspaceMayReduce && (policy.Class == lifecyclemodel.RetentionClassLegalAudit || containsSensitivity(policy.Sensitivity, lifecyclemodel.SensitivityAudit) || containsSensitivity(policy.Sensitivity, lifecyclemodel.SensitivitySecurity)) {
		return fmt.Errorf("legal, audit, and security retention cannot be reduced by a workspace")
	}
	if policy.Class == lifecyclemodel.RetentionClassUserErase && policy.EraseBehavior == lifecyclemodel.EraseBehaviorNotEligible {
		return fmt.Errorf("user erase policy must define delete or anonymize behavior")
	}
	if policy.BackupBehavior == "" || policy.EraseBehavior == "" {
		return fmt.Errorf("backup and erase behavior are required")
	}
	return nil
}

func EffectiveWorkspaceRetention(policy lifecyclemodel.RetentionPolicy, override lifecyclemodel.WorkspaceRetentionOverride) (time.Duration, error) {
	if err := ValidateRetentionPolicy(policy); err != nil {
		return 0, err
	}
	if strings.TrimSpace(override.WorkspaceID) == "" || override.Retention <= 0 {
		return 0, fmt.Errorf("workspace and positive retention are required")
	}
	if override.Retention < policy.DefaultRetention && !policy.WorkspaceMayReduce {
		return 0, fmt.Errorf("workspace retention cannot reduce policy default")
	}
	if override.Retention > policy.DefaultRetention && !policy.WorkspaceMayExtend {
		return 0, fmt.Errorf("workspace retention cannot extend policy default")
	}
	if override.Retention < policy.MinimumRetention {
		return 0, fmt.Errorf("workspace retention cannot be shorter than policy minimum")
	}
	return override.Retention, nil
}

func validRetentionClass(class lifecyclemodel.RetentionClass) bool {
	switch class {
	case lifecyclemodel.RetentionClassProduct, lifecyclemodel.RetentionClassLegalAudit, lifecyclemodel.RetentionClassTechnical, lifecyclemodel.RetentionClassUserErase:
		return true
	default:
		return false
	}
}

func containsSensitivity(values []lifecyclemodel.Sensitivity, target lifecyclemodel.Sensitivity) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
