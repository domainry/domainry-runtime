package automation

import (
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	metadatamodulefixture "github.com/domainry/domainry-runtime/testsupport/metadatamodulefixture"
)

func TestAutomationRuleDefinitionStorePersistsWorkspaceStateWithRevisionCAS(t *testing.T) {
	runtimeStore := openRuntimeStore(t)
	t.Cleanup(func() { _ = runtimeStore.Close() })
	metadatamodulefixture.EnsureBinding(t.Context(), runtimeStore)
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewAutomationRuleDefinitionStore(runtimeStore)
	enabled := true
	state := automationmodel.AutomationRuleDefinition{
		WorkspaceID: "workspace-a", RuleKey: "account.created", Enabled: &enabled,
		Draft:     &automationmodel.AutomationRuleDraft{Revision: 1, Rule: automationmodel.AutomationRuleSchema{Key: "account.created", Name: "Account created"}},
		CreatedBy: "admin-a", UpdatedBy: "admin-a", CreatedAt: "2026-09-29T00:00:00Z", UpdatedAt: "2026-09-29T00:00:00Z",
	}
	created, changed, err := store.Put(t.Context(), state, 0)
	if err != nil || !changed || created.Revision != 1 {
		t.Fatalf("created=%#v changed=%v err=%v", created, changed, err)
	}
	loaded, found, err := store.Get(t.Context(), "workspace-a", state.RuleKey)
	if err != nil || !found || loaded.Draft == nil || loaded.Draft.Rule.Name != "Account created" {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
	if _, found, err := store.Get(t.Context(), "workspace-b", state.RuleKey); err != nil || found {
		t.Fatalf("foreign workspace found=%v err=%v", found, err)
	}
	state = loaded
	state.Draft.Rule.Name = "Updated"
	updated, changed, err := store.Put(t.Context(), state, 1)
	if err != nil || !changed || updated.Revision != 2 {
		t.Fatalf("updated=%#v changed=%v err=%v", updated, changed, err)
	}
	stale, changed, err := store.Put(t.Context(), state, 1)
	if err != nil || changed || stale.Revision != 2 {
		t.Fatalf("stale=%#v changed=%v err=%v", stale, changed, err)
	}
	items, err := store.List(t.Context(), "workspace-a")
	if err != nil || len(items) != 1 || items[0].RuleKey != state.RuleKey {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}
