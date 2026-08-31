package changeplan

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

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
	messages, err := s.runtime.ListPublicationMessages(ctx, "", "", 500, principal)
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
