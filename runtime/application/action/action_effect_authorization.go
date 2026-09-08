package action

import (
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// actionReadEffectAuthorizationPrincipal grants only the object read verb
// declared by the compiler-owned Action EffectSet. Read-only references may
// use the caller's existing read scope. Objects in the write effect retain the
// source Action's scope, even when the caller has broader generic read access.
func actionReadEffectAuthorizationPrincipal(principal principalmodel.Principal, set *definitionmodel.ActionEffectSet, action definitionmodel.ActionSchema, objectKey string) principalmodel.Principal {
	if !actionEffectAllows(set, objectKey, false) {
		return principal
	}
	objectKey = strings.TrimSpace(objectKey)
	if !actionEffectAllows(set, objectKey, true) && principal.HasPermission(objectKey+".read") {
		// A member-owned mutation may reference a publicly readable store or
		// product. Replacing that independent read scope with owner would hide
		// valid references. This borrows no authority the caller did not have.
		return principal
	}
	authorized := principal
	if principal.AccessBundle != nil {
		resourceKey, operationKey := definitionmodel.ActionPermissionSubject(action)
		bundle, err := identitysdk.DeriveExecutionAccess(*principal.AccessBundle, identitysdk.ExecutionGrant{
			Resource: identitysdk.ResourceType(objectKey), Action: identitysdk.Action("read"),
			SourceResource: identitysdk.ResourceType(resourceKey), SourceAction: identitysdk.Action(operationKey),
		}, time.Now().UTC())
		if err == nil {
			authorized.AccessBundle = &bundle
		}
		return authorized
	}
	if principal.SystemScope.Valid() {
		authorized.SystemCapabilities = append(append([]string(nil), principal.SystemCapabilities...), objectKey+".read")
	}
	return authorized
}
