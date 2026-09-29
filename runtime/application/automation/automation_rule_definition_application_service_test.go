package automation

import (
	"context"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type automationDefinitionMemoryStore struct {
	items map[string]automationmodel.AutomationRuleDefinition
}

func (s *automationDefinitionMemoryStore) key(workspaceID, ruleKey string) string {
	return workspaceID + "\x00" + ruleKey
}

func (s *automationDefinitionMemoryStore) Get(_ context.Context, workspaceID, ruleKey string) (automationmodel.AutomationRuleDefinition, bool, error) {
	item, found := s.items[s.key(workspaceID, ruleKey)]
	return item, found, nil
}

func (s *automationDefinitionMemoryStore) List(_ context.Context, workspaceID string) ([]automationmodel.AutomationRuleDefinition, error) {
	result := []automationmodel.AutomationRuleDefinition{}
	for _, item := range s.items {
		if item.WorkspaceID == workspaceID {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *automationDefinitionMemoryStore) Put(_ context.Context, next automationmodel.AutomationRuleDefinition, expectedRevision int) (automationmodel.AutomationRuleDefinition, bool, error) {
	key := s.key(next.WorkspaceID, next.RuleKey)
	current, found := s.items[key]
	if found && current.Revision != expectedRevision || !found && expectedRevision != 0 {
		return current, false, nil
	}
	next.Revision = expectedRevision + 1
	s.items[key] = next
	return next, true, nil
}

func automationDefinitionPrincipal(workspaceID string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: workspaceID, UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{
		"runtime.automation.list_automation_rules", "runtime.automation.get_automation_rule", "runtime.automation.save_rule_draft",
		"runtime.automation.discard_rule_draft", "runtime.automation.publish_rule", "runtime.automation.enable_rule",
	}})
}

func TestAutomationRuleDraftPublishPauseAndWorkspaceIsolation(t *testing.T) {
	store := &automationDefinitionMemoryStore{items: map[string]automationmodel.AutomationRuleDefinition{}}
	source := automationmodel.AutomationRuleSchema{Key: "account.created", Name: "Source", ObjectKey: "account", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "create"}, Instructions: []automationmodel.AutomationInstructionSchema{{Key: "event", Type: "emit_event"}}}
	service := NewAutomationManagementApplicationService(AutomationManagementDependencies{
		Rules: managementRuleRegistry{rules: []automationmodel.AutomationRuleSchema{source}}, Definitions: store,
		ValidateDefinition: func(context.Context, automationmodel.AutomationRuleSchema) error { return nil },
	})
	principal := automationDefinitionPrincipal("workspace-a")
	draftRule := source
	draftRule.Name = "Workspace version"
	draft, err := service.SaveDraft(t.Context(), source.Key, automationmodel.AutomationSaveDraftRequest{Rule: draftRule, ExpectedRevision: 0}, principal)
	if err != nil || draft.Revision != 1 || draft.Draft == nil {
		t.Fatalf("draft=%#v err=%v", draft, err)
	}
	effective, found, err := service.EffectiveRule(t.Context(), principal.WorkspaceID, source.Key)
	if err != nil || !found || effective.Name != "Source" {
		t.Fatalf("unpublished draft changed execution rule=%#v found=%v err=%v", effective, found, err)
	}
	published, err := service.PublishDraft(t.Context(), source.Key, automationmodel.AutomationPublishDraftRequest{ExpectedRevision: 1}, principal)
	if err != nil || published.Revision != 2 || published.Draft != nil || published.Published == nil || published.Published.Version != 1 {
		t.Fatalf("published=%#v err=%v", published, err)
	}
	effective, found, err = service.EffectiveRule(t.Context(), principal.WorkspaceID, source.Key)
	if err != nil || !found || effective.Name != "Workspace version" || !effective.Enabled {
		t.Fatalf("published execution rule=%#v found=%v err=%v", effective, found, err)
	}
	paused, err := service.SetEnabled(t.Context(), source.Key, automationmodel.AutomationSetEnabledRequest{Enabled: false, ExpectedRevision: 2}, principal)
	if err != nil || paused.Revision != 3 || paused.Enabled == nil || *paused.Enabled {
		t.Fatalf("paused=%#v err=%v", paused, err)
	}
	if paused.Published == nil || !paused.Published.Rule.Enabled || paused.Published.Version != 1 {
		t.Fatalf("state toggle mutated immutable published version=%#v", paused.Published)
	}
	effective, _, err = service.EffectiveRule(t.Context(), principal.WorkspaceID, source.Key)
	if err != nil || effective.Enabled {
		t.Fatalf("paused rule remained executable=%#v err=%v", effective, err)
	}
	foreign, _, err := service.EffectiveRule(t.Context(), "workspace-b", source.Key)
	if err != nil || !foreign.Enabled || foreign.Name != "Source" {
		t.Fatalf("workspace override leaked=%#v err=%v", foreign, err)
	}
	if _, err := service.SetEnabled(t.Context(), source.Key, automationmodel.AutomationSetEnabledRequest{Enabled: true, ExpectedRevision: 2}, principal); apperror.CodeOf(err) != "backend.automation.definition_revision_conflict" {
		t.Fatalf("stale state mutation err=%v", err)
	}
}

