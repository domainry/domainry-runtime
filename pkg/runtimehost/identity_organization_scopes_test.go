package runtimehost

import (
	"context"
	partysdkcontract "github.com/domainry/domainry-party-sdk/contract"
	"path/filepath"
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type scopeResolverStub struct{ t *testing.T }

func (s scopeResolverStub) Resolve(_ context.Context, ids []string) (partysdkcontract.OrganizationScopeFacts, error) {
	if !reflect.DeepEqual(ids, []string{"workforce-1"}) {
		s.t.Fatalf("profiles=%v", ids)
	}
	return partysdkcontract.OrganizationScopeFacts{StoreIDs: []string{"store-1"}}, nil
}

func TestRuntimeOrganizationScopeResolverBridgesPartyFacts(t *testing.T) {
	projection := &partyOrganizationScopeProjection{}
	projection.Bind("workspace", scopeResolverStub{t})
	facts, err := projection.Resolve(t.Context(), "workspace", []string{"workforce-1"})
	if err != nil || !reflect.DeepEqual(facts.StoreIDs, []string{"store-1"}) {
		t.Fatalf("facts=%#v err=%v", facts, err)
	}
}

func TestRuntimeBusinessProfileProjectionUsesActiveManifestWithoutRuntimeOwnedBindingTable(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "gym.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureApplicationSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	member := definitionmodel.ObjectSchema{Key: "member", UX: map[string]any{"kind": "identity_profile_extension"}, Fields: []definitionmodel.FieldSchema{
		{Key: "identity_user_id", Type: "relation"}, {Key: "risk", Type: "text"}, {Key: "store_id", Type: "relation"},
	}}
	extension := profilebindingmodel.Binding{
		ObjectKey: "member", IdentityRelationField: "identity_user_id", BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{
			Key: "member", StatusField: "risk", ActiveStatusValues: []string{"stable", "attention", "renewal"},
		},
	}
	metadata := appschemapersistence.NewApplicationSchemaStore(store)
	if err := metadata.SyncManifest(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test Gym profile storage"), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{member}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO member (workspace_id,id,created_at,updated_at,identity_user_id,risk,store_id) VALUES ('default','member_1787940383392750000','now','now','wechat-user','stable','store_seed')`); err != nil {
		t.Fatal(err)
	}
	projection := newRuntimeBusinessProfileProjection(store)
	projection.Publish([]definitionmodel.ObjectSchema{member}, []profilebindingmodel.Binding{extension})
	profiles, err := projection.Resolve(t.Context(), "default", "wechat-user")
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].BindingKey != "member" || profiles[0].ProfileID != "member_1787940383392750000" {
		t.Fatalf("profiles=%+v", profiles)
	}
}
