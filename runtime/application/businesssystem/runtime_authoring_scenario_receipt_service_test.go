package businesssystem

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRuntimeAuthoringScenarioReceiptRejectsTamperingAndStaleBinding(t *testing.T) {
	service := NewRuntimeAuthoringScenarioReceiptService(bytes.Repeat([]byte("k"), 32))
	binding := changeplanmodel.RuntimeAuthoringEvidenceBinding{SnapshotHash: strings.Repeat("a", 64), CoverageHash: strings.Repeat("b", 64)}
	receipt, err := service.Issue(RuntimeAuthoringScenarioStepObservation{
		BuilderTaskID: "task", SnapshotHash: binding.SnapshotHash, CoverageHash: binding.CoverageHash,
		ScenarioID: "order.lifecycle", Categories: []string{"success", "success"}, Label: "create",
		Method: "post", Path: "/records/objects/order/records", ExpectedStatus: []int{201, 201}, ActualStatus: 201,
		RequestHash: strings.Repeat("c", 64), ResponseHash: strings.Repeat("d", 64), IdempotencyKey: "create-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt) > 700 {
		t.Fatalf("opaque Runtime receipt exceeded compact transport budget: %d bytes", len(receipt))
	}
	verified, err := service.Verify(receipt, "task", binding)
	if err != nil || verified.Method != "POST" || len(verified.Categories) != 1 || len(verified.ExpectedStatus) != 1 {
		t.Fatalf("verified=%#v err=%v", verified, err)
	}
	if _, err := service.Verify(receipt+"tampered", "task", binding); err == nil {
		t.Fatal("tampered receipt passed verification")
	}
	stale := binding
	stale.SnapshotHash = strings.Repeat("e", 64)
	if _, err := service.Verify(receipt, "task", stale); err == nil {
		t.Fatal("stale snapshot binding passed verification")
	}
	if _, err := service.Verify(receipt, "other-task", binding); err == nil {
		t.Fatal("cross-task receipt passed verification")
	}
	if _, err := NewRuntimeAuthoringScenarioReceiptService(nil).Issue(RuntimeAuthoringScenarioStepObservation{}); err == nil {
		t.Fatal("receipt service accepted an unavailable key")
	}
}

func TestRuntimeAuthoringEvidenceSessionIssuesPlanBoundStepTokens(t *testing.T) {
	service := NewRuntimeAuthoringScenarioReceiptService(bytes.Repeat([]byte("e"), 32))
	coverage := changeplanmodel.RuntimeAuthoringCoverageLedger{
		Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{RequirementID: "order", ScenarioIDs: []string{"order.lifecycle"}}},
	}
	binding := changeplanmodel.RuntimeAuthoringEvidenceBinding{
		SnapshotHash: strings.Repeat("a", 64), CoverageHash: runtimeAuthoringCoverageHash(&coverage),
	}
	plan := changeplanmodel.RuntimeAuthoringEvidencePlan{
		Scenarios: []changeplanmodel.RuntimeAuthoringEvidenceScenarioPlan{{
			ScenarioID: "order.lifecycle", Categories: append([]string(nil), changeplanmodel.RuntimeAuthoringRequiredScenarioCategories...),
			Steps: []changeplanmodel.RuntimeAuthoringEvidenceStepPlan{{StepID: "create", Label: "create order", Method: "post", Path: "/records/objects/order/records", ExpectedStatus: []int{201}}},
		}},
	}
	session, err := service.IssueEvidenceSession("task", binding, coverage, plan)
	if err != nil || session.SessionID == "" || len(session.Steps) != 1 {
		t.Fatalf("session=%#v err=%v", session, err)
	}
	if len(session.Steps[0].Token) > 512 {
		t.Fatalf("opaque evidence step token exceeded compact transport budget: %d bytes", len(session.Steps[0].Token))
	}
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	for _, duplicated := range []string{"\"version\"", "snapshot_hash", "coverage_hash", "scenario_id"} {
		if strings.Contains(string(sessionJSON), duplicated) {
			t.Fatalf("evidence session repeats plan or binding field %q: %s", duplicated, sessionJSON)
		}
	}
	claims, err := service.VerifyEvidenceStepToken(session.Steps[0].Token)
	if err != nil || claims.SessionID != session.SessionID || claims.StepID != "create" || claims.Method != "POST" || claims.CoverageHash != binding.CoverageHash {
		t.Fatalf("claims=%#v err=%v", claims, err)
	}
	if _, err := service.VerifyEvidenceStepToken(session.Steps[0].Token + "tampered"); err == nil {
		t.Fatal("tampered evidence step token passed verification")
	}
	invalidPlan := plan
	invalidPlan.Scenarios = append([]changeplanmodel.RuntimeAuthoringEvidenceScenarioPlan(nil), plan.Scenarios...)
	invalidPlan.Scenarios[0].Categories = []string{"success"}
	if _, err := service.IssueEvidenceSession("task", binding, coverage, invalidPlan); err == nil {
		t.Fatal("incomplete scenario category plan was accepted")
	}
}

