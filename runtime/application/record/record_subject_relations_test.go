package record_test

import (
	"encoding/json"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubjectErasureFollowsOnlyDeclaredOwnershipAndRequiresFieldPolicies(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "relations.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	retain := map[string]any{"lifecycle_erase": "retain"}
	root := definitionmodel.ObjectSchema{Key: "member", Fields: []definitionmodel.FieldSchema{{Key: "user", Type: "user", Config: retain}}}
	child := definitionmodel.ObjectSchema{Key: "medical_note", Fields: []definitionmodel.FieldSchema{
		{Key: "member", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "member"}, Config: map[string]any{"lifecycle_subject_relation": true, "lifecycle_erase": "retain"}},
		{Key: "note", Type: "text", Config: map[string]any{"lifecycle_erase": "delete"}},
	}}
	shared := definitionmodel.ObjectSchema{Key: "shared_note", Fields: []definitionmodel.FieldSchema{
		{Key: "member", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "member"}, Config: retain},
		{Key: "note", Type: "text", Config: map[string]any{"lifecycle_erase": "delete"}},
	}}
	objects := []definitionmodel.ObjectSchema{root, child, shared}
	if err = store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = appschema.NewApplicationSchemaStore(store).SyncManifest(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "subject relations test"), manifestmodel.ManifestSchema{TemplateID: "subject-relations", Version: "1", Name: "Subject Relations", Objects: objects}); err != nil {
		t.Fatal(err)
	}
	repository := recordpersistence.NewRecordStore(store)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	insert := func(object definitionmodel.ObjectSchema, id string, data map[string]any) {
		t.Helper()
		if err := repository.InsertRecord(t.Context(), "workspace-one", object, recordmodel.Record{ID: id, CreatedAt: now, UpdatedAt: now, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	insert(root, "member-one", map[string]any{"user": "user-one"})
	insert(root, "member-two", map[string]any{"user": "user-two"})
	insert(child, "note-one", map[string]any{"member": "member-one", "note": "PRIVATE"})
	insert(child, "note-two", map[string]any{"member": "member-two", "note": "OTHER"})
	insert(shared, "shared-one", map[string]any{"member": "member-one", "note": "SHARED"})
	service := recordapplication.NewRecordSubjectLifecycleApplicationService(repository, objects, nil)
	exported, err := service.ExportSubject(t.Context(), "workspace-one", "user-one")
	if err != nil || !strings.Contains(string(exported), "PRIVATE") || strings.Contains(string(exported), "OTHER") || strings.Contains(string(exported), "SHARED") {
		t.Fatalf("wrong subject closure: %s %v", exported, err)
	}
	// Missing policies must block the whole request before mutating records.
	child.Fields[1].Config = nil
	if _, err = service.PrepareSubjectErasure(t.Context(), "erase-one", "workspace-one", "user-one"); err == nil {
		t.Fatal("undeclared field retained silently")
	}
	child.Fields[1].Config = map[string]any{"lifecycle_erase": "delete"}
	plan, err := service.PrepareSubjectErasure(t.Context(), "erase-one", "workspace-one", "user-one")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(plan) || strings.Contains(string(plan), "PRIVATE") {
		t.Fatal("invalid or personal plan")
	}
	if _, err = service.ErasePreparedSubject(t.Context(), "erase-one", "workspace-one", "user-one", plan, nil); err != nil {
		t.Fatal(err)
	}
	for _, expect := range []struct {
		object definitionmodel.ObjectSchema
		id     string
		value  any
	}{{child, "note-one", nil}, {child, "note-two", "OTHER"}, {shared, "shared-one", "SHARED"}} {
		row, found, err := repository.GetRecord(t.Context(), "workspace-one", expect.object, expect.id)
		if err != nil || !found || row.Data["note"] != expect.value {
			t.Fatalf("subject boundary: %s %+v %v", expect.id, row, err)
		}
	}
}
