package action

import (
	"context"
	"fmt"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func (e *businessActionExecution) AcquireSynchronousConnectorCall(requested runtimeext.ActionConnectorCapability) (runtimeext.SynchronousConnectorCallLease, error) {
	if e == nil {
		return nil, apperror.New(apperror.KindInternal, runtimeext.ConnectorActionExecutionRequiredErrorCode, nil, nil)
	}
	if requested.Mode != runtimeext.ConnectorModeCall || requested.Effect != runtimeext.ConnectorEffectRead {
		return nil, apperror.New(apperror.KindForbidden, runtimeext.ConnectorActionSideEffectOutboxErrorCode, nil, map[string]string{"connector": requested.ConnectorKey, "operation": requested.OperationKey})
	}
	if !e.hasConnectorGrant(requested) {
		return nil, apperror.New(apperror.KindForbidden, runtimeext.ConnectorActionGrantDeniedErrorCode, nil, map[string]string{"connector": requested.ConnectorKey, "operation": requested.OperationKey})
	}
	return e.unitOfWork.acquireSynchronousConnectorCall()
}

func (e *businessActionExecution) hasConnectorGrant(requested runtimeext.ActionConnectorCapability) bool {
	if !requested.Valid() {
		return false
	}
	for _, grant := range e.connectorGrants {
		if grant == requested {
			return true
		}
	}
	return false
}

func (e *businessActionExecution) StageDurableIntent(ctx context.Context, intent runtimeext.DurableIntent) (runtimeext.DurableIntentReceipt, error) {
	if !intent.Valid() {
		return runtimeext.DurableIntentReceipt{}, apperror.New(apperror.KindBadRequest, "backend.action.durable_intent_invalid", nil, nil)
	}
	grant, ok := e.durableIntentGrant(intent)
	if !ok {
		return runtimeext.DurableIntentReceipt{}, apperror.New(apperror.KindForbidden, runtimeext.ConnectorActionGrantDeniedErrorCode, nil, map[string]string{"connector": intent.ConsumerKey, "operation": intent.OperationKey})
	}
	if grant.Mode == runtimeext.ConnectorModeCall || grant.Effect == runtimeext.ConnectorEffectRead {
		return runtimeext.DurableIntentReceipt{}, apperror.New(apperror.KindForbidden, runtimeext.ConnectorActionSideEffectOutboxErrorCode, nil, map[string]string{"connector": intent.ConsumerKey, "operation": intent.OperationKey})
	}
	if e.dependencies.ValidateDurableIntent == nil {
		return runtimeext.DurableIntentReceipt{}, apperror.New(apperror.KindInternal, "backend.action.durable_intent_validator_required", nil, nil)
	}
	if err := e.dependencies.ValidateDurableIntent(e.unitOfWork.executionContext(ctx), intent, e.invocation.Principal); err != nil {
		return runtimeext.DurableIntentReceipt{}, err
	}
	intent.IntentID = fmt.Sprintf("durable_intent:%s:%d", e.identity.ExecutionID, len(e.intents))
	e.intents = append(e.intents, intent)
	return runtimeext.DurableIntentReceipt{ID: intent.IntentID}, nil
}

func (e *businessActionExecution) durableIntentGrant(intent runtimeext.DurableIntent) (runtimeext.ActionConnectorCapability, bool) {
	for _, grant := range e.connectorGrants {
		if grant.ConnectorKey == intent.ConsumerKey && grant.ConnectionKey == intent.ConnectionKey && grant.OperationKey == intent.OperationKey && grant.ContractSHA256 == intent.ContractSHA256 {
			return grant, true
		}
	}
	return runtimeext.ActionConnectorCapability{}, false
}

func (e *businessActionExecution) canonicalCommits() ([]transactionmodel.RecordMutationCommit, error) {
	commits := make([]transactionmodel.RecordMutationCommit, 0, len(e.plans)+len(e.setCommits))
	for _, plan := range e.plans {
		commits = append(commits, plan.CanonicalCommit())
	}
	commits = append(commits, e.setCommits...)
	if err := e.validateMutationTarget(e.plans); err != nil {
		return nil, err
	}
	if len(e.intents) > 0 && len(commits) == 0 {
		return nil, apperror.New(apperror.KindBadRequest, "backend.action.durable_intent_requires_business_mutation", nil, nil)
	}
	if len(e.notifications) > 0 && len(commits) == 0 {
		return nil, apperror.New(apperror.KindBadRequest, "backend.notification.action_requires_business_mutation", nil, nil)
	}
	for index, intent := range e.intents {
		dedupKey := strings.TrimSpace(intent.IdempotencyKey)
		if dedupKey == "" {
			dedupKey = runtimeext.DurableIntentBatchEntryKey(intent.Payload)
		}
		if dedupKey == "" {
			dedupKey = fmt.Sprintf("%s:intent:%d", e.identity.ExecutionID, index)
		}
		commits[len(commits)-1].Outbox = append(commits[len(commits)-1].Outbox, publicationmodel.Message{
			ID:          intent.IntentID,
			WorkspaceID: e.workspace.ID, ConnectorKey: intent.ConsumerKey, ConnectionKey: intent.ConnectionKey, Operation: intent.OperationKey,
			Payload: intent.Payload, DedupKey: dedupKey, RequestFingerprint: intent.ContractSHA256,
			RequestRef: e.identity.ExecutionID, CreatedBy: e.principal.UserID,
		})
	}
	if len(e.notifications) > 0 {
		commits[len(commits)-1].NotificationEvents = append(commits[len(commits)-1].NotificationEvents, e.notifications...)
		for index, event := range e.notifications {
			commits[len(commits)-1].Audits = append(commits[len(commits)-1].Audits, auditmodel.AuditEvent{
				ID: fmt.Sprintf("notification_audit:%s:%d", e.identity.ExecutionID, index), WorkspaceID: e.workspace.ID,
				Event: runtimeext.NotificationDispatchOperationKey, ObjectKey: event.SubjectType, RecordID: event.SubjectID,
				ActorID: e.principal.UserID, RoleKey: e.principal.RoleKey, Summary: "Staged governed in-app notification",
				Metadata: map[string]any{"event_type": event.EventType, "source_event_id": event.SourceEventID, "subject_version": event.SubjectVersion, "recipient_count": len(event.RecipientUserIDs)}, CreatedAt: event.OccurredAt,
			})
		}
	}
	return attachWorkflowStarts(commits, e.workflowStarts)
}

func (e *businessActionExecution) ApplyRecordMutation(ctx context.Context, mutation runtimeext.RecordMutation) (runtimeext.RecordMutationResult, error) {
	if !mutation.Valid() {
		return runtimeext.RecordMutationResult{}, apperror.New(apperror.KindBadRequest, "backend.action.mutation_invalid", nil, nil)
	}
	if !actionEffectAllows(e.action.EffectSet, mutation.ObjectKey, true) {
		return runtimeext.RecordMutationResult{}, apperror.New(apperror.KindForbidden, "backend.action.effect_authority_denied", nil, map[string]string{"object": mutation.ObjectKey})
	}
	if e.targetGrant != nil && !e.targetResolved {
		return runtimeext.RecordMutationResult{}, apperror.New(apperror.KindBadRequest, "backend.action.target_organization_unresolved", nil, nil)
	}
	var err error
	// Create planning may validate Identity relations through a separately
	// composed module which shares the Runtime SQLite pool. Do not retain the
	// pool's single connection before those read-only validations complete.
	// Commit (or a later locking mutation) opens the physical transaction.
	if mutation.Operation == runtimeext.MutationCreate {
		ctx, err = e.unitOfWork.beginDeferredWriting(ctx)
	} else {
		ctx, err = e.unitOfWork.beginWriting(ctx)
	}
	if err != nil {
		return runtimeext.RecordMutationResult{}, err
	}
	actionResource, actionOperation := definitionmodel.ActionPermissionSubject(e.action)
	ctx = recordmutation.WithMutationInvocation(ctx, recordmutation.MutationInvocation{
		Source: transactionmodel.MutationSourceAction, ActionKey: e.action.Key, IdempotencyKey: e.invocation.IdempotencyKey,
		ActionResource: actionResource, ActionOperation: actionOperation,
		EffectAuthority: actionEffectAuthority(e.action.EffectSet), AssuranceEvidence: e.invocation.AssuranceEvidence,
		WorkflowTriggers:     []string{"action_executed:" + e.action.Key},
		TargetOrganizationID: e.targetOrganization.ID,
	})
	ctx = e.withPlannedRelationRecords(ctx)
	var plans []transactionmodel.MutationPlan
	var record recordmodel.Record
	switch mutation.Operation {
	case runtimeext.MutationCreate:
		if e.dependencies.PlanCreateMutation == nil {
			return runtimeext.RecordMutationResult{}, missingExecutorPort("plan_create")
		}
		var plan transactionmodel.MutationPlan
		plan, record, err = e.dependencies.PlanCreateMutation(ctx, mutation.ObjectKey, mutation.Fields, "", e.actionMutationPrincipal())
		plans = append(plans, plan)
	case runtimeext.MutationUpdate:
		if e.dependencies.PlanUpdateMutation == nil {
			return runtimeext.RecordMutationResult{}, missingExecutorPort("plan_update")
		}
		var plan transactionmodel.MutationPlan
		plan, record, err = e.dependencies.PlanUpdateMutation(ctx, mutation.ObjectKey, mutation.RecordID, mutation.Fields, e.actionMutationPrincipal())
		plans = append(plans, plan)
	case runtimeext.MutationConditionalUpdate:
		plans, record, err = e.planConditionalUpdate(ctx, mutation)
	case runtimeext.MutationDelete:
		if e.dependencies.PlanDeleteMutation == nil {
			return runtimeext.RecordMutationResult{}, missingExecutorPort("plan_delete")
		}
		plans, err = e.dependencies.PlanDeleteMutation(ctx, mutation.ObjectKey, mutation.RecordID, mutation.ExpectedUpdatedAt, e.actionMutationPrincipal())
		record = recordmodel.Record{ID: mutation.RecordID}
	case runtimeext.MutationRestore:
		if e.dependencies.PlanRestoreMutation == nil {
			return runtimeext.RecordMutationResult{}, missingExecutorPort("plan_restore")
		}
		var plan transactionmodel.MutationPlan
		plan, record, err = e.dependencies.PlanRestoreMutation(ctx, mutation.ObjectKey, mutation.RecordID, mutation.ExpectedUpdatedAt, e.actionMutationPrincipal())
		plans = append(plans, plan)
	}
	if err != nil {
		return runtimeext.RecordMutationResult{}, err
	}
	if err := e.validateMutationTarget(plans); err != nil {
		return runtimeext.RecordMutationResult{}, err
	}
	// The canonical plan is the persistence authority. A planner also returns a
	// convenience record, but it must not become a second version source: in
	// particular, notification staging runs before the final transaction commit
	// and needs the exact UpdatedAt that the commit will persist.
	if canonical, ok := canonicalMutationResultRecord(mutation, plans); ok {
		record = canonical
	}
	e.plans = append(e.plans, plans...)
	if e.mutatedRecords == nil {
		e.mutatedRecords = map[string]recordmodel.Record{}
	}
	e.mutatedRecords[strings.TrimSpace(mutation.ObjectKey)+"\x00"+strings.TrimSpace(record.ID)] = record
	e.trackRecordMutation(mutation.Operation, mutation.ObjectKey, record.ID)
	return runtimeext.RecordMutationResult{Record: toRuntimeextRecord(mutation.ObjectKey, record), Applied: true}, nil
}

func canonicalMutationResultRecord(mutation runtimeext.RecordMutation, plans []transactionmodel.MutationPlan) (recordmodel.Record, bool) {
	operation := strings.TrimSpace(string(mutation.Operation))
	switch mutation.Operation {
	case runtimeext.MutationConditionalUpdate:
		operation = "update"
	case runtimeext.MutationDelete:
		return recordmodel.Record{}, false
	}
	objectKey := strings.TrimSpace(mutation.ObjectKey)
	recordID := strings.TrimSpace(mutation.RecordID)
	for _, plan := range plans {
		commit := plan.CanonicalCommit()
		if strings.TrimSpace(commit.Operation) != operation || strings.TrimSpace(commit.Object.Key) != objectKey {
			continue
		}
		candidateID := strings.TrimSpace(commit.Record.ID)
		if candidateID == "" {
			candidateID = strings.TrimSpace(commit.RecordID)
		}
		if candidateID == "" || recordID != "" && candidateID != recordID {
			continue
		}
		commit.Record.ID = candidateID
		return commit.Record, true
	}
	return recordmodel.Record{}, false
}

// withPlannedRelationRecords makes earlier canonical mutations in this Action
// visible to validation of the next mutation. Action mutations are only staged
// until the final UoW commit, so a transaction-bound repository cannot observe
// an earlier create by itself.
func (e *businessActionExecution) withPlannedRelationRecords(ctx context.Context) context.Context {
	if e == nil || len(e.plans) == 0 {
		return ctx
	}
	records := map[string]map[string]recordmodel.Record{}
	for _, plan := range e.plans {
		commit := plan.CanonicalCommit()
		objectKey, recordID, valid := plannedRelationRecordIdentity(commit)
		if !valid {
			continue
		}
		if commit.Operation == "delete" {
			if records[objectKey] != nil {
				delete(records[objectKey], recordID)
			}
			continue
		}
		if records[objectKey] == nil {
			records[objectKey] = map[string]recordmodel.Record{}
		}
		record := commit.Record
		record.ID = recordID
		records[objectKey][recordID] = record
	}
	return recordservice.RecordWithPlannedRelations(ctx, records)
}

func plannedRelationRecordIdentity(commit transactionmodel.RecordMutationCommit) (string, string, bool) {
	objectKey := strings.TrimSpace(commit.Object.Key)
	recordID := strings.TrimSpace(commit.Record.ID)
	if recordID == "" {
		recordID = strings.TrimSpace(commit.RecordID)
	}
	return objectKey, recordID, objectKey != "" && recordID != ""
}

func (e *businessActionExecution) planConditionalUpdate(ctx context.Context, mutation runtimeext.RecordMutation) ([]transactionmodel.MutationPlan, recordmodel.Record, error) {
	if e.dependencies.PlanConditionalUpdate == nil {
		return nil, recordmodel.Record{}, missingExecutorPort("plan_conditional_update")
	}
	input := transactionmodel.ConditionalUpdateInput{Patch: mutation.Fields}
	for _, predicate := range mutation.Predicates {
		input.Predicates = append(input.Predicates, transactionmodel.MutationPredicate{Field: predicate.Field, Operator: predicate.Operator, Value: predicate.Value, ErrorCode: predicate.ErrorCode})
	}
	for _, arithmetic := range mutation.Arithmetic {
		input.Arithmetic = append(input.Arithmetic, transactionmodel.MutationArithmetic{Field: arithmetic.Field, Operation: arithmetic.Operation, Operand: arithmetic.Operand})
	}
	if mutation.ExpectedVersion != nil {
		input.Predicates = append(input.Predicates, transactionmodel.MutationPredicate{Field: "version", Operator: "eq", Value: *mutation.ExpectedVersion, ErrorCode: "backend.record.version_conflict"})
	}
	if expectedUpdatedAt := strings.TrimSpace(mutation.ExpectedUpdatedAt); expectedUpdatedAt != "" {
		input.Predicates = append(input.Predicates, transactionmodel.MutationPredicate{Field: "updated_at", Operator: "eq", Value: expectedUpdatedAt, ErrorCode: "backend.record.version_conflict"})
	}
	plan, record, err := e.dependencies.PlanConditionalUpdate(ctx, mutation.ObjectKey, mutation.RecordID, input, e.actionMutationPrincipal())
	if err != nil && e.isDeclaredConcurrentRecordChange(mutation) {
		return nil, recordmodel.Record{}, apperror.New(
			apperror.KindConflict,
			"backend.record.conflict",
			err,
			map[string]string{"object": strings.TrimSpace(mutation.ObjectKey), "record_id": strings.TrimSpace(mutation.RecordID)},
		)
	}
	return []transactionmodel.MutationPlan{plan}, record, err
}

// isDeclaredConcurrentRecordChange distinguishes a command that became stale
// while it was executing from one that started after a business predicate was
// already false. The Handler must explicitly publish a record-conflict
// equality predicate; Runtime never converts an ordinary business predicate
// failure into a concurrency result.
func (e *businessActionExecution) isDeclaredConcurrentRecordChange(mutation runtimeext.RecordMutation) bool {
	declared := false
	for _, predicate := range mutation.Predicates {
		if strings.TrimSpace(predicate.Operator) == "eq" && strings.TrimSpace(predicate.ErrorCode) == "backend.record.conflict" {
			declared = true
			break
		}
	}
	if !declared || e == nil || e.unitOfWork == nil {
		return false
	}
	startedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(e.unitOfWork.claim.Execution.CreatedAt))
	if err != nil {
		return false
	}
	record, ok := e.observedRecords[strings.TrimSpace(mutation.ObjectKey)+"\x00"+strings.TrimSpace(mutation.RecordID)]
	if !ok {
		return false
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(record.UpdatedAt))
	return err == nil && updatedAt.After(startedAt)
}

func (e *businessActionExecution) trackRecordMutation(operation runtimeext.MutationOperation, objectKey, recordID string) {
	ref := actionmodel.ActionObjectRecordRef{ObjectKey: objectKey, RecordID: recordID}
	switch operation {
	case runtimeext.MutationCreate:
		e.created = append(e.created, ref)
	case runtimeext.MutationDelete:
		e.deleted = append(e.deleted, ref)
	case runtimeext.MutationRestore:
		e.restored = append(e.restored, ref)
	default:
		e.updated = append(e.updated, ref)
	}
}
