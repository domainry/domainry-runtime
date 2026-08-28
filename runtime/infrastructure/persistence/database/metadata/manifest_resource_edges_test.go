package metadata

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

func TestManifestMetadataContextDefaultsAndPreservesWorkspace(t *testing.T) {
	if got := requestcontext.WorkspaceID(manifestMetadataContext(context.Background())); got != principalmodel.InstallationWorkspaceID {
		t.Fatalf("default workspace = %q, want %q", got, principalmodel.InstallationWorkspaceID)
	}
	explicit := requestcontext.WithWorkspaceID(context.Background(), "workspace-explicit")
	if got := requestcontext.WorkspaceID(manifestMetadataContext(explicit)); got != "workspace-explicit" {
		t.Fatalf("explicit workspace = %q, want workspace-explicit", got)
	}
}

func TestEnsureObjectStorageUsesSchemaDatabaseForDDL(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	primaryState := metadataSQLState{
		execSteps: []metadataSQLExecStep{{rows: 1}},
		querySteps: []metadataSQLQueryStep{
			{columns: []string{"cid", "name", "type", "notnull", "dflt_value", "pk"}, rows: [][]driver.Value{
				{int64(0), "workspace_id", "TEXT", int64(1), nil, int64(0)},
				{int64(1), "id", "TEXT", int64(1), nil, int64(0)},
				{int64(2), "created_at", "TEXT", int64(1), nil, int64(0)},
				{int64(3), "updated_at", "TEXT", int64(1), nil, int64(0)},
			}},
			{columns: []string{"name"}},
		},
	}
	repository := scriptedMetadataStore(t, &primaryState, NewMetadataStore(baseDB))
	schemaState := metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}}
	schemaDB := openMetadataScriptedDB(&schemaState)
	t.Cleanup(func() { _ = schemaDB.Close() })
	repository.schemaDB = schemaDB
	repository.createIndex = func(context.Context, string, string, bool, ...string) error { return nil }
	if err := repository.ensureObjectStorage(t.Context(), definitionmodel.ObjectSchema{Key: "account", Name: "Account"}); err != nil {
		t.Fatalf("ensureObjectStorage: %v", err)
	}
	if len(schemaState.execSteps) != 0 {
		t.Fatal("schema DDL was not executed through the schema database")
	}
	if len(primaryState.execSteps) != 1 {
		t.Fatal("schema DDL leaked onto the request/query database")
	}
}

func runMetadataTransaction(t *testing.T, base MetadataStore, state metadataSQLState, call func(MetadataStore, *sql.Tx) error) error {
	t.Helper()
	repository := scriptedMetadataStore(t, &state, base)
	tx, err := repository.database().BeginTx(t.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return call(repository, tx)
}

func metadataResourceTestSeed(payload any) metadataResourceSeed {
	return metadataResourceSeed{ResourceType: "object", Table: "object_definitions", Key: "account", ObjectKey: "account", Name: "Account", SchemaVersion: "1", SourceKind: "generated", SourceID: "template", Payload: payload}
}

func TestInsertMetadataResourceBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	if err := runMetadataTransaction(t, base, metadataSQLState{}, func(repository MetadataStore, tx *sql.Tx) error {
		return repository.insertMetadataResource(t.Context(), tx, metadataResourceTestSeed(make(chan int)), "now")
	}); err == nil {
		t.Fatal("expected resource encoding error")
	}
	for _, testCase := range []struct {
		steps []metadataSQLExecStep
		err   bool
	}{
		{steps: []metadataSQLExecStep{{err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}, {rows: 1}}},
	} {
		err := runMetadataTransaction(t, base, metadataSQLState{execSteps: testCase.steps}, func(repository MetadataStore, tx *sql.Tx) error {
			return repository.insertMetadataResource(t.Context(), tx, metadataResourceTestSeed(map[string]any{"key": "account"}), "now")
		})
		if (err != nil) != testCase.err {
			t.Fatalf("insert resource err=%v", err)
		}
	}
}

func TestSyncMetadataResourceBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	if err := runMetadataTransaction(t, base, metadataSQLState{}, func(repository MetadataStore, tx *sql.Tx) error {
		return repository.syncMetadataResource(t.Context(), tx, metadataResourceTestSeed(make(chan int)), "now")
	}); err == nil {
		t.Fatal("expected resource encoding error")
	}
	columns := []string{"schema_hash", "source_kind", "disabled_at"}
	_, sameHash, err := metadataPayload(map[string]any{"key": "account"})
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		queries []metadataSQLQueryStep
		execs   []metadataSQLExecStep
		err     bool
	}{
		{queries: []metadataSQLQueryStep{{err: errMetadataSQL}}, err: true},
		{queries: []metadataSQLQueryStep{{columns: columns}}, execs: []metadataSQLExecStep{{err: errMetadataSQL}}, err: true},
		{queries: []metadataSQLQueryStep{{columns: columns}}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}}},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"old", "user", nil}}}}},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"old", "generated", "disabled"}}}}},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{sameHash, "generated", nil}}}}},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"old", "generated", nil}}}}, execs: []metadataSQLExecStep{{err: errMetadataSQL}}, err: true},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"old", "generated", nil}}}}, execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, err: true},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"old", "generated", nil}}}}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}}},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"old", "generated", nil}}}, {err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}}, err: true},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"old", "generated", nil}}}, {columns: []string{"resource_type", "resource_key", "schema_version", "schema_hash"}, rows: [][]driver.Value{{"wrong", "account", "1", sameHash}}}}, execs: []metadataSQLExecStep{{rows: 1}}, err: true},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"old", "generated", nil}}}, {columns: []string{"resource_type", "resource_key", "schema_version", "schema_hash"}, rows: [][]driver.Value{{"object", "wrong", "1", sameHash}}}}, execs: []metadataSQLExecStep{{rows: 1}}, err: true},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"old", "generated", nil}}}, {columns: []string{"resource_type", "resource_key", "schema_version", "schema_hash"}, rows: [][]driver.Value{{"object", "account", "wrong", sameHash}}}}, execs: []metadataSQLExecStep{{rows: 1}}, err: true},
		{queries: []metadataSQLQueryStep{{columns: columns, rows: [][]driver.Value{{"old", "generated", nil}}}, {columns: []string{"resource_type", "resource_key", "schema_version", "schema_hash"}, rows: [][]driver.Value{{"object", "account", "1", "wrong"}}}}, execs: []metadataSQLExecStep{{rows: 1}}, err: true},
	} {
		err := runMetadataTransaction(t, base, metadataSQLState{querySteps: testCase.queries, execSteps: testCase.execs}, func(repository MetadataStore, tx *sql.Tx) error {
			return repository.syncMetadataResource(t.Context(), tx, metadataResourceTestSeed(map[string]any{"key": "account"}), "now")
		})
		if (err != nil) != testCase.err {
			t.Fatalf("sync resource err=%v", err)
		}
	}
}

