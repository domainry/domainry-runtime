package composition

import (
	"context"
	"fmt"
	"strings"
	"time"

	apperror "github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	organizationunit "github.com/domainry/domainry-identity/organizationunit"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func assembleActionApplication(records *runtimeAssembly, schema CapabilityAuthoringSchemaProvider, policy recordQueryPolicy, metadata interface {
	ValidateApplicationDefinition(context.Context, string, string, appschemamodel.ApplicationDefinitionUpsertRequest, principalmodel.Principal) (appschemamodel.ApplicationDefinitionValidationResult, error)
}, handlers *runtimeext.BusinessHandlerRegistry, audit func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any)) *actionapplication.ActionApplicationService {
	_ = metadata
	pipelineTransitions := newPipelineTransitionApplicationService(records)
	pipelineDescriptor := actionapplication.SystemOperationDescriptor{Key: "pipeline.transition", Matches: pipelineTransitions.IsAction, WriteOperation: "update"}
	systemCatalog := actionapplication.NewRuntimeSystemOperationCatalog(pipelineDescriptor)
	recordHandlers := actionapplication.NewRecordSystemOperationHandlers(actionapplication.RecordSystemOperationDependencies{
		ObjectForKey: func(key string) (definitionmodel.ObjectSchema, bool) {
			object, ok := records.schema[key]
			return object, ok
		},
		PlanCreateMutation:    records.recordApplicationService.PlanCreateMutation,
		PlanUpdateMutation:    records.recordApplicationService.PlanUpdateMutation,
		PlanDeleteMutation:    records.recordApplicationService.PlanDeleteMutation,
		PlanRestoreMutation:   records.recordApplicationService.PlanRestoreMutation,
		PlanConditionalUpdate: records.recordApplicationService.PlanConditionalUpdateMutation,
	})
	systemExecutor := actionapplication.NewRuntimeSystemOperationExecutor(systemCatalog, recordHandlers,
		actionapplication.SystemOperationBinding{Key: pipelineDescriptor.Key, Handler: func(ctx context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, payload map[string]any) (actionapplication.ActionExecutionResult, error) {
			result, plans, err := pipelineTransitions.Plan(ctx, action.ObjectKey, invocation.RecordID, action, payload, invocation.Principal)
			if err != nil {
				return actionapplication.ActionExecutionResult{}, err
			}
			commits := make([]transactionmodel.RecordMutationCommit, len(plans))
			for index := range plans {
				commits[index] = plans[index].CanonicalCommit()
			}
			return actionapplication.ActionExecutionResult{Record: &result, Commits: commits}, nil
		}},
	)
	actions := make([]definitionmodel.ActionSchema, 0, len(records.actions))
	for _, action := range records.actions {
		actions = append(actions, action)
	}
	catalog := actionapplication.NewActionCatalog(actions, systemCatalog, handlers)
	assuranceDomain := actionservice.NewActionAssuranceDomainService(records.actionAssuranceStore, nil)
	service := actionapplication.NewActionApplication(actionapplication.ActionApplicationDependencies{
		Catalog:          catalog,
		SystemOperations: systemExecutor,
		BusinessHandlers: actionapplication.NewBusinessHandlerExecutor(actionapplication.BusinessHandlerExecutionDependencies{
			RuntimeRevision:           records.actionRuntimeRevision,
			ProjectRevision:           records.actionProjectRevision,
			ApplicationSchemaRevision: records.actionMetadataRevision,
			ResolveMetadataRevision: func(ctx context.Context, _ principalmodel.Principal) (string, error) {
				if records.applicationSchemaRepo == nil {
					return records.actionMetadataRevision, nil
				}
				return records.applicationSchemaRepo.SnapshotRevision(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "resolve Action execution identity"))
			},
			GetRecord:                         records.recordApplicationService.GetRecordForAction,
			GetRecordForUpdate:                records.recordApplicationService.GetRecordForUpdateForAction,
			ListRecords:                       records.recordApplicationService.ListRecordsForAction,
			PlanCreateMutation:                records.recordApplicationService.PlanCreateMutation,
			PlanUpdateMutation:                records.recordApplicationService.PlanUpdateMutation,
			PlanConditionalUpdate:             records.recordApplicationService.PlanConditionalUpdateMutation,
			PlanConditionalUpdateLockedRecord: records.recordApplicationService.PlanConditionalUpdateLockedRecord,
			PlanDeleteMutation:                records.recordApplicationService.PlanDeleteMutation,
			PlanRestoreMutation:               records.recordApplicationService.PlanRestoreMutation,
			ValidateDurableIntent: func(ctx context.Context, intent runtimeext.DurableIntent, principal principalmodel.Principal) error {
				if records.publicationHandoffService == nil {
					return apperror.New(apperror.KindInternal, "backend.action.durable_intent_validator_required", nil, nil)
				}
				return records.publicationHandoffService.ValidateActionDurableIntent(ctx, intent, principal)
			},
			CompileNotification: compileActionNotification(records),
			VerifyFileClean:     records.verifyFileClean,
			ObjectForKey: func(key string) (definitionmodel.ObjectSchema, bool) {
				object, ok := records.schema[strings.TrimSpace(key)]
				return object, ok
			},
			NormalizeAggregateQuery: func(object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) recordmodel.RecordListQuery {
				return records.RecordQueryPolicyDomainService.NormalizeListQueryForAction(object, query, principal, "read")
			},
			WorkspaceAggregateCatalog:    records.workspaceAggregateCatalog,
			WorkspaceActiveResolver:      records.workspaceActiveResolver,
			WorkspaceUsageResolver:       records.workspaceUsageResolver,
			WorkspaceAggregateRepository: records.workspaceAggregateRepository,
			AuditWorkspaceAggregate: func(ctx context.Context, value actionapplication.WorkspaceAggregateAudit) error {
				return records.auditApplicationService.AppendAudit(ctx, auditapplication.AuditAppendRequest{
					Event: "action_cross_workspace_aggregate", ObjectKey: value.ObjectKey, Principal: value.Principal, Summary: "Executed controlled cross-Workspace aggregate",
					Metadata: map[string]any{
						"action_key": value.ActionKey, "capability_key": value.CapabilityKey, "outcome": value.Outcome, "error_code": value.ErrorCode,
						"scope_sha256": value.ScopeSHA256, "workspace_count": value.WorkspaceCount, "source_row_count": value.SourceRowCount, "result_row_count": value.ResultRowCount,
					},
				})
			},
			AuditWorkspaceIdentityUsage: func(ctx context.Context, value actionapplication.WorkspaceIdentityUsageAudit) error {
				return records.auditApplicationService.AppendAudit(ctx, auditapplication.AuditAppendRequest{
					Event: "action_workspace_identity_usage", Principal: value.Principal, Summary: "Read controlled Workspace identity usage aggregates",
					Metadata: map[string]any{
						"action_key": value.ActionKey, "outcome": value.Outcome, "error_code": value.ErrorCode,
						"scope_sha256": value.ScopeSHA256, "workspace_count": value.WorkspaceCount,
					},
				})
			},
			WorkspaceIdentityUsageCursor: records.workspaceIdentityUsageCursor,
			AuthorizeWorkspaceIdentityUsage: func(ctx context.Context, accessToken string) (identitysdk.WorkspaceIdentityUsageAuthorization, error) {
				if records.workspaceIdentityUsageBinder == nil {
					return identitysdk.WorkspaceIdentityUsageAuthorization{}, apperror.New(apperror.KindInternal, "identity.workspace_usage_authority_required", nil, nil)
				}
				return records.workspaceIdentityUsageBinder.AuthorizeWorkspaceIdentityUsage(ctx, identitysdk.WorkspaceIdentityUsageAuthorizationRequest{AccessToken: accessToken})
			},
			ResolveRecordTargetOrganization: func(ctx context.Context, action definitionmodel.ActionSchema, invocation actionmodel.ActionInvocation) (string, error) {
				object, ok := records.schema[strings.TrimSpace(action.ObjectKey)]
				if !ok || strings.TrimSpace(invocation.RecordID) == "" {
					return "", apperror.New(apperror.KindNotFound, "backend.record.not_found", nil, nil)
				}
				operation := actionpolicy.ActionName(action)
				if _, err := records.RecordQueryPolicyDomainService.ObjectForAction(invocation.Principal, object.Key, operation); err != nil {
					return "", err
				}
				query := records.RecordQueryPolicyDomainService.NormalizeListQueryForAction(object, recordmodel.RecordListQuery{
					Page: 1, PageSize: 1, Filters: map[string]any{"id__in": []any{strings.TrimSpace(invocation.RecordID)}},
					LockIntent: recordmodel.RecordQueryLockForUpdate,
				}, invocation.Principal, operation)
				page, err := records.recordRepo.ListRecords(ctx, invocation.Principal.WorkspaceID, object, query)
				if err != nil {
					return "", err
				}
				if len(page.Items) != 1 || strings.TrimSpace(page.Items[0].OwnerOrgID) == "" {
					return "", apperror.New(apperror.KindNotFound, "backend.record.not_found", nil, nil)
				}
				return strings.TrimSpace(page.Items[0].OwnerOrgID), nil
			},
			BindOrganizationUnitDelivery: func(ctx context.Context) (organizationunit.Delivery, error) {
				if records.organizationUnitDeliveryBinder == nil {
					return nil, apperror.New(apperror.KindInternal, "identity.organization_unit_delivery_transaction_required", nil, nil)
				}
				executor := database.ActionExecutionTransaction(ctx)
				if executor == nil {
					return nil, apperror.New(apperror.KindInternal, "identity.organization_unit_delivery_transaction_required", nil, nil)
				}
				return records.organizationUnitDeliveryBinder.BindOrganizationUnitDeliveryUnitOfWork(identitysdk.EmbeddedTransaction{Executor: executor})
			},
			BindStoreOrganizationDelivery: func(ctx context.Context) (identitysdk.StoreOrganizationDelivery, error) {
				if records.storeOrganizationDeliveryBinder == nil {
					return nil, apperror.New(apperror.KindInternal, "identity.store_organization_delivery_transaction_required", nil, nil)
				}
				executor := database.ActionExecutionTransaction(ctx)
				if executor == nil {
					return nil, apperror.New(apperror.KindInternal, "identity.store_organization_delivery_transaction_required", nil, nil)
				}
				return records.storeOrganizationDeliveryBinder.BindStoreOrganizationDeliveryUnitOfWork(identitysdk.EmbeddedTransaction{Executor: executor})
			},
			BindIdentityHandlerDelivery: func(ctx context.Context) (identitysdk.HandlerDelivery, error) {
				if records.identityHandlerDeliveryBinder == nil {
					return nil, apperror.New(apperror.KindInternal, "identity.handler_delivery_transaction_required", nil, nil)
				}
				executor := database.ActionExecutionTransaction(ctx)
				if executor == nil {
					return nil, apperror.New(apperror.KindInternal, "identity.handler_delivery_transaction_required", nil, nil)
				}
				return records.identityHandlerDeliveryBinder.BindHandlerDeliveryUnitOfWork(identitysdk.EmbeddedTransaction{Executor: executor})
			},
			BindWorkspaceIdentityUsage: func(ctx context.Context) (identitysdk.WorkspaceIdentityUsageAggregate, error) {
				if records.workspaceIdentityUsageBinder == nil {
					return nil, apperror.New(apperror.KindInternal, "identity.workspace_usage_transaction_required", nil, nil)
				}
				executor := database.ActionExecutionTransaction(ctx)
				if executor == nil {
					return nil, apperror.New(apperror.KindInternal, "identity.workspace_usage_transaction_required", nil, nil)
				}
				return records.workspaceIdentityUsageBinder.BindWorkspaceIdentityUsageUnitOfWork(identitysdk.EmbeddedTransaction{Executor: executor})
			},
			WorkspaceCommercialConfiguration: records.workspaceCommercialConfiguration,
			ResolveProfileBindingField: func(bindingKey, objectKey string) (string, bool) {
				for _, extension := range records.identityProfileExtensions {
					if strings.TrimSpace(extension.BusinessIdentity.Key) == strings.TrimSpace(bindingKey) && strings.TrimSpace(extension.ObjectKey) == strings.TrimSpace(objectKey) {
						field := strings.TrimSpace(extension.IdentityRelationField)
						return field, field != ""
					}
				}
				return "", false
			},
		}),
		Authorization: actionapplication.ActionAuthorization{ObjectForAction: records.RecordQueryPolicyDomainService.ObjectForAction},
		Assurance:     actionapplication.ActionAssurance{Validate: actionAssuranceValidator(records, assuranceDomain, audit)},
		UnitOfWork:    actionapplication.NewActionUnitOfWorkManager(records.ActionExecutionRuntime),
		ProjectRecord: func(ctx context.Context, principal principalmodel.Principal, objectKey string, record recordmodel.Record) (recordmodel.Record, error) {
			object, ok := records.schema[strings.TrimSpace(objectKey)]
			if !ok {
				return recordmodel.Record{}, apperror.New(apperror.KindInternal, "backend.action.output_object_missing", nil, map[string]string{"object": objectKey})
			}
			projected, err := records.recordApplicationService.ProjectRecordFields(ctx, principal, object, []recordmodel.Record{record}, "read")
			if err != nil {
				return recordmodel.Record{}, err
			}
			if len(projected) != 1 {
				return recordmodel.Record{}, apperror.New(apperror.KindInternal, "backend.action.output_projection_invalid", nil, map[string]string{"object": objectKey})
			}
			return projected[0], nil
		},
		ProjectOutput: func(ctx context.Context, principal principalmodel.Principal, action definitionmodel.ActionSchema, output map[string]any) (map[string]any, error) {
			return actionapplication.ProjectBusinessHandlerOutput(ctx, principal, action, output, func(objectKey string) (definitionmodel.ObjectSchema, bool) {
				object, ok := records.schema[objectKey]
				return object, ok
			})
		},
		Audit: actionapplication.ActionAudit{
			Bulk: func(ctx context.Context, result actionmodel.ActionBulkResult, recordIDs []string, principal principalmodel.Principal) {
				audit(ctx, "record_bulk_action_executed", result.ObjectKey, "", principal, "Executed bulk action "+result.ActionKey, map[string]any{"action_key": result.ActionKey, "total": result.Total, "succeeded": result.Succeeded, "failed": result.Failed, "record_ids": recordIDs})
			},
		},
	})
	_ = policy
	_ = schema
	return service
}

