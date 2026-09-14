package schema

import (
	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
	"strings"
	"testing"
)

func TestUploadSubjectSchemaUsesHostOwnershipAcrossSupportedDialects(t *testing.T) {
	for _, test := range []struct {
		name     string
		profile  persistencedriver.EngineProfile
		renderer ormdialect.Renderer
	}{
		{"sqlite", sqlite.NewEngine(), sqlite.NewEngine().SQLDialect().WithSchema("")},
		{"postgres", postgres.NewEngine(), postgres.NewEngine().SQLDialect().WithSchema("")},
		{"mysql", mysql.NewEngine(), mysql.NewEngine().SQLDialect().WithSchema("")},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := &reportExportSchemaCaptureDB{}
			store := &reportExportSchemaCaptureStore{db: db, driver: test.name, profile: test.profile, renderer: test.renderer}
			if err := EnsureUploadSubjectSchema(t.Context(), store); err != nil {
				t.Fatal(err)
			}
			if len(db.statements) != 1 || !strings.Contains(db.statements[0], "user_id") || !strings.Contains(strings.ToLower(db.statements[0]), "primary key") {
				t.Fatalf("DDL=%q", db.statements)
			}
			if len(store.indexes) != 1 || store.indexes[0] != "uniq_upload_subject_filename:workspace_id,filename" {
				t.Fatalf("indexes=%q", store.indexes)
			}
		})
	}
}