func TestRuntimeAuthoringDeliveryTrustsOnlyRuntimeIssuedStepReceipts(t *testing.T) {
	receipts := NewRuntimeAuthoringScenarioReceiptService(bytes.Repeat([]byte("r"), 32))
	dependencies := runtimeAuthoringEdgeDependencies()
	dependencies.ScenarioReceipts = receipts
	dependencies.CurrentManifest = func(context.Context, principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
		return manifestmodel.ManifestSchema{
			SchemaVersion: "2", TemplateID: "direct", Version: "configuring",
			Objects: []definitionmodel.ObjectSchema{{Key: "order", Name: "Order", Fields: []definitionmodel.FieldSchema{{Key: "status", Name: "Status", Type: "text", Required: true}}}},
		}, nil
	}
	dependencies.CurrentSnapshot = func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
		snapshot := runtimeAuthoringCompleteConfigurationSnapshot(nil)
		snapshot.RuntimeVersion = "runtime-v1"
		snapshot.AuthoringContractHash = "contract-hash"
		snapshot.CapabilityKeys = []string{"schema.object"}
		snapshot.ResourceSources = []changeplanprojection.SystemResourceSource{{ResourceType: "object", ResourceKey: "order", SourceKind: "manifest"}}
		snapshot.Finalize()
		return snapshot, nil
	}
	service := NewRuntimeAuthoringValidationApplicationService(dependencies)
	coverage := changeplanmodel.RuntimeAuthoringCoverageLedger{Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{
		RequirementID: "order", CapabilityKeys: []string{"schema.object"},
		Resources: []changeplanmodel.RuntimeAuthoringCoverageResource{{ResourceType: "object", ResourceKey: "order"}}, ScenarioIDs: []string{"order.lifecycle"},
	}}}
	plan := changeplanmodel.RuntimeAuthoringEvidencePlan{
		Scenarios: []changeplanmodel.RuntimeAuthoringEvidenceScenarioPlan{{
			ScenarioID: "order.lifecycle", Categories: append([]string(nil), changeplanmodel.RuntimeAuthoringRequiredScenarioCategories...),
			Steps: []changeplanmodel.RuntimeAuthoringEvidenceStepPlan{{StepID: "registered-step", Label: "registered step", Method: "GET", Path: "/records/objects/order/records/order-1", ExpectedStatus: []int{200}}},
		}},
	}
	ctx := operationscontract.WithBuilderTaskID(t.Context(), "task")
	validation, err := service.ValidateWithCoverageAndEvidencePlan(ctx, runtimeAuthoringValidationAdmin(), &coverage, &plan)
	if err != nil || !validation.Valid || validation.EvidenceSession == nil || validation.Checks["evidence_plan"] != "ok" {
		t.Fatalf("validation=%#v err=%v", validation, err)
	}
	issue := func(label, path string, status int, responseHash, observation, idempotencyKey string, replayed bool) string {
		t.Helper()
		token, err := receipts.Issue(RuntimeAuthoringScenarioStepObservation{
			SessionID: validation.EvidenceSession.SessionID, StepID: label,
			BuilderTaskID: "task", SnapshotHash: validation.Binding.SnapshotHash, CoverageHash: validation.Binding.CoverageHash,
			ScenarioID: "order.lifecycle", Categories: append([]string(nil), changeplanmodel.RuntimeAuthoringRequiredScenarioCategories...),
			Label: label, Observation: observation, Method: "GET", Path: path, ExpectedStatus: []int{status}, ActualStatus: status,
			RequestHash: strings.Repeat("1", 64), ResponseHash: responseHash, IdempotencyKey: idempotencyKey, IdempotencyReplayed: replayed,
		})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	stateHash := strings.Repeat("2", 64)
	steps := []changeplanmodel.RuntimeAuthoringScenarioStepEvidence{
		{RuntimeReceipt: issue("success", "/records/objects/order/records/order-1", 200, strings.Repeat("3", 64), "", "", false), ActualStatus: 500, Passed: false},
		{RuntimeReceipt: issue("denied", "/records/objects/order/records/order-1", 403, strings.Repeat("4", 64), "", "", false)},
		{RuntimeReceipt: issue("precondition", "/records/objects/order/records/order-1/actions/complete", 422, strings.Repeat("5", 64), "", "", false)},
		{RuntimeReceipt: issue("before", "/records/objects/order/records/order-1", 200, stateHash, "before_state", "", false)},
		{RuntimeReceipt: issue("after", "/records/objects/order/records/order-1", 200, stateHash, "after_state", "", false)},
		{RuntimeReceipt: issue("replay-one", "/records/objects/order/records", 200, strings.Repeat("6", 64), "", "create-1", false)},
		{RuntimeReceipt: issue("replay-two", "/records/objects/order/records", 200, strings.Repeat("6", 64), "", "create-1", true)},
		{RuntimeReceipt: issue("audit", "/audit/events", 200, strings.Repeat("7", 64), "", "", false)},
		{RuntimeReceipt: issue("event", "/business-events/stream", 200, strings.Repeat("8", 64), "", "", false)},
		{RuntimeReceipt: issue("outbox", "/operations/outbox", 200, strings.Repeat("9", 64), "", "", false)},
	}
	submission := changeplanmodel.RuntimeAuthoringDeliverySubmission{Coverage: coverage, Receipts: []string{}}
	for _, step := range steps {
		submission.Receipts = append(submission.Receipts, step.RuntimeReceipt)
	}
	report, err := service.VerifyDelivery(ctx, runtimeAuthoringValidationAdmin(), submission)
	if err != nil || !report.Valid || report.Binding.SnapshotHash != validation.Binding.SnapshotHash || report.Binding.CoverageHash != validation.Binding.CoverageHash || report.Checks["runtime_evidence"] != "ok" {
		t.Fatalf("report=%#v err=%v", report, err)
	}

	tampered := submission
	tampered.Receipts = append([]string(nil), submission.Receipts...)
	tampered.Receipts[0] += "tampered"
	report, err = service.VerifyDelivery(ctx, runtimeAuthoringValidationAdmin(), tampered)
	if err != nil || report.Valid || report.Checks["runtime_evidence"] != "invalid" {
		t.Fatalf("tampered report=%#v err=%v", report, err)
	}

	report, err = service.VerifyDelivery(t.Context(), runtimeAuthoringValidationAdmin(), submission)
	if err != nil || report.Valid || report.Checks["runtime_evidence"] != "invalid" {
		t.Fatalf("taskless report=%#v err=%v", report, err)
	}
}
