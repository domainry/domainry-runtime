package metadata

import (
	"database/sql/driver"
	"testing"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestManifestLocalizedTextSeedsEveryProjection(t *testing.T) {
	manifest := decodeManifestSeed(t, `{
		"template_id":"template","default_locale":" zh-CN ","name":"应用","description":"描述","i18n":{"en-US":{"name":"App"}},
		"objects":[{"key":"account","name":"客户","description":"客户描述","i18n":{"en-US":{"name":"Account"}},"fields":[{"key":"status","name":"状态","i18n":{"en-US":{"name":"Status"}},"options":[{"value":"active","label":"活跃","description":"可用","i18n":{"en-US":{"label":"Active"}}}]}],"validations":[{"type":"required","field_key":"status","message":"必填","i18n":{"en-US":{"message":"Required"}}}]}],
		"views":[{"key":"account.list","name":"列表","i18n":{"en-US":{"name":"List"}}}],
		"actions":[{"key":"account.create","label":"创建","i18n":{"en-US":{"label":"Create"}},"payload_fields":[{"key":"note","name":"备注","i18n":{"en-US":{"name":"Note"}}}]}],
		"workflows":[{"key":"account.sync","name":"同步","i18n":{"en-US":{"name":"Sync"}}}],
		"dictionaries":[{"key":"status","name":"状态","description":"状态字典","i18n":{"en-US":{"name":"Status"}},"items":[{"value":"active","label":"活跃","description":"可用","i18n":{"en-US":{"label":"Active"}}}]}],
		"reports":[{"key":"account.report","name":"报表","i18n":{"en-US":{"name":"Report"}}}],
		"operation_state_examples":[{"key":"account.state","name":"状态示例","i18n":{"en-US":{"name":"State"}}}],
		"sensitive_field_policies":[{"key":"account.secret","name":"敏感策略","i18n":{"en-US":{"name":"Secret"}}}],
		"report_export_controls":[{"key":"account.export","name":"导出控制","i18n":{"en-US":{"name":"Export"}}}],
		"entrypoints":[{"key":"home","name":"首页","description":"入口","i18n":{"en-US":{"name":"Home"}}}],
		"skills":[{"key":"skill","name":"技能","description":"技能描述","i18n":{"en-US":{"name":"Skill"}}}],
		"agents":[{"key":"agent","name":"助手","description":"助手描述","i18n":{"en-US":{"name":"Agent"}}}]
	}`)
	seeds := manifestLocalizedTextSeeds(manifest)
	if len(seeds) < 35 {
		t.Fatalf("localized seed count=%d", len(seeds))
	}
	for index := 1; index < len(seeds); index++ {
		previous, current := seeds[index-1], seeds[index]
		if previous.EntityType > current.EntityType || (previous.EntityType == current.EntityType && previous.EntityKey > current.EntityKey) {
			t.Fatalf("seeds are not sorted at %d: %#v then %#v", index, previous, current)
		}
	}
	if seeds[0].SourceID != "template" {
		t.Fatalf("source ID=%q", seeds[0].SourceID)
	}
	defaults := manifestLocalizedTextSeeds(decodeManifestSeed(t, `{"name":"App","i18n":{"en-US":{"name":"App"}}}`))
	if len(defaults) != 2 || defaults[0].Locale != "en-US" || defaults[0].SourceID != "generated-template" {
		t.Fatalf("defaults=%#v", defaults)
	}
}

func TestLocalizedProjectionHelperBranches(t *testing.T) {
	malformed := decodeManifestSeed(t, `{"i18n":{"":{"name":"x"},"en-US":{"":"x","name":""}},"objects":[{"key":"","name":"x"}]}`)
	if values := manifestLocalizedTextSeeds(malformed); len(values) != 0 {
		t.Fatalf("malformed values=%#v", values)
	}
	out := []metadatamodel.LocalizedText{}
	addValueOptionDefaultTexts(&out, "option", "parent", nil, " ", "source")
	addValueOptionDefaultTexts(&out, "option", "parent", []map[string]any{
		{"value": nil, "key": "fallback", "label": " Label ", "description": nil},
		{"value": "", "key": "", "label": "ignored"},
		{"value": "empty-label", "label": ""},
		{"value": nil, "key": nil, "label": "ignored"},
	}, "en-US", "source")
	addValueOptionI18n(&out, "option", "parent", []any{
		"not-a-map",
		map[string]any{"value": nil, "key": "fallback", "i18n": map[string]any{"en-US": map[string]any{"label": " Label ", "": "ignored", "empty": "", "nil": nil}, "bad": "value", "": map[string]any{"label": "ignored"}}},
		map[string]any{"value": "", "key": "", "i18n": map[string]any{}},
		map[string]any{"value": nil, "key": nil, "i18n": map[string]any{}},
		map[string]any{"value": "no-i18n"},
	}, "source")
	if len(out) != 2 || out[0].EntityKey != "parent.fallback" || out[1].Text != "Label" {
		t.Fatalf("option localized values=%#v", out)
	}
	if maps := localizedValueOptionMaps([]map[string]any{{"key": "one"}}); len(maps) != 1 {
		t.Fatalf("map options=%#v", maps)
	}
	if maps := localizedValueOptionMaps("invalid"); maps != nil {
		t.Fatalf("invalid options=%#v", maps)
	}
	if firstNonEmptyLocalizedText(" ", " value ") != "value" || firstNonEmptyLocalizedText(" ") != "" {
		t.Fatal("localized fallback failed")
	}
}

func localizedTestText() metadatamodel.LocalizedText {
	return metadatamodel.LocalizedText{WorkspaceID: principalmodel.InstallationWorkspaceID, EntityType: "object", EntityKey: "account", Property: "name", Locale: "en-US", Text: "Account", SourceKind: "generated", SourceID: "template"}
}

func TestSyncLocalizedTextSQLBranches(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	run := func(t *testing.T, state *metadataSQLState, text metadatamodel.LocalizedText) error {
		t.Helper()
		repository := scriptedMetadataStore(t, state, base)
		tx, err := repository.database().BeginTx(t.Context(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return repository.syncLocalizedText(t.Context(), tx, text, "now")
	}
	if err := run(t, &metadataSQLState{}, metadatamodel.LocalizedText{}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []metadatamodel.LocalizedText{
		{EntityType: "object"},
		{EntityType: "object", EntityKey: "account"},
		{EntityType: "object", EntityKey: "account", Property: "name"},
		{EntityType: "object", EntityKey: "account", Property: "name", Locale: "en-US"},
	} {
		if err := run(t, &metadataSQLState{}, invalid); err != nil {
			t.Fatal(err)
		}
	}
	for _, testCase := range []struct {
		name  string
		state metadataSQLState
		err   bool
	}{
		{name: "read error", state: metadataSQLState{
			querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}},
		}, err: true},
		{name: "insert", state: metadataSQLState{
			querySteps: []metadataSQLQueryStep{{columns: []string{"text", "source_kind"}}},
			execSteps:  []metadataSQLExecStep{{rows: 1}},
		}},
		{name: "insert error", state: metadataSQLState{
			querySteps: []metadataSQLQueryStep{{columns: []string{"text", "source_kind"}}},
			execSteps:  []metadataSQLExecStep{{err: errMetadataSQL}},
		}, err: true},
		{name: "user preserved", state: metadataSQLState{
			querySteps: []metadataSQLQueryStep{{columns: []string{"text", "source_kind"}, rows: [][]driver.Value{{"Custom", "user"}}}},
		}},
		{name: "unchanged", state: metadataSQLState{
			querySteps: []metadataSQLQueryStep{{columns: []string{"text", "source_kind"}, rows: [][]driver.Value{{"Account", "generated"}}}},
		}},
		{name: "update", state: metadataSQLState{
			querySteps: []metadataSQLQueryStep{{columns: []string{"text", "source_kind"}, rows: [][]driver.Value{{"Old", "generated"}}}},
			execSteps:  []metadataSQLExecStep{{rows: 1}},
		}},
		{name: "update error", state: metadataSQLState{
			querySteps: []metadataSQLQueryStep{{columns: []string{"text", "source_kind"}, rows: [][]driver.Value{{"Old", "generated"}}}},
			execSteps:  []metadataSQLExecStep{{err: errMetadataSQL}},
		}, err: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := run(t, &testCase.state, localizedTestText()); (err != nil) != testCase.err {
				t.Fatalf("err=%v", err)
			}
		})
	}
	repository := scriptedMetadataStore(t, &metadataSQLState{
		querySteps: []metadataSQLQueryStep{{columns: []string{"text", "source_kind"}}},
		execSteps:  []metadataSQLExecStep{{err: errMetadataSQL}},
	}, base)
	tx, err := repository.database().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := repository.syncManifestLocalizedTexts(t.Context(), tx, manifestmodel.ManifestSchema{Name: "App"}, "now"); err == nil {
		t.Fatal("expected manifest localized sync failure")
	}
}

func TestLocalizedTextUpsertAndListSQLFailures(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	base := NewMetadataStore(baseDB)
	request := metadatamodel.LocalizedTextUpsertRequest{EntityType: "object", EntityKey: "account", Property: "name", Locale: "en-US", Text: "Account"}
	if _, err := base.UpsertLocalizedText(t.Context(), principalmodel.InstallationWorkspaceID, metadatamodel.LocalizedTextUpsertRequest{}); err == nil {
		t.Fatal("expected required fields error")
	}
	for _, invalid := range []metadatamodel.LocalizedTextUpsertRequest{
		{EntityType: "object"},
		{EntityType: "object", EntityKey: "account"},
		{EntityType: "object", EntityKey: "account", Property: "name"},
		{EntityType: "object", EntityKey: "account", Property: "name", Locale: "en-US"},
	} {
		if _, err := base.UpsertLocalizedText(t.Context(), principalmodel.InstallationWorkspaceID, invalid); err == nil {
			t.Fatalf("expected required field error for %#v", invalid)
		}
	}
	for _, testCase := range []struct {
		name  string
		state metadataSQLState
	}{
		{name: "begin", state: metadataSQLState{beginErr: errMetadataSQL}},
		{name: "read", state: metadataSQLState{querySteps: []metadataSQLQueryStep{{err: errMetadataSQL}}}},
		{name: "insert", state: metadataSQLState{querySteps: []metadataSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}, execSteps: []metadataSQLExecStep{{err: errMetadataSQL}}}},
		{name: "update", state: metadataSQLState{querySteps: []metadataSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}}, execSteps: []metadataSQLExecStep{{err: errMetadataSQL}}}},
		{name: "commit", state: metadataSQLState{querySteps: []metadataSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}, execSteps: []metadataSQLExecStep{{rows: 1}}, commitErr: errMetadataSQL}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := scriptedMetadataStore(t, &testCase.state, base)
			if _, err := repository.UpsertLocalizedText(t.Context(), principalmodel.InstallationWorkspaceID, request); err == nil {
				t.Fatal("expected upsert error")
			}
		})
	}
	columns := []string{"workspace_id", "entity_type", "entity_key", "property", "locale", "text", "source_kind", "source_id", "created_at", "updated_at"}
	for _, step := range []metadataSQLQueryStep{
		{err: errMetadataSQL},
		{columns: []string{"workspace_id"}, rows: [][]driver.Value{{principalmodel.InstallationWorkspaceID}}},
		{columns: columns, nextErr: errMetadataSQL},
	} {
		repository := scriptedMetadataStore(t, &metadataSQLState{querySteps: []metadataSQLQueryStep{step}}, base)
		if _, err := repository.ListLocalizedTexts(t.Context(), principalmodel.InstallationWorkspaceID, metadatamodel.LocalizedTextQuery{EntityType: "object", EntityKey: "account", Property: "name", Locale: "en-US"}); err == nil {
			t.Fatal("expected list error")
		}
	}
}
