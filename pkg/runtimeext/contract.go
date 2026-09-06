package runtimeext

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
)

const (
	ContractVersion = "runtimeext-v30"
	ContractSHA256  = "213986dd45ea7bd0e78526a8c19641f8a60fd815077444195ebcde7583e0859d"
)

const contractDefinitionV30 = `runtimeext-v30
PackagePath=github.com/domainry/domainry-runtime/pkg/runtimeext
Handler[Capabilities,Input,Output](context.Context,Capabilities,Input)(Output,error)
BusinessHandler.Descriptor()HandlerDescriptor
BusinessHandler.Invoke(context.Context,ActionExecution,json.RawMessage)(json.RawMessage,error)
ActionExecution.Identity()ExecutionIdentity
ActionExecution.Principal()Principal
ActionExecution.Workspace()Workspace
ActionExecution.Phase()ExecutionPhase
ActionExecution.QueryRecords(context.Context,RecordQuery)(RecordQueryResult,error)
ActionExecution.ApplyRecordMutation(context.Context,RecordMutation)(RecordMutationResult,error)
ActionExecution.StageDurableIntent(context.Context,DurableIntent)(DurableIntentReceipt,error)
ActionExecution.AcquireSynchronousConnectorCall(ActionConnectorCapability)(SynchronousConnectorCallLease,error)
ConditionalUpdateManyExecution.ConditionalUpdateMany(context.Context,ConditionalUpdateManyRequest)(ConditionalUpdateManyResult,error)
ApplyConditionalUpdateMany(context.Context,ActionExecution,ConditionalUpdateManyRequest)(ConditionalUpdateManyResult,error)
ConditionalUpdateManySemantics=one_scoped_select_for_update|explicit_exact_distinct_coverage_field_and_values|every_expected_value_exactly_one_locked_row|no_in_filter_inference|one_conditional_update|expected_affected_exact|same_action_transaction|no_owner_input|max_200
ResolveRecordNotificationRecipient(context.Context,ActionExecution,RecordNotificationRecipientRequest)(string,error)
RecordNotificationRecipientOperation=notification_recipient
VerifyFileClean(context.Context,ActionExecution,FileVerificationRequest)(FileVerificationEvidence,error)
FileOperationVerifyClean=verify_clean
FileActionGrantDeniedErrorCode=backend.upload.action_grant_denied
StageNotification(context.Context,ActionExecution,NotificationIntent)(NotificationReceipt,error)
StageNotificationBatch(context.Context,ActionExecution,[]NotificationIntent)([]NotificationReceipt,error)
NotificationDispatchOperationKey=notification.intent.dispatch
NotificationBatchSizeInvalidErrorCode=backend.notification.action_batch_size_invalid
NotificationBatchMaximum=200
SynchronousConnectorCallLease.Release()
ConnectorCallAfterWriteErrorCode=backend.connector.call_after_write_forbidden
ActionWriteDuringConnectorCallErrorCode=backend.action.write_during_connector_call_forbidden
ConnectorActionExecutionRequiredErrorCode=backend.connector.action_execution_required
ConnectorActionGrantDeniedErrorCode=backend.connector.action_grant_denied
ConnectorActionSideEffectOutboxErrorCode=backend.connector.action_side_effect_requires_outbox
BusinessHandlerRegistry.Register(BusinessHandler)error
BusinessHandlerRegistry.RegisterExtensionSet(ExtensionSet)error
BusinessHandlerRegistry.Freeze()
BusinessHandlerRegistry.Binding(string)(BusinessHandlerBinding,bool)
BusinessHandlerRegistry.Descriptors()[]HandlerDescriptor
QueryOperation=get,get_for_update,list,exists,count
RecordQueryPagination=after_id
RecordQuery.StoreOrganization=execution_scoped_catalog_reference|list_only
ExecuteCrossWorkspaceAggregate(context.Context,ActionExecution,CrossWorkspaceAggregateRequest)(CrossWorkspaceAggregateResult,error)
CrossWorkspaceDimensionWorkspace=$workspace
AggregateOperation=count,sum,min,max,avg
ResolveTargetOrganization(ActionExecution)(TargetOrganization,error)
ProvisionStoreOrganization(context.Context,ActionExecution,StoreOrganizationProvisionRequest)(StoreOrganizationProvisionResult,error)
StoreOrganizationProvisionResult=runtime_authoritative_target_id|organization_version|replayed
RenameStoreOrganization(context.Context,ActionExecution,StoreOrganizationRenameRequest)(StoreOrganizationMutationResult,error)
DisableStoreOrganization(context.Context,ActionExecution,StoreOrganizationDisableRequest)(StoreOrganizationMutationResult,error)
TargetOrganizationSource=explicit,explicit_or_sole_authorized_store,record_owner,provisioned_store
ExplicitOrSoleAuthorizedStore=explicit_identity_resolve_and_action_scope|omitted_exactly_one_complete_active_authorized_catalog|zero_multiple_disabled_or_continuation_denied
DeliverIdentity(context.Context,ActionExecution,IdentityHandlerDeliveryRequest)(IdentityHandlerDeliveryResult,error)
ResolveBoundIdentity(context.Context,ActionExecution,string)(IdentityBoundIdentity,error)
ResolveBoundIdentityProfile(context.Context,ActionExecution,string,IdentityHandlerProfileBindingSelector)(IdentityBoundIdentity,error)
IdentityHandlerProfileResolution=generated_static_binding_key_and_object|caller_profile_id|identity_owned_cas_version|active_exact_user
IdentityHandlerOperation=create,update,disable,resolve
IdentityHandlerLoginMode=none,password
ExecuteStoreOrganizationCatalog(context.Context,ActionExecution,StoreOrganizationCatalogRequest)(StoreOrganizationCatalogPage,error)
IssueStoreOrganizationCatalogItem(StoreOrganizationCatalogItem)(StoreOrganizationCatalogItem,string,error)
StoreOrganizationCatalogOwnedRecordLookup=generated_ListForStore|issued_current_action_reference|record_read_effect|workspace_and_identity_scope|read_only
ExecuteWorkspaceIdentityUsage(context.Context,ActionExecution,WorkspaceIdentityUsageRequest)(WorkspaceIdentityUsagePage,error)
ExecuteWorkspaceIdentityUsageResolve(context.Context,ActionExecution,WorkspaceIdentityUsageResolveRequest)(WorkspaceIdentityUsageResolveResult,error)
WorkspaceIdentityUsageMaximumPageSize=100
WorkspaceIdentityUsageProjection=canonical_code,display_name,commercial_plan,included_user_limit,max_user_limit,identity_account_counts|physical_workspace_id_withheld
WorkspaceIdentityUsageResolve=canonical_workspace_code,expected_top_level_workspace_revision|active_workspace_locked_and_rechecked|typed_commercial_configuration|identity_account_counts|same_action_uow|physical_workspace_id_withheld
ExtensionSet.WorkspaceBootstrapParticipant=WorkspaceBootstrapParticipant
WorkspaceBootstrapParticipant.Descriptor()WorkspaceBootstrapDescriptor
WorkspaceBootstrapParticipant.BuildWorkspaceBootstrap(context.Context,WorkspaceBootstrapContext,map[string]any)([]WorkspaceBootstrapRecord,error)
WorkspaceBootstrapInputExactDecimal=canonical_decimal_string|no_binary_float|exact_range_validation
WorkspaceBootstrapRuntimeOwnedFields=workspace_id,id,owner_org_id,created_at,updated_at
RuntimeWorkspaceRoleCatalog=tenant_admin,headquarters_admin,store_manager,staff|exact_manifest_definitions|initial_admin=headquarters_admin
WorkspaceProvisioningVocabulary=workspace_name,workspace_code
AcceptanceFixtureContract=runtime-acceptance-fixture-v2|workspace_code
`

