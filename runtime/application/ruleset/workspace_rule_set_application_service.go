package ruleset

import (
	"context"
	"errors"
	"strings"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	rulesetmodel "github.com/domainry/domainry-runtime/runtime/domain/ruleset/model"
	rulesetrepository "github.com/domainry/domainry-runtime/runtime/domain/ruleset/repository"
	rulesetservice "github.com/domainry/domainry-runtime/runtime/domain/ruleset/service"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type WorkspaceRuleSetApplicationService struct {
	repository rulesetrepository.WorkspaceRuleSetRepository
}

func NewWorkspaceRuleSetApplicationService(repository rulesetrepository.WorkspaceRuleSetRepository) *WorkspaceRuleSetApplicationService {
	return &WorkspaceRuleSetApplicationService{repository: repository}
}

func (s *WorkspaceRuleSetApplicationService) Evaluate(ctx context.Context, ruleSetKey string, effectiveAt time.Time, inputs map[string]any, principal principalmodel.Principal) (rulesetmodel.RuleSetResolution, error) {
	if !principal.Known || principal.SystemScope.Valid() {
		return rulesetmodel.RuleSetResolution{}, apperror.New(apperror.KindForbidden, "backend.rule_set.workspace_principal_required", nil, nil)
	}
	scope, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID)
	if err != nil {
		return rulesetmodel.RuleSetResolution{}, apperror.New(apperror.KindBadRequest, "backend.rule_set.workspace_required", err, nil)
	}
	ruleSetKey = strings.TrimSpace(ruleSetKey)
	if ruleSetKey == "" {
		return rulesetmodel.RuleSetResolution{}, apperror.New(apperror.KindBadRequest, "backend.rule_set.key_required", nil, nil)
	}
	if effectiveAt.IsZero() {
		return rulesetmodel.RuleSetResolution{}, apperror.New(apperror.KindBadRequest, "backend.rule_set.effective_at_required", nil, map[string]string{"rule_set_key": ruleSetKey})
	}
	if s == nil || s.repository == nil {
		return rulesetmodel.RuleSetResolution{}, apperror.New(apperror.KindInternal, "backend.rule_set.repository_unavailable", nil, map[string]string{"rule_set_key": ruleSetKey})
	}
	versions, err := s.repository.ListWorkspaceRuleSetVersions(ctx, scope, ruleSetKey)
	if err != nil {
		return rulesetmodel.RuleSetResolution{}, apperror.New(apperror.KindInternal, "backend.rule_set.versions_read_failed", err, map[string]string{"rule_set_key": ruleSetKey})
	}
	resolved, err := rulesetservice.EvaluateRuleSet(versions, scope.WorkspaceID().String(), ruleSetKey, effectiveAt, inputs)
	if err == nil {
		return resolved, nil
	}
	var ruleSetErr *rulesetmodel.RuleSetError
	// EvaluateRuleSet owns this closed error contract.
	_ = errors.As(err, &ruleSetErr)
	if ruleSetErr.Code == "backend.rule_set.not_effective" {
		return rulesetmodel.RuleSetResolution{}, apperror.FromError(apperror.KindNotFound, err)
	}
	return rulesetmodel.RuleSetResolution{}, apperror.FromError(apperror.KindBadRequest, err)
}
