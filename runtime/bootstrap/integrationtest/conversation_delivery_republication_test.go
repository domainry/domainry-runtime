package integrationtest

import (
	"encoding/json"
	"strings"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
)

func verifyExplicitLegacyMixedSourceRepublishing(t *testing.T, f *businessWebFixture, b *businessBrowser, delivered agent.ConversationDelegationDetail, read func(int), assign func(string, ...string)) {
	t.Helper()
	path := "/agent/delegations/" + delivered.ID
	var accepted agent.ConversationDelegationDetail
	if err := json.Unmarshal(b.call("POST", path+"/decisions", agent.ConversationDelegationUpdate{ClientID: "accept-manual-original", ExpectedRevision: delivered.Revision, Action: "accept_delivery", Reason: "Accept both actual original professional results", Review: &agent.ConversationDeliveryReview{DeliveryDigest: delivered.Verification.DeliveryDigest}}, 200).Body.Bytes(), &accepted); err != nil || accepted.Status != "accepted_delivery" || accepted.Verification == nil {
		t.Fatal("original manual delivery did not reach accepted state", accepted, err)
	}
	var history agent.ConversationDeliveryHistory
	if err := json.Unmarshal(b.call("GET", path+"/deliveries", nil, 200).Body.Bytes(), &history); err != nil || len(history.Items) != 2 {
		t.Fatal("immutable manual submission/acceptance missing", history, err)
	}
	f.close()
	downgradeMixedSourcePublicationFixture(t, f, delivered.ID, true)
	f.open()
	b.session()
	// These actual human records have no saved publishing role or actor run.
	// The old selected execution role must not stand in for a publisher.
	read(403)
	var index agent.ConversationDeliveryPublicationCandidates
	indexResponse := b.call("GET", path+"/delivery-publications", nil, 200)
	if err := json.Unmarshal(indexResponse.Body.Bytes(), &index); err != nil || len(index.Items) != 2 || !index.CurrentAvailable || strings.Contains(indexResponse.Body.String(), delivered.Delivery.Summary) {
		t.Fatal("preparation index did not keep inaccessible content private", index, err)
	}
	var prepared agent.ConversationDeliveryPublicationPreview
	prepare := func(revision int64, want int) {
		t.Helper()
		response := b.call("POST", path+"/delivery-publication", agent.ConversationDeliveryPublicationRequest{DeliveryRevision: revision}, want)
		if want == 200 {
			if err := json.Unmarshal(response.Body.Bytes(), &prepared); err != nil || prepared.Record.Revision != revision || prepared.Publisher.RoleKey != "a_results_reader" || prepared.ExpectedRevision != accepted.Revision || prepared.Record.Delivery.Summary != delivered.Delivery.Summary {
				t.Fatal("preview did not select the exact original with current publisher", prepared, err)
			}
		}
	}
	prepare(0, 200)
	read(403) // Preview creates no grant.
	prepare(accepted.Revision, 200)
	in := agent.ConversationDelegationUpdate{ClientID: "explicit-accepted-original-share", ExpectedRevision: prepared.ExpectedRevision, Action: "republish_delivery", Reason: "Explicitly publish unchanged accepted report/analysis originals", Publication: &agent.ConversationDeliveryPublication{DeliveryRevision: accepted.Revision, RecordDigest: prepared.RecordDigest}}
	bad := in
	copy := *in.Publication
	copy.RecordDigest = strings.Repeat("0", 64)
	bad.ClientID, bad.Publication = "forged-republication", &copy
	b.call("POST", path+"/decisions", bad, 409)
	assign("a_results_field_denied", "a_results_reader", "b_old_analysis", "c_old_report")
	prepare(accepted.Revision, 403)
	b.call("POST", path+"/decisions", in, 403)
	assign("a_results_reader", "b_old_analysis")
	prepare(accepted.Revision, 403)
	b.call("POST", path+"/decisions", in, 403)
	assign("a_results_reader", "b_old_analysis", "c_old_report")
	prepare(accepted.Revision, 200)
	var receipt agent.ConversationDelegationDetail
	response := b.call("POST", path+"/decisions", in, 200)
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || receipt.Publication == nil || receipt.Publication.Publisher.RoleKey != "a_results_reader" || receipt.Publication.DeliveryRevision != accepted.Revision || receipt.Status != "accepted_delivery" || receipt.Delivery != nil || receipt.Task != nil || len(receipt.Messages) != 0 {
		t.Fatal("explicit accepted-history publication did not return a narrow receipt", receipt, err)
	}
	b.call("POST", path+"/decisions", in, 200) // Exact retry keeps one audit.
	read(200)
	var current agent.ConversationDelegationDetail
	if err := json.Unmarshal(b.call("GET", path, nil, 200).Body.Bytes(), &current); err != nil || current.Status != accepted.Status || sharedProfessionalJSON(*current.Verification) != sharedProfessionalJSON(*accepted.Verification) || sharedProfessionalJSON(*current.Delivery) != sharedProfessionalJSON(*accepted.Delivery) {
		t.Fatal("republication changed accepted delivery or assessment", current, err)
	}
	var after agent.ConversationDeliveryHistory
	if err := json.Unmarshal(b.call("GET", path+"/deliveries", nil, 200).Body.Bytes(), &after); err != nil || len(after.Items) != 3 || after.Items[0].Kind != "republish_delivery" || after.Items[0].Publication == nil {
		t.Fatal("publication history/audit was duplicated or missing", after, err)
	}
	for _, old := range history.Items {
		found := false
		for _, saved := range after.Items {
			found = found || saved.Revision == old.Revision && sharedProfessionalJSON(saved) == sharedProfessionalJSON(old)
		}
		if !found {
			t.Fatal("immutable original history was rewritten", old.Revision)
		}
	}
	f.close()
	f.open()
	b.session()
	read(200)
	assign("a_results_other_reader", "b_old_analysis", "c_old_report")
	read(403)
	assign("a_results_reader", "b_old_analysis", "c_old_report")
	read(200)
	t.Log("Actual manual old report/analysis originals, accepted unknown-publisher history, version-only index, independent current-source preview, preview without grant, forged/stale data denial, explicit canonical publication, exact retry, unchanged delivery/assessment/history, publication-role revocation and restart verified")
}
