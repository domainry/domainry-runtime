package record

import (
	"context"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func recordOwnedEffectAuthorizationPrincipal(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema) (principalmodel.Principal, error) {
	const action = "create"
	invocation, ok := recordmutation.MutationInvocationFromContext(ctx)
	if object.AuthorizationParent == nil || !ok || invocation.Source != transactionmodel.MutationSourceAction || len(invocation.EffectAuthority[object.Key]) == 0 {
		return principal, ownedRecordEffectDenied()
	}
	if principal.AccessBundle != nil {
		allowed := false
		for _, grant := range principal.AccessBundle.FunctionGrants {
			if string(grant.Resource) == invocation.ActionResource && string(grant.Action) == invocation.ActionOperation {
				if grant.Effect == identitysdk.EffectDeny {
					return principal, ownedRecordEffectDenied()
				}
				allowed = allowed || grant.Effect == identitysdk.EffectAllow
			}
		}
		if !allowed {
			return principal, ownedRecordEffectDenied()
		}
	}
	// The Action executor exposes only earlier, authorized canonical plans in
	// this transaction. A readable-but-unmodified parent is not write authority.
	parent := object.AuthorizationParent
	ownerIDs := []string{}
	for id, record := range recordservice.RecordPlannedRelations(ctx)[parent.ObjectKey] {
		if id != "" && record.ID == id && !record.Deleted && record.WorkspaceID == principal.WorkspaceID && principal.WorkspaceID != "" {
			ownerIDs = append(ownerIDs, id)
		}
	}
	if len(ownerIDs) == 0 {
		return principal, ownedRecordEffectDenied()
	}
	sort.Strings(ownerIDs)
	authorized := principal
	if principal.AccessBundle != nil {
		// The parent plan already carries the user's authorization. Grant only
		// this internal create verb, then bind its candidate to those exact
		// parent IDs; never pass SourceResource to SDK predicate projection.
		bundle, err := identitysdk.DeriveExecutionAccess(*principal.AccessBundle, identitysdk.ExecutionGrant{Resource: identitysdk.ResourceType(object.Key), Action: action}, principal.AuthorizationEvaluationTime())
		if err != nil {
			return principal, ownedRecordEffectDenied()
		}
		policies := make([]identitysdk.DataPolicy, 0, len(bundle.DataPolicies)+1)
		for _, policy := range bundle.DataPolicies {
			if string(policy.Resource) == object.Key && string(policy.Action) == action && policy.Effect == identitysdk.EffectAllow {
				continue
			}
			policies = append(policies, policy)
		}
		policies = append(policies, identitysdk.DataPolicy{
			Key: "runtime.owned_effect." + object.Key + "." + action, Resource: identitysdk.ResourceType(object.Key), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow,
			Predicate: identitysdk.Predicate{Fact: parent.RelationField, Operator: identitysdk.OperatorIn, Value: ownerIDs},
		})
		bundle.DataPolicies = policies
		authorized.AccessBundle = &bundle
	} else if principal.SystemScope.Valid() && principal.HasPermission(invocation.ActionResource+"."+invocation.ActionOperation) {
		authorized.SystemCapabilities = append(append([]string(nil), principal.SystemCapabilities...), object.Key+"."+action)
	} else {
		return principal, ownedRecordEffectDenied()
	}
	return authorized, nil
}

// Ownership is a local binding check, not another user ACL. It is repeated
// after before-hooks so no later transform can redirect an approved child.
func recordValidateOwnedEffectCandidate(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, principal principalmodel.Principal) error {
	if object.AuthorizationParent == nil {
		return nil
	}
	parent := object.AuthorizationParent
	ownerID, valid := data[parent.RelationField].(string)
	ownerID = strings.TrimSpace(ownerID)
	owner, found := recordservice.RecordPlannedRelations(ctx)[parent.ObjectKey][ownerID]
	invocation, actionOwned := recordmutation.MutationInvocationFromContext(ctx)
	if !valid || ownerID == "" || !found || owner.ID != ownerID || owner.Deleted || principal.WorkspaceID == "" || owner.WorkspaceID != principal.WorkspaceID || !actionOwned || invocation.Source != transactionmodel.MutationSourceAction || len(invocation.EffectAuthority[object.Key]) == 0 {
		return ownedRecordEffectDenied()
	}
	return nil
}

func ownedRecordEffectDenied() error {
	return apperror.New(apperror.KindForbidden, "backend.mutation.owned_parent_denied", nil, nil)
}
