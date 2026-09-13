package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	ormschema "github.com/domainry/domainry-orm/schema"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type businessPrincipalResolver struct {
	mu        sync.Mutex
	principal principalmodel.Principal
	request   identitysdk.PrincipalResolutionRequest
	workspace string
	checks    int
}

func (r *businessPrincipalResolver) Resolve(ctx context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks++
	r.request = request
	r.workspace = requestcontext.WorkspaceID(ctx)
	return agentPrincipalResolution(r.principal), nil
}
func (r *businessPrincipalResolver) revoke() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.principal.Known = false
}

type businessSchemaSnapshot struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (s businessSchemaSnapshot) SchemaForPrincipal(_ context.Context, p principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return appschemaservice.SnapshotForPrincipal(s.snapshot, p)
}

type businessReadProbe struct {
	*recordapplication.RecordApplicationService
	queries    int
	last       recordmodel.RecordListQuery
	lastError  error
	repository recordstore.RecordStore
	object     definitionmodel.ObjectSchema
}

func (r *businessReadProbe) ListRecords(ctx context.Context, key string, query recordmodel.RecordListQuery, p principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	r.queries++
	r.last = query
	page, err := r.RecordApplicationService.ListRecords(ctx, key, query, p)
	r.lastError = err
	return page, err
}

func newConversationBusinessFixture(t *testing.T, extraObjects ...definitionmodel.ObjectSchema) (*ConversationBusinessHost, *businessPrincipalResolver, *businessReadProbe, agentsdk.ConversationAuthority) {
	t.Helper()
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "business-runtime", WorkspaceID: "business-workspace", UserID: "operator", RoleKey: "staff"}
	p := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: a.WorkspaceID, UserID: a.UserID}}, accessfixture.Bundle{
		Key: "staff", Permissions: []string{"customer.read", agentsdk.ConversationToolActionPrefix + "business_catalog", agentsdk.ConversationToolActionPrefix + "query_records", agentsdk.ConversationToolActionPrefix + "get_record"},
		DataPolicies: []accessfixture.DataPolicyFixture{
			{ObjectKey: "customer", Action: "read", Scope: identitysdk.DataScopeOwner, Read: true},
			{ObjectKey: "agent.conversation_tools", Action: "business_catalog", Scope: identitysdk.DataScopeAll, Read: true},
			{ObjectKey: "agent.conversation_tools", Action: "query_records", Scope: identitysdk.DataScopeAll, Read: true},
			{ObjectKey: "agent.conversation_tools", Action: "get_record", Scope: identitysdk.DataScopeAll, Read: true},
		},
		FieldPolicies: []accessfixture.FieldPolicyFixture{
			{ObjectKey: "customer", FieldKey: "name", Read: true},
			{ObjectKey: "customer", FieldKey: "amount", Read: true},
			{ObjectKey: "customer", FieldKey: "account", Read: true, Masked: true},
			{ObjectKey: "customer", FieldKey: "secret", Read: false},
		},
	})
	resolver := &businessPrincipalResolver{principal: p}
	object := definitionmodel.ObjectSchema{Key: "customer", Name: "客户", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Name: "名称", Type: "text"}, {Key: "amount", Name: "数量", Type: "integer"}, {Key: "account", Name: "账户", Type: "text"}, {Key: "secret", Name: "内部秘密", Type: "text"},
	}}
	objects := append([]definitionmodel.ObjectSchema{object, {Key: "private_object", Name: "不可见对象"}}, extraObjects...)
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "business.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureApplicationSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		columns := []ormschema.ColumnDefinition{}
		for _, key := range []string{"workspace_id", "id", "created_at", "updated_at", "owner_user_id", "owner_org_id"} {
			columns = append(columns, ormschema.Column(key, ormschema.TextKey(96)))
		}
		columns = append(columns, ormschema.Column("deleted", ormschema.Boolean()).DefaultValue(false))
		for _, field := range object.Fields {
			typeOf := ormschema.LongText()
			if field.Type == "integer" {
				typeOf = ormschema.BigInt()
			}
			columns = append(columns, ormschema.Column(field.Key, typeOf))
		}
		statement, _, err := ormschema.NewTable(store.SQLRenderer, object.Key).Columns(columns...).PrimaryKey("workspace_id", "id").Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	repository := recordstore.NewRecordStore(store)
	for _, row := range []struct{ id, name, owner, workspace string }{
		{"own-1", "Alpha", a.UserID, a.WorkspaceID}, {"own-2", "Beta", a.UserID, a.WorkspaceID}, {"other-owner", "Hidden", "colleague", a.WorkspaceID}, {"other-workspace", "Hidden workspace", a.UserID, "other-workspace"},
	} {
		err := repository.InsertRecord(t.Context(), row.workspace, object, recordmodel.Record{ID: row.id, OwnerUserID: row.owner, CreatedAt: "2026-09-10T01:00:00Z", UpdatedAt: "2026-09-10T01:00:00Z", Data: map[string]any{"name": row.name, "amount": int64(9007199254740993), "account": "PRIVATE-ACCOUNT-6789", "secret": "PRIVATE-SECRET"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return objects }})
	reads := &businessReadProbe{RecordApplicationService: recordapplication.NewRecordApplicationService(recordapplication.RecordApplicationDependencies{Repository: repository, QueryPolicy: policy, SchemaMap: func() map[string]definitionmodel.ObjectSchema {
		out := map[string]definitionmodel.ObjectSchema{}
		for _, object := range objects {
			out[object.Key] = object
		}
		return out
	}}), repository: repository, object: object}
	schema := appschemaapplication.NewApplicationSchemaQueryApplicationService(businessSchemaSnapshot{appschemamodel.ApplicationSchemaSnapshot{Objects: objects}}, nil)
	host, err := NewConversationBusinessHost(a.RuntimeID, identitysdk.ApplicationScope{WorkspaceID: identitysdk.WorkspaceID(a.WorkspaceID), ApplicationKey: "domainry-runtime"}, resolver, schema, reads)
	if err != nil {
		t.Fatal(err)
	}
	return host, resolver, reads, a
}

