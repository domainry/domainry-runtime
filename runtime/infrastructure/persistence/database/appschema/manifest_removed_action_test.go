package appschema

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestSyncManifestDisablesRemovedGeneratedActionsOnly(t *testing.T) {
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
	for key, wantDisabled := range map[string]bool{"candidate.fake_agent": true, "candidate.user_action": false} {
		var disabledAt any
		if err := store.raw.DB().QueryRowContext(t.Context(), "SELECT disabled_at FROM action_definitions WHERE resource_key = ?", key).Scan(&disabledAt); err != nil {
			t.Fatal(err)
		}
		if gotDisabled := disabledAt != nil; gotDisabled != wantDisabled {
			t.Fatalf("action %s disabled=%t, want %t", key, gotDisabled, wantDisabled)
		}
	}
}

func TestSyncManifestDisablesRemovedGeneratedAutomationRules(t *testing.T) {
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
	for key, wantDisabled := range map[string]bool{"candidate.archive": true, "candidate.notify": false, "candidate.user_rule": false} {
		var disabledAt any
		if err := store.raw.DB().QueryRowContext(t.Context(), "SELECT disabled_at FROM automation_rule_definitions WHERE resource_key = ?", key).Scan(&disabledAt); err != nil {
			t.Fatal(err)
		}
		if gotDisabled := disabledAt != nil; gotDisabled != wantDisabled {
			t.Fatalf("automation rule %s disabled=%t, want %t", key, gotDisabled, wantDisabled)
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

func TestDisableRemovedGeneratedActionsFailureAndSelectionBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	manifest := manifestmodel.ManifestSchema{Actions: []definitionmodel.ActionSchema{{Key: " "}, {Key: "keep"}}}
	for name, state := range map[string]metadataSQLState{
		"query":  {querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}}},
		"scan":   {querySteps: []metadataSQLQueryStep{{columns: []string{"wrong", "extra"}, rows: [][]driver.Value{{"remove", "extra"}}}}},
		"rows":   {querySteps: []metadataSQLQueryStep{{columns: []string{"resource_key"}, nextErr: errMetadataSQL}}},
		"close":  {querySteps: []metadataSQLQueryStep{{columns: []string{"resource_key"}, closeErr: errMetadataSQL}}},
		"update": {querySteps: []metadataSQLQueryStep{{columns: []string{"resource_key"}, rows: [][]driver.Value{{"keep"}, {"remove"}}}}, execSteps: []metadataSQLExecStep{{err: errMetadataSQL}}},
	} {
		t.Run(name, func(t *testing.T) {
			err := runMetadataTransaction(t, base, state, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
				return repository.disableRemovedGeneratedActions(t.Context(), tx, manifest, "now")
			})
			if err == nil {
				t.Fatal("expected generated action synchronization failure")
			}
		})
	}
	if err := runMetadataTransaction(t, base, metadataSQLState{
		querySteps: []metadataSQLQueryStep{{columns: []string{"resource_key"}, rows: [][]driver.Value{{"keep"}, {"remove"}}}},
		execSteps:  []metadataSQLExecStep{{rows: 1}},
	}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
		return repository.disableRemovedGeneratedActions(t.Context(), tx, manifest, "now")
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSyncManifestPropagatesRemovedActionAndCloseFailures(t *testing.T) {
	originalDisable := disableRemovedGeneratedActionsForManifest
	disableRemovedGeneratedActionsForManifest = func(context.Context, ApplicationSchemaStore, *sql.Tx, manifestmodel.ManifestSchema, string) error {
		return errMetadataSQL
	}
	t.Cleanup(func() { disableRemovedGeneratedActionsForManifest = originalDisable })
	store := openStoreForMetadataTest(t)
	if err := store.SyncManifestMetadata(t.Context(), manifestmodel.ManifestSchema{TemplateID: "failure", Version: "1", Objects: []definitionmodel.ObjectSchema{{Key: "account", Name: "Account"}}}); !errors.Is(err, errMetadataSQL) {
		t.Fatalf("removed action error=%v", err)
	}

	originalClose := closeGeneratedActionRows
	closeGeneratedActionRows = func(*sql.Rows) error { return errMetadataSQL }
	t.Cleanup(func() { closeGeneratedActionRows = originalClose })
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	err := runMetadataTransaction(t, base, metadataSQLState{querySteps: []metadataSQLQueryStep{{columns: []string{"resource_key"}}}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
		return repository.disableRemovedGeneratedActions(t.Context(), tx, manifestmodel.ManifestSchema{}, "now")
	})
	if !errors.Is(err, errMetadataSQL) {
		t.Fatalf("close error=%v", err)
	}
}
