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

func TestRuntimeAuthoringScenarioEvidenceMiddlewareIssuesBoundReceipt(t *testing.T) {
	receipts := businesssystemapplication.NewRuntimeAuthoringScenarioReceiptService(bytes.Repeat([]byte("s"), 32))
	router := &HTTPRouter{runtimeAuthoringScenarioReceipts: receipts}
	handler := router.withRuntimeAuthoringScenarioEvidence(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body := make([]byte, r.ContentLength); len(body) > 0 {
			_, _ = r.Body.Read(body)
		}
		w.Header().Set("Idempotency-Replayed", "true")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"order-1"}`))
	}))
	body := []byte(`{"name":"Order"}`)
	request := httptest.NewRequest(http.MethodPost, "/objects/order/records?mode=authoring", bytes.NewReader(body))
	request = request.WithContext(operationscontract.WithBuilderTaskID(request.Context(), "task"))
	request.Header.Set(RuntimeAuthoringScenarioIDHeader, "order.lifecycle")
	request.Header.Set(RuntimeAuthoringScenarioCategoriesHeader, "success,idempotent_replay")
	request.Header.Set(RuntimeAuthoringStepLabelHeader, "create")
	request.Header.Set(RuntimeAuthoringExpectedStatusHeader, "201")
	request.Header.Set(RuntimeAuthoringSnapshotHashHeader, strings.Repeat("a", 64))
	request.Header.Set(RuntimeAuthoringCoverageHashHeader, strings.Repeat("b", 64))
	request.Header.Set("Idempotency-Key", "create-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get(RuntimeAuthoringStepReceiptHeader) == "" || response.Body.String() != `{"id":"order-1"}` {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	claims, err := receipts.Verify(response.Header().Get(RuntimeAuthoringStepReceiptHeader), "task", changeplanmodel.RuntimeAuthoringEvidenceBinding{
		SnapshotHash: strings.Repeat("a", 64), CoverageHash: strings.Repeat("b", 64),
	})
	requestHash := sha256.Sum256(body)
	responseHash := sha256.Sum256([]byte(`{"id":"order-1"}`))
	if err != nil || claims.ActualStatus != http.StatusCreated || claims.Path != "/objects/order/records?mode=authoring" || claims.RequestHash != hex.EncodeToString(requestHash[:]) || claims.ResponseHash != hex.EncodeToString(responseHash[:]) || !claims.IdempotencyReplayed {
		t.Fatalf("claims=%#v err=%v", claims, err)
	}
}

func TestRuntimeAuthoringScenarioEvidenceMiddlewareAcceptsOnePlanBoundToken(t *testing.T) {
	receipts := businesssystemapplication.NewRuntimeAuthoringScenarioReceiptService(bytes.Repeat([]byte("p"), 32))
	coverage := changeplanmodel.RuntimeAuthoringCoverageLedger{
		Version:      changeplanmodel.RuntimeAuthoringCoverageLedgerVersion,
		Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{RequirementID: "order", ScenarioIDs: []string{"order.lifecycle"}}},
	}
	rawCoverage, _ := json.Marshal(coverage)
	coverageSum := sha256.Sum256(rawCoverage)
	binding := changeplanmodel.RuntimeAuthoringEvidenceBinding{SnapshotHash: strings.Repeat("a", 64), CoverageHash: hex.EncodeToString(coverageSum[:])}
	plan := changeplanmodel.RuntimeAuthoringEvidencePlan{
		Version: changeplanmodel.RuntimeAuthoringEvidencePlanVersion,
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
	request.Header.Set(RuntimeAuthoringScenarioIDHeader, "order.lifecycle")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || called {
		t.Fatalf("taskless status=%d called=%v", response.Code, called)
	}

	request = httptest.NewRequest(http.MethodGet, "/objects/order/records", nil)
	request = request.WithContext(operationscontract.WithBuilderTaskID(request.Context(), "task"))
	request.Header.Set(RuntimeAuthoringScenarioIDHeader, "order.lifecycle")
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

	newRequest := func(path string, body []byte) *http.Request {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		request = request.WithContext(operationscontract.WithBuilderTaskID(request.Context(), "task"))
		request.Header.Set(RuntimeAuthoringScenarioIDHeader, "order.lifecycle")
		request.Header.Set(RuntimeAuthoringScenarioCategoriesHeader, "success")
		request.Header.Set(RuntimeAuthoringStepLabelHeader, "create")
		request.Header.Set(RuntimeAuthoringExpectedStatusHeader, "204")
		request.Header.Set(RuntimeAuthoringSnapshotHashHeader, strings.Repeat("a", 64))
		request.Header.Set(RuntimeAuthoringCoverageHashHeader, strings.Repeat("b", 64))
		return request
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest("/business/records/stream", nil))
	if response.Code != http.StatusBadRequest || called {
		t.Fatalf("stream status=%d called=%v", response.Code, called)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest("/objects/order/records", []byte("12345")))
	if response.Code != http.StatusRequestEntityTooLarge || called {
		t.Fatalf("oversized status=%d called=%v", response.Code, called)
	}
}
