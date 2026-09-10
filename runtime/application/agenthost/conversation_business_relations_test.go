package agenthost

import (
	"encoding/json"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func relationTestPrincipal(a agentsdk.ConversationAuthority, denied string) principalmodel.Principal {
	permissions := []string{"customer.read", "order.read", "project.read"}
	var policies []accessfixture.FieldPolicyFixture
	for _, object := range []string{"customer", "order", "project"} {
		for _, field := range []string{"name", "amount", "customer", "sponsor", "project"} {
			policies = append(policies, accessfixture.FieldPolicyFixture{ObjectKey: object, FieldKey: field, Read: object+"."+field != denied})
		}
	}
	policies = append(policies, accessfixture.FieldPolicyFixture{ObjectKey: "order", FieldKey: "masked_ref", Read: true, Masked: true})
	policies = append(policies, accessfixture.FieldPolicyFixture{ObjectKey: "order", FieldKey: "secret", Read: false})
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: a.UserID, WorkspaceID: a.WorkspaceID}}, accessfixture.Bundle{
		Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeOwner), FieldPolicies: policies,
	})
}

func newBusinessRelationFixture(t *testing.T) (*ConversationBusinessHost, *businessPrincipalResolver, *businessReadProbe, agentsdk.ConversationAuthority) {
	ref := func(key, target string) definitionmodel.FieldSchema {
		return definitionmodel.FieldSchema{Key: key, Name: key, Type: "relation", Config: map[string]any{"object_key": target}}
	}
	order := definitionmodel.ObjectSchema{Key: "order", Name: "订单", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "amount", Type: "integer"}, ref("customer", "customer"), ref("sponsor", "customer"), ref("project", "project"), ref("secret", "customer"), ref("masked_ref", "customer")}}
	project := definitionmodel.ObjectSchema{Key: "project", Name: "项目", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, ref("customer", "customer")}}
	h, resolver, reads, a := newConversationBusinessFixture(t, order, project)
	resolver.principal = relationTestPrincipal(a, "")
	for _, row := range []struct {
		object               definitionmodel.ObjectSchema
		id, owner, workspace string
		data                 map[string]any
	}{
		{project, "p1", a.UserID, a.WorkspaceID, map[string]any{"name": "交付项目", "customer": "own-1"}},
		{order, "o1", a.UserID, a.WorkspaceID, map[string]any{"name": "订单甲", "amount": int64(10), "customer": "own-1", "sponsor": "own-2", "project": "p1", "secret": "PRIVATE-RELATION"}},
		{order, "o2", a.UserID, a.WorkspaceID, map[string]any{"name": "订单乙", "amount": int64(20), "customer": "own-1", "sponsor": "own-2"}},
		{order, "o3", a.UserID, a.WorkspaceID, map[string]any{"name": "其他客户订单", "amount": int64(30), "customer": "own-2"}},
		{order, "target-denied", a.UserID, a.WorkspaceID, map[string]any{"name": "不可读客户的订单", "customer": "other-owner"}},
		{order, "unset", a.UserID, a.WorkspaceID, map[string]any{"name": "未关联客户"}},
		{order, "hidden", "colleague", a.WorkspaceID, map[string]any{"name": "PRIVATE-OWNER", "customer": "own-1"}},
		{order, "hidden-workspace", a.UserID, "other-workspace", map[string]any{"name": "PRIVATE-WORKSPACE", "customer": "own-1"}},
	} {
		if err := reads.repository.InsertRecord(t.Context(), row.workspace, row.object, recordmodel.Record{ID: row.id, OwnerUserID: row.owner, CreatedAt: "2026-09-10T01:00:00Z", UpdatedAt: "2026-09-10T01:00:00Z", Data: row.data}); err != nil {
			t.Fatal(err)
		}
	}
	return h, resolver, reads, a
}

func TestConversationBusinessRelationsDiscoverBothDirectionsAndReadScopedPages(t *testing.T) {
	h, _, _, a := newBusinessRelationFixture(t)
	q := agentsdk.ConversationBusinessCatalogQuery{Kind: "relations", ObjectKey: "customer", Limit: 1}
	var relations []agentsdk.ConversationBusinessRelation
	for {
		page, err := h.BusinessCatalog(t.Context(), q, a)
		if err != nil || len(page.Items) != 0 || len(page.Relations) != 1 {
			t.Fatal(page, err)
		}
		relations = append(relations, page.Relations...)
		if page.Complete {
			break
		}
		q.After = page.NextCursor
	}
	if len(relations) != 3 || relations[0].Key != "reverse:order:customer" || relations[1].Key != "reverse:order:sponsor" || relations[2].Key != "reverse:project:customer" {
		t.Fatal(relations)
	}
	related := agentsdk.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: "own-1", RelationKey: relations[0].Key, PageSize: 1, Sort: []agentsdk.ConversationBusinessSort{{Field: "amount", Direction: "asc"}}, Fields: []string{"name", "amount"}}
	first, err := h.QueryRelatedBusinessRecords(t.Context(), related, a)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "o1" || !first.HasNext || first.NextCursor == "" || first.Total == nil || *first.Total != 2 {
		t.Fatal(first, err)
	}
	related.Cursor = first.NextCursor
	last, err := h.QueryRelatedBusinessRecords(t.Context(), related, a)
	if err != nil || last.Page != 2 || len(last.Items) != 1 || last.Items[0].ID != "o2" || last.HasNext || last.NextCursor != "" || last.Total != nil {
		t.Fatal(last, err)
	}
	for _, item := range []agentsdk.ConversationBusinessRelatedPage{first, last} {
		raw, _ := json.Marshal(item)
		if strings.Contains(string(raw), "PRIVATE-") || len(item.Items[0].Data) != 2 {
			t.Fatal(string(raw))
		}
	}
	for _, link := range []struct{ relation, target string }{{"forward:customer", "own-1"}, {"forward:sponsor", "own-2"}, {"forward:project", "p1"}} {
		page, err := h.QueryRelatedBusinessRecords(t.Context(), agentsdk.ConversationBusinessRelatedQuery{ObjectKey: "order", RecordID: "o1", RelationKey: link.relation, Fields: []string{"name"}}, a)
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != link.target || page.HasNext {
			t.Fatal(page, err)
		}
	}
	for _, id := range []string{"unset", "target-denied"} {
		page, err := h.QueryRelatedBusinessRecords(t.Context(), agentsdk.ConversationBusinessRelatedQuery{ObjectKey: "order", RecordID: id, RelationKey: "forward:customer"}, a)
		if err != nil || len(page.Items) != 0 || page.HasNext {
			t.Fatal(page, err)
		}
	}
}

