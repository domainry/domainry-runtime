package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"

	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
)

const (
	RuntimeAuthoringScenarioIDHeader         = changeplanmodel.RuntimeAuthoringScenarioIDHeader
	RuntimeAuthoringScenarioCategoriesHeader = changeplanmodel.RuntimeAuthoringScenarioCategoriesHeader
	RuntimeAuthoringStepLabelHeader          = changeplanmodel.RuntimeAuthoringStepLabelHeader
	RuntimeAuthoringStepObservationHeader    = changeplanmodel.RuntimeAuthoringStepObservationHeader
	RuntimeAuthoringExpectedStatusHeader     = changeplanmodel.RuntimeAuthoringExpectedStatusHeader
	RuntimeAuthoringSnapshotHashHeader       = changeplanmodel.RuntimeAuthoringSnapshotHashHeader
	RuntimeAuthoringCoverageHashHeader       = changeplanmodel.RuntimeAuthoringCoverageHashHeader
	RuntimeAuthoringStepReceiptHeader        = changeplanmodel.RuntimeAuthoringStepReceiptHeader
	RuntimeAuthoringEvidenceErrorHeader      = changeplanmodel.RuntimeAuthoringEvidenceErrorHeader
	RuntimeAuthoringEvidenceStepTokenHeader  = changeplanmodel.RuntimeAuthoringEvidenceStepTokenHeader
)

func (s *HTTPRouter) withRuntimeAuthoringScenarioEvidence(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scenarioID := strings.TrimSpace(r.Header.Get(RuntimeAuthoringScenarioIDHeader))
		stepToken := strings.TrimSpace(r.Header.Get(RuntimeAuthoringEvidenceStepTokenHeader))
		if scenarioID == "" && stepToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		builderTaskID := operationscontract.BuilderTaskID(r.Context())
		if builderTaskID == "" {
			writeError(w, r, http.StatusForbidden, "backend.runtime.scenario_evidence_builder_task_required")
			return
		}
		if runtimeAuthoringScenarioEvidenceStreamingPath(r.URL.Path) {
			writeError(w, r, http.StatusBadRequest, "backend.runtime.scenario_evidence_streaming_unsupported")
			return
		}
		if s.runtimeAuthoringScenarioReceipts == nil {
			writeError(w, r, http.StatusBadRequest, "backend.runtime.scenario_evidence_metadata_invalid")
			return
		}
		stepID, sessionID := "", ""
		categories := []string(nil)
		expectedStatus := []int(nil)
		label, observationKind, snapshotHash, coverageHash := "", "", "", ""
		if stepToken != "" {
			claims, verifyErr := s.runtimeAuthoringScenarioReceipts.VerifyEvidenceStepToken(stepToken)
			if verifyErr != nil || claims.BuilderTaskID != builderTaskID || claims.Method != r.Method || claims.Path != runtimeAuthoringRequestPath(r) {
				writeError(w, r, http.StatusBadRequest, "backend.runtime.scenario_evidence_step_token_invalid")
				return
			}
			sessionID, stepID = claims.SessionID, claims.StepID
			scenarioID, categories, label, observationKind = claims.ScenarioID, claims.Categories, claims.Label, claims.Observation
			snapshotHash, coverageHash, expectedStatus = claims.SnapshotHash, claims.CoverageHash, claims.ExpectedStatus
		} else {
			categories = runtimeAuthoringHeaderValues(r.Header.Get(RuntimeAuthoringScenarioCategoriesHeader))
			var validExpectedStatus bool
			expectedStatus, validExpectedStatus = runtimeAuthoringExpectedStatuses(r.Header.Get(RuntimeAuthoringExpectedStatusHeader))
			label = strings.TrimSpace(r.Header.Get(RuntimeAuthoringStepLabelHeader))
			observationKind = strings.TrimSpace(r.Header.Get(RuntimeAuthoringStepObservationHeader))
			snapshotHash = strings.TrimSpace(r.Header.Get(RuntimeAuthoringSnapshotHashHeader))
			coverageHash = strings.TrimSpace(r.Header.Get(RuntimeAuthoringCoverageHashHeader))
			if label == "" || len(categories) == 0 || !validExpectedStatus || snapshotHash == "" || coverageHash == "" {
				writeError(w, r, http.StatusBadRequest, "backend.runtime.scenario_evidence_metadata_invalid")
				return
			}
		}
		bodyLimit := s.maxJSONBodyBytes
		if bodyLimit <= 0 {
			bodyLimit = 2 << 20
		}
		requestBody, err := io.ReadAll(io.LimitReader(r.Body, bodyLimit+1))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "backend.runtime.scenario_evidence_request_unreadable")
			return
		}
		if int64(len(requestBody)) > bodyLimit {
			if s.httpMetrics != nil {
				s.httpMetrics.ObserveBodyRejection("too_large")
			}
			writeError(w, r, http.StatusRequestEntityTooLarge, "backend.request_body_too_large")
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(requestBody))
		requestSum := sha256.Sum256(requestBody)
		observation := businesssystemapplication.RuntimeAuthoringScenarioStepObservation{
			SessionID: sessionID, StepID: stepID,
			BuilderTaskID: builderTaskID, SnapshotHash: snapshotHash, CoverageHash: coverageHash,
			ScenarioID: scenarioID, Categories: categories, Label: label,
			Observation: observationKind,
			Method:      r.Method, Path: runtimeAuthoringRequestPath(r), ExpectedStatus: expectedStatus,
			RequestHash: hex.EncodeToString(requestSum[:]), IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
		}
		if err := s.runtimeAuthoringScenarioReceipts.ValidateObservation(observation); err != nil {
			writeError(w, r, http.StatusBadRequest, "backend.runtime.scenario_evidence_metadata_invalid")
			return
		}
		captured := newRuntimeAuthoringBufferedResponse()
		next.ServeHTTP(captured, r)
		responseSum := sha256.Sum256(captured.body.Bytes())
		observation.ActualStatus = captured.status
		observation.ResponseHash = hex.EncodeToString(responseSum[:])
		observation.IdempotencyReplayed = strings.EqualFold(strings.TrimSpace(captured.header.Get("Idempotency-Replayed")), "true")
		receipt, receiptErr := s.runtimeAuthoringScenarioReceipts.Issue(observation)
		if receiptErr == nil {
			captured.header.Set(RuntimeAuthoringStepReceiptHeader, receipt)
		} else {
			captured.header.Set(RuntimeAuthoringEvidenceErrorHeader, "receipt_unavailable")
		}
		captured.flushTo(w)
	})
}

