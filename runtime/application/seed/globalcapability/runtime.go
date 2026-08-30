package globalcapabilityseed

import (
	auditrepository "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulerprojection "github.com/domainry/domainry-runtime/runtime/domain/scheduler/projection"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	"github.com/domainry/domainry-foundation/requestcontext"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowrepository "github.com/domainry/domainry-runtime/runtime/domain/workflow/repository"

	"context"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
)

func WithGeneratedSchema(manifest manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
	systemObjects := schedulerprojection.SchedulerSystemObjects()
	manifest.Objects = MergeSystemObjects(manifest.Objects, systemObjects)
	manifest.Dictionaries = MergeDictionaries(manifest.Dictionaries, generatedGlobalDictionaries())
	manifest.Workflows = MergeWorkflows(manifest.Workflows, generatedGlobalWorkflows())
	return manifest
}

func MergeSystemObjects(existing []definitionmodel.ObjectSchema, generated []definitionmodel.ObjectSchema) []definitionmodel.ObjectSchema {
	reserved := make(map[string]bool, len(generated))
	for _, object := range generated {
		reserved[strings.TrimSpace(object.Key)] = true
	}
	out := make([]definitionmodel.ObjectSchema, 0, len(existing)+len(generated))
	for _, object := range existing {
		if !reserved[strings.TrimSpace(object.Key)] {
			out = append(out, object)
		}
	}
	return append(out, generated...)
}

func MergeDictionaries(existing []appschemamodel.DictionarySchema, generated []appschemamodel.DictionarySchema) []appschemamodel.DictionarySchema {
	out := append([]appschemamodel.DictionarySchema(nil), existing...)
	seen := map[string]struct{}{}
	for _, dictionary := range out {
		if key := strings.TrimSpace(dictionary.Key); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, dictionary := range generated {
		key := strings.TrimSpace(dictionary.Key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, dictionary)
	}
	return out
}

func MergeWorkflows(existing []definitionmodel.WorkflowSchema, generated []definitionmodel.WorkflowSchema) []definitionmodel.WorkflowSchema {
	out := append([]definitionmodel.WorkflowSchema(nil), existing...)
	seen := map[string]struct{}{}
	for _, workflow := range out {
		if key := strings.TrimSpace(workflow.Key); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, workflow := range generated {
		key := strings.TrimSpace(workflow.Key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, workflow)
	}
	return out
}

func generatedGlobalDictionaries() []appschemamodel.DictionarySchema {
	return []appschemamodel.DictionarySchema{
		{
			Key:         "platform_operation_status",
			Name:        "Platform operation status",
			Description: "Default status values used by global system capability pages.",
			Source:      "platform",
			Items: []appschemamodel.DictionaryItemSchema{
				{Key: "enabled", Value: "enabled", Label: "Enabled", SortOrder: 10, Status: "active", Color: "green"},
				{Key: "disabled", Value: "disabled", Label: "Disabled", SortOrder: 20, Status: "active", Color: "slate"},
				{Key: "warning", Value: "warning", Label: "Warning", SortOrder: 30, Status: "active", Color: "amber"},
				{Key: "failed", Value: "failed", Label: "Failed", SortOrder: 40, Status: "active", Color: "red"},
			},
		},
		{
			Key:         "workflow_execution_status",
			Name:        "Workflow execution status",
			Description: "Default workflow execution lifecycle values for the global workflow console.",
			Source:      "platform",
			Items: []appschemamodel.DictionaryItemSchema{
				{Key: "queued", Value: "queued", Label: "Queued", SortOrder: 10, Status: "active"},
				{Key: "running", Value: "running", Label: "Running", SortOrder: 20, Status: "active"},
				{Key: "completed", Value: "completed", Label: "Completed", SortOrder: 30, Status: "active"},
				{Key: "failed", Value: "failed", Label: "Failed", SortOrder: 40, Status: "active"},
				{Key: "dead_lettered", Value: "dead_lettered", Label: "Dead lettered", SortOrder: 50, Status: "active"},
			},
		},
		{
			Key:         "audit_event_category",
			Name:        "Audit event category",
			Description: "Default categories shown by audit and operations surfaces.",
			Source:      "platform",
			Items: []appschemamodel.DictionaryItemSchema{
				{Key: "access", Value: "access", Label: "Access", SortOrder: 10, Status: "active"},
				{Key: "metadata", Value: "metadata", Label: "Metadata", SortOrder: 20, Status: "active"},
				{Key: "workflow", Value: "workflow", Label: "Workflow", SortOrder: 30, Status: "active"},
				{Key: "import_export", Value: "import_export", Label: "Import / export", SortOrder: 40, Status: "active"},
				{Key: "operations", Value: "operations", Label: "Operations", SortOrder: 50, Status: "active"},
			},
		},
	}
}

func generatedGlobalWorkflows() []definitionmodel.WorkflowSchema {
	return []definitionmodel.WorkflowSchema{
		{
			Key:               "platform.audit_retention_check",
			Name:              "Audit retention check",
			Enabled:           true,
			Trigger:           map[string]any{"type": "manual"},
			TriggerContract:   &definitionmodel.WorkflowTriggerContract{Type: "manual"},
			Condition:         map[string]any{"type": "always"},
			ConditionContract: &definitionmodel.WorkflowConditionContract{Type: "always"},
			Action:            map[string]any{"type": "workflow_graph"},
			Graph:             &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "manual_trigger", Type: "trigger", Name: "Run audit retention check"}}},
			Retry:             &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 3, DelaySeconds: 60},
			AuditEvent:        "platform_audit_retention_checked",
		},
		{
			Key:               "platform.metadata_health_check",
			Name:              "Metadata health check",
			Enabled:           true,
			Trigger:           map[string]any{"type": "manual"},
			TriggerContract:   &definitionmodel.WorkflowTriggerContract{Type: "manual"},
			Condition:         map[string]any{"type": "always"},
			ConditionContract: &definitionmodel.WorkflowConditionContract{Type: "always"},
			Action:            map[string]any{"type": "workflow_graph"},
			Graph:             &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "manual_trigger", Type: "trigger", Name: "Run metadata health check"}}},
			Retry:             &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 3, DelaySeconds: 60},
			AuditEvent:        "platform_metadata_health_checked",
		},
	}
}

