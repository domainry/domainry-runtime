package dispatch

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	dispatchcontract "github.com/domainry/domainry-runtime/runtime/domain/dispatch/contract"
	schedulergateway "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

// ExecutionHandler is Runtime's target-execution boundary. Scheduler owns
// scheduling, runs, retries and dead letters; this handler accepts only an
// already-claimed execution request for a Runtime-resident target.
type ExecutionHandler struct {
	writeJSON         func(http.ResponseWriter, int, any)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	executor          *dispatchapplication.CallbackExecutionApplicationService
	targetAvailable   bool
	runtimeID         string
	signingSecret     []byte
	now               func() time.Time
}

type TargetExecutionDependencies struct {
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	Executor          TargetExecutor
	Receipts          dispatchcontract.CallbackReceiptStore
	RuntimeID         string
	SigningSecret     []byte
	Now               func() time.Time
}

func NewExecutionHandler(deps TargetExecutionDependencies) *ExecutionHandler {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	writeJSON := deps.WriteJSON
	if writeJSON == nil {
		writeJSON = func(w http.ResponseWriter, status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		}
	}
	writeServiceError := deps.WriteServiceError
	if writeServiceError == nil {
		writeServiceError = func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"status_code": http.StatusInternalServerError, "code": "dispatch.execution_failed"})
		}
	}
	return &ExecutionHandler{
		writeJSON: writeJSON, writeServiceError: writeServiceError,
		executor: dispatchapplication.NewCallbackExecutionApplicationService(dispatchapplication.CallbackExecutionDependencies{
			Executor: deps.Executor, Receipts: deps.Receipts, LeaseOwner: "dispatch-callback:" + strings.TrimSpace(deps.RuntimeID), Now: now,
		}),
		targetAvailable: deps.Executor != nil, runtimeID: strings.TrimSpace(deps.RuntimeID), signingSecret: append([]byte(nil), deps.SigningSecret...), now: now,
	}
}

type TargetExecutor = dispatchapplication.CallbackTargetExecutor

func schedulerSignature(r *http.Request) schedulergateway.Signature {
	return schedulergateway.Signature{
		Version:   r.Header.Get(schedulergateway.SignatureVersionHeader),
		ClientID:  r.Header.Get(schedulergateway.ClientIDHeader),
		Timestamp: r.Header.Get(schedulergateway.TimestampHeader),
		Value:     r.Header.Get(schedulergateway.SignatureHeader),
	}
}

type executionRequest struct {
	RuntimeID      string          `json:"runtime_id"`
	ExecutionID    string          `json:"execution_id"`
	DefinitionKey  string          `json:"definition_key"`
	IdempotencyKey string          `json:"idempotency_key"`
	DueAt          time.Time       `json:"due_at,omitempty"`
	Target         executionTarget `json:"target"`
}

type executionTarget struct {
	Type          string          `json:"type"`
	Owner         string          `json:"owner"`
	Operation     string          `json:"operation"`
	ObjectKey     string          `json:"object_key,omitempty"`
	RunAsRole     string          `json:"run_as_role,omitempty"`
	ConnectionKey string          `json:"connection_key,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

type executionReceipt struct {
	ExecutionID string `json:"execution_id"`
	ID          string `json:"id"`
	Owner       string `json:"owner"`
	Status      string `json:"status"`
	Replay      bool   `json:"replay,omitempty"`
}
