package integration

import (
	"context"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	"strings"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// TestConnectorOperation executes the Integration-owned connection test
// command. Cross-domain composition only supplies the protocol validators.
func (s *IntegrationApplicationService) TestConnectorOperation(ctx context.Context, connectionKey string, req ConnectorOperationTestRequest, principal principalmodel.Principal) (ConnectorOperationTestResult, error) {
	if err := ctx.Err(); err != nil {
		return ConnectorOperationTestResult{}, err
	}
	if err := integrationAuthorizeCommand(principal); err != nil {
		return ConnectorOperationTestResult{}, err
	}
	if !HasPermission(principal, PermissionConnectionTest) {
		return ConnectorOperationTestResult{}, forbidden("auth.permission_denied")
	}
	if !req.Confirm {
		return ConnectorOperationTestResult{}, badRequest("backend.integration.operation_test_confirmation_required")
	}
	connection, ok := s.LookupConnection(ctx, strings.TrimSpace(connectionKey), principalWorkspaceID(principal))
	if !ok || !ConnectionCanTest(connection) {
		return ConnectorOperationTestResult{}, badRequest("backend.integration.connection_unavailable", "connection", connectionKey)
	}
	operation, err := s.IntegrationOperation(connection, req.Operation)
	if err != nil {
		return ConnectorOperationTestResult{}, err
	}
	if err := s.validateOperationInput(connection.ConnectorKey, operation, req.Input); err != nil {
		return ConnectorOperationTestResult{}, err
	}
	timeoutSeconds := operation.TimeoutDefaultSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = integrationpolicy.IntegrationConfigInt(connection.Config, 10, "timeout_seconds", "timeout")
	}
	result, err := s.ExecuteIntegrationSyncCall(ctx, SyncCallRequest{
		ConnectorKey: connection.ConnectorKey, ConnectionKey: connection.Key, Operation: operation.Key, Method: operation.Method,
		Request: req.Input, ActionKey: "connector.operation_test", InvocationKey: operation.Key, InvocationMode: "test", SideEffect: operation.SideEffect,
		RequestRef: "connector-test:" + connection.Key + ":" + operation.Key + ":" + integrationruntime.IntegrationShortHash(principal.RequestID),
		Timeout:    time.Duration(timeoutSeconds) * time.Second,
	}, principal)
	if err != nil {
		if ctx.Err() == nil && operation.Key == "test_connection" {
			_ = s.RecordCredentialTestEvidence(ctx, connection, false, err)
			connection = s.RecordConnectionTestStatus(ctx, connection, "degraded", principal)
		}
		return ConnectorOperationTestResult{Connection: connection, Operation: operation, Response: result.Response, ActionInvocation: result.ActionInvocation}, err
	}
	if err := s.validateOperationOutput(connection.ConnectorKey, operation, result.Response); err != nil {
		return ConnectorOperationTestResult{Connection: connection, Operation: operation, Response: result.Response, ActionInvocation: result.ActionInvocation}, err
	}
	if operation.Key == "test_connection" {
		if err := s.RecordCredentialTestEvidence(ctx, connection, true, nil); err != nil {
			return ConnectorOperationTestResult{Connection: connection, Operation: operation, Response: result.Response, ActionInvocation: result.ActionInvocation}, err
		}
	}
	connection, err = s.PersistConnectionTestStatus(ctx, connection, "verified", principal)
	if err != nil {
		return ConnectorOperationTestResult{Connection: connection, Operation: operation, Response: result.Response, ActionInvocation: result.ActionInvocation}, err
	}
	return ConnectorOperationTestResult{Connection: connection, Operation: operation, Response: result.Response, ActionInvocation: result.ActionInvocation}, nil
}
