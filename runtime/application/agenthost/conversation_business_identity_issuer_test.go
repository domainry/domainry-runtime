package agenthost

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestBusinessEvidenceCannotMoveBetweenIdentityIssuers(t *testing.T) {
	h, _, _, a := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	issuer := "https://identity.example.test"
	if err := WithConversationBusinessIdentityIssuer(issuer)(h); err != nil {
		t.Fatal(err)
	}
	input := agentsdk.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, PageSize: 1}
	page, err := h.QueryBusinessRecords(t.Context(), input, a)
	if err != nil {
		t.Fatal(err)
	}
	evidence := sealedBusinessEvidence(t, h, a, "query_records", input, page)
	for _, nextIssuer := range []string{issuer, "https://other-identity.example.test"} {
		reopened, err := NewConversationBusinessHost(h.runtimeID, h.application, h.principals, h.schema, h.records, WithConversationBusinessEvidenceKey(businessEvidenceTestKey), WithConversationBusinessIdentityIssuer(nextIssuer))
		if err != nil {
			t.Fatal(err)
		}
		err = reopened.RevalidateBusiness(t.Context(), evidence, a)
		if nextIssuer == issuer && err != nil || nextIssuer != issuer && err == nil {
			t.Fatalf("saved proof acceptance for issuer %q: %v", nextIssuer, err)
		}
	}
}
