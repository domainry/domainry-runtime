package businessseed

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

var errBusinessSeedProbe = errors.New("business seed probe failed")

type businessSeedRecordProbe struct {
	totals      map[string]int
	listErr     map[string]error
	insertErr   map[string]error
	listCalls   []string
	inserted    []recordmodel.Record
	insertOrder []string
	workspaces  []string
	queries     []recordmodel.RecordListQuery
}

func (p *businessSeedRecordProbe) ListRecords(_ context.Context, workspace string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	p.listCalls = append(p.listCalls, object.Key)
	p.workspaces = append(p.workspaces, workspace)
	p.queries = append(p.queries, query)
	if err := p.listErr[object.Key]; err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	return recordmodel.RecordPageResult{Total: p.totals[object.Key]}, nil
}

func (p *businessSeedRecordProbe) InsertRecord(_ context.Context, workspace string, object definitionmodel.ObjectSchema, record recordmodel.Record) error {
	if err := p.insertErr[object.Key]; err != nil {
		return err
	}
	p.workspaces = append(p.workspaces, workspace)
	p.insertOrder = append(p.insertOrder, object.Key)
	p.inserted = append(p.inserted, record)
	return nil
}

func businessSeedManifest() manifestmodel.ManifestSchema {
	return manifestmodel.ManifestSchema{
		TemplateID: "template-a", Version: "1.2.3",
		Objects: []definitionmodel.ObjectSchema{
			{Key: "parent", Fields: []definitionmodel.FieldSchema{{Key: "name"}, {Key: "run"}}},
			{Key: "child", Fields: []definitionmodel.FieldSchema{{Key: "parent_id"}, {Key: "nested"}, {Key: "name"}}},
			{Key: " "},
		},
	}
}

func TestSyncManifestBusinessSeedsNoopTargetAndLookupFailures(t *testing.T) {
	manifest := businessSeedManifest()
	row := manifestBusinessSeedRow{Key: "parent-one", ObjectKey: "parent", DataJSON: `{"name":"Parent"}`}
	if err := SyncManifestBusinessSeeds(t.Context(), nil, manifest, []manifestBusinessSeedRow{row}); err != nil {
		t.Fatalf("nil records err=%v", err)
	}
	if err := SyncManifestBusinessSeeds(t.Context(), &businessSeedRecordProbe{}, manifest, nil); err != nil {
		t.Fatalf("empty rows err=%v", err)
	}

	records := &businessSeedRecordProbe{totals: map[string]int{"parent": 1}}
	if err := SyncManifestBusinessSeeds(t.Context(), records, manifest, []manifestBusinessSeedRow{row}); err != nil || len(records.inserted) != 0 {
		t.Fatalf("nonempty inserted=%v err=%v", records.inserted, err)
	}
	records = &businessSeedRecordProbe{listErr: map[string]error{"parent": errBusinessSeedProbe}}
	if err := SyncManifestBusinessSeeds(t.Context(), records, manifest, []manifestBusinessSeedRow{row}); !errors.Is(err, errBusinessSeedProbe) || !strings.Contains(err.Error(), "check domain seed target parent") {
		t.Fatalf("list err=%v", err)
	}
}

