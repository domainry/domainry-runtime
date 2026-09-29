package transport

import (
	"context"
	"encoding/json"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

type conversationBusinessEvidenceSourceStub struct {
	query       agentsdk.ConversationBusinessQuery
	related     agentsdk.ConversationBusinessRelatedQuery
	revalidated agentsdk.ConversationBusinessEvidence
}

func (*conversationBusinessEvidenceSourceStub) BusinessSourceIdentity() string {
	return "runtime-business-v1"
}
func (s *conversationBusinessEvidenceSourceStub) QueryBusinessRecords(_ context.Context, query agentsdk.ConversationBusinessQuery, _ agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRecordPage, error) {
	s.query = query
	return agentsdk.ConversationBusinessRecordPage{ObjectKey: query.ObjectKey, Page: 1, PageSize: query.PageSize, Items: []agentsdk.ConversationBusinessRecord{{ID: "account-1", Version: "revision-1", Data: map[string]json.RawMessage{"name": json.RawMessage(`"Aurora"`)}}}}, nil
}
func (*conversationBusinessEvidenceSourceStub) GetBusinessRecord(_ context.Context, query agentsdk.ConversationBusinessGet, _ agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRecord, error) {
	return agentsdk.ConversationBusinessRecord{ID: query.RecordID, Data: map[string]json.RawMessage{}}, nil
}
func (s *conversationBusinessEvidenceSourceStub) QueryRelatedBusinessRecords(_ context.Context, query agentsdk.ConversationBusinessRelatedQuery, _ agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRelatedPage, error) {
	s.related = query
	return agentsdk.ConversationBusinessRelatedPage{
		SourceObjectKey: query.ObjectKey, SourceRecordID: query.RecordID, RelationKey: query.RelationKey,
		ConversationBusinessRecordPage: agentsdk.ConversationBusinessRecordPage{ObjectKey: "contact", Page: 1, PageSize: query.PageSize, Items: []agentsdk.ConversationBusinessRecord{}},
	}, nil
}
func (*conversationBusinessEvidenceSourceStub) SealBusinessEvidence(context.Context, agentsdk.ConversationBusinessEvidence, agentsdk.ConversationAuthority) (string, error) {
	return "source-proof", nil
}

func TestConversationToolsBusinessSourceOwnsRelatedReadEvidence(t *testing.T) {
	stub := &conversationBusinessEvidenceSourceStub{}
	source := conversationToolsBusinessSource{source: stub}
	authority := toolsdk.Authority{Known: true, RuntimeID: "runtime-1", WorkspaceID: "workspace-1", UserID: "user-1"}
	evidence, err := source.ReadConversationBusiness(t.Context(), toolsdk.ConversationBusinessRead{
		Operation: "query_related_records", Input: json.RawMessage(`{"object_key":"account","record_id":"account-1","relation_key":"reverse:contact:account","fields":["name"],"page_size":5}`),
	}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if stub.related.RecordID != "account-1" || stub.related.RelationKey != "reverse:contact:account" || evidence.Operation != "query_related_records" || evidence.HostProof != "source-proof" {
		t.Fatalf("related=%#v evidence=%#v", stub.related, evidence)
	}
}
func (s *conversationBusinessEvidenceSourceStub) RevalidateBusiness(_ context.Context, evidence agentsdk.ConversationBusinessEvidence, _ agentsdk.ConversationAuthority) error {
	s.revalidated = evidence
	return nil
}

func TestConversationToolsBusinessSourceOwnsReadEvidenceAndRevalidation(t *testing.T) {
	stub := &conversationBusinessEvidenceSourceStub{}
	source := conversationToolsBusinessSource{source: stub}
	authority := toolsdk.Authority{Known: true, RuntimeID: "runtime-1", WorkspaceID: "workspace-1", UserID: "user-1"}
	evidence, err := source.ReadConversationBusiness(t.Context(), toolsdk.ConversationBusinessRead{
		Operation: "query_records", Input: json.RawMessage(`{"object_key":"account","fields":["name"],"page_size":5}`),
	}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if stub.query.ObjectKey != "account" || stub.query.PageSize != 5 || evidence.Source != stub.BusinessSourceIdentity() || evidence.ScopeSHA256 == "" || evidence.HostProof != "source-proof" || evidence.Operation != "query_records" || len(evidence.Data) == 0 {
		t.Fatalf("query=%#v evidence=%#v", stub.query, evidence)
	}
	if err = source.RevalidateConversationBusiness(t.Context(), evidence, authority); err != nil || stub.revalidated.HostProof != "source-proof" || stub.revalidated.ScopeSHA256 != evidence.ScopeSHA256 {
		t.Fatalf("revalidated=%#v err=%v", stub.revalidated, err)
	}
}

func TestConversationToolsBusinessSourceRejectsUnknownPayloadAndOperation(t *testing.T) {
	source := conversationToolsBusinessSource{source: &conversationBusinessEvidenceSourceStub{}}
	authority := toolsdk.Authority{Known: true, RuntimeID: "runtime-1", WorkspaceID: "workspace-1", UserID: "user-1"}
	for name, request := range map[string]toolsdk.ConversationBusinessRead{
		"unknown field": {Operation: "query_records", Input: json.RawMessage(`{"object_key":"account","unexpected":true}`)},
		"operation":     {Operation: "delete_records", Input: json.RawMessage(`{}`)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := source.ReadConversationBusiness(t.Context(), request, authority); err == nil {
				t.Fatalf("invalid request accepted: %#v", request)
			}
		})
	}
}
