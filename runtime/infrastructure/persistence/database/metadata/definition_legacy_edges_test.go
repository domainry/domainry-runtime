package metadata

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func TestLegacyReplaceDefinitionBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	run := func(t *testing.T, state metadataSQLState, expected *string) error {
		t.Helper()
		return runMetadataTransaction(t, base, state, func(repository MetadataStore, tx *sql.Tx) error {
			return repository.replaceMetadataDefinitionVersion(t.Context(), tx, "object_definitions", "object", "account", expected)
		})
	}
	for _, step := range []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}} {
		if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{step}}, nil); (err != nil) != (step.err != nil) {
			t.Fatalf("nil expected err=%v", err)
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
		if err := run(t, metadataSQLState{querySteps: []metadataSQLQueryStep{testCase.step}}, &empty); (err != nil) != testCase.err {
			t.Fatalf("empty expected err=%v", err)
		}
	}
	expected := "expected"
	for _, testCase := range []struct {
		exec  metadataSQLExecStep
		query metadataSQLQueryStep
		err   bool
	}{
		{exec: metadataSQLExecStep{err: errMetadataSQL}, err: true},
		{exec: metadataSQLExecStep{rowsErr: errMetadataSQL}, err: true},
		{exec: metadataSQLExecStep{rows: 1}},
		{exec: metadataSQLExecStep{rows: 0}, query: metadataSQLQueryStep{err: errMetadataSQL}, err: true},
		{exec: metadataSQLExecStep{rows: 0}, query: metadataSQLQueryStep{columns: []string{"hash"}}, err: true},
		{exec: metadataSQLExecStep{rows: 0}, query: metadataSQLQueryStep{columns: []string{"hash"}, rows: [][]driver.Value{{"current"}}}, err: true},
	} {
		if err := run(t, metadataSQLState{execSteps: []metadataSQLExecStep{testCase.exec}, querySteps: []metadataSQLQueryStep{testCase.query}}, &expected); (err != nil) != testCase.err {
			t.Fatalf("expected err=%v", err)
		}
	}
}

func TestLegacyDefinitionListAndVersionFailures(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	if _, err := base.ListMetadataDefinitions(t.Context(), "missing", ""); err == nil {
		t.Fatal("expected list type error")
	}
	for _, step := range []metadataSQLQueryStep{
		{err: errMetadataSQL},
		{columns: []string{"resource_key"}, rows: [][]driver.Value{{"account"}}},
		{columns: metadataDefinitionColumns(), rows: [][]driver.Value{metadataDefinitionRow("1", "hash", "disabled")}, nextErr: errMetadataSQL},
	} {
		repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base)
		if _, err := repository.ListMetadataDefinitions(t.Context(), "object", " workspace "); err == nil {
			t.Fatal("expected legacy list error")
		}
	}
	columns := []string{"schema_version", "schema_hash", "payload_json", "created_at"}
	for _, step := range []metadataSQLQueryStep{
		{err: errMetadataSQL},
		{columns: []string{"schema_version"}, rows: [][]driver.Value{{"1"}}},
		{columns: columns, rows: [][]driver.Value{{"1", "hash", `{}`, "now"}}, nextErr: errMetadataSQL},
	} {
		repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base)
		if _, err := repository.ListMetadataDefinitionVersions(t.Context(), "object", "account"); err == nil {
			t.Fatal("expected version list error")
		}
	}
	for _, rows := range [][][]driver.Value{
		{{"2", "two", `{}`, "old"}, {"10", "ten", `{}`, "older"}},
		{{"alpha", "a", `{}`, "same"}, {"2", "two", `{}`, "same"}},
		{{"2", "two", `{}`, "same"}, {"2", "same", `{}`, "same"}},
		{{"alpha", "a", `{}`, "same"}, {"beta", "b", `{}`, "same"}},
		{{"old", "a", `{}`, "2025"}, {"new", "b", `{}`, "2026"}},
	} {
		repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{{columns: columns, rows: rows}}}, base)
		if versions, err := repository.ListMetadataDefinitionVersions(t.Context(), "object", "account"); err != nil || len(versions) != 2 {
			t.Fatalf("versions=%#v err=%v", versions, err)
		}
	}
}

func TestLegacyDisableAndRollbackIntentBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	if err := base.DisableMetadataDefinition(t.Context(), "missing", "account"); err == nil {
		t.Fatal("expected disable type error")
	}
	for _, step := range []metadataSQLExecStep{{err: errMetadataSQL}, {rowsErr: errMetadataSQL}, {rows: 0}, {rows: 1}} {
		repository := scriptedMetadataStore(t, &metadataSQLState{execSteps: []metadataSQLExecStep{step}}, base)
		err := repository.DisableMetadataDefinition(t.Context(), "object", "account")
		wantErr := step.err != nil || step.rowsErr != nil || step.rows == 0
		if (err != nil) != wantErr {
			t.Fatalf("disable err=%v", err)
		}
	}
	run := func(t *testing.T, state metadataSQLState, request metadatamodel.MetadataDefinitionRollbackRequest) error {
		t.Helper()
		return runMetadataTransaction(t, base, state, func(repository MetadataStore, tx *sql.Tx) error {
			return repository.stageMetadataRollbackIntentTx(t.Context(), tx, "object", "account", request, auditmodel.AuditEvent{WorkspaceID: "default", ActorID: "actor"}, "now")
		})
	}
	if err := run(t, metadataSQLState{}, metadatamodel.MetadataDefinitionRollbackRequest{}); err == nil {
		t.Fatal("expected rollback plan ID error")
	}
	if err := runMetadataTransaction(t, base, metadataSQLState{}, func(repository MetadataStore, tx *sql.Tx) error {
		return repository.stageMetadataRollbackIntentTx(t.Context(), tx, "object", "account", metadatamodel.MetadataDefinitionRollbackRequest{ChangePlanID: "plan"}, auditmodel.AuditEvent{WorkspaceID: " ", ActorID: "actor"}, "now")
	}); err == nil {
		t.Fatal("expected legacy rollback workspace error")
	}
	request := metadatamodel.MetadataDefinitionRollbackRequest{ChangePlanID: "plan", TargetVersion: "1"}
	for _, testCase := range []struct {
		steps []metadataSQLExecStep
		err   bool
	}{
		{steps: []metadataSQLExecStep{{err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rowsErr: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}}},
		{steps: []metadataSQLExecStep{{rows: 0}, {err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 0}, {rows: 1}}},
	} {
		if err := run(t, metadataSQLState{execSteps: testCase.steps}, request); (err != nil) != testCase.err {
			t.Fatalf("rollback intent err=%v", err)
		}
	}
}

func TestLegacyUpsertDefinitionBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	for _, testCase := range []struct {
		resourceType string
		resourceKey  string
		request      metadatamodel.MetadataDefinitionUpsertRequest
	}{
		{resourceType: "missing", resourceKey: "account", request: metadataObjectUpsertRequest()},
		{resourceType: "object", resourceKey: "account"},
		{resourceType: "object", resourceKey: "account", request: metadatamodel.MetadataDefinitionUpsertRequest{Payload: []byte(`{`)}},
	} {
		if _, err := base.UpsertMetadataDefinition(t.Context(), testCase.resourceType, testCase.resourceKey, testCase.request); err == nil {
			t.Fatalf("expected legacy upsert validation error for %#v", testCase)
		}
	}
	if _, err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}}}, base).UpsertMetadataDefinition(t.Context(), "object", "account", metadataObjectUpsertRequest()); err == nil {
		t.Fatal("expected legacy replay error")
	}
	if _, err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{metadataDefinitionMissingStep(), {err: errMetadataSQL}}}, base).UpsertMetadataDefinition(t.Context(), "object", "account", metadataObjectUpsertRequest()); err == nil {
		t.Fatal("expected legacy version count error")
	}
	if _, err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep()}, beginErr: errMetadataSQL}, base).UpsertMetadataDefinition(t.Context(), "object", "account", metadataObjectUpsertRequest()); err == nil {
		t.Fatal("expected legacy begin error")
	}
	for _, testCase := range []struct {
		name    string
		queries []metadataSQLQueryStep
		execs   []metadataSQLExecStep
		commit  error
	}{
		{name: "replace", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep()}, execs: []metadataSQLExecStep{{err: errMetadataSQL}}},
		{name: "insert", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), {err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}},
		{name: "insert no replay", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), metadataDefinitionMissingStep()}, execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}},
		{name: "version", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), {err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}}},
		{name: "version no replay", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), metadataDefinitionMissingStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}}},
		{name: "commit", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), {err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}}, commit: errMetadataSQL},
		{name: "commit no replay", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), metadataDefinitionMissingStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}}, commit: errMetadataSQL},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: testCase.queries, execSteps: testCase.execs, commitErr: testCase.commit}, base)
			if _, err := repository.UpsertMetadataDefinition(t.Context(), "object", "account", metadataObjectUpsertRequest()); err == nil {
				t.Fatal("expected legacy upsert error")
			}
		})
	}
	repository := scriptedMetadataStore(t, &metadataSQLState{
		querySteps: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep()},
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}},
	}, base)
	request := metadataObjectUpsertRequest()
	request.SourceKind, request.SourceID = " builder ", " plan "
	definition, err := repository.UpsertMetadataDefinition(t.Context(), "object", "account", request)
	if err != nil || definition.ResourceKey != "account" || definition.SourceKind != "builder" || definition.SourceID != "plan" {
		t.Fatalf("legacy definition=%#v err=%v", definition, err)
	}
}