func TestSyncManifestBusinessSeedsOrdersResolvesFiltersAndPersistsEvidence(t *testing.T) {
	t.Setenv("SEED_RUN_ID", " run-42 ")
	manifest := businessSeedManifest()
	rows := []manifestBusinessSeedRow{
		{Key: "child-one", ObjectKey: "child", DataJSON: `{"parent_id":"$record:parent-one","nested":{"refs":["$record:parent-one","literal"]},"name":"Child ${RUN_ID}","unknown":"drop","empty":""}`, SourceKind: " plugin ", SourceID: " source-a "},
		{Key: "parent-one", ObjectKey: "parent", DataJSON: `{"name":"Parent","run":"${RUN_ID}","__seed_key":"drop"}`, OwnerUserID: " owner-user ", OwnerOrgID: " owner-org "},
		{Key: "ignored", ObjectKey: "missing", DataJSON: `{"name":"ignored"}`},
		{Key: "", ObjectKey: "parent", DataJSON: `{"name":"Fallback"}`},
	}
	records := &businessSeedRecordProbe{}
	if err := SyncManifestBusinessSeeds(t.Context(), records, manifest, rows); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records.insertOrder, []string{"parent", "child", "parent"}) {
		t.Fatalf("insert order=%v", records.insertOrder)
	}
	if len(records.inserted) != 3 || records.inserted[0].ID != "parent_parent_one" || records.inserted[1].ID != "child_child_one" || records.inserted[2].ID != "parent" {
		t.Fatalf("inserted=%#v", records.inserted)
	}
	if records.inserted[0].OwnerUserID != "owner-user" || records.inserted[0].OwnerOrgID != "owner-org" {
		t.Fatalf("seed ownership=%#v", records.inserted[0])
	}
	child := records.inserted[1]
	if child.Data["parent_id"] != "parent_parent_one" || child.Data["name"] != "Child run-42" {
		t.Fatalf("child=%#v", child)
	}
	if _, found := child.Data["unknown"]; found {
		t.Fatalf("unknown field retained: %#v", child.Data)
	}
	nested := child.Data["nested"].(map[string]any)
	if !reflect.DeepEqual(nested["refs"], []any{"parent_parent_one", "literal"}) {
		t.Fatalf("nested refs=%#v", nested)
	}
	for _, workspace := range records.workspaces {
		if workspace != principalmodel.InstallationWorkspaceID {
			t.Fatalf("workspace=%q", workspace)
		}
	}
	if len(records.queries) != 2 || records.queries[0].Page != 1 || records.queries[0].PageSize != 1 {
		t.Fatalf("queries=%#v calls=%v", records.queries, records.listCalls)
	}
}

func TestSyncManifestBusinessSeedsAcceptsEmptyData(t *testing.T) {
	records := &businessSeedRecordProbe{}
	err := SyncManifestBusinessSeeds(t.Context(), records, businessSeedManifest(), []manifestBusinessSeedRow{{Key: "empty", ObjectKey: "parent"}})
	if err != nil || len(records.inserted) != 1 || len(records.inserted[0].Data) != 0 {
		t.Fatalf("inserted=%#v err=%v", records.inserted, err)
	}
}