func TestConversationBusinessRuntimeReadsApplyScopesMaskingAndExactIntegers(t *testing.T) {
	host, resolver, reads, a := newConversationBusinessFixture(t)
	catalog, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{}, a)
	if err != nil || len(catalog.Items) != 1 || catalog.Items[0].Key != "customer" || len(catalog.Items[0].Fields) != 0 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	detail, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{ObjectKey: "customer"}, a)
	if err != nil || len(detail.Items) != 1 {
		t.Fatal(detail, err)
	}
	for _, field := range detail.Items[0].Fields {
		if field.Key == "secret" {
			t.Fatal("hidden field metadata disclosed")
		}
		if field.Key == "account" && (field.Sortable || len(field.FilterOperators) > 0) {
			t.Fatal("masked field advertised as queryable")
		}
		if field.Key == "amount" && !slices.Contains(field.FilterOperators, "gte") {
			t.Fatal("integer comparison missing")
		}
	}
	q := agentsdk.ConversationBusinessQuery{ObjectKey: "customer", PageSize: 1, Sort: []agentsdk.ConversationBusinessSort{{Field: "name", Direction: "asc"}}, Filters: []agentsdk.ConversationBusinessFilter{{Field: "amount", Operator: "eq", Value: json.RawMessage(`9007199254740993`)}}}
	first, err := host.QueryBusinessRecords(t.Context(), q, a)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "own-1" || first.Total == nil || *first.Total != 2 || first.NextCursor == "" || !first.HasNext {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	q.Cursor = first.NextCursor
	page, err := host.QueryBusinessRecords(t.Context(), q, a)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "own-2" || page.Page != 2 || page.Total != nil || page.HasNext || page.NextCursor != "" {
		t.Fatalf("page=%+v err=%v record error=%v cause=%v", page, err, reads.lastError, errors.Unwrap(reads.lastError))
	}
	if string(page.Items[0].Data["amount"]) != "9007199254740993" {
		t.Fatal("integer precision changed", string(page.Items[0].Data["amount"]))
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "PRIVATE-") || strings.Contains(string(raw), "secret") {
		t.Fatal("unreadable or unmasked values returned")
	}
	for _, id := range []string{"other-owner", "other-workspace"} {
		if _, err := host.GetBusinessRecord(t.Context(), agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: id}, a); err == nil {
			t.Fatal("unauthorized record returned", id)
		}
	}
	for _, change := range []func(*agentsdk.ConversationAuthority){func(v *agentsdk.ConversationAuthority) { v.WorkspaceID = "other" }, func(v *agentsdk.ConversationAuthority) { v.RuntimeID = "other" }, func(v *agentsdk.ConversationAuthority) { v.UserID = "other" }} {
		bad := a
		change(&bad)
		if _, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{}, bad); err == nil {
			t.Fatal("principal boundary ignored")
		}
	}
	if resolver.workspace != string(host.application.WorkspaceID) {
		t.Fatal("identity application scope omitted")
	}
}

