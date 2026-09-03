package action

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func actionValidateInvocationAssurance(ctx context.Context, validate func(context.Context, actionmodel.ActionInvocation) (map[string]string, error), invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocation, error) {
	if invocation.AssuranceValidated || validate == nil {
		return invocation, nil
	}
	evidence, err := validate(ctx, invocation)
	if err != nil {
		return invocation, err
	}
	invocation.AssuranceEvidence = evidence
	invocation.AssuranceValidated = true
	return invocation, nil
}

func actionAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID); !principal.Known || err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func actionAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceCommandScope(principal.WorkspaceID); !principal.Known || err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

// ActionAllowed evaluates the Identity-owned permission model at the Action
// application boundary instead of coupling the Action domain to Identity
// behavior packages.
func ActionAllowed(principal principalmodel.Principal, action definitionmodel.ActionSchema) bool {
	if !principal.Known {
		return false
	}
	return principal.HasExactPermission(strings.TrimSpace(action.Key))
}

// ActionPersistencePrincipal preserves an already-approved Action as the
// write authority for its own object. Record scope and field rules still apply.

func ActionPersistencePrincipal(principal principalmodel.Principal, action definitionmodel.ActionSchema, operation string) principalmodel.Principal {
	resourceKey, operationKey := definitionmodel.ActionPermissionSubject(action)
	objectKey := strings.TrimSpace(action.ObjectKey)
	operation = strings.TrimSpace(operation)
	if principal.AccessBundle != nil {
		bundle, err := identitysdk.DeriveExecutionAccess(*principal.AccessBundle, identitysdk.ExecutionGrant{
			Resource: identitysdk.ResourceType(objectKey), Action: identitysdk.Action(operation),
			SourceResource: identitysdk.ResourceType(resourceKey), SourceAction: identitysdk.Action(operationKey),
		}, time.Now().UTC())
		if err == nil {
			principal.AccessBundle = &bundle
		}
		return principal
	}
	if principal.SystemScope.Valid() {
		permission := objectKey + "." + operation
		if !principal.HasPermission(permission) {
			principal.SystemCapabilities = append(append([]string(nil), principal.SystemCapabilities...), permission)
		}
	}
	return principal
}
