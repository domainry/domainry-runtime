package appschema

import (
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestDefinitionRefreshIntentAndCompletionBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	definition := appschemamodel.ApplicationDefinition{ResourceType: "object", ResourceKey: "account", SchemaVersion: "1", SchemaHash: strings.Repeat("a", 64)}
	for _, step := range []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}} {
		err := runMetadataTransaction(t, base, metadataSQLState{execSteps: []metadataSQLExecStep{step}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			return repository.insertDefinitionRefreshIntentTx(t.Context(), tx, definition)
		})
		if (err != nil) != (step.err != nil) {
			t.Fatalf("insert refresh intent err=%v", err)
		}
	}
	if metadataDefinitionRefreshIntentID("object", "account", "hash") == metadataDefinitionRefreshIntentID("object", "other", "hash") {
		t.Fatal("refresh intent ID collision")
	}
	if err := base.CompleteDefinitionRefresh(t.Context(), identitySystemScopeZero(), "object", "account", "hash", ""); err == nil {
		t.Fatal("expected completion scope error")
	}
}

func identitySystemScopeZero() principalmodel.SystemScope { return principalmodel.SystemScope{} }

func TestDefinitionDisableAndReplaceBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	if err := base.DisableDefinition(t.Context(), metadataInstallScope(), "missing", "account"); err == nil {
		t.Fatal("expected disable type error")
	}
	if err := scriptedApplicationSchemaStore(t, &metadataSQLState{beginErr: errMetadataSQL}, base).DisableDefinition(t.Context(), metadataInstallScope(), "object", "account"); err == nil {
		t.Fatal("expected disable begin error")
	}
	for _, step := range []metadataSQLExecStep{{err: errMetadataSQL}, {rowsErr: errMetadataSQL}, {rows: 0}} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{execSteps: []metadataSQLExecStep{step}}, base)
		err := repository.DisableDefinition(t.Context(), metadataInstallScope(), "object", "account")
		if err == nil {
			t.Fatalf("disable step=%#v err=%v", step, err)
		}
	}
	for _, testCase := range []struct {
		name    string
		queries []metadataSQLQueryStep
		execs   []metadataSQLExecStep
		commit  error
	}{
		{name: "catalog", queries: []metadataSQLQueryStep{{err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}}},
		{name: "catalog write", queries: metadataEmptyCatalogHashSteps(), execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}},
		{name: "commit", queries: metadataEmptyCatalogHashSteps(), execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}}, commit: errMetadataSQL},
		{name: "success", queries: metadataEmptyCatalogHashSteps(), execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}}},
	} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: testCase.queries, execSteps: testCase.execs, commitErr: testCase.commit}, base)
		err := repository.DisableDefinition(t.Context(), metadataInstallScope(), "object", "account")
		wantErr := testCase.name != "success"
		if (err != nil) != wantErr {
			t.Fatalf("disable %s err=%v", testCase.name, err)
		}
	}
	if err := base.DisableDefinition(t.Context(), identitySystemScopeZero(), "object", "account"); err == nil {
		t.Fatal("expected disable scope error")
	}
	runReplace := func(t *testing.T, state metadataSQLState, expected *string) error {
		t.Helper()
		return runMetadataTransaction(t, base, state, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			return repository.replaceDefinition(t.Context(), tx, "object_definitions", "object", "account", expected)
		})
	}
	for _, step := range []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}} {
		if err := runReplace(t, metadataSQLState{execSteps: []metadataSQLExecStep{step}}, nil); (err != nil) != (step.err != nil) {
			t.Fatalf("nil expected replace err=%v", err)
		}
	}
	empty := " "
	for _, testCase := range []struct {
		step metadataSQLQueryStep
		err  bool
	}{
		{step: metadataSQLQueryStep{columns: []string{"hash"}}},
		{step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"hash"}, rows: [][]driver.Value{{"current"}}}, err: true},
	} {
		if err := runReplace(t, metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, &empty); (err != nil) != testCase.err {
			t.Fatalf("empty expected replace err=%v", err)
		}
	}
	expected := "expected"
	for _, testCase := range []struct {
		exec  metadataSQLExecStep
		query *metadataSQLQueryStep
		err   bool
	}{
		{exec: metadataSQLExecStep{err: errMetadataSQL}, err: true},
		{exec: metadataSQLExecStep{rowsErr: errMetadataSQL}, err: true},
		{exec: metadataSQLExecStep{rows: 1}},
		{exec: metadataSQLExecStep{rows: 0}, query: &metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{exec: metadataSQLExecStep{rows: 0}, query: &metadataSQLQueryStep{columns: []string{"hash"}}, err: true},
		{exec: metadataSQLExecStep{rows: 0}, query: &metadataSQLQueryStep{columns: []string{"hash"}, rows: [][]driver.Value{{"current"}}}, err: true},
	} {
		state := metadataSQLState{execSteps: []metadataSQLExecStep{testCase.exec}}
		if testCase.query != nil {
			state.querySteps = []metadataSQLQueryStep{*testCase.query}
		}
		if err := runReplace(t, state, &expected); (err != nil) != testCase.err {
			t.Fatalf("expected replace err=%v", err)
		}
	}
}