func SyncRows(ctx context.Context, auditStore auditrepository.AuditEventRepository, workflowStore workflowrepository.WorkflowExecutionRepository) error {
	if auditStore == nil || workflowStore == nil {
		return nil
	}
	if requestcontext.WorkspaceID(ctx) == "" {
		ctx = requestcontext.WithWorkspaceID(ctx, principalmodel.InstallationWorkspaceID)
	}
	if events, err := auditStore.ListAuditEvents(ctx, principalmodel.InstallationWorkspaceID, auditmodel.AuditEventQuery{Limit: 1}); err != nil {
		return err
	} else if len(events) == 0 {
		if err := insertGeneratedAuditEvents(ctx, auditStore); err != nil {
			return err
		}
	}
	if executions, err := workflowStore.ListExecutions(ctx, principalmodel.InstallationWorkspaceID, 1); err != nil {
		return err
	} else if len(executions) == 0 {
		if err := insertGeneratedWorkflowExecutions(ctx, workflowStore); err != nil {
			return err
		}
	}
	return nil
}

func insertGeneratedAuditEvents(ctx context.Context, auditStore auditrepository.AuditEventRepository) error {
	now := time.Now().UTC()
	events := []auditmodel.AuditEvent{
		{
			ID:          "platform_seed_audit_bootstrap",
			WorkspaceID: principalmodel.InstallationWorkspaceID,
			Event:       "platform_seed_initialized",
			ObjectKey:   "platform",
			RecordID:    "global_capability_seed",
			ActorID:     "system",
			Summary:     "Initialized global platform capability seed data.",
			Metadata:    map[string]any{"source": "runtime_bootstrap", "capability": "global_system"},
			CreatedAt:   now.Add(-2 * time.Minute).Format(time.RFC3339),
		},
	}
	for _, event := range events {
		if err := auditStore.InsertAuditEvent(ctx, event.WorkspaceID, event); err != nil {
			return err
		}
	}
	return nil
}

func insertGeneratedWorkflowExecutions(ctx context.Context, workflowStore workflowrepository.WorkflowExecutionRepository) error {
	now := time.Now().UTC()
	executions := []workflowmodel.WorkflowExecution{
		{
			WorkspaceID:    principalmodel.InstallationWorkspaceID,
			ID:             "platform_seed_workflow_execution_completed",
			WorkflowKey:    "platform.metadata_health_check",
			Name:           "Metadata health check",
			Trigger:        "manual",
			Status:         "completed",
			ActionType:     "emit_audit",
			Action:         map[string]any{"type": "emit_audit", "event": "platform_metadata_health_checked"},
			Payload:        map[string]any{"source": "runtime_bootstrap"},
			Result:         map[string]any{"checked": true, "warnings": 0},
			ActorID:        "system",
			RunAs:          "admin",
			IdempotencyKey: "platform-seed-metadata-health-check",
			Attempt:        1,
			MaxAttempts:    3,
			Message:        "Seeded metadata health check completed.",
			CreatedAt:      now.Add(-45 * time.Second).Format(time.RFC3339),
			UpdatedAt:      now.Add(-45 * time.Second).Format(time.RFC3339),
		},
		{
			WorkspaceID:    principalmodel.InstallationWorkspaceID,
			ID:             "platform_seed_workflow_execution_warning",
			WorkflowKey:    "platform.audit_retention_check",
			Name:           "Audit retention check",
			Trigger:        "manual",
			Status:         "failed",
			ActionType:     "emit_audit",
			Action:         map[string]any{"type": "emit_audit", "event": "platform_audit_retention_checked"},
			Payload:        map[string]any{"source": "runtime_bootstrap"},
			Result:         map[string]any{"checked": true, "needs_review": true},
			ActorID:        "system",
			RunAs:          "admin",
			IdempotencyKey: "platform-seed-audit-retention-check",
			Attempt:        1,
			MaxAttempts:    3,
			LastError:      "Seeded example failure for retry visibility.",
			Message:        "Seeded audit retention check needs review.",
			CreatedAt:      now.Add(-30 * time.Second).Format(time.RFC3339),
			UpdatedAt:      now.Add(-30 * time.Second).Format(time.RFC3339),
		},
	}
	for _, execution := range executions {
		if err := workflowStore.InsertExecution(ctx, principalmodel.InstallationWorkspaceID, execution); err != nil {
			return err
		}
	}
	return nil
}
