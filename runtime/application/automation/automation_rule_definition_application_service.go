package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

var automationManagedRuleKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

func (s *AutomationManagementApplicationService) ManagedRules(ctx context.Context, principal principalmodel.Principal) ([]automationmodel.AutomationManagedRule, error) {
	if err := automationAuthorizeEndpoint(principal, "GET /automation/rules"); err != nil {
		return nil, err
	}
	return s.managedRules(ctx, automationWorkspaceID(principal))
}

func (s *AutomationManagementApplicationService) managedRules(ctx context.Context, workspaceID string) ([]automationmodel.AutomationManagedRule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	definitions, err := s.workspaceDefinitions(ctx, workspaceID)
	if err != nil {
		return nil, managementError(apperror.KindInternal, "backend.automation.definition_read_failed", err)
	}
	items := map[string]automationmodel.AutomationManagedRule{}
	if s.dependencies.Rules != nil {
		for _, rule := range s.dependencies.Rules.List() {
			items[rule.Key] = automationmodel.AutomationManagedRule{AutomationRuleSchema: cloneAutomationRule(rule), Source: "project"}
		}
	}
	for _, definition := range definitions {
		item, exists := items[definition.RuleKey]
		if definition.Published != nil {
			item.AutomationRuleSchema = cloneAutomationRule(definition.Published.Rule)
			item.Source = "workspace"
		} else if !exists && definition.Draft != nil {
			item.AutomationRuleSchema = cloneAutomationRule(definition.Draft.Rule)
			item.Source = "draft"
		}
		if definition.Enabled != nil {
			item.Enabled = *definition.Enabled
		}
		item.ManagementRevision = definition.Revision
		item.Draft = cloneAutomationDraft(definition.Draft)
		if definition.Published != nil {
			item.PublishedVersion = definition.Published.Version
		}
		item.UpdatedAt = definition.UpdatedAt
		items[definition.RuleKey] = item
	}
	result := make([]automationmodel.AutomationManagedRule, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

func (s *AutomationManagementApplicationService) ManagedRule(ctx context.Context, ruleKey string, principal principalmodel.Principal) (automationmodel.AutomationManagedRule, bool, error) {
	if err := automationAuthorizeEndpoint(principal, "GET /automation/rules/{ruleKey}"); err != nil {
		return automationmodel.AutomationManagedRule{}, false, err
	}
	items, err := s.managedRules(ctx, automationWorkspaceID(principal))
	if err != nil {
		return automationmodel.AutomationManagedRule{}, false, err
	}
	ruleKey = strings.TrimSpace(ruleKey)
	for _, item := range items {
		if item.Key == ruleKey {
			return item, true, nil
		}
	}
	return automationmodel.AutomationManagedRule{}, false, nil
}

func (s *AutomationManagementApplicationService) EffectiveRules(ctx context.Context, workspaceID string) ([]automationmodel.AutomationRuleSchema, error) {
	rules := map[string]automationmodel.AutomationRuleSchema{}
	if s.dependencies.Rules != nil {
		for _, rule := range s.dependencies.Rules.List() {
			rules[rule.Key] = cloneAutomationRule(rule)
		}
	}
	definitions, err := s.workspaceDefinitions(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, definition := range definitions {
		rule, exists := rules[definition.RuleKey]
		if definition.Published != nil {
			rule, exists = cloneAutomationRule(definition.Published.Rule), true
		}
		if !exists {
			continue
		}
		if definition.Enabled != nil {
			rule.Enabled = *definition.Enabled
		}
		rules[definition.RuleKey] = rule
	}
	result := make([]automationmodel.AutomationRuleSchema, 0, len(rules))
	for _, rule := range rules {
		result = append(result, rule)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

func (s *AutomationManagementApplicationService) EffectiveRule(ctx context.Context, workspaceID, ruleKey string) (automationmodel.AutomationRuleSchema, bool, error) {
	rules, err := s.EffectiveRules(ctx, workspaceID)
	if err != nil {
		return automationmodel.AutomationRuleSchema{}, false, err
	}
	ruleKey = strings.TrimSpace(ruleKey)
	for _, rule := range rules {
		if rule.Key == ruleKey {
			return rule, true, nil
		}
	}
	return automationmodel.AutomationRuleSchema{}, false, nil
}

func (s *AutomationManagementApplicationService) SaveDraft(ctx context.Context, ruleKey string, request automationmodel.AutomationSaveDraftRequest, principal principalmodel.Principal) (automationmodel.AutomationRuleDefinition, error) {
	if err := automationAuthorizeEndpoint(principal, "PUT /automation/rules/{ruleKey}/draft"); err != nil {
		return automationmodel.AutomationRuleDefinition{}, err
	}
	ruleKey = strings.TrimSpace(ruleKey)
	request.Rule.Key = strings.TrimSpace(request.Rule.Key)
	if !automationManagedRuleKeyPattern.MatchString(ruleKey) || request.Rule.Key != ruleKey {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindBadRequest, "backend.automation.rule_key_invalid", nil)
	}
	if s.dependencies.Definitions == nil {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindInternal, "backend.automation.definition_store_unavailable", nil)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	definition, found, err := s.dependencies.Definitions.Get(ctx, automationWorkspaceID(principal), ruleKey)
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindInternal, "backend.automation.definition_read_failed", err)
	}
	if !found {
		definition = automationmodel.AutomationRuleDefinition{WorkspaceID: automationWorkspaceID(principal), RuleKey: ruleKey, CreatedBy: principal.UserID, CreatedAt: now}
	}
	draftRevision := 1
	if definition.Draft != nil {
		draftRevision = definition.Draft.Revision + 1
	}
	definition.Draft = &automationmodel.AutomationRuleDraft{Revision: draftRevision, Rule: cloneAutomationRule(request.Rule), UpdatedBy: principal.UserID, UpdatedAt: now}
	definition.UpdatedBy, definition.UpdatedAt = principal.UserID, now
	stored, changed, err := s.dependencies.Definitions.Put(ctx, definition, request.ExpectedRevision)
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindInternal, "backend.automation.definition_write_failed", err)
	}
	if !changed {
		return stored, managementError(apperror.KindConflict, "backend.automation.definition_revision_conflict", nil)
	}
	s.auditDefinition(ctx, "automation_rule_draft_saved", ruleKey, principal, stored)
	return stored, nil
}