func TestConversationBusinessRejectsInvalidQueriesBeforeRuntimeRead(t *testing.T) {
	host, _, reads, a := newConversationBusinessFixture(t)
	for _, q := range []agentsdk.ConversationBusinessQuery{
		{ObjectKey: "customer", Fields: []string{"secret"}},
		{ObjectKey: "customer", Sort: []agentsdk.ConversationBusinessSort{{Field: "account", Direction: "asc"}}},
		{ObjectKey: "customer", Sort: []agentsdk.ConversationBusinessSort{{Field: "guess", Direction: "asc"}}},
		{ObjectKey: "customer", Filters: []agentsdk.ConversationBusinessFilter{{Field: "account", Operator: "eq", Value: json.RawMessage(`"guess"`)}}},
		{ObjectKey: "customer", Filters: []agentsdk.ConversationBusinessFilter{{Field: "amount", Operator: "eq", Value: json.RawMessage(`"invalid"`)}}},
		{ObjectKey: "customer", Filters: []agentsdk.ConversationBusinessFilter{{Field: "name", Operator: "in", Value: json.RawMessage(`[]`)}}},
		{ObjectKey: "customer", PageSize: 26},
		{ObjectKey: "customer", Page: 2},
	} {
		if _, err := host.QueryBusinessRecords(t.Context(), q, a); err == nil {
			t.Fatalf("invalid query accepted: %+v", q)
		}
	}
	if reads.queries != 0 {
		t.Fatal("invalid query reached the record service", reads.queries)
	}
	result, err := host.GetBusinessRecord(t.Context(), agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: []string{"name"}}, a)
	if err != nil || len(result.Data) != 1 || string(result.Data["name"]) != `"Alpha"` {
		t.Fatal(result, err)
	}
}

func TestConversationBusinessCursorTraversesNullsTiesAndSelectedFields(t *testing.T) {
	host, _, reads, a := newConversationBusinessFixture(t)
	for _, item := range []struct {
		id   string
		name any
	}{{"own-3", "Alpha"}, {"own-4", nil}, {"own-5", nil}, {"own-6", ""}} {
		if err := reads.repository.InsertRecord(t.Context(), a.WorkspaceID, reads.object, recordmodel.Record{ID: item.id, OwnerUserID: a.UserID, CreatedAt: "2026-09-10T01:00:00Z", UpdatedAt: "2026-09-10T01:00:00Z", Data: map[string]any{"name": item.name, "amount": int64(9007199254740993)}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		sort []agentsdk.ConversationBusinessSort
		want []string
	}{
		{[]agentsdk.ConversationBusinessSort{{Field: "name", Direction: "asc"}}, []string{"own-6", "own-1", "own-3", "own-2", "own-4", "own-5"}},
		{[]agentsdk.ConversationBusinessSort{{Field: "name", Direction: "desc"}}, []string{"own-2", "own-1", "own-3", "own-6", "own-4", "own-5"}},
		{nil, []string{"own-1", "own-2", "own-3", "own-4", "own-5", "own-6"}},
		{[]agentsdk.ConversationBusinessSort{{Field: "amount", Direction: "desc"}}, []string{"own-1", "own-2", "own-3", "own-4", "own-5", "own-6"}},
	} {
		q := agentsdk.ConversationBusinessQuery{ObjectKey: "customer", PageSize: 1, Fields: []string{"amount"}, Sort: tc.sort}
		got := []string{}
		for number := 1; number <= 7; number++ {
			page, err := host.QueryBusinessRecords(t.Context(), q, a)
			if err != nil || len(page.Items) != 1 || page.Page != number {
				t.Fatalf("cursor page=%+v error=%v", page, err)
			}
			if len(page.Items[0].Data) != 1 {
				t.Fatal("cursor sort projection escaped requested fields")
			}
			got = append(got, page.Items[0].ID)
			if !page.HasNext {
				break
			}
			q.Cursor = page.NextCursor
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("sort=%+v got=%v want=%v", tc.sort, got, tc.want)
		}
		bad := q
		bad.Fields = []string{"name"}
		if _, err := host.QueryBusinessRecords(t.Context(), bad, a); err == nil {
			t.Fatal("cursor reused with a different field projection")
		}
	}
}

func TestConversationBusinessRevalidatesCurrentIdentityAndWholeEvidence(t *testing.T) {
	host, resolver, _, a := newConversationBusinessFixture(t)
	q := agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: []string{"name"}}
	record, err := host.GetBusinessRecord(t.Context(), q, a)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(q)
	data, _ := json.Marshal(record)
	e := agentsdk.ConversationBusinessEvidence{Version: 1, Source: host.BusinessSourceIdentity(), ScopeSHA256: conversationBusinessDigest([]string{host.source, a.RuntimeID, a.WorkspaceID, a.UserID}), Operation: "get_record", Input: input, Data: data}
	if err := host.RevalidateBusiness(t.Context(), e, a); err != nil {
		t.Fatal(err)
	}
	bad := e
	bad.Data = json.RawMessage(`{"id":"own-1","data":{"name":"fabricated"}}`)
	if err := host.RevalidateBusiness(t.Context(), bad, a); err == nil {
		t.Fatal("changed saved content accepted")
	}
	definition := agentsdk.BusinessConversationTools()[0]
	auth, err := host.AuthorizeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: a, Definition: definition})
	if err != nil || !auth.Granted {
		t.Fatal(auth, err)
	}
	resolver.revoke()
	if err := host.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("revoked principal reused saved result")
	}
	auth, err = host.AuthorizeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: a, Definition: definition})
	if err == nil && auth.Granted {
		t.Fatal("revoked tool grant retained")
	}
}
