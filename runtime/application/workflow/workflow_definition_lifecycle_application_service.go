package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowvalidation "github.com/domainry/domainry-runtime/runtime/domain/workflow/validation"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

// InitializePublishedWorkflowDefinitions projects the already-published
// Metadata snapshot into the Workflow execution registry. Authoring and
// publication are owned exclusively by the reviewed system change-plan flow.
func (s *WorkflowApplicationService) InitializePublishedWorkflowDefinitions(ctx context.Context, workflows []definitionmodel.WorkflowSchema, scope principalmodel.SystemScope) error {
	if err := workflowAuthorizeSystemCommand(scope); err != nil {
		return err
	}
	if s.definitionRepo == nil {
		return nil
	}
	existingDefinitions, err := s.definitionRepo.ListDefinitions(ctx)
	if err != nil {
		return fmt.Errorf("list workflow projections: %w", err)
	}
	existingByKey := make(map[string]workflowmodel.WorkflowDefinition, len(existingDefinitions))
	for _, definition := range existingDefinitions {
		existingByKey[definition.Key] = definition
	}
	activeWorkflows := make(map[string]definitionmodel.WorkflowSchema, len(workflows))
	for _, workflow := range workflows {
		activeWorkflows[workflow.Key] = workflow
		definition, found := existingByKey[workflow.Key]
		if !found {
			report := s.validateWorkflowDefinition(ctx, workflow)
			if !report.Valid {
				return fmt.Errorf("initialize workflow %s: validation failed: %#v", workflow.Key, report.Issues)
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			definition = workflowmodel.WorkflowDefinition{ID: workflowProjectionID(ctx, "workflow_definition"), Key: workflow.Key, Name: workflow.Name, Enabled: workflow.Enabled, CreatedAt: now, UpdatedAt: now}
			version := workflowmodel.WorkflowDefinitionVersion{ID: workflowProjectionID(ctx, "workflow_version"), DefinitionID: definition.ID, Version: 1, Status: workflowmodel.WorkflowVersionDraft, Revision: 1, Workflow: cloneWorkflowSchema(workflow), ValidationReport: report, CreatedBy: "system", CreatedAt: now, UpdatedAt: now}
			definition.CurrentDraftVersionID = version.ID
			if err := s.definitionRepo.InsertDefinition(ctx, definition, version); err != nil {
				return fmt.Errorf("seed workflow projection %s: %w", workflow.Key, err)
			}
			version.ContentHash, version.PublishedBy, version.PublishedAt = WorkflowDefinitionHash(version.Workflow), "system", now
			if published, err := s.definitionRepo.PublishDraft(ctx, definition, version, workflowMetadataPublishKey(version)); err != nil || !published {
				return fmt.Errorf("publish workflow projection %s: published=%t err=%w", workflow.Key, published, err)
			}
			continue
		}
		if err := s.synchronizePublishedWorkflowDefinition(ctx, definition, workflow); err != nil {
			return err
		}
	}
	definitions, err := s.definitionRepo.ListDefinitions(ctx)
	if err != nil {
		return fmt.Errorf("list published workflow projections: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for index := range definitions {
		workflow, active := activeWorkflows[definitions[index].Key]
		enabled := active && workflow.Enabled
		if definitions[index].Enabled == enabled {
			continue
		}
		updated, err := s.definitionRepo.SetDefinitionEnabled(ctx, definitions[index].ID, enabled, now)
		if err != nil {
			return fmt.Errorf("synchronize workflow projection %s enabled=%t: %w", definitions[index].Key, enabled, err)
		}
		if !updated {
			return fmt.Errorf("synchronize workflow projection %s enabled=%t: definition not found", definitions[index].Key, enabled)
		}
		definitions[index].Enabled = enabled
	}
	publishedWorkflows := make(map[string]definitionmodel.WorkflowSchema, len(definitions))
	versions, err := s.definitionRepo.ListVersions(ctx, "")
	if err != nil {
		return fmt.Errorf("list published workflow projection versions: %w", err)
	}
	versionsByID := make(map[string]workflowmodel.WorkflowDefinitionVersion, len(versions))
	for _, version := range versions {
		versionsByID[version.ID] = version
	}
	for _, definition := range definitions {
		if definition.CurrentPublishedVersionID == "" || !definition.Enabled {
			continue
		}
		published, found := versionsByID[definition.CurrentPublishedVersionID]
		if !found {
			return fmt.Errorf("load published workflow projection %s: published version %s not found", definition.Key, definition.CurrentPublishedVersionID)
		}
		published.Workflow.DefinitionVersionID = published.ID
		published.Workflow.PublishedVersion = published.Version
		publishedWorkflows[definition.Key] = published.Workflow
	}
	for _, existing := range s.registry.List() {
		s.registry.Delete(existing.Key)
	}
	for key, workflow := range publishedWorkflows {
		s.registry.Set(key, workflow)
	}
	return nil
}

func (s *WorkflowApplicationService) synchronizePublishedWorkflowDefinition(
	ctx context.Context,
	definition workflowmodel.WorkflowDefinition,
	workflow definitionmodel.WorkflowSchema,
) error {
	incomingHash := WorkflowDefinitionHash(workflow)
	if definition.CurrentPublishedVersionID != "" {
		published, found, err := s.definitionRepo.GetVersion(ctx, definition.CurrentPublishedVersionID)
		if err != nil {
			return fmt.Errorf("load current workflow projection %s: %w", workflow.Key, err)
		}
		if !found {
			return fmt.Errorf("load current workflow projection %s: published version %s not found", workflow.Key, definition.CurrentPublishedVersionID)
		}
		publishedHash := published.ContentHash
		if publishedHash == "" {
			publishedHash = WorkflowDefinitionHash(published.Workflow)
		}
		if publishedHash == incomingHash {
			return nil
		}
	}
	report := s.validateWorkflowDefinition(ctx, workflow)
	if !report.Valid {
		return fmt.Errorf("synchronize workflow %s: validation failed: %#v", workflow.Key, report.Issues)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var version workflowmodel.WorkflowDefinitionVersion
	if definition.CurrentDraftVersionID != "" {
		draft, found, err := s.definitionRepo.GetVersion(ctx, definition.CurrentDraftVersionID)
		if err != nil {
			return fmt.Errorf("load current workflow projection draft %s: %w", workflow.Key, err)
		}
		if !found || draft.CreatedBy != "system" || WorkflowDefinitionHash(draft.Workflow) != incomingHash {
			return fmt.Errorf("synchronize workflow projection %s: current draft %s blocks metadata publication", workflow.Key, definition.CurrentDraftVersionID)
		}
		version = draft
	} else {
		versions, err := s.definitionRepo.ListVersions(ctx, definition.ID)
		if err != nil {
			return fmt.Errorf("list workflow projection versions %s: %w", workflow.Key, err)
		}
		nextVersion := 1
		for _, existing := range versions {
			if existing.Version >= nextVersion {
				nextVersion = existing.Version + 1
			}
		}
		version = workflowmodel.WorkflowDefinitionVersion{
			ID:               workflowProjectionID(ctx, "workflow_version"),
			DefinitionID:     definition.ID,
			Version:          nextVersion,
			Status:           workflowmodel.WorkflowVersionDraft,
			Revision:         1,
			Workflow:         cloneWorkflowSchema(workflow),
			ValidationReport: report,
			CreatedBy:        "system",
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		created, err := s.definitionRepo.InsertDraftVersion(ctx, definition.ID, version)
		if err != nil {
			return fmt.Errorf("create workflow projection version %s: %w", workflow.Key, err)
		}
		if !created {
			return fmt.Errorf("create workflow projection version %s: current draft already exists", workflow.Key)
		}
	}
	version.ContentHash, version.PublishedBy, version.PublishedAt = incomingHash, "system", now
	definition.UpdatedAt = now
	if published, err := s.definitionRepo.PublishDraft(ctx, definition, version, workflowMetadataPublishKey(version)); err != nil || !published {
		return fmt.Errorf("publish synchronized workflow projection %s: published=%t err=%w", workflow.Key, published, err)
	}
	return nil
}

func workflowMetadataPublishKey(version workflowmodel.WorkflowDefinitionVersion) string {
	return fmt.Sprintf("metadata:%s:version:%d", version.ContentHash, version.Version)
}

func (s *WorkflowApplicationService) validateWorkflowDefinition(ctx context.Context, workflow definitionmodel.WorkflowSchema) workflowmodel.WorkflowValidation {
	report := emptyWorkflowValidation()
	report.ValidatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := workflowvalidation.WorkflowValidateGraph(workflow.Graph); err != nil {
		code, diagnostic := valueOrDefault(apperror.CodeOf(err), "backend.workflow.graph_invalid"), err.Error()
		report.Issues = append(report.Issues, workflowvalidation.WorkflowValidationIssueFromError(err, code, diagnostic))
	}
	report.Issues = append(report.Issues, s.referenceValidator.validateWorkflowReferences(ctx, workflow)...)
	report.Valid = len(report.Issues) == 0
	return report
}

func (s *WorkflowApplicationService) ValidateWorkflowDefinition(ctx context.Context, workflow definitionmodel.WorkflowSchema, principal principalmodel.Principal) (workflowmodel.WorkflowValidation, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return workflowmodel.WorkflowValidation{}, err
	}
	return s.validateWorkflowDefinition(ctx, workflow), nil
}

func emptyWorkflowValidation() workflowmodel.WorkflowValidation {
	return workflowmodel.WorkflowValidation{Issues: []workflowmodel.WorkflowValidationIssue{}}
}

func cloneWorkflowSchema(workflow definitionmodel.WorkflowSchema) definitionmodel.WorkflowSchema {
	payload, _ := json.Marshal(workflow)
	var cloned definitionmodel.WorkflowSchema
	_ = json.Unmarshal(payload, &cloned)
	return cloned
}

func workflowProjectionID(ctx context.Context, prefix string) string {
	_ = ctx
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
