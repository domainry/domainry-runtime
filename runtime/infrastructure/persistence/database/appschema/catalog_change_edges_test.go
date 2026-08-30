package appschema

import (
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormmysql "github.com/domainry/domainry-orm/mysql"
	ormpostgres "github.com/domainry/domainry-orm/postgres"
	ormsqlite "github.com/domainry/domainry-orm/sqlite"
)

func TestMetadataCatalogSQLBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	for _, testCase := range []struct {
		step metadataSQLQueryStep
		want bool
		err  bool
	}{
		{step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}},
		{step: metadataSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}, want: true},
	} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, base)
		got, err := repository.manifestMetadataSeeded(t.Context())
		if got != testCase.want || (err != nil) != testCase.err {
			t.Fatalf("seeded=%v err=%v", got, err)
		}
	}
	for _, testCase := range []struct {
		step metadataSQLQueryStep
		want string
		err  bool
	}{
		{step: metadataSQLQueryStep{columns: []string{"value"}}},
		{step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"value"}, rows: [][]driver.Value{{" 7 "}}}, want: "7"},
	} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, base)
		got, err := repository.ManifestIdentitySeedSyncedVersion(t.Context())
		if got != testCase.want || (err != nil) != testCase.err {
			t.Fatalf("version=%q err=%v", got, err)
		}
	}
	for _, step := range []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{execSteps: []metadataSQLExecStep{step}}, base)
		err := repository.SetManifestIdentitySeedSyncedVersion(t.Context(), " 7 ")
		if (err != nil) != (step.err != nil) {
			t.Fatalf("set version err=%v", err)
		}
	}
	for _, testCase := range []struct {
		step metadataSQLQueryStep
		want string
		err  bool
	}{
		{step: metadataSQLQueryStep{columns: []string{"value"}}},
		{step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"value"}, rows: [][]driver.Value{{" ready "}}}, want: "ready"},
	} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, base)
		got, err := repository.ManifestOrganizationScopeSeedState(t.Context())
		if got != testCase.want || (err != nil) != testCase.err {
			t.Fatalf("organization seed state=%q err=%v", got, err)
		}
	}
	for _, step := range []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{execSteps: []metadataSQLExecStep{step}}, base)
		err := repository.SetManifestOrganizationScopeSeedState(t.Context(), " ready ")
		if (err != nil) != (step.err != nil) {
			t.Fatalf("set organization seed state err=%v", err)
		}
	}
	for _, testCase := range []struct {
		name    ormdialect.Name
		profile ormdriver.Profile
		want    string
	}{
		{ormdialect.MySQL, ormmysql.NewProfile(), "ON DUPLICATE KEY"},
		{ormdialect.Postgres, ormpostgres.NewProfile(), "ON CONFLICT"},
		{ormdialect.SQLite, ormsqlite.NewProfile(), "ON CONFLICT"},
	} {
		renderer, _ := ormdialect.New(testCase.name)
		base.store.SQLRenderer = renderer.WithSchema("")
		base.store.Engine = testCase.profile
		query, _, err := buildMetadataCatalogUpsert(base, "key", "value", "now")
		if err != nil || !strings.Contains(query, testCase.want) {
			t.Fatalf("%s SQL=%s err=%v", testCase.name, query, err)
		}
	}
	emptyQueries := metadataCatalogHashQuerySteps(metadataSQLQueryStep{columns: []string{"resource_key", "schema_hash"}})
	if err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: emptyQueries, execSteps: []metadataSQLExecStep{{rows: 1}}}, base).refreshMetadataCatalogHash(t.Context()); err != nil {
		t.Fatal(err)
	}
	emptyQueries = metadataCatalogHashQuerySteps(metadataSQLQueryStep{columns: []string{"resource_key", "schema_hash"}})
	if err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: emptyQueries, execSteps: []metadataSQLExecStep{{err: errMetadataSQL}}}, base).refreshMetadataCatalogHash(t.Context()); err == nil {
		t.Fatal("expected catalog hash error")
	}
}

func TestMetadataCatalogTransactionBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	run := func(t *testing.T, state metadataSQLState, call func(ApplicationSchemaStore, *sql.Tx) error) error {
		t.Helper()
		repository := scriptedApplicationSchemaStore(t, &state, base)
		tx, err := repository.database().BeginTx(t.Context(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return call(repository, tx)
	}
	for _, step := range []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}} {
		err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{step}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			return repository.insertMetadataCatalog(t.Context(), tx, " key ", " value ", "now")
		})
		if (err != nil) != (step.err != nil) {
			t.Fatalf("insert err=%v", err)
		}
	}
	for _, testCase := range []struct {
		steps []metadataSQLExecStep
		err   bool
	}{
		{steps: []metadataSQLExecStep{{err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}}},
		{steps: []metadataSQLExecStep{{rowsErr: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 0}, {rows: 1}}},
		{steps: []metadataSQLExecStep{{rows: 0}, {err: errMetadataSQL}}, err: true},
	} {
		err := run(t, metadataSQLState{execSteps: append([]metadataSQLExecStep(nil), testCase.steps...)}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			return repository.upsertMetadataCatalog(t.Context(), tx, "key", "value", "now")
		})
		if (err != nil) != testCase.err {
			t.Fatalf("upsert err=%v", err)
		}
	}
	for _, testCase := range []struct {
		step metadataSQLQueryStep
		want string
		err  bool
	}{
		{step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(4)}}}, want: "5"},
	} {
		var got string
		err := run(t, metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			var err error
			got, err = repository.nextApplicationSchemaVersionTx(t.Context(), tx, "object", "account")
			return err
		})
		if got != testCase.want || (err != nil) != testCase.err {
			t.Fatalf("next version=%q err=%v", got, err)
		}
	}
	for _, step := range []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}} {
		err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{step}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			return repository.insertApplicationDefinitionVersionTx(t.Context(), tx, "object", "account", "1", strings.Repeat("a", 64), []byte(`{}`), "now")
		})
		if (err != nil) != (step.err != nil) {
			t.Fatalf("insert version err=%v", err)
		}
	}
	for _, testCase := range []struct {
		event auditmodel.AuditEvent
		step  metadataSQLExecStep
		err   bool
	}{
		{event: auditmodel.AuditEvent{Before: map[string]any{"bad": make(chan int)}}, err: true},
		{event: auditmodel.AuditEvent{After: map[string]any{"bad": make(chan int)}}, err: true},
		{event: auditmodel.AuditEvent{Metadata: map[string]any{"bad": make(chan int)}}, err: true},
		{event: auditmodel.AuditEvent{}, step: metadataSQLExecStep{err: errMetadataSQL}, err: true},
		{event: auditmodel.AuditEvent{}, step: metadataSQLExecStep{rows: 1}},
	} {
		err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{testCase.step}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			event := testCase.event
			event.WorkspaceID = "workspace"
			return repository.insertMetadataChangeAudit(t.Context(), tx, event)
		})
		if (err != nil) != testCase.err {
			t.Fatalf("audit err=%v", err)
		}
	}
	for _, testCase := range []struct {
		step metadataSQLQueryStep
		want string
	}{
		{step: metadataSQLQueryStep{err: errMetadataSQL}},
		{step: metadataSQLQueryStep{columns: []string{"hash"}, rows: [][]driver.Value{{"hash"}}}, want: "hash"},
	} {
		var got string
		if err := run(t, metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			got = repository.currentMetadataHashTx(t.Context(), tx, "object_definitions", "account")
			return nil
		}); err != nil || got != testCase.want {
			t.Fatalf("current hash=%q err=%v", got, err)
		}
	}
	if metadataMutationValueOrDefault(" value ", "fallback") != "value" || metadataMutationValueOrDefault(" ", "fallback") != "fallback" {
		t.Fatal("mutation default failed")
	}
}
