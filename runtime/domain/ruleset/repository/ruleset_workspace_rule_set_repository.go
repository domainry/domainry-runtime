package repository

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	rulesetmodel "github.com/domainry/domainry-runtime/runtime/domain/ruleset/model"
)

// WorkspaceRuleSetRepository exposes only workspace-scoped version reads.
// Persistence adapters must reject missing or system scopes.
type WorkspaceRuleSetRepository interface {
	ListWorkspaceRuleSetVersions(context.Context, principalmodel.QueryScope, string) ([]rulesetmodel.RuleSetVersion, error)
}
