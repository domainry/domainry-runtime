package integration

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

const APIKeyTokenPrefix = "vapi_"

type ConnectorOperationTestRequest struct {
	Operation string         `json:"operation"`
	Input     map[string]any `json:"input,omitempty"`
	Confirm   bool           `json:"confirm"`
}

type ConnectorOperationTestResult struct {
	Connection       integrationmodel.IntegrationConnection    `json:"connection"`
	Operation        integrationmodel.ConnectorOperationSchema `json:"operation"`
	Response         map[string]any                            `json:"response"`
	ActionInvocation integrationmodel.IntegrationInvocation    `json:"invocation"`
}

type EventProcessDecision struct {
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

type EventHandler interface {
	ProcessIntegrationEvent(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error)
}

type EventProcessBatchResult struct {
	Processed    int                                 `json:"processed"`
	Retried      int                                 `json:"retried"`
	DeadLettered int                                 `json:"dead_lettered"`
	Skipped      int                                 `json:"skipped"`
	Events       []integrationmodel.IntegrationEvent `json:"events"`
}

type OutboxSendResult struct {
	ResponseRef       string         `json:"response_ref,omitempty"`
	Status            string         `json:"status,omitempty"`
	AckTimeoutSeconds int            `json:"ack_timeout_seconds,omitempty"`
	Provider          string         `json:"provider,omitempty"`
	Response          map[string]any `json:"response,omitempty"`
}

type OutboxSender interface {
	SendIntegrationOutboxMessage(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error)
}

type OutboxProcessBatchResult struct {
	Sent                   int                                         `json:"sent"`
	Retried                int                                         `json:"retried"`
	DeadLettered           int                                         `json:"dead_lettered"`
	ReconciliationRequired int                                         `json:"reconciliation_required"`
	Skipped                int                                         `json:"skipped"`
	Messages               []integrationmodel.IntegrationOutboxMessage `json:"messages"`
}
