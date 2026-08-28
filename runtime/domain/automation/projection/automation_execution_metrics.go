package projection

import (
	"fmt"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type AutomationExecutionMetrics struct {
	Total                     int            `json:"total"`
	ByStatus                  map[string]int `json:"by_status"`
	Succeeded                 int            `json:"succeeded"`
	Blocked                   int            `json:"blocked"`
	Failed                    int            `json:"failed"`
	BlockRate                 float64        `json:"block_rate"`
	FailureRate               float64        `json:"failure_rate"`
	AverageMS                 int64          `json:"average_duration_ms"`
	MaxMS                     int64          `json:"max_duration_ms"`
	ConnectorCalls            int            `json:"connector_calls"`
	ConnectorSucceeded        int            `json:"connector_succeeded"`
	ConnectorFailed           int            `json:"connector_failed"`
	AverageConnectorLatencyMS int64          `json:"average_connector_latency_ms"`
	MaxConnectorLatencyMS     int64          `json:"max_connector_latency_ms"`
	RetryCount                int            `json:"retry_count"`
	DeadLetterCount           int            `json:"dead_letter_count"`
}

type AutomationExecutionHistory struct {
	Items   []automationmodel.AutomationRuleExecution `json:"items"`
	Count   int                                       `json:"count"`
	Metrics AutomationExecutionMetrics                `json:"metrics"`
}

func BuildAutomationExecutionMetrics(items []automationmodel.AutomationRuleExecution, invocations []integrationmodel.IntegrationInvocation, outbox []integrationmodel.IntegrationOutboxMessage) AutomationExecutionMetrics {
	metrics := AutomationExecutionMetrics{Total: len(items), ByStatus: map[string]int{}}
	var totalDuration int64
	eventIDs, invocationIDs := map[string]bool{}, map[string]bool{}
	for _, item := range items {
		metrics.ByStatus[item.Status]++
		totalDuration += item.DurationMS
		if eventID := strings.TrimSpace(item.EventID); eventID != "" {
			eventIDs[eventID] = true
		}
		for _, action := range executionTraceActions(item.Trace) {
			if invocationID := strings.TrimSpace(fmt.Sprint(action["invocation_id"])); invocationID != "" && invocationID != "<nil>" {
				invocationIDs[invocationID] = true
			}
		}
		if item.DurationMS > metrics.MaxMS {
			metrics.MaxMS = item.DurationMS
		}
		switch item.Status {
		case "succeeded":
			metrics.Succeeded++
		case "blocked":
			metrics.Blocked++
		case "failed":
			metrics.Failed++
		}
	}
	if len(items) > 0 {
		metrics.AverageMS = totalDuration / int64(len(items))
		metrics.BlockRate = float64(metrics.Blocked) / float64(len(items))
		metrics.FailureRate = float64(metrics.Blocked+metrics.Failed) / float64(len(items))
	}
	var connectorDuration int64
	for _, invocation := range invocations {
		if !invocationIDs[strings.TrimSpace(invocation.ID)] {
			continue
		}
		metrics.ConnectorCalls++
		connectorDuration += invocation.DurationMS
		if invocation.DurationMS > metrics.MaxConnectorLatencyMS {
			metrics.MaxConnectorLatencyMS = invocation.DurationMS
		}
		if invocation.Status == "succeeded" || invocation.Status == "success" || invocation.Status == "sent" {
			metrics.ConnectorSucceeded++
		} else {
			metrics.ConnectorFailed++
		}
	}
	if metrics.ConnectorCalls > 0 {
		metrics.AverageConnectorLatencyMS = connectorDuration / int64(metrics.ConnectorCalls)
	}
	for _, message := range outbox {
		if !eventIDs[strings.TrimSpace(message.EventID)] {
			continue
		}
		metrics.RetryCount += message.AttemptCount
		if message.Status == "dead_letter" || message.Status == "dead_lettered" {
			metrics.DeadLetterCount++
		}
	}
	return metrics
}

func executionTraceActions(trace map[string]any) []map[string]any {
	raw, _ := trace["actions"].([]any)
	if len(raw) == 0 {
		if typed, ok := trace["actions"].([]map[string]any); ok {
			return typed
		}
	}
	actions := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if typed, ok := item.(map[string]any); ok {
			actions = append(actions, typed)
		}
	}
	return actions
}
