package agent

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestAgentSessionApplicationOwnsLifecycleAndVisibility(t *testing.T) {
	application := NewAgentApplicationService(NewAgentMemoryStateRepository())
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, accessfixture.Bundle{Key: "admin"})
	created, err := application.UpsertSession(t.Context(), AgentSessionUpsertRequest{ExternalSessionID: "session-1", LastSummary: "Customer follow up", Context: map[string]any{"surface": "customer", "record_id": "customer-1"}}, admin)
	if err != nil || created.Title != "Customer follow up" || created.Surface != "customer" {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	listed, err := application.ListSessions(t.Context(), AgentSessionQuery{Search: "follow", Surface: "customer"}, admin)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
	other := admin
	other.UserID = "user-b"
	if hidden, err := application.ListSessions(t.Context(), AgentSessionQuery{}, other); err != nil || len(hidden) != 0 {
		t.Fatalf("cross-user sessions=%#v err=%v", hidden, err)
	}
	archived, err := application.SetSessionArchived(t.Context(), "session-1", true, admin)
	if err != nil || !archived.Archived {
		t.Fatalf("archived=%#v err=%v", archived, err)
	}
	if active, _ := application.ListSessions(t.Context(), AgentSessionQuery{}, admin); len(active) != 0 {
		t.Fatalf("archived session leaked active=%#v", active)
	}
	if archivedList, _ := application.ListSessions(t.Context(), AgentSessionQuery{IncludeArchived: true}, admin); len(archivedList) != 1 {
		t.Fatalf("archived list=%#v", archivedList)
	}
}
