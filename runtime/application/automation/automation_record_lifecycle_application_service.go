package automation

import (
	"context"
	"fmt"
	"strings"
	"time"

	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	automationdomain "github.com/domainry/domainry-runtime/runtime/domain/automation/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	requestcontext "github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

func AutomationFindBeforeCreateReplay(ctx context.Context, rules []automationmodel.AutomationRuleSchema, repository automationcontract.AutomationRecordReader, object definitionmodel.ObjectSchema, input map[string]any, principal principalmodel.Principal, canAccess func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool) (recordmodel.Record, bool, error) {
	for _, rule := range automationdomain.MatchingRules(rules, object.Key, "before", "create", nil, input) {
		if len(rule.Execution.IdempotencyKeys) == 0 {
			continue
		}
		filters := map[string]any{}
		complete := true
		for _, rawKey := range rule.Execution.IdempotencyKeys {
			key := strings.TrimSpace(rawKey)
			if key == "" || recordcontract.RecordIsEmptyValue(input[key]) {
				complete = false
				break
			}
			filters[key] = input[key]
		}
		if !complete {
			continue
		}
		page, err := repository.ListRecords(ctx, principal.WorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 2, Filters: filters})
		if err != nil {
			return recordmodel.Record{}, false, automationError(apperror.KindInternal, "backend.internal", err, "operation", "find automation create replay")
		}
		if len(page.Items) == 0 {
			continue
		}
		if len(page.Items) > 1 {
			return recordmodel.Record{}, false, automationError(apperror.KindConflict, "backend.automation.idempotency_ambiguous", nil, "rule", rule.Key)
		}
		record := page.Items[0]
		if canAccess != nil && !canAccess(principal, object, record) {
			return recordmodel.Record{}, false, automationError(apperror.KindConflict, "backend.automation.idempotency_conflict", nil, "rule", rule.Key)
		}
		return record, true, nil
	}
	return recordmodel.Record{}, false, nil
}

func AutomationAfterOutbox(rules []automationmodel.AutomationRuleSchema, objectKey, operation string, before map[string]any, record recordmodel.Record, principal principalmodel.Principal, workspaceID string) []integrationmodel.IntegrationOutboxMessage {
	matched := automationdomain.MatchingRules(rules, objectKey, "after", operation, before, record.Data)
	messages := make([]integrationmodel.IntegrationOutboxMessage, 0, len(matched))
	for _, rule := range matched {
		recordVersion := automationdomain.MutationVersion(before, record)
		eventID := fmt.Sprintf("automation:%s:%s:%s:%s:%s", rule.Key, objectKey, record.ID, operation, recordVersion)
		correlationID := valueOrDefault(strings.TrimSpace(principal.CorrelationID), principal.RequestID)
		causationID := valueOrDefault(strings.TrimSpace(principal.CausationID), principal.RequestID)
		event := automationmodel.AutomationLifecycleEvent{
			ID: eventID, RuleKey: rule.Key, ObjectKey: objectKey, Operation: operation, RecordID: record.ID, RecordVersion: recordVersion,
			Before: recordcontract.RecordCloneData(before), Record: record, ActorUserID: principal.UserID, ActorRoleKey: principal.RoleKey,
			RequestID: principal.RequestID, CorrelationID: correlationID, CausationID: causationID, IdentityPolicy: "revalidate_initiator",
			AutomationDepth: principal.AutomationDepth, VisitedRuleKeys: append([]string(nil), principal.VisitedRuleKeys...), OccurredAt: time.Now().UTC().Format(time.RFC3339),
		}
		messages = append(messages, integrationmodel.IntegrationOutboxMessage{ID: eventID, WorkspaceID: workspaceID, ConnectorKey: "__automation__", Operation: rule.Key, Status: "queued", EventID: eventID, RequestRef: principal.RequestID, DedupKey: eventID, CreatedBy: principal.UserID, Payload: automationdomain.LifecycleEventPayload(event)})
	}
	return messages
}

