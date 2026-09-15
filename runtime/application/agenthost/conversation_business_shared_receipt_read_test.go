package agenthost

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type sharedBusinessPrincipals struct {
	producer, reader agent.ConversationAuthority
	original, actual principalmodel.Principal
}

func (r *sharedBusinessPrincipals) Resolve(_ context.Context, in identity.PrincipalResolutionRequest) (identity.PrincipalResolution, error) {
	if string(in.SubjectID) == r.producer.UserID && in.RoleKey == r.producer.RoleKey && r.original.Known {
		return agentPrincipalResolution(r.original), nil
	}
	if string(in.SubjectID) == r.reader.UserID && in.RoleKey == r.reader.RoleKey && r.actual.Known {
		return agentPrincipalResolution(r.actual), nil
	}
	return identity.PrincipalResolution{}, &identity.Error{StatusCode: 403, Code: "identity.subject_not_found"}
}

func bindSharedBusinessReader(h *ConversationBusinessHost, resolver *businessPrincipalResolver, producer agent.ConversationAuthority) (*sharedBusinessPrincipals, agent.ConversationAuthority) {
	reader := producer
	reader.UserID, reader.RoleKey = "actual-reader", "read-only"
	p := resolver.principal
	p.UserID = reader.UserID
	p.RoleKey = reader.RoleKey
	b := *p.AccessBundle
	b.Subject.SubjectID = identity.SubjectID(reader.UserID)
	b.FunctionGrants = slices.DeleteFunc(slices.Clone(b.FunctionGrants), func(g identity.FunctionGrant) bool { return g.Resource == "agent.conversation_tools" })
	b.DataPolicies = slices.DeleteFunc(slices.Clone(b.DataPolicies), func(g identity.DataPolicy) bool { return g.Resource == "agent.conversation_tools" })
	for i := range b.DataPolicies {
		if b.DataPolicies[i].Action == "read" {
			b.DataPolicies[i].DataScopes = []identity.DataScope{identity.DataScopeAll}
			b.DataPolicies[i].Predicate = identity.Predicate{}
		}
	}
	p.AccessBundle = &b
	r := &sharedBusinessPrincipals{producer: producer, reader: reader, original: resolver.principal, actual: p}
	h.principals = r
	return r, reader
}

func TestSharedBusinessSameUserOriginalRoleUsesCurrentReaderFields(t *testing.T) {
	h, resolver, _, producer := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	get := agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: []string{"name", "amount"}}
	row, err := h.GetBusinessRecord(t.Context(), get, producer)
	if err != nil {
		t.Fatal(err)
	}
	e := sealedBusinessEvidence(t, h, producer, "get_record", get, row)
	principals, reader := bindSharedBusinessReader(h, resolver, producer)
	reader.UserID = producer.UserID
	principals.reader = reader
	principals.actual.UserID = reader.UserID
	b := *principals.actual.AccessBundle
	b.Subject.SubjectID = identity.SubjectID(reader.UserID)
	principals.actual.AccessBundle = &b
	if err := agent.AuthorizeSharedBusinessResultRead(t.Context(), h, e, reader, producer); err != nil {
		t.Fatal("same-user original role was replaced by read role", err)
	}
	b.FieldPolicies = slices.Clone(b.FieldPolicies)
	for i := range b.FieldPolicies {
		if b.FieldPolicies[i].Field == "amount" {
			b.FieldPolicies[i].Read = false
		}
	}
	principals.actual.AccessBundle = &b
	if err := agent.AuthorizeSharedBusinessResultRead(t.Context(), h, e, reader, producer); err == nil {
		t.Fatal("original role overrode current reader field scope")
	}
}

