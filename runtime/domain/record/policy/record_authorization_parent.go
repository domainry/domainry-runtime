package policy

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// Parent-authorized dependent reads need no separately assigned child verb.
// Supply only the default field-read envelope; explicit field rules, masks,
// sensitive-field closure and guardrails still apply. Record row visibility
// remains the parent's compiled scope, not a copied child data policy.
func RecordAuthorizationParentReadFieldPrincipal(principal principalmodel.Principal, object definitionmodel.ObjectSchema) principalmodel.Principal {
	if object.AuthorizationParent == nil || principal.AccessBundle == nil {
		return principal
	}
	if allowed, _ := RecordSDKAllowsObjectAction(principal, object.AuthorizationParent.ObjectKey, "read"); !allowed {
		return principal
	}
	for _, field := range principal.AccessBundle.FieldPolicies {
		if field.Resource == identitysdk.ResourceType(object.Key) && field.Field == "*" {
			return principal
		}
	}
	bundle := *principal.AccessBundle
	bundle.FieldPolicies = append(append([]identitysdk.FieldPolicy(nil), bundle.FieldPolicies...), identitysdk.FieldPolicy{Resource: identitysdk.ResourceType(object.Key), Field: "*", Read: true})
	principal.AccessBundle = &bundle
	return principal
}