func (s *AutomationApplicationService) ExecuteOutboxMessage(ctx context.Context, message integrationmodel.IntegrationOutboxMessage) error {
	workspaceID, err := principalmodel.NewWorkspaceID(message.WorkspaceID)
	if err != nil {
		return automationError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	event := automationdomain.LifecycleEventFromPayload(message.Payload)
	rule, ok := s.rules.Get(event.RuleKey)
	if !ok || !rule.Enabled || rule.Trigger.Phase != "after" {
		return automationError(apperror.KindBadRequest, "backend.automation.after_rule_not_found", nil, "rule", event.RuleKey)
	}
	record := event.Record
	record.UpdatedAt = valueOrDefault(event.RecordVersion, record.UpdatedAt)
	principal := s.principal(ctx, event.ActorUserID, event.ActorRoleKey, "")
	principal.WorkspaceID = workspaceID.String()
	if automationAuthorizeCommand(principal) != nil || strings.TrimSpace(principal.RoleKey) != strings.TrimSpace(event.ActorRoleKey) {
		return automationError(apperror.KindForbidden, "backend.automation.identity_revoked", nil, "rule", event.RuleKey, "actor", event.ActorUserID)
	}
	if event.IdentityPolicy != "" && event.IdentityPolicy != "revalidate_initiator" {
		return automationError(apperror.KindBadRequest, "backend.automation.identity_policy_invalid", nil, "policy", event.IdentityPolicy)
	}
	if stringSliceContains(event.VisitedRuleKeys, rule.Key) {
		return automationError(apperror.KindBadRequest, "backend.automation.recursion_detected", nil, "rule", rule.Key)
	}
	maxDepth := rule.Execution.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 8
	}
	if event.AutomationDepth >= maxDepth {
		return automationError(apperror.KindBadRequest, "backend.automation.max_depth_exceeded", nil, "rule", rule.Key)
	}
	principal.RequestID = event.ID
	principal.CorrelationID = valueOrDefault(event.CorrelationID, event.RequestID)
	principal.CausationID = event.ID
	principal.AutomationDepth = event.AutomationDepth + 1
	principal.VisitedRuleKeys = append(append([]string(nil), event.VisitedRuleKeys...), rule.Key)
	ctx = requestcontext.WithRequestID(ctx, event.ID)
	ctx = requestcontext.WithCorrelationID(ctx, principal.CorrelationID)
	var execution automationmodel.AutomationRuleExecution
	trace, executionErr := s.executeRuleWithPersistence(ctx, rule, "after", recordcontract.RecordCloneData(record.Data), event.Before, recordcontract.RecordCloneData(record.Data), &record, principal, func(_ context.Context, value automationmodel.AutomationRuleExecution) error {
		execution = value
		return nil
	})
	// After-rule execution constructs its stable execution ID before invoking
	// the persistence callback above, so the captured evidence is complete.
	notification, notify, err := s.terminalResultNotification(rule, trace, principal, executionErr)
	if err != nil {
		return automationError(apperror.KindInternal, "backend.automation.notification_compile_failed", err, "rule", rule.Key)
	}
	if notify {
		if s.commitNotification == nil {
			return automationError(apperror.KindInternal, "backend.automation.notification_committer_unavailable", nil, "rule", rule.Key)
		}
		err = s.commitNotification.CommitAutomationExecutionNotification(ctx, execution, notification)
	} else if s.commitNotification != nil {
		err = s.commitNotification.CommitAutomationExecution(ctx, execution)
	} else if s.executionRepo != nil {
		_, err = s.executionRepo.InsertExecution(ctx, execution.WorkspaceID, execution)
	}
	if err != nil {
		return automationError(apperror.KindInternal, "backend.automation.execution_commit_failed", err, "rule", rule.Key)
	}
	return executionErr
}

func (s *AutomationApplicationService) terminalResultNotification(rule automationmodel.AutomationRuleSchema, trace automationprojection.AutomationRuleTrace, principal principalmodel.Principal, executionErr error) (notificationmodel.NotificationEvent, bool, error) {
	mode := strings.TrimSpace(rule.Execution.ResultNotification)
	failed := executionErr != nil || trace.Status == "failed" || trace.Status == "blocked"
	if s.compileNotification == nil || strings.TrimSpace(principal.UserID) == "" || mode == "" || mode == "none" || mode == "failures" && !failed {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	status := "completed"
	if failed {
		status = "failed"
	}
	sourceID := "automation:" + trace.ExecutionID + ":" + status
	intent := notificationmodel.NotificationIntent{
		WorkspaceID: principal.WorkspaceID, SourceEventID: sourceID, EventType: "automation.execution." + status, Surface: "business_workspace",
		RecipientUserIDs: []string{principal.UserID}, SubjectType: "automation_rule", SubjectID: rule.Key, DedupeKey: sourceID,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), Variables: map[string]any{"rule_key": rule.Key, "object_key": rule.ObjectKey, "execution_id": trace.ExecutionID, "status": trace.Status, "error_code": trace.ErrorCode},
	}
	event, err := s.compileNotification(intent)
	return event, true, err
}
