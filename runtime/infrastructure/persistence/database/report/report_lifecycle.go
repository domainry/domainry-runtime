package report

import (
	"context"
	"encoding/json"
	"time"

	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

type lifecycleExecutor struct {
	relational         lifecyclecontract.OwnerLifecycleExecutor
	relationalPolicies map[string]bool
	repository         agentrepository.AgentLifecycleRepository
	archives           lifecyclecontract.ArchiveWriter
}

const reportStateResource = "agent.state"

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore, repository agentrepository.AgentLifecycleRepository, objects ...definitionmodel.ObjectSchema) lifecyclecontract.OwnerLifecycleExecutor {
	available := make(map[string]bool, len(objects))
	for _, object := range objects {
		available[object.Key] = true
	}
	specs := []lifecyclepersistence.RelationalCleanupSpec{}
	if available["download_task"] {
		specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "report.download.v1", Table: "download_task", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"expired", "failed", "cancelled"}})
	}
	if available["report_export_audit"] {
		specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "report.export.v1", Table: "report_export_audit", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"completed", "failed", "cancelled"}, RetentionGroup: "succeeded", ReferenceChecks: []lifecyclepersistence.RelationalReferenceCheck{{Table: "download_task", TenantColumn: "workspace_id", ReferenceColumn: "report_export_audit"}}})
	}
	if available["report_query_run"] {
		specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "report.export.v1", Table: "report_query_run", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"completed", "failed", "cancelled"}, RetentionGroup: "succeeded", ReferenceChecks: []lifecyclepersistence.RelationalReferenceCheck{{Table: "report_export_audit", TenantColumn: "workspace_id", ReferenceColumn: "report_query_run"}}})
	}
	policies := make(map[string]bool, len(specs))
	for _, spec := range specs {
		policies[spec.PolicyKey] = true
	}
	return lifecycleExecutor{relational: lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "report", specs...), relationalPolicies: policies, repository: repository, archives: archives}
}

func (lifecycleExecutor) Owner(context.Context) string { return "report" }

func (e lifecycleExecutor) Preview(ctx context.Context, workspaceID string, policy lifecyclemodel.PolicyVersion, now time.Time) (lifecyclecontract.CleanupPreview, error) {
	result := lifecyclecontract.CleanupPreview{}
	if e.relationalPolicies[policy.Policy.Key] {
		var err error
		result, err = e.relational.Preview(ctx, workspaceID, policy, now)
		if err != nil {
			return result, err
		}
	}
	states, err := e.states(ctx, workspaceID, policy.Policy, now, 0)
	if err != nil {
		return result, err
	}
	result.Rows += int64(len(states))
	result.Bytes += int64(len(states)) * 1024
	for _, state := range states {
		updated := time.Unix(0, state.State.UpdatedAt).UTC()
		if result.OldestEligible.IsZero() || updated.Before(result.OldestEligible) {
			result.OldestEligible = updated
		}
	}
	return result, nil
}

func (e lifecycleExecutor) ProcessBatch(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, holds []lifecyclemodel.LegalHold, batchSize int) (lifecyclemodel.CleanupBatchResult, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	result := lifecyclemodel.CleanupBatchResult{Done: true}
	if e.relationalPolicies[policy.Policy.Key] {
		var err error
		result, err = e.relational.ProcessBatch(ctx, job, policy, holds, batchSize)
		if err != nil {
			return result, err
		}
	}
	remaining := batchSize - int(result.Scanned)
	if remaining <= 0 {
		result.Done = false
		return result, nil
	}
	states, err := e.states(ctx, job.WorkspaceID, policy.Policy, job.UpdatedAt, remaining)
	if err != nil {
		return result, err
	}
	if len(states) >= remaining {
		result.Done = false
	}
	for _, state := range states {
		resourceID := state.ResourceID
		result.Scanned++
		result.Checkpoint = reportStateResource + ":" + resourceID
		updated := time.Unix(0, state.State.UpdatedAt).UTC()
		if result.OldestEligible.IsZero() || updated.Before(result.OldestEligible) {
			result.OldestEligible = updated
		}
		if reportLifecycleHeld(holds, reportStateResource, resourceID, job.UpdatedAt) {
			result.Skipped++
			continue
		}
		if job.Operation == lifecyclemodel.OperationPurge {
			referenced, referenceErr := e.repository.LifecycleCandidateReferenced(ctx, job.WorkspaceID, state)
			if referenceErr != nil {
				return result, referenceErr
			}
			if referenced {
				result.Skipped++
				continue
			}
		}
		if job.DryRun {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"workspace_id": job.WorkspaceID, "kind": state.State.Kind, "state_key": state.State.Key, "user_id": state.State.UserID, "role_key": state.State.RoleKey, "payload_json": json.RawMessage(state.State.Payload), "updated_at": state.State.UpdatedAt})
		archived, archiveErr := e.archives.ArchivePayload(ctx, "report", job, policy, reportStateResource, resourceID, raw)
		if archiveErr != nil {
			result.Failed++
			return result, archiveErr
		}
		if archived {
			result.Archived++
		}
		if job.Operation == lifecyclemodel.OperationArchive {
			continue
		}
		deleted, deleteErr := e.repository.DeleteLifecycleCandidate(ctx, job.WorkspaceID, state)
		if deleteErr != nil {
			result.Failed++
			return result, deleteErr
		}
		if deleted {
			result.Purged++
		}
	}
	return result, nil
}

func (e lifecycleExecutor) states(ctx context.Context, workspaceID string, policy lifecyclemodel.RetentionPolicy, now time.Time, limit int) ([]agentrepository.LifecycleCandidate, error) {
	if e.repository == nil {
		return nil, nil
	}
	return e.repository.ListLifecycleCandidates(ctx, workspaceID, agentrepository.LifecycleQuery{Owner: "report", PolicyKey: policy.Key, Now: now, Retention: policy.DefaultRetention, StatusRetention: policy.StatusRetention, Limit: limit})
}

func reportLifecycleHeld(holds []lifecyclemodel.LegalHold, resourceType, resourceID string, now time.Time) bool {
	for _, hold := range holds {
		if now.Before(hold.StartsAt) || (hold.EndsAt != nil && !now.Before(*hold.EndsAt)) {
			continue
		}
		if (hold.Owner == "" || hold.Owner == "report") && (hold.ResourceType == "" || hold.ResourceType == resourceType) && (hold.ResourceID == "" || hold.ResourceID == resourceID) {
			return true
		}
	}
	return false
}