func TestSyncMetadataResourceCanReturnToHistoricalGeneratedVersion(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	if err := baseDB.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewMetadataStore(baseDB)
	seed := metadataResourceTestSeed(map[string]any{"key": "account", "name": "Account A"})
	sync := func(seed metadataResourceSeed, now string) error {
		tx, err := baseDB.DB().BeginTx(t.Context(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := repository.syncMetadataResource(t.Context(), tx, seed, now); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := sync(seed, "2026-08-06T00:00:00Z"); err != nil {
		t.Fatalf("insert version A: %v", err)
	}
	seed.Payload = map[string]any{"key": "account", "name": "Account B"}
	if err := sync(seed, "2026-08-06T00:01:00Z"); err != nil {
		t.Fatalf("sync version B: %v", err)
	}
	seed.Payload = map[string]any{"key": "account", "name": "Account A"}
	if err := sync(seed, "2026-08-06T00:02:00Z"); err != nil {
		t.Fatalf("return to version A: %v", err)
	}
	seed.Payload = map[string]any{"key": "account", "name": "Account B"}
	if err := sync(seed, "2026-08-06T00:03:00Z"); err != nil {
		t.Fatalf("return to historical version B: %v", err)
	}
	seed.Payload = map[string]any{"key": "account", "name": "Account A"}
	if err := sync(seed, "2026-08-06T00:04:00Z"); err != nil {
		t.Fatalf("return to historical version A again: %v", err)
	}

	_, expectedHash, err := metadataPayload(seed.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var currentHash string
	if err := baseDB.DB().QueryRowContext(t.Context(), "SELECT schema_hash FROM object_definitions WHERE resource_key = ?", seed.Key).Scan(&currentHash); err != nil {
		t.Fatal(err)
	}
	if currentHash != expectedHash {
		t.Fatalf("active definition did not return to version A: got %s want %s", currentHash, expectedHash)
	}
	var versionCount int
	if err := baseDB.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM metadata_definition_versions WHERE resource_type = ? AND resource_key = ?", seed.ResourceType, seed.Key).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if versionCount != 3 {
		t.Fatalf("historical version was duplicated: got %d versions, want 3", versionCount)
	}
}

func TestManifestMetadataTransactionFailures(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	manifest := manifestmodel.ManifestSchema{TemplateID: "template", Version: "1", Name: "App", Objects: []definitionmodel.ObjectSchema{{Key: "account", Name: "Account"}}}
	if err := base.EnsureManifestMetadata(t.Context(), manifestmodel.ManifestSchema{}); err == nil {
		t.Fatal("expected empty ensure error")
	}
	if err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}}}, base).EnsureManifestMetadata(t.Context(), manifest); err == nil {
		t.Fatal("expected seed status error")
	}
	if err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}, beginErr: errMetadataSQL}, base).EnsureManifestMetadata(t.Context(), manifest); err == nil {
		t.Fatal("expected seed begin error")
	}
	invalid := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: " "}}}
	if err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}}, base).EnsureManifestMetadata(t.Context(), invalid); err == nil {
		t.Fatal("expected invalid seed error")
	}
	countZero := metadataSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}
	successExecs := func(count int) []metadataSQLExecStep {
		steps := make([]metadataSQLExecStep, count)
		for index := range steps {
			steps[index].rows = 1
		}
		return steps
	}
	for _, testCase := range []struct {
		name  string
		state metadataSQLState
	}{
		{name: "catalog", state: metadataSQLState{querySteps: []metadataSQLQueryStep{countZero}, execSteps: []metadataSQLExecStep{{err: errMetadataSQL}}}},
		{name: "resource", state: metadataSQLState{querySteps: []metadataSQLQueryStep{countZero}, execSteps: append(successExecs(5), metadataSQLExecStep{err: errMetadataSQL})}},
		{name: "localized", state: metadataSQLState{
			querySteps: []metadataSQLQueryStep{countZero, {columns: []string{"text", "source_kind"}}},
			execSteps:  append(successExecs(8), metadataSQLExecStep{err: errMetadataSQL}),
		}},
		{name: "commit", state: metadataSQLState{
			querySteps: []metadataSQLQueryStep{countZero, {columns: []string{"text", "source_kind"}}, {columns: []string{"text", "source_kind"}}, {columns: []string{"text", "source_kind"}}},
			execSteps:  successExecs(9), commitErr: errMetadataSQL,
		}},
	} {
		t.Run("ensure "+testCase.name, func(t *testing.T) {
			repository := scriptedMetadataStore(t, &testCase.state, base)
			if err := repository.EnsureManifestMetadata(t.Context(), manifest); err == nil {
				t.Fatal("expected ensure error")
			}
		})
	}
	if err := base.SyncManifestMetadata(t.Context(), manifestmodel.ManifestSchema{}); err == nil {
		t.Fatal("expected empty sync error")
	}
	if err := base.SyncManifestMetadata(t.Context(), invalid); err == nil {
		t.Fatal("expected invalid sync seed error")
	}
	if err := scriptedMetadataStore(t, &metadataSQLState{beginErr: errMetadataSQL}, base).SyncManifestMetadata(t.Context(), manifest); err == nil {
		t.Fatal("expected sync begin error")
	}
	for _, testCase := range []struct {
		name  string
		state metadataSQLState
	}{
		{name: "catalog", state: metadataSQLState{execSteps: []metadataSQLExecStep{{err: errMetadataSQL}}}},
		{name: "resource", state: metadataSQLState{execSteps: successExecs(5), querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}}}},
		{name: "localized", state: metadataSQLState{
			execSteps: append(successExecs(5), metadataSQLExecStep{err: errMetadataSQL}),
			querySteps: []metadataSQLQueryStep{
				{columns: []string{"schema_hash", "source_kind", "disabled_at"}, rows: [][]driver.Value{{"old", "user", nil}}},
				{columns: []string{"schema_hash", "source_kind", "disabled_at"}, rows: [][]driver.Value{{"old", "user", nil}}},
				{columns: []string{"text", "source_kind"}},
			},
		}},
		{name: "commit", state: metadataSQLState{
			execSteps: successExecs(8), commitErr: errMetadataSQL,
			querySteps: []metadataSQLQueryStep{
				{columns: []string{"schema_hash", "source_kind", "disabled_at"}, rows: [][]driver.Value{{"old", "user", nil}}},
				{columns: []string{"schema_hash", "source_kind", "disabled_at"}, rows: [][]driver.Value{{"old", "user", nil}}},
				{columns: []string{"text", "source_kind"}},
				{columns: []string{"text", "source_kind"}},
				{columns: []string{"text", "source_kind"}},
			},
		}},
	} {
		t.Run("sync "+testCase.name, func(t *testing.T) {
			repository := scriptedMetadataStore(t, &testCase.state, base)
			if err := repository.SyncManifestMetadata(t.Context(), manifest); err == nil {
				t.Fatal("expected sync error")
			}
		})
	}
}
