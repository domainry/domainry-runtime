package runtimeext

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
)

const (
	ContractVersion = "runtimeext-v17"
	ContractSHA256  = "6f1a744659ab5638edfdf8ddc9689a2faffc03bacf7ddc693e3ce7f79b5f5e6c"
)

const contractSurfaceV17 = `runtimeext-v17
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
	var surface strings.Builder
	surface.WriteString(contractSurfaceV17)
	for _, value := range structs {
		current := reflect.TypeOf(value)
		surface.WriteString(current.Name())
		surface.WriteByte('{')
		for index := 0; index < current.NumField(); index++ {
			if index > 0 {
				surface.WriteByte(',')
			}
			field := current.Field(index)
			surface.WriteString(field.Name)
			surface.WriteByte(':')
			surface.WriteString(field.Type.String())
		}
		surface.WriteString("}\n")
	}
	sum := sha256.Sum256([]byte(surface.String()))
	return hex.EncodeToString(sum[:])
}
