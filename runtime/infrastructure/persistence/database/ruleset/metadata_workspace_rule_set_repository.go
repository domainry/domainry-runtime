package ruleset

import (
	"context"
	"fmt"
	"strings"

	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	rulesetmodel "github.com/domainry/domainry-runtime/runtime/domain/ruleset/model"
	rulesetrepository "github.com/domainry/domainry-runtime/runtime/domain/ruleset/repository"
	rulesetvalidation "github.com/domainry/domainry-runtime/runtime/domain/ruleset/validation"
)

type MetadataWorkspaceRuleSetRepository struct {
	metadata metadatarepository.MetadataRepository
}

var _ rulesetrepository.WorkspaceRuleSetRepository = (*MetadataWorkspaceRuleSetRepository)(nil)

func NewMetadataWorkspaceRuleSetRepository(metadata metadatarepository.MetadataRepository) *MetadataWorkspaceRuleSetRepository {
	return &MetadataWorkspaceRuleSetRepository{metadata: metadata}
}

func (r *MetadataWorkspaceRuleSetRepository) ListWorkspaceRuleSetVersions(ctx context.Context, scope principalmodel.QueryScope, ruleSetKey string) ([]rulesetmodel.RuleSetVersion, error) {
	if !scope.Valid() || !scope.WorkspaceID().Valid() {
		return nil, fmt.Errorf("workspace rule set query scope is required")
	}
	if r == nil || r.metadata == nil {
		return nil, fmt.Errorf("metadata rule set source is unavailable")
	}
	ruleSetKey = strings.TrimSpace(ruleSetKey)
	if ruleSetKey == "" {
		return nil, fmt.Errorf("rule set key is required")
	}
	installation := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "resolve workspace rule set definition versions")
	current, found, err := r.metadata.GetDefinition(ctx, installation, "rule_set", ruleSetKey)
	if err != nil {
		return nil, fmt.Errorf("read current rule set %s: %w", ruleSetKey, err)
	}
	if !found || strings.TrimSpace(current.DisabledAt) != "" {
		return []rulesetmodel.RuleSetVersion{}, nil
	}
	versions, err := r.metadata.ListDefinitionVersions(ctx, installation, "rule_set", ruleSetKey)
	if err != nil {
		return nil, fmt.Errorf("list rule set versions %s: %w", ruleSetKey, err)
	}
	workspaceID := scope.WorkspaceID().String()
	result := make([]rulesetmodel.RuleSetVersion, 0, len(versions))
	for _, version := range versions {
		definition, decodeErr := rulesetvalidation.DecodeRuleSetDefinition(ruleSetKey, version.Payload)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode rule set %s version %s: %w", ruleSetKey, version.SchemaVersion, decodeErr)
		}
		result = append(result, rulesetmodel.RuleSetVersion{WorkspaceID: workspaceID, Definition: definition, Version: version.SchemaVersion, ResourceHash: version.SchemaHash})
	}
	return result, nil
}