func runtimeAuthoringScenarioEvidenceStreamingPath(path string) bool {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	return path == "/events/business" || strings.HasSuffix(path, "/stream")
}

type runtimeAuthoringBufferedResponse struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func newRuntimeAuthoringBufferedResponse() *runtimeAuthoringBufferedResponse {
	return &runtimeAuthoringBufferedResponse{header: make(http.Header), status: http.StatusOK}
}

func (w *runtimeAuthoringBufferedResponse) Header() http.Header { return w.header }

func (w *runtimeAuthoringBufferedResponse) WriteHeader(status int) {
	if w.wroteHeader || status < 100 {
		return
	}
	w.status = status
	w.wroteHeader = true
}

func (w *runtimeAuthoringBufferedResponse) Write(value []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(value)
}

func (w *runtimeAuthoringBufferedResponse) flushTo(target http.ResponseWriter) {
	for key, values := range w.header {
		target.Header()[key] = append([]string(nil), values...)
	}
	target.WriteHeader(w.status)
	_, _ = target.Write(w.body.Bytes())
}

func runtimeAuthoringHeaderValues(raw string) []string {
	values := []string{}
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func runtimeAuthoringExpectedStatuses(raw string) ([]int, bool) {
	parts := runtimeAuthoringHeaderValues(raw)
	if len(parts) == 0 {
		return nil, false
	}
	statuses := make([]int, 0, len(parts))
	for _, part := range parts {
		status, err := strconv.Atoi(part)
		if err != nil || status < 100 || status > 599 {
			return nil, false
		}
		statuses = append(statuses, status)
	}
	return statuses, true
}

func runtimeAuthoringRequestPath(r *http.Request) string {
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	return path
}
