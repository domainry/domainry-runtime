package appschema

import (
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/metadatamodulefixture"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
)

func TestProjectModelInitializationAppliesIncrementalSourceAndPreservesRecords(t *testing.T) {
	raw, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "project.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	metadatamodulefixture.EnsureBinding(t.Context(), raw)
	if err := raw.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewApplicationSchemaStore(raw)
	model := projectmodel.RuntimeModel{
		SchemaVersion: "1", ProjectKey: "crm", ProjectName: "CRM", DefaultLocale: "zh-CN",
		TimeZone: "Asia/Shanghai", ContentHash: strings.Repeat("a", 64),
		Objects:          []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text", Required: true}}}},
		IdentityProfiles: []profilebindingmodel.Binding{{ObjectKey: "customer", BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "customer"}}},
	}
	if err := store.InitializeProjectModel(t.Context(), metadataTestInstallationScope(), model); err != nil {
		t.Fatal(err)
	}
	if matched, err := store.ProjectModelMatches(t.Context(), metadataTestInstallationScope(), model); err != nil || !matched {
		t.Fatalf("matched=%t err=%v", matched, err)
	}
	if err := store.InitializeProjectModel(t.Context(), metadataTestInstallationScope(), model); err != nil {
		t.Fatalf("same model restart failed: %v", err)
	}
	var states int
	if err := store.database().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.store.TableIdentifier("_project_model_state")+" WHERE "+store.store.Identifier("id")+" = "+store.store.Placeholder(1), "current").Scan(&states); err != nil {
		t.Fatal(err)
	}
	if states != 1 {
		t.Fatalf("project model current-state rows=%d want=1", states)
	}
	metadataDefinitions, err := store.metadata.Definitions().List(t.Context(), metadatasdk.DefinitionQuery{Owner: metadatasdk.DefinitionOwnerMetadata})
	if err != nil || len(metadataDefinitions) != 3 {
		t.Fatalf("metadata definitions=%#v err=%v", metadataDefinitions, err)
	}
	identityDefinitions, err := store.metadata.Definitions().List(t.Context(), metadatasdk.DefinitionQuery{Owner: metadatasdk.DefinitionOwnerIdentity})
	if err != nil || len(identityDefinitions) != 1 || identityDefinitions[0].ResourceType != "identity_profile_binding" {
		t.Fatalf("identity definitions=%#v err=%v", identityDefinitions, err)
	}
	changed := model
	changed.ContentHash = strings.Repeat("b", 64)
	changed.ProjectName = "CRM Inventory"
	statement, args, err := query.NewInsertBuilder(store.store.SQLRenderer, "customer").
		Columns("id", "workspace_id", "name").Values("retained-customer", principalmodel.InstallationWorkspaceID, "Retained customer").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.database().ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
	changed.Objects = append(append([]definitionmodel.ObjectSchema{}, model.Objects...), definitionmodel.ObjectSchema{
		Key: "spare_part", Name: "Spare part", Fields: []definitionmodel.FieldSchema{{Key: "code", Name: "Code", Type: "text", Required: true}},
	})
	changed.Objects[0].Fields = append([]definitionmodel.FieldSchema{}, model.Objects[0].Fields...)
	changed.Objects[0].Fields[0].Name = "Customer name"
	changed.Objects[0].Fields = append(changed.Objects[0].Fields, definitionmodel.FieldSchema{Key: "reference", Name: "Reference", Type: "text"})
	if err := store.InitializeProjectModel(t.Context(), metadataTestInstallationScope(), changed); err != nil {
		t.Fatalf("incremental source failed: %v", err)
	}
	if matched, err := store.ProjectModelMatches(t.Context(), metadataTestInstallationScope(), changed); err != nil || !matched {
		t.Fatalf("incremental restart matched=%t err=%v", matched, err)
	}
	statement, args, err = query.NewSelectBuilder(store.store.SQLRenderer, "customer").Columns("name").Where(query.Equal("id", "retained-customer")).Build()
	if err != nil {
		t.Fatal(err)
	}
	var retainedName string
	if err := store.database().QueryRowContext(t.Context(), statement, args...).Scan(&retainedName); err != nil || retainedName != "Retained customer" {
		t.Fatalf("retained name=%q err=%v", retainedName, err)
	}
	columns, err := store.tableColumns(t.Context(), "spare_part")
	if err != nil || !columns["code"] {
		t.Fatalf("incremental storage columns=%v err=%v", columns, err)
	}
	metadataDefinitions, err = store.metadata.Definitions().List(t.Context(), metadatasdk.DefinitionQuery{Owner: metadatasdk.DefinitionOwnerMetadata})
	if err != nil || len(metadataDefinitions) != 6 {
		t.Fatalf("incremental definitions=%#v err=%v", metadataDefinitions, err)
	}
	columns, err = store.tableColumns(t.Context(), "customer")
	if err != nil || !columns["reference"] {
		t.Fatalf("updated existing storage columns=%v err=%v", columns, err)
	}
	field, found, err := store.metadata.Definitions().Get(t.Context(), metadatasdk.DefinitionOwnerMetadata, "field", metadataJoinedKey("customer", "name"))
	if err != nil || !found || field.Name != "Customer name" {
		t.Fatalf("updated existing field=%#v found=%t err=%v", field, found, err)
	}
	otherProject := changed
	otherProject.ProjectKey = "other-project"
	if err := store.InitializeProjectModel(t.Context(), metadataTestInstallationScope(), otherProject); apperror.CodeOf(err) != projectmodel.ProjectIdentityMismatchCode {
		t.Fatalf("different project error=%v", err)
	}
}