func TestAutomationNewRulePublishesPausedAndDraftCanBeDiscarded(t *testing.T) {
	store := &automationDefinitionMemoryStore{items: map[string]automationmodel.AutomationRuleDefinition{}}
	service := NewAutomationManagementApplicationService(AutomationManagementDependencies{
		Rules: managementRuleRegistry{}, Definitions: store,
		ValidateDefinition: func(context.Context, automationmodel.AutomationRuleSchema) error { return nil },
	})
	principal := automationDefinitionPrincipal("workspace-a")
	rule := automationmodel.AutomationRuleSchema{Key: "account.notify", Name: "Notify", ObjectKey: "account", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "create"}, Instructions: []automationmodel.AutomationInstructionSchema{{Key: "event", Type: "emit_event"}}}
	draft, err := service.SaveDraft(t.Context(), rule.Key, automationmodel.AutomationSaveDraftRequest{Rule: rule}, principal)
	if err != nil {
		t.Fatal(err)
	}
	discarded, err := service.DiscardDraft(t.Context(), rule.Key, automationmodel.AutomationDiscardDraftRequest{ExpectedRevision: draft.Revision}, principal)
	if err != nil || discarded.Draft != nil {
		t.Fatalf("discarded=%#v err=%v", discarded, err)
	}
	draft, err = service.SaveDraft(t.Context(), rule.Key, automationmodel.AutomationSaveDraftRequest{Rule: rule, ExpectedRevision: discarded.Revision}, principal)
	if err != nil {
		t.Fatal(err)
	}
	published, err := service.PublishDraft(t.Context(), rule.Key, automationmodel.AutomationPublishDraftRequest{ExpectedRevision: draft.Revision}, principal)
	if err != nil || published.Enabled == nil || *published.Enabled {
		t.Fatalf("new rule must publish paused: %#v err=%v", published, err)
	}
	effective, found, err := service.EffectiveRule(t.Context(), principal.WorkspaceID, rule.Key)
	if err != nil || !found || effective.Enabled {
		t.Fatalf("new published rule=%#v found=%v err=%v", effective, found, err)
	}
}

func TestAutomationPauseStopsNewTriggersWithoutInvalidatingQueuedRuleSnapshot(t *testing.T) {
	store := &automationDefinitionMemoryStore{items: map[string]automationmodel.AutomationRuleDefinition{}}
	rule := automationmodel.AutomationRuleSchema{Key: "account.changed", Name: "Account changed", ObjectKey: "account", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"}, Instructions: []automationmodel.AutomationInstructionSchema{}}
	registry := managementRuleRegistry{rules: []automationmodel.AutomationRuleSchema{rule}}
	service := NewAutomationApplicationService(AutomationApplicationDependencies{
		Rules: registry, Definitions: store,
		Principal: func(_ context.Context, userID, roleKey, _ string) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: userID, RoleKey: roleKey}}, accessfixture.Bundle{Key: roleKey})
		},
	})
	principal := automationDefinitionPrincipal("workspace-a")
	principal.RoleKey = "automation-manager"
	messages, err := service.AfterOutbox(t.Context(), "account", "update", map[string]any{"name": "Old"}, recordmodel.Record{ID: "account-1", Data: map[string]any{"name": "New"}, UpdatedAt: "v2"}, principal)
	if err != nil || len(messages) != 1 {
		t.Fatalf("queued messages=%#v err=%v", messages, err)
	}
	paused, err := service.SetAutomationRuleEnabled(t.Context(), rule.Key, automationmodel.AutomationSetEnabledRequest{Enabled: false, ExpectedRevision: 0}, principal)
	if err != nil || paused.Revision != 1 {
		t.Fatalf("paused=%#v err=%v", paused, err)
	}
	if err := service.ExecuteOutboxMessage(t.Context(), messages[0]); err != nil {
		t.Fatalf("queued snapshot was invalidated by pause: %v", err)
	}
	messages, err = service.AfterOutbox(t.Context(), "account", "update", map[string]any{"name": "New"}, recordmodel.Record{ID: "account-1", Data: map[string]any{"name": "Newest"}, UpdatedAt: "v3"}, principal)
	if err != nil || len(messages) != 0 {
		t.Fatalf("pause did not stop new triggers: messages=%#v err=%v", messages, err)
	}
}