func TestSharedBusinessSignedReadsKeepOriginalValuesCursorAndCurrentReader(t *testing.T) {
	h, resolver, reads, producer := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	q := agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "amount"}, PageSize: 1}
	first, err := h.QueryBusinessRecords(t.Context(), q, producer)
	if err != nil || first.NextCursor == "" || first.Total == nil || *first.Total != 2 {
		t.Fatal(first, err)
	}
	eFirst := sealedBusinessEvidence(t, h, producer, "query_records", q, first)
	q.Cursor = first.NextCursor
	last, err := h.QueryBusinessRecords(t.Context(), q, producer)
	if err != nil || last.Page != 2 {
		t.Fatal(last, err)
	}
	eLast := sealedBusinessEvidence(t, h, producer, "query_records", q, last)
	get := agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: q.Fields}
	row, err := h.GetBusinessRecord(t.Context(), get, producer)
	if err != nil {
		t.Fatal(err)
	}
	eGet := sealedBusinessEvidence(t, h, producer, "get_record", get, row)
	cq := agent.ConversationBusinessCatalogQuery{ObjectKey: "customer"}
	catalog, err := h.BusinessCatalog(t.Context(), cq, producer)
	if err != nil {
		t.Fatal(err)
	}
	eCatalog := businessReadEvidence(t, h, producer, "business_catalog", cq, catalog)
	withoutBusinessToolGrants(resolver)
	principals, reader := bindSharedBusinessReader(h, resolver, producer)
	mutateBusinessEvidenceRow(t, reads, producer, reads.object, "own-1", func(r *recordmodel.Record) { r.Data["name"] = "Changed after signed snapshot" })
	for _, e := range []agent.ConversationBusinessEvidence{eFirst, eLast, eGet, eCatalog} {
		t.Run(e.Operation+fmt.Sprint(len(e.Input)), func(t *testing.T) {
			before := string(e.Data)
			if err := agent.AuthorizeSharedBusinessResultRead(t.Context(), h, e, reader, producer); err != nil || string(e.Data) != before {
				t.Fatal("original result lost or replaced", err)
			}
			for _, bad := range []func(*agent.ConversationBusinessEvidence){
				func(e *agent.ConversationBusinessEvidence) {
					e.Data = json.RawMessage(strings.Replace(string(e.Data), "9007199254740993", "9007199254740992", 1))
				},
				func(e *agent.ConversationBusinessEvidence) { e.ScopeSHA256 = h.cursorScope(reader) },
				func(e *agent.ConversationBusinessEvidence) { e.HostProof += "forged" },
			} {
				copy := e
				bad(&copy)
				if string(copy.Data) == before && copy.ScopeSHA256 == e.ScopeSHA256 && copy.HostProof == e.HostProof {
					continue
				}
				if err := agent.AuthorizeSharedBusinessResultRead(t.Context(), h, copy, reader, producer); err == nil {
					t.Fatal("forged original accepted")
				}
			}
		})
	}
	if _, err := h.QueryBusinessRecords(t.Context(), q, reader); err == nil {
		t.Fatal("shared receipt granted actual use of producer cursor")
	}
	originalReader := principals.actual
	for _, denied := range []string{"read-grant", "record-scope", "field", "masked", "producer-role", "reader-role", "workspace"} {
		t.Run(denied, func(t *testing.T) {
			principals.actual, principals.original = originalReader, resolver.principal
			a, p := reader, producer
			b := *principals.actual.AccessBundle
			switch denied {
			case "read-grant":
				b.FunctionGrants = nil
			case "record-scope":
				b.DataPolicies = nil
			case "field", "masked":
				b.FieldPolicies = slices.Clone(b.FieldPolicies)
				for i := range b.FieldPolicies {
					if b.FieldPolicies[i].Field == "amount" {
						b.FieldPolicies[i].Read, b.FieldPolicies[i].Masked = denied != "field", denied == "masked"
					}
				}
			case "producer-role":
				p.RoleKey = "unbound"
			case "reader-role":
				a.RoleKey = "unbound"
			case "workspace":
				a.WorkspaceID = "other"
			}
			principals.actual.AccessBundle = &b
			if err := agent.AuthorizeSharedBusinessResultRead(t.Context(), h, eGet, a, p); err == nil {
				t.Fatal("current read scope bypassed", denied)
			}
		})
	}
	principals.actual = originalReader
	// Reopened host shares no process-local grant/cache; proof and current
	// principals suffice to validate the exact persisted original bytes.
	reopened, err := NewConversationBusinessHost(h.runtimeID, h.application, principals, h.schema, h.records, WithConversationBusinessEvidenceKey(businessEvidenceTestKey))
	if err != nil || reopened.AuthorizeSharedBusinessResultRead(t.Context(), eLast, reader, producer) != nil {
		t.Fatal("reopened original read", err)
	}
}

func TestSharedBusinessRelatedPagesRecheckReaderOriginAndOriginalCursor(t *testing.T) {
	h, resolver, _, producer := newBusinessRelationFixture(t)
	enableBusinessEvidence(t, h)
	q := agent.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: "own-1", RelationKey: "reverse:order:customer", Fields: []string{"name"}, PageSize: 1}
	first, err := h.QueryRelatedBusinessRecords(t.Context(), q, producer)
	if err != nil || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	q.Cursor = first.NextCursor
	last, err := h.QueryRelatedBusinessRecords(t.Context(), q, producer)
	if err != nil {
		t.Fatal(err)
	}
	e := sealedBusinessEvidence(t, h, producer, "query_related_records", q, last)
	principals, reader := bindSharedBusinessReader(h, resolver, producer)
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
		t.Fatal("original related page", err)
	}
	if _, err := h.QueryRelatedBusinessRecords(t.Context(), q, reader); err == nil {
		t.Fatal("original related cursor granted for execution")
	}
	b := *principals.actual.AccessBundle
	b.FieldPolicies = slices.Clone(b.FieldPolicies)
	for i := range b.FieldPolicies {
		if b.FieldPolicies[i].Resource == "order" && b.FieldPolicies[i].Field == "customer" {
			b.FieldPolicies[i].Read = false
		}
	}
	principals.actual.AccessBundle = &b
	if err := h.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
		t.Fatal("reader relation field revoke bypassed")
	}
}
