package record

import (
	"context"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

// recordEffectAuthorizationPrincipal allows the canonical Record use case to
// resolve and policy-check an Action-owned internal write without granting the
// caller broad object CRUD permissions. Only the object operation is derived;
// field permissions do not govern writes. The original principal remains the
// actor used by validation, audit, workflow and the Mutation Planner.
func recordEffectAuthorizationPrincipal(ctx context.Context, principal principalmodel.Principal, objectKey, action string) principalmodel.Principal {
	action = strings.TrimSpace(action)
	if action != "create" && action != "update" && action != "delete" && action != "restore" {
		return principal
	}
	invocation, ok := recordmutation.MutationInvocationFromContext(ctx)
	if !ok || invocation.Source != transactionmodel.MutationSourceAction {
		return principal
	}
	fields, ok := invocation.EffectAuthority[strings.TrimSpace(objectKey)]
	if !ok {
		return principal
	}
	hasAuthority := false
	for _, field := range fields {
		if strings.TrimSpace(field) != "" {
			hasAuthority = true
			break
		}
	}
	if !hasAuthority {
		return principal
	}
	authorized := principal
	if principal.AccessBundle != nil {
		bundle, err := identitysdk.DeriveExecutionAccess(*principal.AccessBundle, identitysdk.ExecutionGrant{
			Resource: identitysdk.ResourceType(strings.TrimSpace(objectKey)), Action: identitysdk.Action(action),
			SourceResource: identitysdk.ResourceType(strings.TrimSpace(invocation.ActionResource)), SourceAction: identitysdk.Action(strings.TrimSpace(invocation.ActionOperation)),
		}, time.Now().UTC())
		if err == nil {
			authorized.AccessBundle = &bundle
		}
		return authorized
	}
	if principal.SystemScope.Valid() {
		authorized.SystemCapabilities = append(append([]string(nil), principal.SystemCapabilities...), strings.TrimSpace(objectKey)+"."+action)
	}
	return authorized
}