func TestSyncManifestBusinessSeedsUsesContextWorkspace(t *testing.T) {
	records := &businessSeedRecordProbe{}
	ctx := requestcontext.WithWorkspaceID(t.Context(), "acceptance-tenant-b")
	err := SyncManifestBusinessSeeds(ctx, records, businessSeedManifest(), []manifestBusinessSeedRow{{Key: "tenant-parent", ObjectKey: "parent", DataJSON: `{"name":"Tenant Parent"}`}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records.workspaces) == 0 {
		t.Fatal("business seed did not access the record repository")
	}
	for _, workspace := range records.workspaces {
		if workspace != "acceptance-tenant-b" {
			t.Fatalf("workspace=%q calls=%#v", workspace, records.workspaces)
		}
	}
}

func TestSyncManifestBusinessSeedsOrderingDecodeAndPersistenceFailures(t *testing.T) {
	manifest := businessSeedManifest()
	for _, test := range []struct {
		name string
		rows []manifestBusinessSeedRow
		want string
	}{
		{"duplicate", []manifestBusinessSeedRow{{Key: "a", ObjectKey: "parent"}, {Key: "a", ObjectKey: "parent"}}, "duplicate domain seed key"},
		{"missing-ref", []manifestBusinessSeedRow{{Key: "a", ObjectKey: "parent", DataJSON: `{"name":"$record:missing"}`}}, "domain seed reference not found"},
		{"nested-missing-ref", []manifestBusinessSeedRow{{Key: "a", ObjectKey: "parent", DataJSON: `{"name":"$record:b"}`}, {Key: "b", ObjectKey: "parent", DataJSON: `{"name":"$record:missing"}`}}, "domain seed reference not found"},
		{"decode", []manifestBusinessSeedRow{{Key: "a", ObjectKey: "parent", DataJSON: `{`}}, "decode domain seed parent/a"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := SyncManifestBusinessSeeds(t.Context(), &businessSeedRecordProbe{}, manifest, test.rows)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	row := manifestBusinessSeedRow{Key: "a", ObjectKey: "parent", DataJSON: `{"name":"A"}`}
	records := &businessSeedRecordProbe{insertErr: map[string]error{"parent": errBusinessSeedProbe}}
	if err := SyncManifestBusinessSeeds(t.Context(), records, manifest, []manifestBusinessSeedRow{row}); !errors.Is(err, errBusinessSeedProbe) || !strings.Contains(err.Error(), "sync domain seed parent/a") {
		t.Fatalf("insert err=%v", err)
	}
}

func TestSyncManifestBusinessSeedsResolvesCyclicReferencesWithDeterministicIDs(t *testing.T) {
	rows := []manifestBusinessSeedRow{
		{Key: "a", ObjectKey: "parent", DataJSON: `{"name":"$record:b"}`},
		{Key: "b", ObjectKey: "parent", DataJSON: `{"name":"$record:a"}`},
	}
	records := &businessSeedRecordProbe{}
	if err := SyncManifestBusinessSeeds(t.Context(), records, businessSeedManifest(), rows); err != nil {
		t.Fatal(err)
	}
	if len(records.inserted) != 2 {
		t.Fatalf("inserted=%#v", records.inserted)
	}
	byID := map[string]recordmodel.Record{}
	for _, record := range records.inserted {
		byID[record.ID] = record
	}
	if byID["parent_a"].Data["name"] != "parent_b" || byID["parent_b"].Data["name"] != "parent_a" {
		t.Fatalf("cyclic references were not resolved: %#v", byID)
	}
}

func TestManifestBusinessSeedRowsFromManifestNormalizesSkipsAndCopies(t *testing.T) {
	manifest := businessSeedManifest()
	manifest.SeedRecords = []businessseedmodel.SeedRecordSchema{
		{ObjectKey: " ", Data: map[string]any{"name": "ignored"}},
		{ObjectKey: "parent", Data: map[string]any{"name": "generated"}},
		{ObjectKey: "parent", Data: map[string]any{"__seed_key": "", "name": "blank-key"}},
		{ObjectKey: "parent", Data: map[string]any{"__seed_key": nil, "name": "nil-key"}},
		{ObjectKey: "parent", OwnerUserID: " owner-user ", OwnerOrgID: " owner-org ", SourceKind: " plugin ", SourceID: " source ", Data: map[string]any{"__seed_key": "explicit", "name": "kept"}},
		{ObjectKey: "child", Data: map[string]any{"__seed_key": "explicit", "name": "duplicate"}},
		{ObjectKey: "child", Data: map[string]any{"__seed_key": "unsupported", "value": make(chan int)}},
	}
	rows := ManifestBusinessSeedRowsFromManifest(manifest)
	if len(rows) != 4 || rows[0].Key != "parent_seed_002" || rows[1].Key != "parent_seed_003" || rows[2].Key != "parent_seed_004" || rows[3].Key != "explicit" {
		t.Fatalf("rows=%#v", rows)
	}
	if rows[0].SourceKind != "template" || rows[0].SourceID != "template-a" || rows[3].SourceKind != "plugin" || rows[3].SourceID != "source" {
		t.Fatalf("sources=%#v", rows)
	}
	if rows[3].OwnerUserID != "owner-user" || rows[3].OwnerOrgID != "owner-org" {
		t.Fatalf("seed ownership=%#v", rows[3])
	}
	manifest.SeedRecords[3].Data["name"] = "mutated"
	if strings.Contains(rows[2].DataJSON, "mutated") {
		t.Fatalf("row aliased manifest data: %s", rows[2].DataJSON)
	}
}

func TestManifestBusinessSeedHelpersCoverNestedAndBoundaryValues(t *testing.T) {
	if valueOrDefault(" value ", "fallback") != "value" || valueOrDefault(" ", "fallback") != "fallback" {
		t.Fatal("value fallback mismatch")
	}
	t.Setenv("SEED_RUN_ID", "")
	if manifestBusinessSeedRunID() == "" {
		t.Fatal("generated run id is empty")
	}
	objects := manifestBusinessSeedObjects(businessSeedManifest())
	if len(objects) != 2 || objects["parent"].Key != "parent" {
		t.Fatalf("objects=%v", objects)
	}
	rows := []manifestBusinessSeedRow{{Key: "a", ObjectKey: "parent"}, {Key: "b", ObjectKey: "parent"}, {Key: "c", ObjectKey: "missing"}}
	records := &businessSeedRecordProbe{}
	if empty, err := manifestBusinessSeedTargetsEmpty(t.Context(), records, principalmodel.InstallationWorkspaceID, objects, rows); err != nil || !empty || !reflect.DeepEqual(records.listCalls, []string{"parent"}) {
		t.Fatalf("empty=%v calls=%v err=%v", empty, records.listCalls, err)
	}
	if ordered, err := orderManifestBusinessSeedRows([]manifestBusinessSeedRow{{ObjectKey: "parent"}}); err != nil || len(ordered) != 1 {
		t.Fatalf("blank ordering=%v err=%v", ordered, err)
	}
	if refs := collectManifestBusinessSeedRefs(`{"items":["$record: b ","plain",1],"nested":{"ref":"$record:a"}}`); !reflect.DeepEqual(refs, []string{"a", "b"}) {
		t.Fatalf("refs=%v", refs)
	}
	for _, input := range []string{"", "{", `"plain"`} {
		if refs := collectManifestBusinessSeedRefs(input); len(refs) != 0 {
			t.Fatalf("input=%q refs=%v", input, refs)
		}
	}
	refs := map[string]bool{}
	collectManifestBusinessSeedRefsFromValue([]any{"$record: ", 7, map[string]any{"ref": "$record:a"}}, refs)
	if !refs["a"] || len(refs) != 1 {
		t.Fatalf("refs=%v", refs)
	}
	if ids := manifestBusinessSeedIDs([]manifestBusinessSeedRow{{Key: " a ", ObjectKey: "parent"}, {Key: " "}}); !reflect.DeepEqual(ids, map[string]string{"a": "parent_a"}) {
		t.Fatalf("ids=%v", ids)
	}
	if manifestBusinessSeedRecordID(" ", " ") != "seed_record" || manifestBusinessSeedRecordID(" order ", "a-b.c/d:e") != "order_a_b_c_d_e" {
		t.Fatal("record id normalization mismatch")
	}
	resolved := resolveManifestBusinessSeedRefs(map[string]any{"direct": "$record:a", "missing": "$record:x", "items": []any{"$record:a", 3}}, map[string]string{"a": "parent_a"}).(map[string]any)
	if resolved["direct"] != "parent_a" || resolved["missing"] != "$record:x" || !reflect.DeepEqual(resolved["items"], []any{"parent_a", 3}) {
		t.Fatalf("resolved=%#v", resolved)
	}
	placeholder := resolveManifestBusinessSeedPlaceholders(map[string]any{"direct": "x-${RUN_ID}", "items": []any{"${RUN_ID}", 3}}, "run").(map[string]any)
	if placeholder["direct"] != "x-run" || !reflect.DeepEqual(placeholder["items"], []any{"run", 3}) || resolveManifestBusinessSeedPlaceholders("${RUN_ID}", "") != "${RUN_ID}" {
		t.Fatalf("placeholder=%#v", placeholder)
	}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "name"}, {Key: "count"}}}
	if filtered := filterManifestBusinessSeedData(object, "not-a-map"); len(filtered) != 0 {
		t.Fatalf("non-map=%v", filtered)
	}
	filtered := filterManifestBusinessSeedData(object, map[string]any{"__seed_key": "a", "name": " value ", "count": 0, "unknown": true})
	if !reflect.DeepEqual(filtered, map[string]any{"name": " value ", "count": 0}) {
		t.Fatalf("filtered=%#v", filtered)
	}
	if filtered := filterManifestBusinessSeedData(object, map[string]any{"name": ""}); len(filtered) != 0 {
		t.Fatalf("empty value retained=%#v", filtered)
	}
}