func TestConversationBusinessRelationsRejectUnauthorizedOriginsAndCursorReuse(t *testing.T) {
	h, resolver, reads, a := newBusinessRelationFixture(t)
	q := agentsdk.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: "own-1", RelationKey: "reverse:order:customer", PageSize: 1, Fields: []string{"name"}}
	first, err := h.QueryRelatedBusinessRecords(t.Context(), q, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*agentsdk.ConversationBusinessRelatedQuery){
		func(q *agentsdk.ConversationBusinessRelatedQuery) { q.RecordID = "own-2" },
		func(q *agentsdk.ConversationBusinessRelatedQuery) { q.RelationKey = "reverse:order:sponsor" },
		func(q *agentsdk.ConversationBusinessRelatedQuery) { q.Fields = []string{"amount"} },
	} {
		bad := q
		bad.Cursor = first.NextCursor
		change(&bad)
		if _, err := h.QueryRelatedBusinessRecords(t.Context(), bad, a); err == nil {
			t.Fatal("cursor escaped original relationship", bad)
		}
	}
	for _, change := range []func(*agentsdk.ConversationBusinessRelatedQuery){
		func(q *agentsdk.ConversationBusinessRelatedQuery) { q.RecordID = "other-owner" },
		func(q *agentsdk.ConversationBusinessRelatedQuery) { q.RecordID = "other-workspace" },
		func(q *agentsdk.ConversationBusinessRelatedQuery) { q.RelationKey = "reverse:order:secret" },
		func(q *agentsdk.ConversationBusinessRelatedQuery) { q.RelationKey = "reverse:order:masked_ref" },
		func(q *agentsdk.ConversationBusinessRelatedQuery) { q.RelationKey = "invented.path.customer" },
		func(q *agentsdk.ConversationBusinessRelatedQuery) { q.PageSize = 26 },
		func(q *agentsdk.ConversationBusinessRelatedQuery) { q.Fields = []string{"secret"} },
	} {
		bad := q
		change(&bad)
		count := reads.queries
		if _, err := h.QueryRelatedBusinessRecords(t.Context(), bad, a); err == nil || reads.queries != count {
			t.Fatal("invalid relationship reached target query", bad, err)
		}
	}
	input, _ := json.Marshal(q)
	data, _ := json.Marshal(first)
	e := agentsdk.ConversationBusinessEvidence{Version: 1, Source: h.source, ScopeSHA256: h.cursorScope(a), Operation: "query_related_records", Input: input, Data: data}
	if err := h.RevalidateBusiness(t.Context(), e, a); err != nil {
		t.Fatal(err)
	}
	for _, denied := range []string{"order.customer", "order.name"} {
		resolver.principal = relationTestPrincipal(a, denied)
		if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
			t.Fatal("revoked field remained in historical relation result", denied)
		}
	}
	resolver.principal = relationTestPrincipal(a, "")
	if err := h.RevalidateBusiness(t.Context(), e, a); err != nil {
		t.Fatal(err)
	}
	resolver.revoke()
	if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("revoked source principal reused")
	}
}

func TestConversationBusinessRelationCatalogBoundsEmbeddedDiscovery(t *testing.T) {
	var fields []definitionmodel.FieldSchema
	for _, key := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"} {
		fields = append(fields, definitionmodel.FieldSchema{Key: key, Type: "relation", Config: map[string]any{"object_key": "customer"}})
	}
	h, resolver, _, a := newConversationBusinessFixture(t, definitionmodel.ObjectSchema{Key: "many", Fields: fields})
	permissions := []string{"customer.read", "many.read"}
	resolver.principal = accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: a.UserID, WorkspaceID: a.WorkspaceID}}, accessfixture.Bundle{Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll)})
	first, err := h.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{ObjectKey: "customer"}, a)
	if err != nil || len(first.Items) != 1 || len(first.Items[0].Relations) != 10 || first.Items[0].RelationsNextCursor == "" {
		t.Fatal(first, err)
	}
	last, err := h.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "relations", ObjectKey: "customer", After: first.Items[0].RelationsNextCursor, Limit: 25}, a)
	if err != nil || !last.Complete || len(last.Relations) != 3 || last.NextCursor != "" || last.Relations[0].Key != "reverse:many:k" {
		t.Fatal(last, err)
	}
	if _, err := h.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "relations"}, a); err == nil {
		t.Fatal("unbounded relation catalog without source accepted")
	}
}
