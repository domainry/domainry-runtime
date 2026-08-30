package integration

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	"context"
	"errors"
	"fmt"

	"net/http"
	"strconv"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
)

// ActionConnectorGateway is the only Runtime-owned synchronous Connector
// entrypoint for generated Action clients. Administrative connection tests and
// other non-Action callers keep their separate application entrypoints.
type ActionConnectorGateway struct {
	execute func(context.Context, SyncCallRequest, principalmodel.Principal) (SyncCallResult, error)
}

func NewActionConnectorGateway(application *IntegrationApplicationService) *ActionConnectorGateway {
	if application == nil {
		return &ActionConnectorGateway{}
	}
	return &ActionConnectorGateway{execute: application.ExecuteIntegrationSyncCall}
}

func (g *ActionConnectorGateway) Call(ctx context.Context, execution runtimeext.ActionExecution, request SyncCallRequest) (SyncCallResult, error) {
	if execution == nil {
		return SyncCallResult{}, apperror.New(apperror.KindInternal, runtimeext.ConnectorActionExecutionRequiredErrorCode, nil, nil)
	}
	request.ActionExecution = true
	request.ActionKey = execution.Identity().ActionKey
	lease, err := execution.AcquireSynchronousConnectorCall(runtimeext.ActionConnectorCapability{
		ConnectorKey: request.ConnectorKey, ConnectionKey: request.ConnectionKey, OperationKey: request.Operation,
		ContractSHA256: request.ContractSHA256, Mode: runtimeext.ConnectorOperationMode(request.OperationMode), Effect: runtimeext.ConnectorOperationEffect(request.OperationEffect),
	})
	if err != nil {
		return SyncCallResult{}, err
	}
	if lease == nil {
		return SyncCallResult{}, apperror.New(apperror.KindInternal, runtimeext.ConnectorActionExecutionRequiredErrorCode, nil, nil)
	}
	defer lease.Release()
	if g == nil || g.execute == nil {
		return SyncCallResult{}, apperror.New(apperror.KindInternal, "backend.connector.gateway_unavailable", nil, nil)
	}
	if strings.TrimSpace(request.ContractSHA256) == "" || strings.TrimSpace(request.OperationMode) == "" {
		return SyncCallResult{}, badRequest("backend.integration.sync_call.operation_identity_required")
	}
	return g.execute(ctx, request, actionConnectorPrincipal(execution))
}

func actionConnectorPrincipal(execution runtimeext.ActionExecution) principalmodel.Principal {
	principal, workspace := execution.Principal(), execution.Workspace()
	return principalmodel.Principal{Principal: identitysdk.Principal{UserID: principal.UserID, WorkspaceID: workspace.ID, DepartmentID: principal.DepartmentID,
		RoleKey: principal.RoleKey, Known: principal.Known, AuthorizationRevision: principal.AuthorizationRevision}, RequestID: principal.RequestID, CorrelationID: principal.CorrelationID, CausationID: principal.CausationID,
		SurfaceKey: principal.SurfaceKey,
	}
}

