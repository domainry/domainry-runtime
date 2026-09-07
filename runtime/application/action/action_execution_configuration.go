package action

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type ApplicationExecutionConfiguration struct {
	SchemaRevision string
	TimeZone       string
}

func (e *BusinessHandlerExecutor) executionConfiguration(ctx context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, descriptor runtimeext.HandlerDescriptor, executionID string) (runtimeext.ExecutionIdentity, string, error) {
	if e.dependencies.ResolveApplicationConfiguration == nil {
		identity, err := e.executionIdentity(ctx, invocation, action, descriptor, executionID)
		if err != nil {
			return runtimeext.ExecutionIdentity{}, "", err
		}
		zone := e.dependencies.ApplicationTimeZone
		// Existing handwritten executors can omit the optional capability. A
		// Handler that actually requests it gets an explicit unavailable error.
		if zone != "" {
			if err := runtimeext.ValidateApplicationTimeZone(zone); err != nil {
				return runtimeext.ExecutionIdentity{}, "", err
			}
		}
		return identity, zone, nil
	}
	configuration, err := e.dependencies.ResolveApplicationConfiguration(ctx, invocation.Principal)
	if err != nil {
		return runtimeext.ExecutionIdentity{}, "", apperror.New(apperror.KindInternal, "backend.action.application_configuration_unavailable", err, nil)
	}
	if err := runtimeext.ValidateApplicationTimeZone(configuration.TimeZone); err != nil {
		return runtimeext.ExecutionIdentity{}, "", err
	}
	identity, err := e.executionIdentityWithRevision(invocation, action, descriptor, executionID, configuration.SchemaRevision)
	if err != nil {
		return runtimeext.ExecutionIdentity{}, "", err
	}
	return identity, configuration.TimeZone, nil
}

func (e *businessActionExecution) ApplicationTimeZone() (string, error) {
	if e == nil {
		return "", &runtimeext.BusinessError{Code: "backend.action.application_time_zone_unavailable"}
	}
	if err := runtimeext.ValidateApplicationTimeZone(e.applicationTimeZone); err != nil {
		return "", err
	}
	return e.applicationTimeZone, nil
}