func compileActionNotification(records *runtimeAssembly) func(context.Context, string, runtimeext.NotificationIntent, principalmodel.Principal) (notificationmodel.NotificationEvent, error) {
	return func(ctx context.Context, eventID string, intent runtimeext.NotificationIntent, principal principalmodel.Principal) (notificationmodel.NotificationEvent, error) {
		if records == nil || records.recordNotificationCompiler == nil || records.identityProjection == nil {
			return notificationmodel.NotificationEvent{}, apperror.New(apperror.KindInternal, "backend.notification.action_compiler_required", nil, nil)
		}
		for _, recipient := range intent.RecipientUserIDs {
			_, found, err := records.identityProjection.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(strings.TrimSpace(recipient))})
			if err != nil {
				return notificationmodel.NotificationEvent{}, err
			}
			if !found {
				return notificationmodel.NotificationEvent{}, apperror.New(apperror.KindBadRequest, "backend.notification.recipient_invalid", nil, nil)
			}
		}
		variables := make(map[string]any, len(intent.Variables))
		for _, variable := range intent.Variables {
			variables[strings.TrimSpace(variable.Key)] = variable.Value()
		}
		return records.recordNotificationCompiler(notificationmodel.NotificationIntent{
			ID: eventID, WorkspaceID: principal.WorkspaceID, SourceEventID: strings.TrimSpace(intent.SourceEventID), EventType: strings.TrimSpace(intent.EventType),
			RecipientUserIDs: append([]string(nil), intent.RecipientUserIDs...), SubjectType: strings.TrimSpace(intent.SubjectObjectKey), SubjectID: strings.TrimSpace(intent.SubjectRecordID), SubjectVersion: strings.TrimSpace(intent.SubjectVersion),
			DedupeKey: strings.TrimSpace(intent.DedupeKey), GroupKey: strings.TrimSpace(intent.GroupKey), AlertState: func() string {
				if intent.Alert {
					return notificationmodel.NotificationAlertFiring
				}
				return ""
			}(), OccurredAt: intent.OccurredAt.UTC().Format(time.RFC3339Nano), Variables: variables,
		})
	}
}