func (s *IntegrationApplicationService) ExecuteIntegrationSyncCall(ctx context.Context, req SyncCallRequest, principal principalmodel.Principal) (SyncCallResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return SyncCallResult{}, err
	}
	connectorKey, connectionKey, operation := strings.TrimSpace(req.ConnectorKey), strings.TrimSpace(req.ConnectionKey), strings.TrimSpace(req.Operation)
	if connectorKey == "" || operation == "" {
		return SyncCallResult{}, badRequest("backend.integration.invocation.missing_identity")
	}
	if !s.connectorExists(connectorKey) {
		return SyncCallResult{}, notFound("backend.integration.connector.not_found")
	}
	if connectionKey == "" {
		return SyncCallResult{}, badRequest("backend.integration.connection.missing_key")
	}
	connection, ok, err := s.findConnection(ctx, connectionKey, principalWorkspaceID(principal))
	if err != nil {
		return SyncCallResult{}, err
	}
	connectionAllowed := connectionCanSend(connection) || (strings.TrimSpace(req.InvocationMode) == "test" && connectionCanTest(connection))
	if !ok || connection.ConnectorKey != connectorKey || !connectionAllowed {
		return SyncCallResult{}, badRequest("backend.integration.connection_unavailable")
	}
	if !s.ProviderSupportsIntegrationOperation(connection, operation) {
		return SyncCallResult{}, badRequest("backend.integration.operation.provider_unsupported", "connector", connectorKey, "provider", connection.ProviderKey, "operation", operation)
	}
	operationContract, err := s.IntegrationOperation(connection, operation)
	if err != nil {
		return SyncCallResult{}, err
	}
	if req.ActionExecution && (strings.TrimSpace(operationContract.ExecutionMode) != "sync" || strings.TrimSpace(operationContract.SideEffect) != "read") {
		return SyncCallResult{}, badRequest(runtimeext.ConnectorActionSideEffectOutboxErrorCode, "connector", connectorKey, "operation", operation)
	}
	declaredSideEffect := strings.TrimSpace(req.SideEffect)
	if declaredSideEffect != "" && declaredSideEffect != strings.TrimSpace(operationContract.SideEffect) {
		return SyncCallResult{}, badRequest("backend.integration.invocation.side_effect_mismatch", "connector", connectorKey, "operation", operation, "expected", operationContract.SideEffect, "actual", declaredSideEffect)
	}
	if err := s.validateSyncOperationIdentity(connection, req); err != nil {
		return SyncCallResult{}, err
	}
	req.SideEffect = strings.TrimSpace(operationContract.SideEffect)
	req = normalizeSyncCallGovernance(req, connection, operationContract)
	prepared, err := s.prepareRegisteredSyncCall(connection, connectorKey)
	if err != nil {
		return SyncCallResult{}, err
	}
	if integrationpolicy.IntegrationConnectionUsesRefreshToken(connection) {
		releaseCredentialLease, err := s.AcquireCredentialRefreshLease(ctx, connection, req.Timeout)
		if err != nil {
			return SyncCallResult{}, err
		}
		defer releaseCredentialLease()
	}
	secrets, err := s.ResolveAdapterSecrets(ctx, connection)
	if err != nil {
		return SyncCallResult{}, err
	}
	requestPayload := cloneMap(req.Request)
	if requestPayload == nil {
		requestPayload = map[string]any{}
	}
	redactedRequestPayload := integrationpolicy.RedactSensitiveMap(cloneMap(requestPayload))
	invocationKey := strings.TrimSpace(req.InvocationKey)
	if invocationKey == "" {
		invocationKey = operation
	}
	requestRef := strings.TrimSpace(req.RequestRef)
	if requestRef == "" {
		requestRef = "action:" + req.ActionKey + ":" + invocationKey + ":" + shortHash(fmt.Sprint(redactedRequestPayload))
	}
	started := time.Now().UTC()
	invocation := integrationmodel.IntegrationInvocation{
		WorkspaceID: principalWorkspaceID(principal), ConnectorKey: connectorKey, ProviderKey: connection.ProviderKey,
		ConnectionKey: connectionKey, Operation: operation, Status: "prepared", RequestRef: requestRef,
		ObjectKey: strings.TrimSpace(req.ObjectKey), RecordID: strings.TrimSpace(req.RecordID),
		Metadata: integrationpolicy.RedactSensitiveMap(map[string]any{
			"action_key": req.ActionKey, "invocation_key": invocationKey, "actor_id": principal.UserID, "role_key": principal.RoleKey,
			"request": redactedRequestPayload, "invocation_mode": strings.TrimSpace(req.InvocationMode), "side_effect": strings.TrimSpace(req.SideEffect),
			"compensation": integrationpolicy.RedactSensitiveMap(cloneMap(req.Compensation)), "compensation_policy": req.CompensationPolicy,
		}),
	}
	preparedInvocation, insertErr := s.invocationRepo.InsertInvocation(ctx, invocation.WorkspaceID, invocation)
	if insertErr != nil {
		return SyncCallResult{}, insertErr
	}
	responseRef, responsePayload, providerErrorCode := "", map[string]any{}, ""
	var admissionError, executionContextError error
	callErr := s.beforeSyncPolicy(ctx, req, connection, principal, started)
	if callErr == nil {
		lease, decision := s.connectorCapacity.Acquire(ctx, capacityplatform.Request{WorkspaceID: principalWorkspaceID(principal), UseCase: connection.ProviderKey, Retry: strings.Contains(strings.ToLower(req.InvocationMode), "retry"), Essential: true})
		if !decision.Allowed {
			kind := apperror.KindRateLimited
			if decision.Dimension == capacityplatform.DimensionProcess {
				kind = apperror.KindUnavailable
			}
			admissionError = &apperror.AppError{Kind: kind, Code: "backend.integration.sync_call.capacity_exhausted", Params: map[string]string{"dimension": string(decision.Dimension), "current": strconv.Itoa(decision.Current), "limit": strconv.Itoa(decision.Limit)}}
			callErr, responseRef = admissionError, "policy:capacity_"+string(decision.Dimension)
		} else {
			callResult, executeErr := func() (SyncAdapterResult, error) {
				defer lease.Release()
				callCtx, cancel := context.WithTimeout(ctx, req.Timeout)
				defer cancel()
				result, executeErr := prepared.Execute(callCtx, req, requestPayload, requestRef, principal, secrets)
				if executeErr == nil && callCtx.Err() != nil {
					executeErr = callCtx.Err()
				}
				return result, executeErr
			}()
			callErr, providerErrorCode = executeErr, callResult.ProviderErrorCode
			if errors.Is(executeErr, context.Canceled) || errors.Is(executeErr, context.DeadlineExceeded) {
				executionContextError = executeErr
			}
			if callResult.Response != nil {
				responsePayload = RedactProviderResponse(callResult.Response, secrets)
			}
			responseRef = callResult.ResponseRef
			if errors.Is(executeErr, context.DeadlineExceeded) && ctx.Err() == nil {
				callErr = errors.New("backend.integration.sync_call.timeout")
				responseRef = "policy:timeout"
			}
			persistErr := s.PersistAdapterSecretUpdates(ctx, connection, secrets, callResult.SecretUpdates)
			if persistErr != nil {
				callErr = persistErr
			} else {
				s.recordProviderResourceHealthFromCall(ctx, connection, callResult.ResourceHealth)
			}
			if persistErr == nil && executeErr == nil && connection.Status == "degraded" {
				s.recordCredentialRefreshRecovery(ctx, connection, principal)
			}
			if callErr == nil && req.ResponseSchema != nil {
				normalizedResponse, validationErr := validateSyncResponse(req.ResponseSchema, responsePayload, req.ResponseStrict)
				if validationErr != nil {
					callErr = validationErr
				} else {
					responsePayload = normalizedResponse
				}
			}
		}
		if admissionError == nil {
			_ = s.afterSyncPolicy(ctx, req, connection, principal, callErr == nil, time.Now().UTC())
		}
	}
	if callErr != nil && ctx.Err() == nil && responseRef == "oauth:refresh_failed" {
		s.recordCredentialRefreshFailure(ctx, connection, principal, callErr)
	}
	if callErr != nil && responseRef == "" {
		switch callErr.Error() {
		case "backend.integration.sync_call.circuit_open":
			responseRef = "policy:circuit_open"
		case "backend.integration.sync_call.rate_limited":
			responseRef = "policy:rate_limited"
		}
	}
	status, errorText := "succeeded", ""
	if callErr != nil {
		status = "failed"
		errorText, providerErrorCode = RedactSecretText(callErr.Error(), secrets), RedactSecretText(providerErrorCode, secrets)
	}
	retryable, retryReason := syncRetryability(errorText, responseRef)
	outcomeMetadata := cloneMap(preparedInvocation.Metadata)
	outcomeMetadata["response"], outcomeMetadata["retryable"], outcomeMetadata["retry_reason"] = responsePayload, retryable, retryReason
	outcomeMetadata["provider_status"], outcomeMetadata["provider_error_code"] = responseRef, providerErrorCode
	outcomeMetadata["response_schema"], outcomeMetadata["sync_policy"] = syncResponseSchemaMetadata(req.ResponseSchema, req.ResponseStrict), syncPolicyMetadata(req)
	var saved integrationmodel.IntegrationInvocation
	var updateErr error
	if outcomes, ok := s.invocationRepo.(integrationrepository.IntegrationInvocationOutcomeRepository); ok {
		saved, updateErr = outcomes.CompleteInvocation(ctx, preparedInvocation.WorkspaceID, preparedInvocation.ID, status, time.Since(started).Milliseconds(), responseRef, errorText, outcomeMetadata)
	} else {
		saved, updateErr = s.invocationRepo.UpdateInvocationStatus(ctx, preparedInvocation.WorkspaceID, preparedInvocation.ID, status, time.Since(started).Milliseconds(), responseRef, errorText)
	}
	if updateErr != nil {
		return SyncCallResult{ActionInvocation: preparedInvocation, Response: responsePayload}, updateErr
	}
	s.audit(ctx, "integration_invocation_recorded", "integration_invocation", saved.ID, principal, "Recorded integration invocation "+saved.Operation, nil, integrationprojection.IntegrationInvocationAuditShape(saved), map[string]any{
		"workspace_id": saved.WorkspaceID, "connector_key": saved.ConnectorKey, "connection_key": saved.ConnectionKey,
		"operation": saved.Operation, "status": saved.Status, "object_key": saved.ObjectKey, "record_id": saved.RecordID,
	})
	result := SyncCallResult{ActionInvocation: saved, Response: responsePayload}
	if callErr != nil {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if executionContextError != nil {
			return result, executionContextError
		}
		if admissionError != nil {
			return result, admissionError
		}
		return result, badRequest(valueOrDefault(errorText, "backend.integration.sync_call.failed"))
	}
	return result, nil
}

