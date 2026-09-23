package appschema

import (
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
)

func TestProjectModelInitializationIsExactHashOnly(t *testing.T) {
	store := openStoreForMetadataTest(t)
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
	if err != nil || len(metadataDefinitions) != 2 {
		t.Fatalf("metadata definitions=%#v err=%v", metadataDefinitions, err)
	}
	identityDefinitions, err := store.metadata.Definitions().List(t.Context(), metadatasdk.DefinitionQuery{Owner: metadatasdk.DefinitionOwnerIdentity})
	if err != nil || len(identityDefinitions) != 1 || identityDefinitions[0].ResourceType != "identity_profile_binding" {
		t.Fatalf("identity definitions=%#v err=%v", identityDefinitions, err)
	}
	changed := model
	changed.ContentHash = strings.Repeat("b", 64)
	if err := store.InitializeProjectModel(t.Context(), metadataTestInstallationScope(), changed); apperror.CodeOf(err) != projectmodel.ChangedRequiresEmptyDatabaseCode {
		t.Fatalf("changed model error=%v code=%q", err, apperror.CodeOf(err))
	}
}
