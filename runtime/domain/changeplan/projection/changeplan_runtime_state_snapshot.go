package projection

import (
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	"sort"
	"strings"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func ProjectWorkflowProcesses(processes []workflowmodel.WorkflowProcessInstance) []WorkflowProcessSummary {
	items := []WorkflowProcessSummary{}
	for _, process := range processes {
		if process.Status != "running" && process.Status != "waiting" && process.Status != "configuration_error" {
			continue
		}
		items = append(items, WorkflowProcessSummary{ID: process.ID, WorkflowKey: process.WorkflowKey, DefinitionVersionID: process.DefinitionVersionID, DefinitionVersion: process.DefinitionVersion, DefinitionHash: process.DefinitionHash, ObjectKey: process.ObjectKey, RecordID: process.RecordID, Status: process.Status, CurrentNodeIDs: append([]string(nil), process.CurrentNodeIDs...), UpdatedAt: process.UpdatedAt})
	}
	return items
}

func ProjectIntegrationConnections(connections []integrationmodel.IntegrationConnection, ready func(integrationmodel.IntegrationConnection) bool) []IntegrationConnectionSummary {
	items := make([]IntegrationConnectionSummary, 0, len(connections))
	for _, connection := range connections {
		isReady := false
		if ready != nil {
			isReady = ready(connection)
		}
		items = append(items, IntegrationConnectionSummary{Key: connection.Key, ConnectorKey: connection.ConnectorKey, ProviderKey: connection.ProviderKey, Name: connection.Name, Status: connection.Status, Ready: isReady, UpdatedAt: connection.UpdatedAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items
}

func ProjectIntegrationOutbox(messages []integrationmodel.IntegrationOutboxMessage) []IntegrationOutboxSummary {
	items := make([]IntegrationOutboxSummary, 0, len(messages))
	for _, message := range messages {
		items = append(items, IntegrationOutboxSummary{ID: message.ID, ConnectorKey: message.ConnectorKey, ConnectionKey: message.ConnectionKey, Operation: message.Operation, Status: message.Status, AttemptCount: message.AttemptCount, NextAttemptAt: message.NextAttemptAt, UpdatedAt: message.UpdatedAt})
	}
	sort.Slice(items, func(i, j int) bool { return strings.Compare(items[i].UpdatedAt, items[j].UpdatedAt) > 0 })
	return items
}

type BusinessRuntimeStateSnapshot struct {
	RunningWorkflowProcesses []WorkflowProcessSummary                     `json:"running_workflow_processes"`
	AutomationRules          []automationmodel.AutomationRuleSchema       `json:"automation_rules"`
	RecentAutomationRuns     []automationmodel.AutomationRuleExecution    `json:"recent_automation_runs"`
	Scheduler                SchedulerStateSnapshot                       `json:"scheduler"`
	Reports                  []reportmodel.ReportSchema                   `json:"reports"`
	Connectors               []integrationmodel.ConnectorSchema           `json:"connectors"`
	Connections              []IntegrationConnectionSummary               `json:"connections"`
	RecentOutboxMessages     []IntegrationOutboxSummary                   `json:"recent_outbox_messages"`
	Idempotency              deploymentmodel.IdempotencyOperationalStatus `json:"idempotency"`
}

type WorkflowProcessSummary struct {
	ID                  string   `json:"id"`
	WorkflowKey         string   `json:"workflow_key"`
	DefinitionVersionID string   `json:"definition_version_id,omitempty"`
	DefinitionVersion   int      `json:"definition_version"`
	DefinitionHash      string   `json:"definition_hash,omitempty"`
	ObjectKey           string   `json:"object_key,omitempty"`
	RecordID            string   `json:"record_id,omitempty"`
	Status              string   `json:"status"`
	CurrentNodeIDs      []string `json:"current_node_ids,omitempty"`
	UpdatedAt           string   `json:"updated_at"`
}

type SchedulerStateSnapshot struct {
	Definitions []recordmodel.Record `json:"definitions"`
	RecentRuns  []recordmodel.Record `json:"recent_runs"`
	DeadLetters []recordmodel.Record `json:"dead_letters"`
}

type IntegrationConnectionSummary struct {
	Key          string `json:"key"`
	ConnectorKey string `json:"connector_key"`
	ProviderKey  string `json:"provider_key"`
	Name         string `json:"name,omitempty"`
	Status       string `json:"status"`
	Ready        bool   `json:"ready"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type IntegrationOutboxSummary struct {
	ID            string `json:"id"`
	ConnectorKey  string `json:"connector_key"`
	ConnectionKey string `json:"connection_key,omitempty"`
	Operation     string `json:"operation"`
	Status        string `json:"status"`
	AttemptCount  int    `json:"attempt_count"`
	NextAttemptAt string `json:"next_attempt_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

func (snapshot *BusinessRuntimeStateSnapshot) Normalize() {
	if snapshot.Idempotency.Backlog == nil {
		snapshot.Idempotency.Backlog = map[string]int{}
	}
	if snapshot.RunningWorkflowProcesses == nil {
		snapshot.RunningWorkflowProcesses = []WorkflowProcessSummary{}
	}
	if snapshot.AutomationRules == nil {
		snapshot.AutomationRules = []automationmodel.AutomationRuleSchema{}
	}
	if snapshot.RecentAutomationRuns == nil {
		snapshot.RecentAutomationRuns = []automationmodel.AutomationRuleExecution{}
	}
	if snapshot.Scheduler.Definitions == nil {
		snapshot.Scheduler.Definitions = []recordmodel.Record{}
	}
	if snapshot.Scheduler.RecentRuns == nil {
		snapshot.Scheduler.RecentRuns = []recordmodel.Record{}
	}
	if snapshot.Scheduler.DeadLetters == nil {
		snapshot.Scheduler.DeadLetters = []recordmodel.Record{}
	}
	if snapshot.Reports == nil {
		snapshot.Reports = []reportmodel.ReportSchema{}
	}
	if snapshot.Connectors == nil {
		snapshot.Connectors = []integrationmodel.ConnectorSchema{}
	}
	if snapshot.Connections == nil {
		snapshot.Connections = []IntegrationConnectionSummary{}
	}
	if snapshot.RecentOutboxMessages == nil {
		snapshot.RecentOutboxMessages = []IntegrationOutboxSummary{}
	}
	sort.Slice(snapshot.RunningWorkflowProcesses, func(i, j int) bool {
		return snapshot.RunningWorkflowProcesses[i].ID < snapshot.RunningWorkflowProcesses[j].ID
	})
	sort.Slice(snapshot.AutomationRules, func(i, j int) bool { return snapshot.AutomationRules[i].Key < snapshot.AutomationRules[j].Key })
	sort.Slice(snapshot.RecentAutomationRuns, func(i, j int) bool { return snapshot.RecentAutomationRuns[i].ID < snapshot.RecentAutomationRuns[j].ID })
	sortRecordsByID(snapshot.Scheduler.Definitions)
	sortRecordsByID(snapshot.Scheduler.RecentRuns)
	sortRecordsByID(snapshot.Scheduler.DeadLetters)
	sort.Slice(snapshot.Reports, func(i, j int) bool { return snapshot.Reports[i].Key < snapshot.Reports[j].Key })
	sort.Slice(snapshot.Connectors, func(i, j int) bool { return snapshot.Connectors[i].Key < snapshot.Connectors[j].Key })
}

func sortRecordsByID(records []recordmodel.Record) {
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
}
