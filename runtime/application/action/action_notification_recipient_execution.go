package action

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func (e *businessActionExecution) ResolveRecordNotificationRecipient(ctx context.Context, request runtimeext.RecordNotificationRecipientRequest) (string, error) {
	objectKey, recordID := strings.TrimSpace(request.ObjectKey), strings.TrimSpace(request.RecordID)
	if e == nil || objectKey == "" || recordID == "" {
		return "", apperror.New(apperror.KindBadRequest, "backend.notification.record_recipient_request_invalid", nil, nil)
	}
	if !e.hasObjectGrant(objectKey, runtimeext.RecordNotificationRecipientOperation) {
		return "", apperror.New(apperror.KindForbidden, "backend.action.effect_authority_denied", nil, map[string]string{"object": objectKey, "operation": runtimeext.RecordNotificationRecipientOperation})
	}
	record, found := e.observedRecords[objectKey+"\x00"+recordID]
	if !found {
		if e.dependencies.GetRecord == nil {
			return "", missingExecutorPort("get_record")
		}
		principal := actionReadEffectAuthorizationPrincipal(e.invocation.Principal, e.action.EffectSet, e.action, objectKey)
		var err error
		record, err = e.dependencies.GetRecord(e.unitOfWork.executionContext(ctx), objectKey, recordID, principal)
		if err != nil {
			return "", err
		}
		e.observeRecord(objectKey, record)
	}
	ownerUserID := strings.TrimSpace(record.OwnerUserID)
	if ownerUserID == "" {
		return "", apperror.New(apperror.KindConflict, "backend.notification.record_recipient_owner_missing", nil, map[string]string{"object": objectKey, "record_id": recordID})
	}
	return ownerUserID, nil
}

func (e *businessActionExecution) hasObjectGrant(objectKey, operation string) bool {
	for _, capability := range e.objectGrants {
		if strings.TrimSpace(capability.ObjectKey) != objectKey {
			continue
		}
		for _, candidate := range capability.Operations {
			if strings.TrimSpace(candidate) == operation {
				return true
			}
		}
	}
	return false
}
