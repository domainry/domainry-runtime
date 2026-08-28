package automationseed

import (
	"context"
	"fmt"
	"strings"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"
)

func SyncExecutionSeeds(ctx context.Context, repository automationrepository.ExecutionSeedRepository, seeds []automationmodel.AutomationRuleExecution) error {
	if repository == nil {
		return nil
	}
	for index, seed := range seeds {
		if strings.TrimSpace(seed.ID) == "" {
			return fmt.Errorf("automation execution seed %d: id is required", index)
		}
		if strings.TrimSpace(seed.RuleKey) == "" || strings.TrimSpace(seed.ObjectKey) == "" || strings.TrimSpace(seed.Phase) == "" || strings.TrimSpace(seed.Operation) == "" || strings.TrimSpace(seed.Status) == "" {
			return fmt.Errorf("automation execution seed %s: rule_key, object_key, phase, operation, and status are required", seed.ID)
		}
		if _, err := repository.InsertExecutionSeed(ctx, seed.WorkspaceID, seed); err != nil {
			return fmt.Errorf("sync automation execution seed %s: %w", seed.ID, err)
		}
	}
	return nil
}
