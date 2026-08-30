package appschema

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

func TestApplyDefinitionArchiveBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	run := func(t *testing.T, state metadataSQLState, mutation appschemamodel.ApplicationDefinitionMutation) (appschemamodel.ApplicationDefinition, error) {
		t.Helper()
		var definition appschemamodel.ApplicationDefinition
		err := runMetadataTransaction(t, base, state, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			var err error
			definition, err = repository.applyDefinitionArchive(t.Context(), tx, mutation)
			return err
		})
		return definition, err
	}
	if _, err := run(t, metadataSQLState{}, appschemamodel.ApplicationDefinitionMutation{ResourceType: "missing"}); err == nil {
		t.Fatal("expected archive type error")
	}
	if _, err := run(t, metadataSQLState{}, appschemamodel.ApplicationDefinitionMutation{ResourceType: "object", ResourceKey: "account"}); err == nil {
		t.Fatal("expected archive hash conflict")
	}
	empty, expected := " ", "expected"
	if _, err := run(t, metadataSQLState{}, appschemamodel.ApplicationDefinitionMutation{ResourceType: "object", ResourceKey: "account", Request: appschemamodel.ApplicationDefinitionUpsertRequest{ExpectedSchemaHash: &empty}}); err == nil {
		t.Fatal("expected empty archive hash conflict")
	}
	mutation := appschemamodel.ApplicationDefinitionMutation{ResourceType: "object", ResourceKey: "account", Request: appschemamodel.ApplicationDefinitionUpsertRequest{ExpectedSchemaHash: &expected}}
	for _, testCase := range []struct {
		exec  metadataSQLExecStep
		query *metadataSQLQueryStep
		err   bool
	}{
		{exec: metadataSQLExecStep{err: errMetadataSQL}, err: true},
		{exec: metadataSQLExecStep{rowsErr: errMetadataSQL}, err: true},
		{exec: metadataSQLExecStep{rows: 0}, query: &metadataSQLQueryStep{columns: []string{"hash"}, rows: [][]driver.Value{{"current"}}}, err: true},
		{exec: metadataSQLExecStep{rows: 1}},
	} {
		state := metadataSQLState{execSteps: []metadataSQLExecStep{testCase.exec}}
		if testCase.query != nil {
			state.querySteps = []metadataSQLQueryStep{*testCase.query}
		}
		definition, err := run(t, state, mutation)
		if (err != nil) != testCase.err {
			t.Fatalf("archive err=%v", err)
		}
		if err == nil && definition.DisabledAt == "" {
			t.Fatal("archive did not return disabled timestamp")
		}
	}
}

func TestDefinitionMutationAuditAndLocalizedProjectionBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	for _, testCase := range []struct {
		event auditmodel.AuditEvent
		step  metadataSQLExecStep
		err   bool
	}{
		{event: auditmodel.AuditEvent{Before: map[string]any{"bad": make(chan int)}}, err: true},
		{event: auditmodel.AuditEvent{After: map[string]any{"bad": make(chan int)}}, err: true},
		{event: auditmodel.AuditEvent{Metadata: map[string]any{"bad": make(chan int)}}, err: true},
		{step: metadataSQLExecStep{err: errMetadataSQL}, err: true},
		{step: metadataSQLExecStep{rows: 1}},
	} {
		err := runMetadataTransaction(t, base, metadataSQLState{execSteps: []metadataSQLExecStep{testCase.step}}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			event := testCase.event
			event.WorkspaceID = "default"
			return repository.insertChangeAudit(t.Context(), tx, event)
		})
		if (err != nil) != testCase.err {
			t.Fatalf("audit err=%v", err)
		}
	}
	runProjection := func(t *testing.T, state metadataSQLState, payload string) error {
		t.Helper()
		return runMetadataTransaction(t, base, state, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			return repository.syncLocalizedProjection(t.Context(), tx, "object", "account", []byte(payload), "source", "now")
		})
	}
	if err := runProjection(t, metadataSQLState{}, `{`); err == nil {
		t.Fatal("expected projection decode error")
	}
	if err := runProjection(t, metadataSQLState{}, `{}`); err != nil {
		t.Fatal(err)
	}
	payload := `{"i18n":{"en-US":{"name":"Account"}}}`
	for _, testCase := range []struct {
		steps []metadataSQLExecStep
		err   bool
	}{
		{steps: []metadataSQLExecStep{{err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}, {rowsErr: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}, {rows: 1}}},
		{steps: []metadataSQLExecStep{{rows: 1}, {rows: 0}, {err: errMetadataSQL}}, err: true},
		{steps: []metadataSQLExecStep{{rows: 1}, {rows: 0}, {rows: 1}}},
	} {
		if err := runProjection(t, metadataSQLState{execSteps: testCase.steps}, payload); (err != nil) != testCase.err {
			t.Fatalf("projection err=%v", err)
		}
	}
}

func TestStageRollbackIntentBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	run := func(t *testing.T, state metadataSQLState, request appschemamodel.ApplicationDefinitionRollbackRequest) error {
		t.Helper()
		return runMetadataTransaction(t, base, state, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			return repository.stageRollbackIntent(t.Context(), tx, "object", "account", request, auditmodel.AuditEvent{WorkspaceID: "default", ActorID: "actor"}, "now")
		})
	}
	if err := run(t, metadataSQLState{}, appschemamodel.ApplicationDefinitionRollbackRequest{}); err == nil {
		t.Fatal("expected plan ID error")
	}
	if err := runMetadataTransaction(t, base, metadataSQLState{}, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
		return repository.stageRollbackIntent(t.Context(), tx, "object", "account", appschemamodel.ApplicationDefinitionRollbackRequest{ChangePlanID: "plan"}, auditmodel.AuditEvent{WorkspaceID: " ", ActorID: "actor"}, "now")
	}); err == nil {
		t.Fatal("expected rollback workspace error")
	}
	request := appschemamodel.ApplicationDefinitionRollbackRequest{ChangePlanID: " plan ", TargetVersion: "1"}
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
			t.Fatalf("stage rollback err=%v", err)
		}
	}
}

func TestApplyDefinitionMutationsControlBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	if _, err := base.ApplyDefinitionMutations(t.Context(), identitySystemScopeZero(), nil, nil, nil); err == nil {
		t.Fatal("expected mutation scope error")
	}
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{beginErr: errMetadataSQL}, base).ApplyDefinitionMutations(t.Context(), metadataInstallScope(), nil, nil, nil); err == nil {
		t.Fatal("expected mutation begin error")
	}
	unsupported := []appschemamodel.ApplicationDefinitionMutation{{Operation: "unsupported"}}
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{}, base).ApplyDefinitionMutations(t.Context(), metadataInstallScope(), unsupported, nil, nil); err == nil {
		t.Fatal("expected unsupported operation error")
	}
	noop := []appschemamodel.ApplicationDefinitionMutation{{Operation: "noop"}}
	if definitions, err := scriptedApplicationSchemaStore(t, &metadataSQLState{}, base).ApplyDefinitionMutations(t.Context(), metadataInstallScope(), noop, nil, nil); err != nil || len(definitions) != 0 {
		t.Fatalf("noop definitions=%#v err=%v", definitions, err)
	}
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{commitErr: errMetadataSQL}, base).ApplyDefinitionMutations(t.Context(), metadataInstallScope(), noop, nil, nil); err == nil {
		t.Fatal("expected mutation commit error")
	}
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}}}, base).ApplyDefinitionMutations(t.Context(), metadataInstallScope(), noop, nil, nil); err == nil {
		t.Fatal("expected mutation catalog refresh error")
	}
	badAudit := []auditmodel.AuditEvent{{Before: map[string]any{"bad": make(chan int)}}}
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{}, base).ApplyDefinitionMutations(t.Context(), metadataInstallScope(), nil, badAudit, nil); err == nil {
		t.Fatal("expected mutation audit error")
	}
	publication := &changeplanmodel.BusinessChangePlanPublication{WorkspaceID: "default", PlanID: "plan", ExpectedRevision: 1}
	invalidPublication := *publication
	invalidPublication.WorkspaceID = " "
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: metadataEmptyCatalogHashSteps(), execSteps: []metadataSQLExecStep{{rows: 1}}}, base).ApplyDefinitionMutations(t.Context(), metadataInstallScope(), nil, nil, &invalidPublication); err == nil {
		t.Fatal("expected publication workspace error")
	}
	for _, step := range []metadataSQLExecStep{{err: errMetadataSQL}, {rowsErr: errMetadataSQL}, {rows: 0}, {rows: 1}} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: metadataEmptyCatalogHashSteps(), execSteps: []metadataSQLExecStep{{rows: 1}, step}}, base)
		_, err := repository.ApplyDefinitionMutations(t.Context(), metadataInstallScope(), nil, nil, publication)
		wantErr := step.err != nil || step.rowsErr != nil || step.rows == 0
		if (err != nil) != wantErr {
			t.Fatalf("publication step=%#v err=%v", step, err)
		}
	}
	repository := scriptedApplicationSchemaStore(t, &metadataSQLState{execSteps: []metadataSQLExecStep{{rows: 1}}}, base)
	if _, err := repository.ApplyDefinitionMutations(t.Context(), metadataInstallScope(), nil, []auditmodel.AuditEvent{{WorkspaceID: "default"}}, &changeplanmodel.BusinessChangePlanPublication{}); err != nil {
		t.Fatalf("valid audit without publication revision: %v", err)
	}
	create := []appschemamodel.ApplicationDefinitionMutation{{Operation: "create", ResourceType: "object", ResourceKey: "account", Request: metadataObjectUpsertRequest()}}
	queries := []metadataSQLQueryStep{metadataVersionCountStep()}
	queries = append(queries, metadataEmptyCatalogHashSteps()...)
	repository = scriptedApplicationSchemaStore(t, &metadataSQLState{
		querySteps: queries,
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}},
	}, base)
	definitions, err := repository.ApplyDefinitionMutations(t.Context(), metadataInstallScope(), create, nil, nil)
	if err != nil || len(definitions) != 1 || definitions[0].ResourceKey != "account" {
		t.Fatalf("created definitions=%#v err=%v", definitions, err)
	}
	roleCreate := []appschemamodel.ApplicationDefinitionMutation{{Operation: "create", ResourceType: "role", ResourceKey: "operator", Request: appschemamodel.ApplicationDefinitionUpsertRequest{Payload: []byte(`{"key":"operator","name":"Operator"}`)}}}
	repository = scriptedApplicationSchemaStore(t, &metadataSQLState{
		querySteps: []metadataSQLQueryStep{metadataVersionCountStep()},
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {err: errMetadataSQL}},
	}, base)
	if _, err := repository.ApplyDefinitionMutations(t.Context(), metadataInstallScope(), roleCreate, nil, &changeplanmodel.BusinessChangePlanPublication{WorkspaceID: "default"}); err == nil {
		t.Fatal("expected identity role directory projection error")
	}
	expected := "hash"
	archive := []appschemamodel.ApplicationDefinitionMutation{{Operation: "archive", ResourceType: "object", ResourceKey: "account", Request: appschemamodel.ApplicationDefinitionUpsertRequest{ExpectedSchemaHash: &expected}}}
	repository = scriptedApplicationSchemaStore(t, &metadataSQLState{
		querySteps: metadataEmptyCatalogHashSteps(),
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}},
	}, base)
	definitions, err = repository.ApplyDefinitionMutations(t.Context(), metadataInstallScope(), archive, nil, nil)
	if err != nil || len(definitions) != 1 || definitions[0].DisabledAt == "" {
		t.Fatalf("archived definitions=%#v err=%v", definitions, err)
	}
}

func TestApplyDefinitionUpsertFailureBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	run := func(t *testing.T, state metadataSQLState, mutation appschemamodel.ApplicationDefinitionMutation) error {
		t.Helper()
		return runMetadataTransaction(t, base, state, func(repository ApplicationSchemaStore, tx *sql.Tx) error {
			_, err := repository.applyDefinitionUpsert(t.Context(), tx, mutation)
			return err
		})
	}
	for _, mutation := range []appschemamodel.ApplicationDefinitionMutation{
		{ResourceType: "missing", ResourceKey: "account", Request: metadataObjectUpsertRequest()},
		{ResourceType: "object", ResourceKey: "other", Request: metadataObjectUpsertRequest()},
		{ResourceType: "object", ResourceKey: "account", Request: appschemamodel.ApplicationDefinitionUpsertRequest{Payload: []byte(`{`)}},
	} {
		if err := run(t, metadataSQLState{}, mutation); err == nil {
			t.Fatalf("expected upsert validation error for %#v", mutation)
		}
	}
	mutation := appschemamodel.ApplicationDefinitionMutation{ResourceType: "object", ResourceKey: "account", Request: metadataObjectUpsertRequest()}
	for _, testCase := range []struct {
		queries []metadataSQLQueryStep
		execs   []metadataSQLExecStep
		err     bool
	}{
		{execs: []metadataSQLExecStep{{err: errMetadataSQL}}, err: true},
		{queries: []metadataSQLQueryStep{{err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}}, err: true},
		{queries: []metadataSQLQueryStep{metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, err: true},
		{queries: []metadataSQLQueryStep{metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}}, err: true},
		{queries: []metadataSQLQueryStep{metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}}},
	} {
		if err := run(t, metadataSQLState{querySteps: testCase.queries, execSteps: testCase.execs}, mutation); (err != nil) != testCase.err {
			t.Fatalf("upsert err=%v", err)
		}
	}
	i18nMutation := mutation
	i18nMutation.Request.Payload = []byte(`{"key":"account","name":"Account","i18n":{"en-US":{"name":"Account"}}}`)
	if err := run(t, metadataSQLState{
		querySteps: []metadataSQLQueryStep{metadataVersionCountStep()},
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {err: errMetadataSQL}},
	}, i18nMutation); err == nil {
		t.Fatal("expected localized projection failure")
	}
}

func TestRollbackDefinitionBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewApplicationSchemaStore(baseDB)
	request := appschemamodel.ApplicationDefinitionRollbackRequest{TargetVersion: "1", ExpectedSchemaHash: "current", ChangePlanID: "plan"}
	audit := auditmodel.AuditEvent{WorkspaceID: "default", ActorID: "actor"}
	if _, err := base.RollbackDefinition(t.Context(), identitySystemScopeZero(), "object", "account", request, audit); err == nil {
		t.Fatal("expected rollback scope error")
	}
	if _, err := base.RollbackDefinition(t.Context(), metadataInstallScope(), "missing", "account", request, audit); err == nil {
		t.Fatal("expected rollback type error")
	}
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{beginErr: errMetadataSQL}, base).RollbackDefinition(t.Context(), metadataInstallScope(), "object", "account", request, audit); err == nil {
		t.Fatal("expected rollback begin error")
	}
	versionColumns := []string{"payload_json", "schema_hash"}
	validVersion := metadataSQLQueryStep{columns: versionColumns, rows: [][]driver.Value{{`{"key":"account","name":"Account"}`, "target"}}}
	for _, step := range []metadataSQLQueryStep{{columns: versionColumns}, {err: errMetadataSQL}} {
		repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base)
		if _, err := repository.RollbackDefinition(t.Context(), metadataInstallScope(), "object", "account", request, audit); err == nil {
			t.Fatal("expected rollback version read error")
		}
	}
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{validVersion, {err: errMetadataSQL}}}, base).RollbackDefinition(t.Context(), metadataInstallScope(), "object", "account", request, audit); err == nil {
		t.Fatal("expected rollback next version error")
	}
	invalidVersion := metadataSQLQueryStep{columns: versionColumns, rows: [][]driver.Value{{`{`, "target"}}}
	if _, err := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{invalidVersion, metadataVersionCountStep()}}, base).RollbackDefinition(t.Context(), metadataInstallScope(), "object", "account", request, audit); err == nil {
		t.Fatal("expected rollback target shape error")
	}
	for _, testCase := range []struct {
		name    string
		queries []metadataSQLQueryStep
		execs   []metadataSQLExecStep
		request appschemamodel.ApplicationDefinitionRollbackRequest
		audit   auditmodel.AuditEvent
		commit  error
	}{
		{name: "update", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{err: errMetadataSQL}}, request: request, audit: audit},
		{name: "update rows", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rowsErr: errMetadataSQL}}, request: request, audit: audit},
		{name: "conflict", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep(), {columns: []string{"hash"}, rows: [][]driver.Value{{"actual"}}}}, execs: []metadataSQLExecStep{{rows: 0}}, request: request, audit: audit},
		{name: "version insert", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}, request: request, audit: audit},
		{name: "audit", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}}, request: request, audit: auditmodel.AuditEvent{Before: map[string]any{"bad": make(chan int)}}},
		{name: "stage", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}}, request: appschemamodel.ApplicationDefinitionRollbackRequest{TargetVersion: "1", ExpectedSchemaHash: "current"}, audit: audit},
		{name: "refresh", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep(), {err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}}, request: request, audit: audit},
		{name: "commit", queries: []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}}, request: request, audit: audit, commit: errMetadataSQL},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := scriptedApplicationSchemaStore(t, &metadataSQLState{querySteps: testCase.queries, execSteps: testCase.execs, commitErr: testCase.commit}, base)
			if _, err := repository.RollbackDefinition(t.Context(), metadataInstallScope(), "object", "account", testCase.request, testCase.audit); err == nil {
				t.Fatal("expected rollback error")
			}
		})
	}
	i18nVersion := metadataSQLQueryStep{columns: versionColumns, rows: [][]driver.Value{{`{"key":"account","name":"Account","i18n":{"en-US":{"name":"Account"}}}`, "target"}}}
	repository := scriptedApplicationSchemaStore(t, &metadataSQLState{
		querySteps: []metadataSQLQueryStep{i18nVersion, metadataVersionCountStep()},
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}},
	}, base)
	if _, err := repository.RollbackDefinition(t.Context(), metadataInstallScope(), "object", "account", request, audit); err == nil {
		t.Fatal("expected rollback projection error")
	}
	queries := []metadataSQLQueryStep{validVersion, metadataVersionCountStep()}
	queries = append(queries, metadataEmptyCatalogHashSteps()...)
	queries = append(queries, metadataSQLQueryStep{columns: metadataDefinitionColumns(), rows: [][]driver.Value{metadataDefinitionRow("2", "target", nil)}})
	repository = scriptedApplicationSchemaStore(t, &metadataSQLState{
		querySteps: queries,
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}},
	}, base)
	definition, err := repository.RollbackDefinition(t.Context(), metadataInstallScope(), "object", "account", request, audit)
	if err != nil || definition.ResourceKey != "account" {
		t.Fatalf("rollback definition=%#v err=%v", definition, err)
	}
}
