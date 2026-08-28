package changeplan

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import (
	"context"
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func (s *ChangePlanReferenceApplicationService) addRuntimeEvidenceReferences(ctx context.Context, builder *changeplanprojection.ChangePlanReferenceGraphBuilder, principal principalmodel.Principal) error {
	if s.runtime == nil {
		return nil
	}
	processes, err := s.runtime.WorkflowProcesses(ctx, principal, workflowmodel.WorkflowProcessFilter{Limit: 500})
	if err != nil {
		return err
	}
	for _, process := range processes {
		if process.Status != "running" && process.Status != "waiting" && process.Status != "configuration_error" {
			continue
		}
		builder.Node("workflow_process", process.ID, process.ObjectKey, process.WorkflowName, "runtime")
		builder.Edge("workflow_process", process.ID, "workflow", process.WorkflowKey, "runs_workflow_snapshot", "workflow_key")
	}
	for _, objectKey := range []string{"job_run", "job_dead_letter"} {
		records, listErr := s.runtime.SnapshotObjectRecords(ctx, objectKey, principal, 500)
		if listErr != nil {
			return listErr
		}
		for _, record := range records {
			resourceType := "scheduler_run"
			if objectKey == "job_dead_letter" {
				resourceType = "scheduler_dead_letter"
			}
			builder.Node(resourceType, record.ID, "", strings.TrimSpace(fmt.Sprint(record.Data["status"])), "runtime")
			builder.Edge(resourceType, record.ID, "scheduler", strings.TrimSpace(fmt.Sprint(record.Data["scheduler_definition_key"])), "belongs_to_definition", "scheduler_definition_key")
		}
	}
	messages, err := s.runtime.ListIntegrationOutboxMessages(ctx, "", "", 500, principal)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if message.Status == "sent" || message.Status == "cancelled" {
			continue
		}
		builder.Node("outbox_message", message.ID, "", message.Status, "runtime")
		builder.Edge("outbox_message", message.ID, "connector_operation", message.ConnectorKey+"."+message.Operation, "delivers_operation", "operation")
		builder.Edge("outbox_message", message.ID, "connection", message.ConnectionKey, "uses_connection", "connection_key")
	}
	return nil
}
