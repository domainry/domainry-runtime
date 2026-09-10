package agenthost

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

var businessEvidenceTestKey = []byte("business-evidence-fixture-key-32-bytes")

func enableBusinessEvidence(t *testing.T, h *ConversationBusinessHost) {
	t.Helper()
	if err := WithConversationBusinessEvidenceKey(businessEvidenceTestKey)(h); err != nil {
		t.Fatal(err)
	}
}

func sealedBusinessEvidence(t *testing.T, h *ConversationBusinessHost, a agentsdk.ConversationAuthority, op string, input, output any) agentsdk.ConversationBusinessEvidence {
	t.Helper()
	in, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	e := agentsdk.ConversationBusinessEvidence{Version: 1, Source: h.source, ScopeSHA256: conversationBusinessDigest([]string{h.source, a.RuntimeID, a.WorkspaceID, a.UserID}), Operation: op, Input: in, Data: data}
	e.HostProof, err = h.SealBusinessEvidence(t.Context(), e, a)
	if err != nil || e.HostProof == "" {
		t.Fatalf("seal: %v", err)
	}
	return e
}

func mutateBusinessEvidenceRow(t *testing.T, r *businessReadProbe, a agentsdk.ConversationAuthority, object definitionmodel.ObjectSchema, id string, mutate func(*recordmodel.Record)) {
	t.Helper()
	row, found, err := r.repository.GetRecord(t.Context(), a.WorkspaceID, object, id)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	mutate(&row)
	row.UpdatedAt = "2026-09-10T02:00:00Z"
	if err := r.repository.UpdateRecord(t.Context(), a.WorkspaceID, object, row); err != nil {
		t.Fatal(err)
	}
}

func TestConversationBusinessSnapshotKeepsHistoricalValuesAndRechecksSavedRows(t *testing.T) {
	h, resolver, reads, a := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	query := agentsdk.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, PageSize: 1, Filters: []agentsdk.ConversationBusinessFilter{{Field: "name", Operator: "eq", Value: json.RawMessage(`"Alpha"`)}}}
	page, err := h.QueryBusinessRecords(t.Context(), query, a)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	e := sealedBusinessEvidence(t, h, a, "query_records", query, page)
	mutateBusinessEvidenceRow(t, reads, a, reads.object, "own-1", func(r *recordmodel.Record) { r.Data["name"] = "Updated" })
	current, err := h.QueryBusinessRecords(t.Context(), query, a)
	if err != nil || len(current.Items) != 0 {
		t.Fatal(current, err)
	}
	if err := h.RevalidateBusiness(t.Context(), e, a); err != nil {
		t.Fatalf("ordinary update invalidated old read: %v", err)
	}
	if !strings.Contains(string(e.Data), "Alpha") || strings.Contains(string(e.Data), "Updated") {
		t.Fatal("frozen result was replaced")
	}

	// An unchanged policy must not conceal an actual transfer of a saved row.
	mutateBusinessEvidenceRow(t, reads, a, reads.object, "own-1", func(r *recordmodel.Record) { r.OwnerUserID = "colleague" })
	if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("transferred row remained visible")
	}
	mutateBusinessEvidenceRow(t, reads, a, reads.object, "own-1", func(r *recordmodel.Record) { r.OwnerUserID = a.UserID })
	if err := h.RevalidateBusiness(t.Context(), e, a); err != nil {
		t.Fatal(err)
	}

	// Policy changes are evaluated independently of immutable content changes.
	original := *resolver.principal.AccessBundle
	changed := original
	changed.FieldPolicies = append([]identitysdk.FieldPolicy(nil), original.FieldPolicies...)
	for i := range changed.FieldPolicies {
		if changed.FieldPolicies[i].Field == "name" {
			changed.FieldPolicies[i].Read = false
		}
	}
	resolver.principal.AccessBundle = &changed
	if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("revoked field remained visible")
	}
	original.AuthorizationRevision = "new-resolution-after-restore"
	original.ExpiresAt = time.Now().Add(time.Hour)
	resolver.principal.AccessBundle = &original
	if err := h.RevalidateBusiness(t.Context(), e, a); err != nil {
		t.Fatalf("restored equivalent policy lost history: %v", err)
	}

	// Persist/deserialize and construct another host with the same source/key.
	raw, _ := json.Marshal(e)
	var restored agentsdk.ConversationBusinessEvidence
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewConversationBusinessHost(h.runtimeID, h.application, h.principals, h.schema, h.records, WithConversationBusinessEvidenceKey(businessEvidenceTestKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.RevalidateBusiness(t.Context(), restored, a); err != nil {
		t.Fatalf("persistent proof failed after host reconstruction: %v", err)
	}
	if err := WithConversationBusinessEvidenceKey([]byte(strings.Repeat("z", 32)))(reopened); err != nil {
		t.Fatal(err)
	}
	if err := reopened.RevalidateBusiness(t.Context(), restored, a); err == nil {
		t.Fatal("rotated key accepted old proof")
	}
	mutateBusinessEvidenceRow(t, reads, a, reads.object, "own-1", func(r *recordmodel.Record) { r.Deleted = true })
	if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("deleted row remained visible")
	}
}

