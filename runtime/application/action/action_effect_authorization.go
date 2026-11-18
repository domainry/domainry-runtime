package action

import (
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// actionReadEffectAuthorizationPrincipal grants the read verb required by the
// compiler-owned Action EffectSet. A dependent object with an authorization
// parent has no independent read ACL: the EffectSet still limits which object
// the Handler may query, while the transient execution principal receives only
// the parent's read authority used by Record policy.
//
// Read-only references may use the caller's existing read scope. Objects in
// the write effect retain the source Action's scope, even when the caller has
// broader generic read access.
func actionReadEffectAuthorizationPrincipal(principal principalmodel.Principal, set *definitionmodel.ActionEffectSet, action definitionmodel.ActionSchema, objectKey string, objectForKey func(string) (definitionmodel.ObjectSchema, bool)) principalmodel.Principal {
	if !actionEffectAllows(set, objectKey, false) {
		return principal
	}
	objectKey = strings.TrimSpace(objectKey)
	authorizationObjectKey := actionReadAuthorizationObjectKey(objectKey, objectForKey)
	if authorizationObjectKey == "" {
		return principal
	}
	if !actionEffectAllows(set, objectKey, true) && principal.HasPermission(authorizationObjectKey+".read") {
		// A member-owned mutation may reference a publicly readable store or
		// product. Replacing that independent read scope with owner would hide
		// valid references. This borrows no authority the caller did not have.
		return principal
	}
	authorized := principal
	if principal.AccessBundle != nil {
		resourceKey, operationKey := definitionmodel.ActionPermissionSubject(action)
		bundle, err := identitysdk.DeriveExecutionAccess(*principal.AccessBundle, identitysdk.ExecutionGrant{
			Resource: identitysdk.ResourceType(authorizationObjectKey), Action: identitysdk.Action("read"),
			SourceResource: identitysdk.ResourceType(resourceKey), SourceAction: identitysdk.Action(operationKey),
		}, principal.AuthorizationEvaluationTime())
		if err == nil {
			authorized.AccessBundle = &bundle
		}
		return authorized
	}
	if principal.SystemScope.Valid() {
		authorized.SystemCapabilities = append(append([]string(nil), principal.SystemCapabilities...), authorizationObjectKey+".read")
	}
	return authorized
}

func actionReadAuthorizationObjectKey(objectKey string, objectForKey func(string) (definitionmodel.ObjectSchema, bool)) string {
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" || objectForKey == nil {
		return objectKey
	}
	object, found := objectForKey(objectKey)
	if !found || object.AuthorizationParent == nil {
		return objectKey
	}
	parentKey := strings.TrimSpace(object.AuthorizationParent.ObjectKey)
	if parentKey == "" {
		// Invalid model state fails closed. Model validation normally rejects
		// this before Runtime starts, but execution must never manufacture a
		// child ACL when the declared parent cannot be resolved.
		return ""
	}
	if _, found := objectForKey(parentKey); !found {
		return ""
	}
	return parentKey
}
