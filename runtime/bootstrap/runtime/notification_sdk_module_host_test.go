package runtime

import (
	"strings"
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormschema "github.com/domainry/domainry-orm/schema"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func TestNotificationSDKDialectPreservesNamedRendererForDefinitionSchema(t *testing.T) {
	dialect, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	store := &persistence.RuntimeStore{SQLDatabase: &base.SQLDatabase{SQLRenderer: dialect.WithSchema("")}}
	renderer := notificationSDKDialect{store: store}

	if renderer.Name() != ormdialect.SQLite {
		t.Fatalf("dialect name = %q, want %q", renderer.Name(), ormdialect.SQLite)
	}
	statement, _, err := ormschema.NewTable(renderer, "_notification_definitions").
		Columns(ormschema.Column("id", ormschema.TextKey(96)).NotNull()).
		PrimaryKey("id").
		Build()
	if err != nil {
		t.Fatalf("build Notification definition table: %v", err)
	}
	if !strings.Contains(statement, `CREATE TABLE "_notification_definitions"`) {
		t.Fatalf("unexpected definition table statement: %s", statement)
	}
}