func TestConversationBusinessSnapshotRejectsForgedProofOrEvidence(t *testing.T) {
	h, _, reads, a := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	q := agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: []string{"name"}}
	row, err := h.GetBusinessRecord(t.Context(), q, a)
	if err != nil {
		t.Fatal(err)
	}
	e := sealedBusinessEvidence(t, h, a, "get_record", q, row)
	for name, change := range map[string]func(*agentsdk.ConversationBusinessEvidence){
		"data": func(e *agentsdk.ConversationBusinessEvidence) {
			e.Data = json.RawMessage(`{"id":"own-1","data":{"name":"fabricated"}}`)
		},
		"input": func(e *agentsdk.ConversationBusinessEvidence) {
			e.Input = json.RawMessage(`{"object_key":"customer","record_id":"other-owner"}`)
		},
		"operation": func(e *agentsdk.ConversationBusinessEvidence) { e.Operation = "query_records" },
		"source":    func(e *agentsdk.ConversationBusinessEvidence) { e.Source = "another-service" },
		"owner":     func(e *agentsdk.ConversationBusinessEvidence) { e.ScopeSHA256 = strings.Repeat("0", 64) },
		"signature": func(e *agentsdk.ConversationBusinessEvidence) { e.HostProof += "0" },
		"policy": func(e *agentsdk.ConversationBusinessEvidence) {
			parts := strings.Split(e.HostProof, ":")
			parts[1] = strings.Repeat("0", 64)
			e.HostProof = strings.Join(parts, ":")
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := e
			change(&bad)
			if err := h.RevalidateBusiness(t.Context(), bad, a); err == nil {
				t.Fatal("forged evidence accepted")
			}
		})
	}
	bad := e
	bad.HostProof = ""
	bad.Data = json.RawMessage(`{"id":"own-1","data":{"name":"fabricated"}}`)
	if _, err := h.SealBusinessEvidence(t.Context(), bad, a); err == nil {
		t.Fatal("host signed fabricated content")
	}
	mutateBusinessEvidenceRow(t, reads, a, reads.object, "own-1", func(r *recordmodel.Record) { r.Data["name"] = "Updated" })
	if err := h.RevalidateBusiness(t.Context(), e, a); err != nil {
		t.Fatal(err)
	}
	unsigned := e
	unsigned.HostProof = ""
	if err := h.RevalidateBusiness(t.Context(), unsigned, a); err == nil {
		t.Fatal("unsigned changed snapshot accepted")
	}
	if err := WithConversationBusinessEvidenceKey([]byte("short"))(h); err == nil {
		t.Fatal("weak signing key accepted")
	}
}

func TestConversationBusinessSnapshotRejectsNewConditionalMaskPolicy(t *testing.T) {
	h, resolver, reads, a := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	q := agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "own-1", Fields: []string{"name"}}
	row, err := h.GetBusinessRecord(t.Context(), q, a)
	if err != nil || string(row.Data["name"]) != `"Alpha"` {
		t.Fatal(row, err)
	}
	e := sealedBusinessEvidence(t, h, a, "get_record", q, row)
	permissions := []string{"customer.read"}
	resolver.principal = accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: a.UserID, WorkspaceID: a.WorkspaceID}}, accessfixture.Bundle{
		Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeOwner),
		FieldPolicies: []accessfixture.FieldPolicyFixture{
			{ObjectKey: "customer", FieldKey: "name", Read: true, Policies: []accessfixture.FieldRuleFixture{{Key: "alpha-masked", Priority: 100, Actions: []string{"read"}, Effect: "mask", MaskStrategy: &accessfixture.MaskFixture{Type: "last_n", LastN: 1}, Predicate: &accessfixture.PredicateFixture{Operator: "eq", FieldKey: "name", ValueSource: "literal", Values: []string{"Alpha"}}}}},
		},
	})
	p, err := h.principal(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	current, err := reads.RecordApplicationService.GetRecord(t.Context(), "customer", "own-1", p)
	if err != nil || current.Data["name"] == "Alpha" {
		t.Fatal(current, err)
	}
	if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("formerly unmasked field survived conditional masking")
	}
}

func TestConversationBusinessSnapshotRelationsRecheckOriginAndTarget(t *testing.T) {
	h, _, reads, a := newBusinessRelationFixture(t)
	enableBusinessEvidence(t, h)
	q := agentsdk.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: "own-1", RelationKey: "reverse:order:customer", Fields: []string{"name"}, PageSize: 1}
	page, err := h.QueryRelatedBusinessRecords(t.Context(), q, a)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	e := sealedBusinessEvidence(t, h, a, "query_related_records", q, page)
	order, _, err := h.object(t.Context(), "order", a)
	if err != nil {
		t.Fatal(err)
	}
	mutateBusinessEvidenceRow(t, reads, a, order, page.Items[0].ID, func(r *recordmodel.Record) { r.Data["name"] = "更新订单" })
	if err := h.RevalidateBusiness(t.Context(), e, a); err != nil {
		t.Fatal(err)
	}
	mutateBusinessEvidenceRow(t, reads, a, reads.object, "own-1", func(r *recordmodel.Record) { r.OwnerUserID = "colleague" })
	if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("inaccessible origin accepted")
	}
	mutateBusinessEvidenceRow(t, reads, a, reads.object, "own-1", func(r *recordmodel.Record) { r.OwnerUserID = a.UserID })
	mutateBusinessEvidenceRow(t, reads, a, order, page.Items[0].ID, func(r *recordmodel.Record) { r.OwnerUserID = "colleague" })
	if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("inaccessible saved target accepted")
	}
	mutateBusinessEvidenceRow(t, reads, a, order, page.Items[0].ID, func(r *recordmodel.Record) { r.OwnerUserID = a.UserID })
	mutateBusinessEvidenceRow(t, reads, a, reads.object, "own-1", func(r *recordmodel.Record) { r.Deleted = true })
	if err := h.RevalidateBusiness(t.Context(), e, a); err == nil {
		t.Fatal("deleted relation origin accepted")
	}
}