func (s *AutomationManagementApplicationService) PublishDraft(ctx context.Context, ruleKey string, request automationmodel.AutomationPublishDraftRequest, principal principalmodel.Principal) (automationmodel.AutomationRuleDefinition, error) {
	if err := automationAuthorizeEndpoint(principal, "POST /automation/rules/{ruleKey}/publish"); err != nil {
		return automationmodel.AutomationRuleDefinition{}, err
	}
	definition, found, err := s.loadDefinition(ctx, principal, ruleKey)
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, err
	}
	if !found || definition.Draft == nil {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindNotFound, "backend.automation.draft_not_found", nil)
	}
	if definition.Revision != request.ExpectedRevision {
		return definition, managementError(apperror.KindConflict, "backend.automation.definition_revision_conflict", nil)
	}
	rule := cloneAutomationRule(definition.Draft.Rule)
	if s.dependencies.ValidateDefinition == nil {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindInternal, "backend.automation.validation_unavailable", nil)
	}
	if err := s.dependencies.ValidateDefinition(ctx, rule); err != nil {
		return automationmodel.AutomationRuleDefinition{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	version := 1
	if definition.Published != nil {
		version = definition.Published.Version + 1
	}
	if definition.Enabled == nil {
		enabled := false
		if source, ok := s.sourceRule(rule.Key); ok {
			enabled = source.Enabled
		}
		definition.Enabled = &enabled
	}
	rule.Enabled = *definition.Enabled
	definition.Published = &automationmodel.AutomationRuleVersion{Version: version, ContentHash: automationRuleContentHash(rule), Rule: rule, PublishedBy: principal.UserID, PublishedAt: now}
	definition.Draft = nil
	definition.UpdatedBy, definition.UpdatedAt = principal.UserID, now
	stored, changed, err := s.dependencies.Definitions.Put(ctx, definition, request.ExpectedRevision)
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindInternal, "backend.automation.definition_write_failed", err)
	}
	if !changed {
		return stored, managementError(apperror.KindConflict, "backend.automation.definition_revision_conflict", nil)
	}
	s.auditDefinition(ctx, "automation_rule_published", ruleKey, principal, stored)
	return stored, nil
}

func (s *AutomationManagementApplicationService) SetEnabled(ctx context.Context, ruleKey string, request automationmodel.AutomationSetEnabledRequest, principal principalmodel.Principal) (automationmodel.AutomationRuleDefinition, error) {
	if err := automationAuthorizeEndpoint(principal, "POST /automation/rules/{ruleKey}/state"); err != nil {
		return automationmodel.AutomationRuleDefinition{}, err
	}
	definition, found, err := s.loadDefinition(ctx, principal, ruleKey)
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, err
	}
	_, sourceExists := s.sourceRule(strings.TrimSpace(ruleKey))
	if !sourceExists && (!found || definition.Published == nil) {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindNotFound, "backend.automation.not_found", nil)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if !found {
		definition = automationmodel.AutomationRuleDefinition{WorkspaceID: automationWorkspaceID(principal), RuleKey: strings.TrimSpace(ruleKey), CreatedBy: principal.UserID, CreatedAt: now}
	}
	definition.Enabled = boolPointer(request.Enabled)
	definition.UpdatedBy, definition.UpdatedAt = principal.UserID, now
	stored, changed, err := s.dependencies.Definitions.Put(ctx, definition, request.ExpectedRevision)
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindInternal, "backend.automation.definition_write_failed", err)
	}
	if !changed {
		return stored, managementError(apperror.KindConflict, "backend.automation.definition_revision_conflict", nil)
	}
	event := "automation_rule_paused"
	if request.Enabled {
		event = "automation_rule_enabled"
	}
	s.auditDefinition(ctx, event, ruleKey, principal, stored)
	return stored, nil
}

