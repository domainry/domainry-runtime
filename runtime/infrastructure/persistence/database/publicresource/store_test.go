package publicresource

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-orm/recordschema"
	"github.com/domainry/domainry-orm/schema"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestFindRequiresExactPublishedCapabilityAndFailsClosedOnCollision(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "public-resource.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	statement, arguments, err := recordschema.NewTable(runtimeStore.RuntimeRenderer(), "card").Columns(
		schema.Column("share_key", schema.TextKey(255)).NotNull(),
		schema.Column("publication_status", schema.TextKey(32)).NotNull(),
		schema.Column("full_name", schema.Text()),
		schema.Column("version", schema.Integer()),
		schema.Column("portrait_file", schema.TextKey(255)),
	).PrimaryKey("workspace_id", "id").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeStore.DB().ExecContext(t.Context(), statement, arguments...); err != nil {
		t.Fatal(err)
	}
	insert := func(workspace, id, key, status string) {
		t.Helper()
		statement, arguments, err := query.NewInsertBuilder(runtimeStore.RuntimeRenderer(), "card").
			Columns("workspace_id", "id", "created_at", "updated_at", "share_key", "publication_status", "full_name", "version", "portrait_file").
			Values(workspace, id, "2026-09-18T00:00:00Z", "2026-09-18T00:00:00Z", key, status, "Yuki", int64(3), "source-1").Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtimeStore.DB().ExecContext(t.Context(), statement, arguments...); err != nil {
			t.Fatal(err)
		}
	}
	object := definitionmodel.ObjectSchema{Key: "card", Fields: []definitionmodel.FieldSchema{
		{Key: "share_key", Type: "text"}, {Key: "publication_status", Type: "select"}, {Key: "full_name", Type: "text"}, {Key: "version", Type: "integer"}, {Key: "portrait_file", Type: "relation"},
	}}
	resource := definitionmodel.ObjectPublicResource{Key: "digital_business_card", AccessKeyField: "share_key", StateField: "publication_status", ActiveState: "published", Fields: []string{"full_name", "version"}, Files: []definitionmodel.ObjectPublicResourceFile{{FieldKey: "portrait_file"}}}
	store := NewStore(runtimeStore)
	key := "abcdefghijklmnopqrstuvwx"
	insert("workspace-a", "card-a", key, "published")
	projection, found, err := store.Find(t.Context(), object, resource, key)
	if err != nil || !found || projection.WorkspaceID != "workspace-a" || projection.RecordID != "card-a" || projection.Data["version"] != int64(3) || projection.FileRecords["portrait_file"] != "source-1" {
		t.Fatalf("projection=%#v found=%t err=%v", projection, found, err)
	}
	for _, forbidden := range []string{"share_key", "publication_status", "portrait_file"} {
		if _, exists := projection.Data[forbidden]; exists {
			t.Fatalf("gate/internal field %s leaked in %#v", forbidden, projection.Data)
		}
	}
	insert("workspace-b", "card-disabled", "zyxwvutsrqponmlkjihgfedc", "disabled")
	if _, found, err := store.Find(t.Context(), object, resource, "zyxwvutsrqponmlkjihgfedc"); err != nil || found {
		t.Fatalf("disabled resource found=%t err=%v", found, err)
	}
	insert("workspace-b", "card-b", key, "published")
	if _, found, err := store.Find(t.Context(), object, resource, key); err == nil || found || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("collision found=%t err=%v", found, err)
	}
}
