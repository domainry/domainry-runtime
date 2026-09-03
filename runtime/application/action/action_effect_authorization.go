package action

import (
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// actionReadEffectAuthorizationPrincipal grants only the object read verb
// declared by the compiler-owned Action EffectSet. Data permissions and
// guardrails remain those of the caller, so the Action cannot widen record
// scope or bypass an explicit governance restriction.
func actionReadEffectAuthorizationPrincipal(principal principalmodel.Principal, set *definitionmodel.ActionEffectSet, action definitionmodel.ActionSchema, objectKey string) principalmodel.Principal {
	if !actionEffectAllows(set, objectKey, false) {
		return principal
	}
	objectKey = strings.TrimSpace(objectKey)
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
