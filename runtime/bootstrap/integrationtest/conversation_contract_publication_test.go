package integrationtest

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-orm/query"
)

// Offline legacy conversion changes only the absent requirements snapshot
// marker. Actual original tools, admission receipts and task runs are preserved.
func downgradeAgreementRequirementsFixture(t *testing.T, f *businessWebFixture, id string) {
	t.Helper()
	if f.runtime != nil || f.cfg.DatabaseDriver != "sqlite" {
		t.Fatal("conversion requires a stopped SQLite fixture")
	}
	db, err := sql.Open("sqlite", f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	renderer, err := dialect.ParseRenderer("sqlite", "", "")
	if err != nil {
		t.Fatal(err)
	}
	q, args, err := query.NewSelectBuilder(renderer, "_agent_delegation_agreements").Columns("owner_key", "payload_json").Where(query.And(query.Equal("delegation_id", id), query.Equal("revision", 1))).Build()
	if err != nil {
		t.Fatal(err)
	}
	var owner string
	var raw []byte
	if err = db.QueryRowContext(t.Context(), q, args...).Scan(&owner, &raw); err != nil {
		t.Fatal(err)
	}
	var original agent.ConversationAgreementRevision
	if err = json.Unmarshal(raw, &original); err != nil || original.Requirements == nil || len(original.Requirements.Sources) != 2 {
		t.Fatal("fixture did not freeze both admitted roots", original, err)
	}
	original.Requirements = nil
	raw, err = json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	q, args, err = query.NewUpdateBuilder(renderer, "_agent_delegation_agreements").Set("payload_json", raw).Where(query.And(query.Equal("owner_key", owner), query.Equal("delegation_id", id), query.Equal("revision", 1))).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func verifyLegacyAgreementLifecycle(t *testing.T, b *businessBrowser, id, originalRun string, originalPages, receipts []agent.ConversationResultReference, assign func(string, ...string)) {
	t.Helper()
	path := "/agent/delegations/" + id
	var before agent.ConversationDelegationDetail
	var history agent.ConversationAgreementHistory
	decode := func(value any, raw []byte) {
		t.Helper()
		if err := json.Unmarshal(raw, value); err != nil {
			t.Fatal(err)
		}
	}
	decode(&before, b.call("GET", path, nil, 200).Body.Bytes())
	decode(&history, b.call("GET", path+"/requirements", nil, 200).Body.Bytes())
	if before.Status != "accepted_delivery" || len(history.Items) != 1 || history.Items[0].Requirements != nil {
		t.Fatal("lifecycle requires actual accepted legacy original", before.Status, history)
	}
	original := sharedProfessionalJSON(history.Items[0])
	readOriginalPages := func() {
		t.Helper()
		for _, ref := range originalPages {
			var page agent.ConversationResultSlice
			decode(&page, b.call("POST", "/agent/conversations/"+ref.ConversationID+"/runs/"+ref.RunID+"/result", agent.ConversationResultRead{Reference: ref, MaxBytes: 8192}, 200).Body.Bytes())
			if !page.Complete || page.Reference != ref {
				t.Fatal("historical source page changed", page)
			}
		}
	}
	brief := before.Brief
	brief.Version++
	brief.Goal += " after reviewing the revised requirement"
	var current agent.ConversationDelegationDetail
	decode(&current, b.call("POST", path+"/decisions", agent.ConversationDelegationUpdate{ClientID: "update-legacy-agreement", ExpectedRevision: before.Revision, Action: "update_brief", Reason: "Update the explicit goal while preserving both original admitted receipts", Brief: &brief}, 200).Body.Bytes())
	if current.Status != "needs_update" || current.AgreementRevision != 2 || current.Brief.Version != 2 {
		t.Fatal("new requirement not recorded", current)
	}
	readOriginalPages()
	decode(&history, b.call("GET", path+"/requirements", nil, 200).Body.Bytes())
	if len(history.Items) != 2 || history.Items[0].Requirements == nil || len(history.Items[0].Requirements.Sources) != 2 || sharedProfessionalJSON(history.Items[1]) != original {
		t.Fatal("updating the agreement replaced legacy history or required roots", history)
	}
	b.f.close()
	b.f.open()
	b.session()
	readOriginalPages()
	decode(&current, b.call("GET", path, nil, 200).Body.Bytes())
	in := agent.ConversationDelegationUpdate{ClientID: "resume-legacy-agreement", ExpectedRevision: current.Revision, Action: "resume", Reason: "Reviewed and continue the exact revised agreement"}
	assign("a_results_other_reader", "b_old_analysis", "c_old_report")
	b.call("POST", path+"/decisions", in, 403)
	assign("a_results_other_reader", "a_results_reader", "b_old_analysis", "c_old_report")
	decode(&current, b.call("GET", path, nil, 200).Body.Bytes())
	if current.Revision != in.ExpectedRevision || current.Status != "needs_update" {
		t.Fatal("executor withdrawal admitted work or changed the agreement", current)
	}
	b.call("POST", path+"/decisions", in, 200)
	if current.Task == nil || current.Task.Budget.TimeoutSeconds <= 0 {
		t.Fatal("revised task lost its accepted execution timeout", current.Task)
	}
	deadline := time.Now().Add(time.Duration(current.Task.Budget.TimeoutSeconds) * time.Second)
	for {
		decode(&current, b.call("GET", path, nil, 200).Body.Bytes())
		if current.Task != nil && current.Task.Status == "completed" && current.Delivery != nil && current.Delivery.AgreementRevision == 2 {
			decode(&current, b.call("GET", path, nil, 200).Body.Bytes())
			break
		}
		if current.Status == "failed" || current.Task != nil && current.Task.Status == "completed" || time.Now().After(deadline) {
			deliveryAgreement := int64(0)
			if current.Delivery != nil {
				deliveryAgreement = current.Delivery.AgreementRevision
			}
			t.Log("Revised public view", current.Status, "delivery present", current.Delivery != nil, "delivery agreement", deliveryAgreement, "verification present", current.Verification != nil, "contract omitted", current.ContractOmitted, "access", current.Access)
			if current.Task != nil && current.Task.ExecutionRunID != "" {
				t.Log("Failed revised execution", b.call("GET", "/agent/conversations/"+current.ConversationID+"/runs/"+current.Task.ExecutionRunID, nil, 200).Body.String())
			}
			t.Fatal("revised legacy task did not complete", sharedProfessionalJSON(current.Task), current.Status)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if current.Task.ExecutionRunID == originalRun || current.Verification == nil || !current.Verification.Ready || current.Delivery.BriefVersion != 2 {
		t.Fatal("revised work reused the old run or accepted the old delivery", current)
	}
	var execution agent.ConversationRun
	decode(&execution, b.call("GET", "/agent/conversations/"+current.ConversationID+"/runs/"+current.Task.ExecutionRunID, nil, 200).Body.Bytes())
	if execution.Agent == nil || execution.Agent.DelegationRoleKey != "a_results_reader" || execution.BackgroundTask == nil || execution.BackgroundTask.AgreementRevision != 2 || execution.BackgroundTask.BriefVersion != 2 || len(execution.BackgroundTask.Requirements.Sources) != 2 {
		t.Fatal("manager role replaced accepted execution subject or admitted roots", execution)
	}
	for _, step := range execution.Steps {
		for _, call := range step.Calls {
			if call.Name != "delegation_source_read" && call.Name != "delegation_get" && call.Name != "delegation_update" {
				t.Fatal("resumed reader executed a professional tool", call.Name)
			}
		}
	}
	for _, ref := range receipts {
		b.call("POST", path+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref, MaxBytes: 8192}}, 200)
	}
	b.call("POST", path+"/decisions", agent.ConversationDelegationUpdate{ClientID: "accept-revised-legacy-agreement", ExpectedRevision: current.Revision, Action: "accept_delivery", Reason: "Verified revised agreement and both original totals", Review: &agent.ConversationDeliveryReview{DeliveryDigest: current.Verification.DeliveryDigest}}, 200)
	readOriginalPages()
	decode(&history, b.call("GET", path+"/requirements", nil, 200).Body.Bytes())
	if len(history.Items) != 2 || sharedProfessionalJSON(history.Items[1]) != original {
		t.Fatal("resumption or acceptance changed the old legacy agreement", history)
	}
	t.Log("Real legacy original requirements updated, restart and original pages retained, withdrawn accepted executor rejected before mutation, exact resumed original execution role, new requirements/delivery/acceptance verified")
}

func verifyLegacyAgreementRepublishing(t *testing.T, b *businessBrowser, id string, assign func(string, ...string)) {
	t.Helper()
	path := "/agent/delegations/" + id
	decode := func(value any, r []byte) {
		t.Helper()
		if err := json.Unmarshal(r, value); err != nil {
			t.Fatal(err)
		}
	}
	var before agent.ConversationDelegationDetail
	assign("a_results_reader", "b_old_analysis", "c_old_report")
	decode(&before, b.call("GET", path, nil, 200).Body.Bytes())
	var originalHistory agent.ConversationAgreementHistory
	decode(&originalHistory, b.call("GET", path+"/requirements", nil, 200).Body.Bytes())
	if len(originalHistory.Items) != 1 || originalHistory.Items[0].Requirements != nil {
		t.Fatal("virtual recovery changed legacy snapshot", originalHistory)
	}
	assign("a_results_other_reader", "a_results_reader", "b_old_analysis", "c_old_report")
	b.call("GET", path+"/requirements", nil, 403) // This older role's decision prefix was never shared.
	var candidates agent.ConversationContractPublicationCandidates
	decode(&candidates, b.call("GET", path+"/contract-candidates", nil, 200).Body.Bytes())
	if len(candidates.Items) != 1 || candidates.Items[0].Revision != 1 {
		t.Fatal(candidates)
	}
	var preview agent.ConversationContractPublicationPreview
	decode(&preview, b.call("POST", path+"/contract-publication", agent.ConversationContractPublicationRequest{AgreementRevision: 1}, 200).Body.Bytes())
	if preview.Agreement.Requirements != nil || len(preview.Requirements.Sources) != 2 || preview.Publisher.RoleKey != "a_results_other_reader" {
		t.Fatal("legacy preview substituted current/original roles or roots", preview)
	}
	t.Log("Preparing exact legacy agreement", before.Status, before.Revision, preview.ExpectedRevision)
	in := agent.ConversationDelegationUpdate{ClientID: "explicit-legacy-agreement", ExpectedRevision: preview.ExpectedRevision, Action: "republish_contract", Reason: "Republish the original two admitted professional sources", ContractPublication: &agent.ConversationContractPublication{AgreementRevision: 1, RecordDigest: preview.RecordDigest}}
	assign("a_results_field_denied", "a_results_other_reader", "a_results_reader", "b_old_analysis", "c_old_report")
	b.call("POST", path+"/decisions", in, 403)
	assign("a_results_other_reader", "a_results_reader", "b_old_analysis", "c_old_report")
	var current agent.ConversationDelegationDetail
	decode(&current, b.call("GET", path, nil, 200).Body.Bytes())
	if current.Revision != preview.ExpectedRevision {
		t.Fatal("rejected source publication changed relationship revision", current.Revision, preview.ExpectedRevision)
	}
	var published agent.ConversationDelegationDetail
	decode(&published, b.call("POST", path+"/decisions", in, 200).Body.Bytes())
	if published.ContractPublication == nil || published.ContractPublication.Publisher.RoleKey != "a_results_other_reader" || published.Task != nil || published.Delivery != nil || published.Brief.Goal != "" {
		t.Fatal("publication leaked execution or lost actual publisher", published)
	}
	b.call("POST", path+"/decisions", in, 200)
	var after agent.ConversationDelegationDetail
	assign("a_results_reader", "b_old_analysis", "c_old_report")
	decode(&after, b.call("GET", path, nil, 200).Body.Bytes())
	if after.Status != before.Status || sharedProfessionalJSON(after.Task) != sharedProfessionalJSON(before.Task) || sharedProfessionalJSON(after.Delivery) != sharedProfessionalJSON(before.Delivery) || sharedProfessionalJSON(after.Verification) != sharedProfessionalJSON(before.Verification) || after.Decision != before.Decision || after.AgreementRevision != before.AgreementRevision {
		t.Fatal("original execution, delivery or accepted decision changed")
	}
	var history agent.ConversationContractPublicationHistory
	decode(&history, b.call("GET", path+"/contract-publications", nil, 200).Body.Bytes())
	if len(history.Items) != 1 || history.Items[0].RecordDigest != preview.RecordDigest {
		t.Fatal("retry duplicated original publication", history)
	}
	var afterHistory agent.ConversationAgreementHistory
	assign("a_results_other_reader", "a_results_reader", "b_old_analysis", "c_old_report")
	decode(&afterHistory, b.call("GET", path+"/requirements", nil, 200).Body.Bytes())
	if sharedProfessionalJSON(afterHistory) != sharedProfessionalJSON(originalHistory) {
		t.Fatal("legacy history was rewritten")
	}
	b.f.close()
	b.f.open()
	b.session()
	decode(&history, b.call("GET", path+"/contract-publications", nil, 200).Body.Bytes())
	if len(history.Items) != 1 {
		t.Fatal("publication audit lost on restart")
	}
	t.Log("Real original report/analysis sources, legacy requirements recovery, exact preview, current field withdrawal, explicit actual publisher, original accepted task/delivery/history preservation, idempotency and restart verified")
}