func (s *AutomationManagementApplicationService) DiscardDraft(ctx context.Context, ruleKey string, request automationmodel.AutomationDiscardDraftRequest, principal principalmodel.Principal) (automationmodel.AutomationRuleDefinition, error) {
	if err := automationAuthorizeEndpoint(principal, "POST /automation/rules/{ruleKey}/draft/discard"); err != nil {
		return automationmodel.AutomationRuleDefinition{}, err
	}
	definition, found, err := s.loadDefinition(ctx, principal, ruleKey)
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, err
	}
	if !found || definition.Draft == nil {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindNotFound, "backend.automation.draft_not_found", nil)
	}
	definition.Draft = nil
	definition.UpdatedBy, definition.UpdatedAt = principal.UserID, time.Now().UTC().Format(time.RFC3339Nano)
	stored, changed, err := s.dependencies.Definitions.Put(ctx, definition, request.ExpectedRevision)
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, managementError(apperror.KindInternal, "backend.automation.definition_write_failed", err)
	}
	if !changed {
		return stored, managementError(apperror.KindConflict, "backend.automation.definition_revision_conflict", nil)
	}
	s.auditDefinition(ctx, "automation_rule_draft_discarded", ruleKey, principal, stored)
	return stored, nil
}

func (s *AutomationManagementApplicationService) loadDefinition(ctx context.Context, principal principalmodel.Principal, ruleKey string) (automationmodel.AutomationRuleDefinition, bool, error) {
	if s.dependencies.Definitions == nil {
		return automationmodel.AutomationRuleDefinition{}, false, managementError(apperror.KindInternal, "backend.automation.definition_store_unavailable", nil)
	}
	definition, found, err := s.dependencies.Definitions.Get(ctx, automationWorkspaceID(principal), strings.TrimSpace(ruleKey))
	if err != nil {
		return automationmodel.AutomationRuleDefinition{}, false, managementError(apperror.KindInternal, "backend.automation.definition_read_failed", err)
	}
	return definition, found, nil
}

func (s *AutomationManagementApplicationService) workspaceDefinitions(ctx context.Context, workspaceID string) ([]automationmodel.AutomationRuleDefinition, error) {
	if s.dependencies.Definitions == nil {
		return []automationmodel.AutomationRuleDefinition{}, nil
	}
	return s.dependencies.Definitions.List(ctx, strings.TrimSpace(workspaceID))
}

func (s *AutomationManagementApplicationService) sourceRule(ruleKey string) (automationmodel.AutomationRuleSchema, bool) {
	if s.dependencies.Rules == nil {
		return automationmodel.AutomationRuleSchema{}, false
	}
	return s.dependencies.Rules.Get(strings.TrimSpace(ruleKey))
}

func (s *AutomationManagementApplicationService) auditDefinition(ctx context.Context, event, ruleKey string, principal principalmodel.Principal, definition automationmodel.AutomationRuleDefinition) {
	if s.dependencies.Audit == nil {
		return
	}
	s.dependencies.Audit(ctx, event, "automation_rule", strings.TrimSpace(ruleKey), principal, event, nil, nil, map[string]any{
		"rule_key": strings.TrimSpace(ruleKey), "revision": definition.Revision, "enabled": definition.Enabled, "has_draft": definition.Draft != nil,
	})
}

func cloneAutomationRule(rule automationmodel.AutomationRuleSchema) automationmodel.AutomationRuleSchema {
	payload, _ := json.Marshal(rule)
	var cloned automationmodel.AutomationRuleSchema
	_ = json.Unmarshal(payload, &cloned)
	return cloned
}

func cloneAutomationDraft(draft *automationmodel.AutomationRuleDraft) *automationmodel.AutomationRuleDraft {
	if draft == nil {
		return nil
	}
	cloned := *draft
	cloned.Rule = cloneAutomationRule(draft.Rule)
	return &cloned
}

func automationRuleContentHash(rule automationmodel.AutomationRuleSchema) string {
	payload, _ := json.Marshal(rule)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func boolPointer(value bool) *bool { return &value }
