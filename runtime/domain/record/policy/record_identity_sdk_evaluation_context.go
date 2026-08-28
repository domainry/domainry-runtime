package policy

import (
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// RecordSDKEvaluationContext supplies Runtime-owned business-profile claims to
// the SDK evaluator without modifying the Identity-issued AccessBundle.
func RecordSDKEvaluationContext(principal principalmodel.Principal) identityevaluator.EvaluationContext {
	context := identityevaluator.EvaluationContext{BusinessClaims: map[string]any{}}
	if principal.AccessBundle != nil {
		context.Subject = principal.AccessBundle.Subject
	}
	if principal.ActiveBusinessProfile != nil {
		context.BusinessClaims["business_profile_id"] = principal.ActiveBusinessProfile.RecordID
	}
	for key, claim := range principal.BusinessClaims {
		context.BusinessClaims[key] = claim.Value
	}
	return context
}