func validateSyncResponse(schema *definitionmodel.ObjectSchema, response map[string]any, strict bool) (map[string]any, error) {
	if schema == nil || len(schema.Fields) == 0 {
		return response, nil
	}
	declaredFields, declaredData := map[string]bool{}, map[string]any{}
	for _, field := range schema.Fields {
		key := strings.TrimSpace(field.Key)
		if key == "" {
			continue
		}
		declaredFields[key] = true
		if value, ok := response[key]; ok {
			declaredData[key] = value
		}
	}
	if strict {
		for key := range response {
			if !declaredFields[key] {
				return nil, badRequest("backend.integration.sync_call.response_schema_unknown_field", "field", key)
			}
		}
	}
	normalized, err := recordvalidation.RecordNormalizeData(*schema, declaredData, false)
	if err != nil {
		return nil, badRequest("backend.integration.sync_call.response_schema_invalid")
	}
	if err := recordvalidation.RecordValidateData(*schema, normalized, false); err != nil {
		return nil, badRequest("backend.integration.sync_call.response_schema_invalid")
	}
	out := cloneMap(response)
	if out == nil {
		out = map[string]any{}
	}
	for key, value := range normalized {
		out[key] = value
	}
	return out, nil
}

func syncResponseSchemaMetadata(schema *definitionmodel.ObjectSchema, strict bool) map[string]any {
	if schema == nil || len(schema.Fields) == 0 {
		return nil
	}
	fields := []string{}
	for _, field := range schema.Fields {
		if key := strings.TrimSpace(field.Key); key != "" {
			fields = append(fields, key)
		}
	}
	return map[string]any{"fields": fields, "strict": strict}
}