func TestLegacyUpsertConcurrentReplayFallbacks(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	shape, err := metadataDefinitionShape(t.Context(), "object", "account", metadataObjectUpsertRequest())
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := metadataPayload(shape.Payload)
	if err != nil {
		t.Fatal(err)
	}
	concurrent := metadataSQLQueryStep{columns: metadataDefinitionColumns(), rows: [][]driver.Value{metadataDefinitionRow("1", hash, nil)}}
	for _, testCase := range []struct {
		name   string
		execs  []metadataSQLExecStep
		commit error
	}{
		{name: "insert", execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}},
		{name: "version", execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}}},
		{name: "commit", execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}}, commit: errMetadataSQL},
	} {
		repository := scriptedMetadataStore(t, &metadataSQLState{
			querySteps: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), concurrent},
			execSteps:  testCase.execs, commitErr: testCase.commit,
		}, base)
		definition, err := repository.UpsertMetadataDefinition(t.Context(), "object", "account", metadataObjectUpsertRequest())
		if err != nil || definition.SchemaHash != hash {
			t.Fatalf("%s definition=%#v err=%v", testCase.name, definition, err)
		}
	}
}

func TestLegacyRollbackDefinitionBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	request := metadatamodel.MetadataDefinitionRollbackRequest{TargetVersion: "1", ExpectedSchemaHash: "current", ChangePlanID: "plan"}
	audit := auditmodel.AuditEvent{WorkspaceID: "default", ActorID: "actor"}
	if _, err := base.RollbackMetadataDefinition(t.Context(), "missing", "account", request, audit); err == nil {
		t.Fatal("expected rollback type error")
	}
	if _, err := scriptedMetadataStore(t, &metadataSQLState{beginErr: errMetadataSQL}, base).RollbackMetadataDefinition(t.Context(), "object", "account", request, audit); err == nil {
		t.Fatal("expected rollback begin error")
	}
	versionColumns := []string{"payload_json", "schema_hash"}
	validVersion := metadataSQLQueryStep{columns: versionColumns, rows: [][]driver.Value{{`{"key":"account","name":"Account"}`, "target"}}}
	for _, step := range []metadataSQLQueryStep{{columns: versionColumns}, {err: errMetadataSQL}} {
		repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base)
		if _, err := repository.RollbackMetadataDefinition(t.Context(), "object", "account", request, audit); err == nil {
			t.Fatal("expected rollback version error")
		}
	}
	if _, err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{validVersion, {err: errMetadataSQL}}}, base).RollbackMetadataDefinition(t.Context(), "object", "account", request, audit); err == nil {
		t.Fatal("expected rollback count error")
	}
	invalidVersion := metadataSQLQueryStep{columns: versionColumns, rows: [][]driver.Value{{`{`, "target"}}}
	if _, err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{invalidVersion, metadataVersionCountStep()}}, base).RollbackMetadataDefinition(t.Context(), "object", "account", request, audit); err == nil {
		t.Fatal("expected rollback target error")
	}
	for _, testCase := range []struct {
		name    string
		queries []metadataSQLQueryStep
		execs   []metadataSQLExecStep
		request metadatamodel.MetadataDefinitionRollbackRequest
		audit   auditmodel.AuditEvent
		commit  error
	}{
		{name: "update", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{err: errMetadataSQL}}, request: request, audit: audit},
		{name: "update rows", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rowsErr: errMetadataSQL}}, request: request, audit: audit},
		{name: "conflict", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep(), {columns: []string{"hash"}, rows: [][]driver.Value{{"actual"}}}}, execs: []metadataSQLExecStep{{rows: 0}}, request: request, audit: audit},
		{name: "version", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, request: request, audit: audit},
		{name: "audit", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}}, request: request, audit: auditmodel.AuditEvent{Before: map[string]any{"bad": make(chan int)}}},
		{name: "stage", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}}, request: metadatamodel.MetadataDefinitionRollbackRequest{TargetVersion: "1", ExpectedSchemaHash: "current"}, audit: audit},
		{name: "commit", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}}, request: request, audit: audit, commit: errMetadataSQL},
	} {
		repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: testCase.queries, execSteps: testCase.execs, commitErr: testCase.commit}, base)
		if _, err := repository.RollbackMetadataDefinition(t.Context(), "object", "account", testCase.request, testCase.audit); err == nil {
			t.Fatalf("expected %s rollback error", testCase.name)
		}
	}
	i18nVersion := metadataSQLQueryStep{columns: versionColumns, rows: [][]driver.Value{{`{"key":"account","name":"Account","i18n":{"en-US":{"name":"Account"}}}`, "target"}}}
	repository := scriptedMetadataStore(t, &metadataSQLState{
		querySteps: []metadataSQLQueryStep{i18nVersion, metadataVersionCountStep()},
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}},
	}, base)
	if _, err := repository.RollbackMetadataDefinition(t.Context(), "object", "account", request, audit); err == nil {
		t.Fatal("expected rollback projection error")
	}
	repository = scriptedMetadataStore(t, &metadataSQLState{
		querySteps: []metadataSQLQueryStep{validVersion, metadataVersionCountStep(), {columns: metadataDefinitionColumns(), rows: [][]driver.Value{metadataDefinitionRow("2", "target", nil)}}},
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}},
	}, base)
	definition, err := repository.RollbackMetadataDefinition(t.Context(), "object", "account", request, audit)
	if err != nil || definition.ResourceKey != "account" {
		t.Fatalf("legacy rollback definition=%#v err=%v", definition, err)
	}
}
