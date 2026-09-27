package action

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func (e *businessActionExecution) ResolveRecordManagerRecipient(ctx context.Context, request runtimeext.RecordManagerRecipientRequest) (string, error) {
	objectKey, recordID, fieldKey := strings.TrimSpace(request.ObjectKey), strings.TrimSpace(request.RecordID), strings.TrimSpace(request.UserFieldKey)
	if e == nil || objectKey == "" || recordID == "" || fieldKey == "" || e.unitOfWork == nil || strings.TrimSpace(e.workspace.ID) == "" {
		return "", apperror.New(apperror.KindBadRequest, "backend.notification.manager_recipient_request_invalid", nil, nil)
	}
	if !e.hasObjectGrant(objectKey, runtimeext.RecordManagerRecipientOperation) || !e.hasObjectGrant(objectKey, runtimeext.ObjectCapabilityGet) {
		return "", apperror.New(apperror.KindForbidden, "backend.action.effect_authority_denied", nil, map[string]string{"object": objectKey, "operation": runtimeext.RecordManagerRecipientOperation})
	}
	if e.dependencies.ObjectForKey == nil {
		return "", missingExecutorPort("object_schema")
	}
	object, found := e.dependencies.ObjectForKey(objectKey)
	if !found {
		return "", apperror.New(apperror.KindBadRequest, "backend.notification.manager_recipient_field_invalid", nil, nil)
	}
	userField := false
	for _, field := range object.Fields {
		if field.Key == fieldKey && field.Type == "user" && field.DisabledAt == "" {
			userField = true
			break
		}
	}
	if !userField {
		return "", apperror.New(apperror.KindBadRequest, "backend.notification.manager_recipient_field_invalid", nil, nil)
	}
	result, err := e.QueryRecords(ctx, runtimeext.RecordQuery{Operation: runtimeext.QueryGet, ObjectKey: objectKey, RecordID: recordID})
	if err != nil {
		return "", err
	}
	if len(result.Records) != 1 {
		return "", apperror.New(apperror.KindNotFound, runtimeext.ErrorCodeRecordNotFound, nil, nil)
	}
	ownerID, ok := result.Records[0].Fields[fieldKey].(string)
	ownerID = strings.TrimSpace(ownerID)
	if !ok || ownerID == "" {
		return "", apperror.New(apperror.KindConflict, "backend.notification.manager_recipient_owner_invalid", nil, nil)
	}
	if e.dependencies.FindIdentityUser == nil {
		return "", missingExecutorPort("identity_user")
	}
	identityContext := requestcontext.WithWorkspaceID(e.unitOfWork.executionContext(ctx), e.workspace.ID)
	owner, found, err := e.dependencies.FindIdentityUser(identityContext, ownerID)
	if err != nil {
		return "", err
	}
	if !found || strings.TrimSpace(owner.ID) != ownerID {
		return "", nil
	}
	managerID := strings.TrimSpace(owner.ManagerUserID)
	if managerID == "" {
		return "", nil
	}
	if managerID == ownerID {
		return "", apperror.New(apperror.KindConflict, "backend.notification.manager_recipient_cycle", nil, nil)
	}
	manager, found, err := e.dependencies.FindIdentityUser(identityContext, managerID)
	if err != nil {
		return "", err
	}
	if !found || strings.TrimSpace(manager.ID) != managerID || strings.TrimSpace(manager.Status) != identitysdk.UserStatusActive || strings.TrimSpace(manager.WorkStatus) == "terminated" {
		return "", nil
	}
	return managerID, nil
}
