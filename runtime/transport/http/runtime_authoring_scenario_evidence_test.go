package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
)

func TestRuntimeAuthoringScenarioEvidenceMiddlewareAcceptsOnePlanBoundToken(t *testing.T) {
	receipts := businesssystemapplication.NewRuntimeAuthoringScenarioReceiptService(bytes.Repeat([]byte("p"), 32))
	coverage := changeplanmodel.RuntimeAuthoringCoverageLedger{
		Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{RequirementID: "order", ScenarioIDs: []string{"order.lifecycle"}}},
	}
	rawCoverage, _ := json.Marshal(coverage)
	coverageSum := sha256.Sum256(rawCoverage)
	binding := changeplanmodel.RuntimeAuthoringEvidenceBinding{SnapshotHash: strings.Repeat("a", 64), CoverageHash: hex.EncodeToString(coverageSum[:])}
	plan := changeplanmodel.RuntimeAuthoringEvidencePlan{
		Scenarios: []changeplanmodel.RuntimeAuthoringEvidenceScenarioPlan{{
			ScenarioID: "order.lifecycle", Categories: append([]string(nil), changeplanmodel.RuntimeAuthoringRequiredScenarioCategories...),
			Steps: []changeplanmodel.RuntimeAuthoringEvidenceStepPlan{{StepID: "create", Label: "create", Method: http.MethodPost, Path: "/objects/order/records?mode=authoring", ExpectedStatus: []int{http.StatusCreated}}},
		}},
	}
	session, err := receipts.IssueEvidenceSession("task", binding, coverage, plan)
	if err != nil {
		t.Fatal(err)
	}
	router := &HTTPRouter{runtimeAuthoringScenarioReceipts: receipts}
	called := 0
	handler := router.withRuntimeAuthoringScenarioEvidence(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusCreated)
	}))
	request := httptest.NewRequest(http.MethodPost, "/objects/order/records?mode=authoring", strings.NewReader(`{"name":"Order"}`))
	request = request.WithContext(operationscontract.WithBuilderTaskID(request.Context(), "task"))
	request.Header.Set(RuntimeAuthoringEvidenceStepTokenHeader, session.Steps[0].Token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	issued, verifyErr := receipts.Verify(response.Header().Get(RuntimeAuthoringStepReceiptHeader), "task", binding)
	if response.Code != http.StatusCreated || called != 1 || verifyErr != nil || issued.SessionID != session.SessionID || issued.StepID != "create" || issued.ScenarioID != "order.lifecycle" {
		t.Fatalf("status=%d called=%d receipt=%#v err=%v body=%s", response.Code, called, issued, verifyErr, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/objects/order/records?mode=other", nil)
	request = request.WithContext(operationscontract.WithBuilderTaskID(request.Context(), "task"))
	request.Header.Set(RuntimeAuthoringEvidenceStepTokenHeader, session.Steps[0].Token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || called != 1 {
		t.Fatalf("mismatched path status=%d called=%d", response.Code, called)
	}
}

func TestRuntimeAuthoringScenarioEvidenceMiddlewareFailsClosedBeforeExecution(t *testing.T) {
	receipts := businesssystemapplication.NewRuntimeAuthoringScenarioReceiptService(bytes.Repeat([]byte("s"), 32))
	router := &HTTPRouter{runtimeAuthoringScenarioReceipts: receipts}
	called := false
	handler := router.withRuntimeAuthoringScenarioEvidence(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/objects/order/records", nil)
	request.Header.Set(RuntimeAuthoringEvidenceStepTokenHeader, "invalid")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || called {
		t.Fatalf("taskless status=%d called=%v", response.Code, called)
	}

	request = httptest.NewRequest(http.MethodGet, "/objects/order/records", nil)
	request = request.WithContext(operationscontract.WithBuilderTaskID(request.Context(), "task"))
	request.Header.Set(RuntimeAuthoringEvidenceStepTokenHeader, "invalid")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || called {
		t.Fatalf("incomplete status=%d called=%v", response.Code, called)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plain", nil))
	if response.Code != http.StatusNoContent || !called || response.Header().Get(RuntimeAuthoringStepReceiptHeader) != "" {
		t.Fatalf("plain status=%d called=%v headers=%v", response.Code, called, response.Header())
	}
}

func TestRuntimeAuthoringScenarioEvidenceMiddlewareRejectsStreamingAndOversizedRequests(t *testing.T) {
	receipts := businesssystemapplication.NewRuntimeAuthoringScenarioReceiptService(bytes.Repeat([]byte("s"), 32))
	router := &HTTPRouter{runtimeAuthoringScenarioReceipts: receipts, maxJSONBodyBytes: 4}
	called := false
	handler := router.withRuntimeAuthoringScenarioEvidence(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	coverage := changeplanmodel.RuntimeAuthoringCoverageLedger{Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{RequirementID: "order", ScenarioIDs: []string{"order.lifecycle"}}}}
	rawCoverage, _ := json.Marshal(coverage)
	coverageSum := sha256.Sum256(rawCoverage)
	binding := changeplanmodel.RuntimeAuthoringEvidenceBinding{SnapshotHash: strings.Repeat("a", 64), CoverageHash: hex.EncodeToString(coverageSum[:])}
	plan := changeplanmodel.RuntimeAuthoringEvidencePlan{Scenarios: []changeplanmodel.RuntimeAuthoringEvidenceScenarioPlan{{
		ScenarioID: "order.lifecycle", Categories: []string{"success"},
		Steps: []changeplanmodel.RuntimeAuthoringEvidenceStepPlan{{StepID: "create", Label: "create", Method: http.MethodPost, Path: "/objects/order/records", ExpectedStatus: []int{http.StatusNoContent}}},
	}}}
	session, err := receipts.IssueEvidenceSession("task", binding, coverage, plan)
	if err == nil {
		t.Fatal("incomplete required-category plan was accepted")
	}
	plan.Scenarios[0].Categories = append([]string(nil), changeplanmodel.RuntimeAuthoringRequiredScenarioCategories...)
	session, err = receipts.IssueEvidenceSession("task", binding, coverage, plan)
	if err != nil {
		t.Fatal(err)
	}
	newRequest := func(path string, body []byte, token string) *http.Request {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		request = request.WithContext(operationscontract.WithBuilderTaskID(request.Context(), "task"))
		request.Header.Set(RuntimeAuthoringEvidenceStepTokenHeader, token)
		return request
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest("/business/records/stream", nil, "present"))
	if response.Code != http.StatusBadRequest || called {
		t.Fatalf("stream status=%d called=%v", response.Code, called)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest("/objects/order/records", []byte("12345"), session.Steps[0].Token))
	if response.Code != http.StatusRequestEntityTooLarge || called {
		t.Fatalf("oversized status=%d called=%v", response.Code, called)
	}
}
