package integration

// This file coordinates Integration event mappings with Record mutations.

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"

	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

// ExecuteIntegrationEventMapping is a cross-domain dispatch boundary: it
// selects the Integration-owned mapping, then invokes Workflow, Action, or the
// Record-backed owner-task composition and records the shared audit result.
func (s *IntegrationApplicationService) ExecuteIntegrationEventMapping(ctx context.Context, event integrationmodel.IntegrationEvent, principal principalmodel.Principal) (EventProcessDecision, bool, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return EventProcessDecision{}, false, err
	}
	plan, handled, err := s.PlanEventMappingExecution(event)
	if err != nil || !handled {
		return EventProcessDecision{}, handled, err
	}
	mapping := plan.Mapping
	externalIdentity := plan.ExternalIdentity
	payload := plan.Payload
	switch plan.TargetType {
	case "workflow":
		result, err := s.executeEventWorkflow(ctx, plan.WorkflowKey, integrationmodel.IntegrationEntrypointWorkflowRequest{
			ExternalIdentity: externalIdentity,
			Payload:          payload,
		}, principal)
		if err != nil {
			return EventProcessDecision{}, true, err
		}
		s.audit(ctx, "integration_event_mapping_executed", "integration_event", event.ID, principal, "Integration event mapping executed "+mapping.Key, integrationprojection.IntegrationEventAuditShape(event), nil, map[string]any{
			"mapping_key":  mapping.Key,
			"provider":     event.Provider,
			"event_type":   event.EventType,
			"target_type":  "workflow",
			"workflow_key": result.Workflow.WorkflowKey,
			"status":       result.Workflow.Status,
		})
		return EventProcessDecision{Status: "processed"}, true, nil
	case "action":
		result, err := s.executeEventAction(ctx, plan.ObjectKey, plan.RecordID, plan.ActionKey, integrationmodel.IntegrationEntrypointActionRequest{
			ExternalIdentity: externalIdentity,
			Data:             payload,
			IdempotencyKey:   event.ID,
		}, principal)
		if err != nil {
			return EventProcessDecision{}, true, err
		}
		s.audit(ctx, "integration_event_mapping_executed", "integration_event", event.ID, principal, "Integration event mapping executed "+mapping.Key, integrationprojection.IntegrationEventAuditShape(event), nil, map[string]any{
			"mapping_key": mapping.Key,
			"provider":    event.Provider,
			"event_type":  event.EventType,
			"target_type": "action",
			"object_key":  result.Action.ObjectKey,
			"record_id":   result.Action.RecordID,
			"action_key":  result.Action.ActionKey,
		})
		return EventProcessDecision{Status: "processed"}, true, nil
	default: // PlanEventMappingExecution has already restricted the target to owner_task here.
		resolved, resolvedPrincipal, err := s.resolveEventIdentity(ctx, externalIdentity, principal)
		if err != nil {
			return EventProcessDecision{}, true, err
		}
		taskPayload := integrationprojection.IntegrationEntrypointPayload(payload, resolved)
		task, related, err := s.createIntegrationOwnerTask(ctx, event, mapping, taskPayload, resolvedPrincipal)
		if err != nil {
			s.audit(ctx, "integration_event_owner_task_denied", "integration_event", event.ID, resolvedPrincipal, "Integration event owner task denied "+mapping.Key, integrationprojection.IntegrationEventAuditShape(event), nil, integrationprojection.IntegrationEntrypointAuditMetadata(resolved, map[string]any{
				"mapping_key": mapping.Key,
				"provider":    event.Provider,
				"event_type":  event.EventType,
				"target_type": "owner_task",
				"error_code":  stableIntegrationFailureCode(err, "backend.integration.event.owner_task_denied"),
			}))
			return EventProcessDecision{}, true, err
		}
		s.audit(ctx, "integration_event_owner_task_created", "integration_event", event.ID, resolvedPrincipal, "Integration event owner task created "+task.ID, integrationprojection.IntegrationEventAuditShape(event), map[string]any{
			"task_id": task.ID,
			"data":    task.Data,
		}, integrationprojection.IntegrationEntrypointAuditMetadata(resolved, map[string]any{
			"mapping_key":          mapping.Key,
			"provider":             event.Provider,
			"event_type":           event.EventType,
			"target_type":          "owner_task",
			"task_id":              task.ID,
			"related_customer":     related["related_customer"],
			"related_contact":      related["related_contact"],
			"related_opportunity":  related["related_opportunity"],
			"raw_payload_redacted": true,
		}))
		return EventProcessDecision{Status: "processed"}, true, nil
	}
}

// createIntegrationOwnerTask performs only repository composition: obtain the
// Activity schema, optionally resolve Record relations, ask Integration to
// project the payload, and create the Record.
func (s *IntegrationApplicationService) createIntegrationOwnerTask(ctx context.Context, event integrationmodel.IntegrationEvent, mapping integrationmodel.IntegrationEventMappingSchema, payload map[string]any, principal principalmodel.Principal) (recordmodel.Record, map[string]string, error) {
	activity := s.schemaMap(ctx)["activity"]
	related := IntegrationEnrichOwnerTaskReferences(ctx, payload, func(ctx context.Context, objectKey, fieldKey, value string) (recordmodel.Record, bool) {
		return s.IntegrationFindFirstRecordByField(ctx, objectKey, fieldKey, value, principal)
	})
	data, err := IntegrationBuildOwnerTaskProjection(IntegrationOwnerTaskProjectionRequest{
		Activity: activity, Event: event, Mapping: mapping, Payload: payload, Principal: principal, Related: related, Now: time.Now(),
	})
	if err != nil {
		return recordmodel.Record{}, related, err
	}
	task, err := s.eventRecords.CreateRecord(ctx, "activity", data, principal)
	if err != nil {
		return recordmodel.Record{}, related, err
	}
	return task, related, nil
}

// integrationFindFirstRecordByField is the narrow Record query port used by
// owner-task relation enrichment; it contains no projection policy.
func (s *IntegrationApplicationService) IntegrationFindFirstRecordByField(ctx context.Context, objectKey string, fieldKey string, value string, principal principalmodel.Principal) (recordmodel.Record, bool) {
	if integrationAuthorizeQuery(principal) != nil {
		return recordmodel.Record{}, false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return recordmodel.Record{}, false
	}
	page, err := s.eventRecords.ListRecords(ctx, objectKey, recordmodel.RecordListQuery{Page: 1, PageSize: 1, Filters: map[string]any{fieldKey: value}}, principal)
	if err != nil || len(page.Items) == 0 {
		return recordmodel.Record{}, false
	}
	return page.Items[0], true
}
