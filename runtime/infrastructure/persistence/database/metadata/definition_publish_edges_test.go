package metadata

import (
	"database/sql/driver"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func metadataObjectUpsertRequest() metadatamodel.MetadataDefinitionUpsertRequest {
	return metadatamodel.MetadataDefinitionUpsertRequest{Payload: []byte(`{"key":"account","name":"Account"}`)}
}

func metadataDefinitionMissingStep() metadataSQLQueryStep {
	return metadataSQLQueryStep{columns: metadataDefinitionColumns()}
}

func metadataVersionCountStep() metadataSQLQueryStep {
	return metadataSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}
}

func metadataEmptyCatalogHashSteps() []metadataSQLQueryStep {
	return metadataCatalogHashQuerySteps(metadataSQLQueryStep{columns: []string{"resource_key", "schema_hash"}})
}

func TestPublishDefinitionValidationAndTransactionFailures(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	if _, err := base.PublishDefinition(t.Context(), identitySystemScopeZero(), "object", "account", metadataObjectUpsertRequest(), auditmodel.AuditEvent{}); err == nil {
		t.Fatal("expected publish scope error")
	}
	for _, testCase := range []struct {
		resourceType string
		resourceKey  string
		request      metadatamodel.MetadataDefinitionUpsertRequest
	}{
		{resourceType: "missing", resourceKey: "account", request: metadataObjectUpsertRequest()},
		{resourceType: "object", resourceKey: "account"},
		{resourceType: "object", resourceKey: "other", request: metadataObjectUpsertRequest()},
		{resourceType: "object", resourceKey: "account", request: metadatamodel.MetadataDefinitionUpsertRequest{Payload: []byte(`{`)}},
	} {
		if _, err := base.publishDefinition(t.Context(), metadataInstallScope(), testCase.resourceType, testCase.resourceKey, testCase.request, nil); err == nil {
			t.Fatalf("expected validation error for %#v", testCase)
		}
	}
	if _, err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}}}, base).publishDefinition(t.Context(), metadataInstallScope(), "object", "account", metadataObjectUpsertRequest(), nil); err == nil {
		t.Fatal("expected replay read error")
	}
	if _, err := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{metadataDefinitionMissingStep()}, beginErr: errMetadataSQL}, base).publishDefinition(t.Context(), metadataInstallScope(), "object", "account", metadataObjectUpsertRequest(), nil); err == nil {
		t.Fatal("expected publish begin error")
	}
	tests := []struct {
		name    string
		queries []metadataSQLQueryStep
		execs   []metadataSQLExecStep
		commit  error
	}{
		{name: "replace", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep()}, execs: []metadataSQLExecStep{{err: errMetadataSQL}}},
		{name: "version count", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), {err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}}},
		{name: "insert", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), {err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}},
		{name: "insert no replay", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), metadataDefinitionMissingStep()}, execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}},
		{name: "insert version", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), {err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}}},
		{name: "insert version no replay", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), metadataDefinitionMissingStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}}},
		{name: "intent", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep()}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {err: errMetadataSQL}}},
		{name: "refresh", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), {err: errMetadataSQL}}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}}},
		{name: "commit", queries: append(append([]metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep()}, metadataEmptyCatalogHashSteps()...), metadataSQLQueryStep{err: errMetadataSQL}), execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}}, commit: errMetadataSQL},
		{name: "commit no replay", queries: append(append([]metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep()}, metadataEmptyCatalogHashSteps()...), metadataDefinitionMissingStep()), execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}}, commit: errMetadataSQL},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: testCase.queries, execSteps: testCase.execs, commitErr: testCase.commit}, base)
			if _, err := repository.publishDefinition(t.Context(), metadataInstallScope(), "object", "account", metadataObjectUpsertRequest(), nil); err == nil {
				t.Fatal("expected publish error")
			}
		})
	}
	badAudit := auditmodel.AuditEvent{Metadata: map[string]any{"bad": make(chan int)}}
	repository := scriptedMetadataStore(t, &metadataSQLState{
		querySteps: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep()},
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}},
	}, base)
	if _, err := repository.PublishDefinition(t.Context(), metadataInstallScope(), "object", "account", metadataObjectUpsertRequest(), badAudit); err == nil {
		t.Fatal("expected publish audit error")
	}
}

func TestPublishDefinitionConcurrentReplayFallbacks(t *testing.T) {
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
		name    string
		queries []metadataSQLQueryStep
		execs   []metadataSQLExecStep
		commit  error
	}{
		{name: "insert", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), concurrent}, execs: []metadataSQLExecStep{{rows: 1}, {err: errMetadataSQL}}},
		{name: "version", queries: []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep(), concurrent}, execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {err: errMetadataSQL}}},
		{name: "commit", queries: append(append([]metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep()}, metadataEmptyCatalogHashSteps()...), concurrent), execs: []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}}, commit: errMetadataSQL},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: testCase.queries, execSteps: testCase.execs, commitErr: testCase.commit}, base)
			definition, err := repository.publishDefinition(t.Context(), metadataInstallScope(), "object", "account", metadataObjectUpsertRequest(), nil)
			if err != nil || definition.SchemaHash != hash {
				t.Fatalf("definition=%#v err=%v", definition, err)
			}
		})
	}
}

func TestPublishDefinitionScriptedSuccess(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	queries := []metadataSQLQueryStep{metadataDefinitionMissingStep(), metadataVersionCountStep()}
	queries = append(queries, metadataEmptyCatalogHashSteps()...)
	repository := scriptedMetadataStore(t, &metadataSQLState{
		querySteps: queries,
		execSteps:  []metadataSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}},
	}, base)
	definition, err := repository.publishDefinition(t.Context(), metadataInstallScope(), "object", "account", metadataObjectUpsertRequest(), nil)
	if err != nil || definition.ResourceKey != "account" || definition.SchemaVersion != "1" || definition.SourceKind != "user" || definition.SourceID != "metadata_api" {
		t.Fatalf("definition=%#v err=%v", definition, err)
	}
}
