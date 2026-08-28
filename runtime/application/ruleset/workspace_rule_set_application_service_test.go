package ruleset

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	expressionmodel "github.com/domainry/domainry-runtime/runtime/domain/expression/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	rulesetmodel "github.com/domainry/domainry-runtime/runtime/domain/ruleset/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type workspaceRuleSetRepositoryStub struct {
	versions []rulesetmodel.RuleSetVersion
	err      error
	scope    principalmodel.QueryScope
	key      string
}

func (r *workspaceRuleSetRepositoryStub) ListWorkspaceRuleSetVersions(_ context.Context, scope principalmodel.QueryScope, key string) ([]rulesetmodel.RuleSetVersion, error) {
	r.scope, r.key = scope, key
	return append([]rulesetmodel.RuleSetVersion(nil), r.versions...), r.err
}

func TestWorkspaceRuleSetApplicationEvaluatesExplicitWorkspaceAndEvidence(t *testing.T) {
	repository := &workspaceRuleSetRepositoryStub{versions: []rulesetmodel.RuleSetVersion{{WorkspaceID: "workspace-a", Version: "7", ResourceHash: "hash-7", Definition: rulesetApplicationDefinition()}}}
	service := NewWorkspaceRuleSetApplicationService(repository)
	resolved, err := service.Evaluate(t.Context(), " policy.limit ", time.Date(2026, 7, 21, 3, 4, 5, 0, time.UTC), map[string]any{"count": 2}, ruleSetPrincipal("workspace-a"))
	if err != nil {
		t.Fatal(err)
	}
	if !repository.scope.Valid() || repository.scope.WorkspaceID().String() != "workspace-a" || repository.key != "policy.limit" || resolved.Version != "7" || resolved.Outputs["allowed"] != true {
		t.Fatalf("scope=%+v key=%q resolved=%+v", repository.scope, repository.key, resolved)
	}
}

func TestWorkspaceRuleSetApplicationRejectsInvalidBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		service   *WorkspaceRuleSetApplicationService
		key       string
		effective time.Time
		principal principalmodel.Principal
		code      string
	}{
		{name: "unknown principal", service: NewWorkspaceRuleSetApplicationService(&workspaceRuleSetRepositoryStub{}), key: "policy", effective: now, code: "backend.rule_set.workspace_principal_required"},
		{name: "system principal", service: NewWorkspaceRuleSetApplicationService(&workspaceRuleSetRepositoryStub{}), key: "policy", effective: now, principal: principalmodel.NewSystemPrincipal("system", principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test")), code: "backend.rule_set.workspace_principal_required"},
		{name: "workspace required", service: NewWorkspaceRuleSetApplicationService(&workspaceRuleSetRepositoryStub{}), key: "policy", effective: now, principal: ruleSetPrincipal(""), code: "backend.rule_set.workspace_required"},
		{name: "key required", service: NewWorkspaceRuleSetApplicationService(&workspaceRuleSetRepositoryStub{}), effective: now, principal: ruleSetPrincipal("workspace-a"), code: "backend.rule_set.key_required"},
		{name: "effective required", service: NewWorkspaceRuleSetApplicationService(&workspaceRuleSetRepositoryStub{}), key: "policy", principal: ruleSetPrincipal("workspace-a"), code: "backend.rule_set.effective_at_required"},
		{name: "repository required", service: NewWorkspaceRuleSetApplicationService(nil), key: "policy", effective: now, principal: ruleSetPrincipal("workspace-a"), code: "backend.rule_set.repository_unavailable"},
		{name: "nil service", service: nil, key: "policy", effective: now, principal: ruleSetPrincipal("workspace-a"), code: "backend.rule_set.repository_unavailable"},
		{name: "repository failure", service: NewWorkspaceRuleSetApplicationService(&workspaceRuleSetRepositoryStub{err: errors.New("read failed")}), key: "policy", effective: now, principal: ruleSetPrincipal("workspace-a"), code: "backend.rule_set.versions_read_failed"},
		{name: "version absent", service: NewWorkspaceRuleSetApplicationService(&workspaceRuleSetRepositoryStub{}), key: "policy", effective: now, principal: ruleSetPrincipal("workspace-a"), code: "backend.rule_set.not_effective"},
		{name: "invalid version", service: NewWorkspaceRuleSetApplicationService(&workspaceRuleSetRepositoryStub{versions: []rulesetmodel.RuleSetVersion{{WorkspaceID: "workspace-a", Version: "1", Definition: rulesetmodel.RuleSetDefinition{Key: "policy", EffectiveFrom: "invalid"}}}}), key: "policy", effective: now, principal: ruleSetPrincipal("workspace-a"), code: "backend.rule_set.effective_from_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.service.Evaluate(t.Context(), test.key, test.effective, map[string]any{}, test.principal)
			if apperror.CodeOf(err) != test.code {
				t.Fatalf("code=%q want=%q err=%v", apperror.CodeOf(err), test.code, err)
			}
		})
	}
}

func rulesetLiteral(valueType string, value any) expressionmodel.BusinessExpression {
	return expressionmodel.BusinessExpression{Kind: "literal", ValueType: valueType, Value: value}
}

func rulesetApplicationDefinition() rulesetmodel.RuleSetDefinition {
	return rulesetmodel.RuleSetDefinition{Key: "policy.limit", Name: "Limit", MatchPolicy: "first_match", InputTypes: map[string]string{"count": "integer"}, OutputTypes: map[string]string{"allowed": "boolean"}, EffectiveFrom: "2026-01-01", Rules: []rulesetmodel.RuleSetRule{{Key: "allow", Priority: 1, When: rulesetLiteral("boolean", true), Outputs: map[string]expressionmodel.BusinessExpression{"allowed": rulesetLiteral("boolean", true)}}}, DefaultOutputs: map[string]expressionmodel.BusinessExpression{"allowed": rulesetLiteral("boolean", false)}}
}

func ruleSetPrincipal(workspaceID string) principalmodel.Principal {
	return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: workspaceID, UserID: "user-a"}}
}