func actionAssuranceValidator(records *runtimeAssembly, assurance *actionservice.ActionAssuranceDomainService, audit func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any)) func(context.Context, actionmodel.ActionInvocation) (map[string]string, error) {
	return func(ctx context.Context, invocation actionmodel.ActionInvocation) (map[string]string, error) {
		action, exists := records.actions[invocation.ActionKey]
		if !exists || action.AssurancePolicy == nil || len(action.AssurancePolicy.RequiredMethods) == 0 {
			return nil, nil
		}
		if actionAssuranceOnlyNormalLogin(action.AssurancePolicy.RequiredMethods) {
			audit(ctx, "action_assurance_succeeded", invocation.ObjectKey, invocation.RecordID, invocation.Principal, "Action assurance verified", map[string]any{"action_key": action.Key, "methods": definitionmodel.ActionAssuranceNormalLogin})
			return map[string]string{"methods": definitionmodel.ActionAssuranceNormalLogin}, nil
		}
		if strings.TrimSpace(invocation.AssuranceToken) == "" {
			appErr := apperror.New(apperror.KindForbidden, "backend.action.assurance_required", nil, map[string]string{
				"action_key": action.Key, "object_key": invocation.ObjectKey, "record_id": invocation.RecordID,
				"required_methods": strings.Join(action.AssurancePolicy.RequiredMethods, ","),
			})
			audit(ctx, "action_assurance_denied", invocation.ObjectKey, invocation.RecordID, invocation.Principal, "Action assurance required", map[string]any{"action_key": action.Key, "required_methods": action.AssurancePolicy.RequiredMethods, "error_code": apperror.CodeOf(appErr)})
			return nil, appErr
		}
		evidence, err := assurance.ValidateAndConsume(ctx, action, invocation.Principal.WorkspaceID, invocation.Principal.UserID, invocation.ObjectKey, invocation.RecordID, invocation.Input, invocation.AssuranceToken)
		if err != nil {
			appErr := apperror.FromError(apperror.KindForbidden, err)
			audit(ctx, "action_assurance_denied", invocation.ObjectKey, invocation.RecordID, invocation.Principal, "Action assurance denied", map[string]any{"action_key": action.Key, "error_code": apperror.CodeOf(appErr)})
			return nil, appErr
		}
		if actionAssuranceContains(action.AssurancePolicy.RequiredMethods, definitionmodel.ActionAssuranceWorkflowApproval) {
			object := records.schema[invocation.ObjectKey]
			record, found, getErr := records.recordRepo.GetRecord(ctx, invocation.Principal.WorkspaceID, object, invocation.RecordID)
			if getErr != nil {
				return nil, getErr
			}
			if !found || fmt.Sprint(record.Data[action.AssurancePolicy.ApprovalVersionField]) != evidence.ApprovalVersion || fmt.Sprint(record.Data[action.AssurancePolicy.ApprovalHashField]) != evidence.ApprovalHash {
				appErr := apperror.New(apperror.KindConflict, "backend.action.assurance_approval_stale", nil, nil)
				audit(ctx, "action_assurance_denied", invocation.ObjectKey, invocation.RecordID, invocation.Principal, "Action approval assurance became stale", map[string]any{"action_key": action.Key, "grant_id": evidence.GrantID, "error_code": apperror.CodeOf(appErr)})
				return nil, appErr
			}
		}
		audit(ctx, "action_assurance_succeeded", invocation.ObjectKey, invocation.RecordID, invocation.Principal, "Action assurance verified", map[string]any{"action_key": action.Key, "grant_id": evidence.GrantID, "methods": evidence.Methods, "approval_version": evidence.ApprovalVersion, "approval_hash": evidence.ApprovalHash})
		return map[string]string{"grant_id": evidence.GrantID, "methods": strings.Join(evidence.Methods, ","), "approval_version": evidence.ApprovalVersion, "approval_hash": evidence.ApprovalHash, "payload_digest": evidence.Facts["payload_digest"]}, nil
	}
}

func actionAssuranceOnlyNormalLogin(methods []string) bool {
	return len(methods) == 1 && strings.TrimSpace(methods[0]) == definitionmodel.ActionAssuranceNormalLogin
}

func actionAssuranceContains(methods []string, expected string) bool {
	for _, method := range methods {
		if strings.TrimSpace(method) == expected {
			return true
		}
	}
	return false
}
