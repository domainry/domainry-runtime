package dispatch

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	schedulergateway "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

// ExecutionHandler is Runtime's target-execution boundary. Scheduler owns
// scheduling, runs, retries and dead letters; this handler accepts only an
// already-claimed execution request for a Runtime-resident target.
type ExecutionHandler struct {
	writeJSON         func(http.ResponseWriter, int, any)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	executor          TargetExecutor
	runtimeID         string
	signingSecret     []byte
	now               func() time.Time
}

type TargetExecutionDependencies struct {
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	Executor          TargetExecutor
	RuntimeID         string
	SigningSecret     []byte
	Now               func() time.Time
}

func NewExecutionHandler(deps TargetExecutionDependencies) *ExecutionHandler {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &ExecutionHandler{
		writeJSON: deps.WriteJSON, writeServiceError: deps.WriteServiceError,
		executor: deps.Executor, runtimeID: strings.TrimSpace(deps.RuntimeID), signingSecret: append([]byte(nil), deps.SigningSecret...), now: now,
	}
}

type TargetExecutor interface {
	Execute(context.Context, dispatchapplication.ExecutionRequest) (dispatchapplication.ExecutionReceipt, error)
}

func schedulerSignature(r *http.Request) schedulergateway.Signature {
	return schedulergateway.Signature{
		ClientID:  r.Header.Get(schedulergateway.ClientIDHeader),
		Timestamp: r.Header.Get(schedulergateway.TimestampHeader),
		Value:     r.Header.Get(schedulergateway.SignatureHeader),
	}
}

type executionRequest struct {
	RuntimeID      string          `json:"runtime_id"`
	ExecutionID    string          `json:"execution_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	DueAt          time.Time       `json:"due_at,omitempty"`
	Target         executionTarget `json:"target"`
}

type executionTarget struct {
	Type          string          `json:"type"`
	Owner         string          `json:"owner"`
	Operation     string          `json:"operation"`
	ConnectionKey string          `json:"connection_key,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

type executionReceipt struct {
	ExecutionID string `json:"execution_id"`
	ID          string `json:"id"`
	Owner       string `json:"owner"`
	Status      string `json:"status"`
}