// ComputedContractSHA256 returns the canonical public contract identity used
// by generated project code and Runtime readiness checks.
func ComputedContractSHA256() string {
	structs := []any{
		BusinessProfileReference{}, Principal{}, Workspace{}, ExecutionIdentity{}, ActionObjectCapability{}, ActionConnectorCapability{}, FileVerificationRequest{}, FileVerificationEvidence{}, RecordNotificationRecipientRequest{},
		CrossWorkspaceAggregateDimension{}, CrossWorkspaceAggregateDimensionTransform{}, CrossWorkspaceAggregateDateBucketTransform{}, CrossWorkspaceAggregateMeasure{}, CrossWorkspaceAggregateFilterCapability{}, CrossWorkspaceAggregateCapability{}, CrossWorkspaceAggregateFilter{}, CrossWorkspaceAggregateRequest{}, CrossWorkspaceAggregateRow{}, CrossWorkspaceAggregateResult{},
		ActionTargetOrganizationCapability{}, TargetOrganization{}, StoreOrganizationProvisionRequest{}, StoreOrganizationProvisionResult{}, StoreOrganizationRenameRequest{}, StoreOrganizationDisableRequest{}, StoreOrganizationMutationResult{}, ActionStoreOrganizationMutationCapability{},
		IdentityProfileBindingCapability{}, IdentityHandlerDeliveryCapability{}, IdentityUser{}, IdentityHandlerUserMutation{}, IdentityHandlerProfileBindingMutation{}, IdentityHandlerDeliveryRequest{}, IdentityHandlerProfileBinding{}, IdentityHandlerProfileBindingSelector{}, IdentityHandlerDeliveryResult{}, IdentityBoundIdentity{},
		StoreOrganizationCatalogCapability{}, StoreOrganizationCatalogRequest{}, StoreOrganizationCatalogItem{}, StoreOrganizationCatalogPage{},
		WorkspaceIdentityUsageCapability{}, WorkspaceIdentityUsageRequest{}, WorkspaceIdentityUsageResolveRequest{}, WorkspaceIdentityAccountCounts{}, WorkspaceCommercialTerms{}, WorkspaceCommercialConfiguration{}, WorkspaceIdentityUsageItem{}, WorkspaceIdentityUsagePage{}, WorkspaceIdentityUsageResolveResult{},
		WorkspaceBootstrapInputField{}, WorkspaceBootstrapRecordCapability{}, WorkspaceBootstrapDescriptor{}, WorkspaceBootstrapContext{}, WorkspaceBootstrapRecord{},
		HandlerDescriptor{}, BusinessHandlerBinding{},
		Record{}, Filter{}, Sort{}, RecordQuery{}, RecordQueryResult{},
		Predicate{}, Arithmetic{}, RecordMutation{}, RecordMutationResult{}, ConditionalUpdateManyExactCoverage{}, ConditionalUpdateManyRequest{}, ConditionalUpdateManyResult{},
		DurableIntent{}, DurableIntentReceipt{}, BusinessError{}, ExtensionSet{},
		NotificationVariable{}, NotificationIntent{}, NotificationReceipt{},
	}
	var definition strings.Builder
	definition.WriteString(contractDefinitionV30)
	for _, value := range structs {
		current := reflect.TypeOf(value)
		definition.WriteString(current.Name())
		definition.WriteByte('{')
		for index := 0; index < current.NumField(); index++ {
			if index > 0 {
				definition.WriteByte(',')
			}
			field := current.Field(index)
			definition.WriteString(field.Name)
			definition.WriteByte(':')
			definition.WriteString(field.Type.String())
		}
		definition.WriteString("}\n")
	}
	sum := sha256.Sum256([]byte(definition.String()))
	return hex.EncodeToString(sum[:])
}