func syncPolicyMetadata(req SyncCallRequest) map[string]any {
	out := map[string]any{}
	if req.Timeout > 0 {
		out["timeout_seconds"] = int(req.Timeout.Seconds())
	}
	if req.CircuitThreshold > 0 {
		cooldown := req.CircuitCooldown
		if cooldown <= 0 {
			cooldown = time.Minute
		}
		out["circuit_breaker"] = map[string]any{"failure_threshold": req.CircuitThreshold, "cooldown_seconds": int(cooldown.Seconds())}
	}
	if req.RateLimitCount > 0 {
		window := req.RateLimitWindow
		if window <= 0 {
			window = time.Minute
		}
		out["rate_limit"] = map[string]any{"limit": req.RateLimitCount, "window_seconds": int(window.Seconds())}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func syncRetryability(errorText, responseRef string) (bool, string) {
	errorText, responseRef = strings.TrimSpace(errorText), strings.TrimSpace(responseRef)
	if errorText == "" {
		return false, ""
	}
	if strings.Contains(errorText, "backend.integration.sync_call.http_status_") {
		status := syncHTTPStatusFromError(errorText)
		switch {
		case status == http.StatusRequestTimeout:
			return true, "http_timeout"
		case status == http.StatusTooManyRequests:
			return true, "rate_limited"
		case status >= 500 && status <= 599:
			return true, "server_error"
		default:
			return false, "client_or_business_error"
		}
	}
	switch errorText {
	case "backend.integration.sync_call.http_failed":
		return true, "network_error"
	case "backend.integration.sync_call.timeout":
		return true, "timeout"
	case "backend.integration.sync_call.read_failed":
		return true, "response_read_failed"
	case "backend.integration.sync_call.response_too_large":
		return false, "response_too_large"
	case "backend.integration.sync_call.invalid_json":
		return false, "invalid_json"
	case "backend.integration.sync_call.rate_limited":
		return true, "rate_limited"
	case "backend.integration.sync_call.circuit_open":
		return true, "circuit_open"
	case "backend.integration.sync_call.invalid_endpoint", "backend.integration.sync_call.encode_failed":
		return false, "configuration_or_request_error"
	default:
		if strings.HasPrefix(responseRef, "http:5") {
			return true, "server_error"
		}
		return false, "non_retryable_error"
	}
}