func TestDefinitionVersionAndAuditHelpers(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	for _, testCase := range []struct {
		step metadataSQLQueryStep
		want string
		err  bool
	}{
		{step: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{step: metadataSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(2)}}}, want: "3"},
	} {
		var got string
		err := runMetadataTransaction(t, base, metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			var err error
			got, err = repository.nextVersion(t.Context(), tx, "object", "account")
			return err
		})
		if got != testCase.want || (err != nil) != testCase.err {
			t.Fatalf("version=%q err=%v", got, err)
		}
	}
	for _, step := range []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}} {
		err := runMetadataTransaction(t, base, metadataSQLState{execSteps: []metadataSQLExecStep{step}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			return repository.insertDefinitionVersion(t.Context(), tx, "object", "account", "1", strings.Repeat("a", 64), []byte(`{}`), "now")
		})
		if (err != nil) != (step.err != nil) {
			t.Fatalf("insert version err=%v", err)
		}
	}
	valid := metadataDefinitionAuditValue(appschemamodel.ApplicationDefinition{ResourceType: "object", Payload: []byte(`{"key":"account"}`)})
	invalid := metadataDefinitionAuditValue(appschemamodel.ApplicationDefinition{ResourceType: "object", Payload: []byte(`{`)})
	if valid["payload"] == nil || invalid["payload"] != nil {
		t.Fatalf("valid=%#v invalid=%#v", valid, invalid)
	}
}

func metadataCatalogHashQuerySteps(first metadataSQLQueryStep) []metadataSQLQueryStep {
	steps := []metadataSQLQueryStep{first}
	for len(steps) < len(metadataCatalogDefinitionTables()) {
		steps = append(steps, metadataSQLQueryStep{columns: []string{"resource_key", "schema_hash"}})
	}
	return steps
}

func TestDefinitionCatalogHashRefreshBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	tests := []struct {
		name  string
		first metadataSQLQueryStep
		exec  metadataSQLExecStep
		err   bool
	}{
		{name: "query", first: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{name: "scan", first: metadataSQLQueryStep{columns: []string{"resource_key"}, rows: [][]driver.Value{{"account"}}}, err: true},
		{name: "rows", first: metadataSQLQueryStep{columns: []string{"resource_key", "schema_hash"}, rows: [][]driver.Value{{"account", "hash"}}, nextErr: errMetadataSQL}, err: true},
		{name: "exec", first: metadataSQLQueryStep{columns: []string{"resource_key", "schema_hash"}}, exec: metadataSQLExecStep{err: errMetadataSQL}, err: true},
		{name: "success", first: metadataSQLQueryStep{columns: []string{"resource_key", "schema_hash"}, rows: [][]driver.Value{{"account", "hash"}}}, exec: metadataSQLExecStep{rows: 1}},
	}
	for _, testCase := range tests {
		t.Run("direct "+testCase.name, func(t *testing.T) {
			repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: metadataCatalogHashQuerySteps(testCase.first), execSteps: []metadataSQLExecStep{testCase.exec}}, base)
			if err := repository.refreshCatalogHash(t.Context()); (err != nil) != testCase.err {
				t.Fatalf("refresh err=%v", err)
			}
		})
		t.Run("transaction "+testCase.name, func(t *testing.T) {
			err := runMetadataTransaction(t, base, metadataSQLState{querySteps: metadataCatalogHashQuerySteps(testCase.first), execSteps: []metadataSQLExecStep{testCase.exec}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
				return repository.refreshCatalogHashTx(t.Context(), tx, "now")
			})
			if (err != nil) != testCase.err {
				t.Fatalf("refresh tx err=%v", err)
			}
		})
	}
}
