package runtimeext

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
)

const (
	ContractVersion = "runtimeext-v18"
	ContractSHA256  = "858d3a59ade02810d9dcd99e6116e23bd795be99958a02cf5370465ded03f62f"
)

const contractDefinitionV18 = `runtimeext-v18
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
`

// ComputedContractSHA256 returns the canonical public contract identity used
// by generated project code and Runtime readiness checks.
func ComputedContractSHA256() string {
	structs := []any{
		BusinessProfileReference{}, Principal{}, Workspace{}, ExecutionIdentity{}, ActionObjectCapability{}, ActionConnectorCapability{}, FileVerificationRequest{}, FileVerificationEvidence{}, HandlerDescriptor{}, BusinessHandlerBinding{},
		Record{}, Filter{}, Sort{}, RecordQuery{}, RecordQueryResult{},
		Predicate{}, Arithmetic{}, RecordMutation{}, RecordMutationResult{},
		DurableIntent{}, DurableIntentReceipt{}, BusinessError{}, ExtensionSet{},
		NotificationVariable{}, NotificationIntent{}, NotificationReceipt{},
	}
	var definition strings.Builder
	definition.WriteString(contractDefinitionV18)
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
