package appschema

import (
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestSyncManifestDeletesRemovedGeneratedActionsOnly(t *testing.T) {
	store := openStoreForMetadataTest(t)
	manifest := manifestmodel.ManifestSchema{
		TemplateID: "agent-runtime", Version: "1.0.0", Name: "Agent Runtime",
		Objects: []definitionmodel.ObjectSchema{{Key: "candidate", Name: "Candidate"}},
		Actions: []definitionmodel.ActionSchema{{Key: "candidate.fake_agent", ObjectKey: "candidate", Label: "Fake Agent"}},
	}
	if err := store.EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.raw.DB().ExecContext(t.Context(), "INSERT INTO action_definitions (id, resource_key, object_key, name, payload_json, schema_version, schema_hash, source_kind, source_id, disabled_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)", "action:user_action", "candidate.user_action", "candidate", "User Action", `{}`, "1", "user-hash", "user", "user", "now", "now"); err != nil {
		t.Fatal(err)
	}
	manifest.Actions = nil
	if err := store.SyncManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	for key, wantCount := range map[string]int{"candidate.fake_agent": 0, "candidate.user_action": 1} {
		var count int
		if err := store.raw.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM action_definitions WHERE resource_key = ?", key).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != wantCount {
			t.Fatalf("action %s rows=%d, want %d", key, count, wantCount)
		}
	}
}

func TestSyncManifestDeletesRemovedGeneratedAutomationRules(t *testing.T) {
	store := openStoreForMetadataTest(t)
	manifest := manifestmodel.ManifestSchema{
		TemplateID: "agent-runtime", Version: "1.0.0", Name: "Agent Runtime",
		Objects: []definitionmodel.ObjectSchema{{Key: "candidate", Name: "Candidate"}},
		AutomationRules: []automationmodel.AutomationRuleSchema{
			{Key: "candidate.notify", Name: "Notify", ObjectKey: "candidate", Enabled: true},
			{Key: "candidate.archive", Name: "Archive", ObjectKey: "candidate", Enabled: true},
		},
	}
	if err := store.EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.raw.DB().ExecContext(t.Context(), "INSERT INTO automation_rule_definitions (id, resource_key, object_key, name, payload_json, schema_version, schema_hash, source_kind, source_id, disabled_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)", "automation_rule:user_rule", "candidate.user_rule", "candidate", "User Rule", `{"key":"candidate.user_rule"}`, "1", "user-hash", "user", "user", "now", "now"); err != nil {
		t.Fatal(err)
	}
	// Evolution removes candidate.archive from the model, then the repackaged
	// manifest is synced onto the existing Runtime database.
	manifest.AutomationRules = manifest.AutomationRules[:1]
	if err := store.SyncManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	for key, wantCount := range map[string]int{"candidate.archive": 0, "candidate.notify": 1, "candidate.user_rule": 1} {
		var count int
		if err := store.raw.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM automation_rule_definitions WHERE resource_key = ?", key).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != wantCount {
			t.Fatalf("automation rule %s rows=%d, want %d", key, count, wantCount)
		}
	}
	loaded, err := loadMetadataSlice[automationmodel.AutomationRuleSchema](t.Context(), store.ApplicationSchemaStore, "automation_rule_definitions")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range loaded {
		if rule.Key == "candidate.archive" {
			t.Fatalf("removed automation rule still loads on the existing Runtime database: %+v", loaded)
		}
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded automation rules=%+v", loaded)
	}
}
