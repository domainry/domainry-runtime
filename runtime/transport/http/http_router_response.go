package http

import (
	"context"
	"encoding/json"
	"errors"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	"io"
	"net/http"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"

	apperror "github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.uber.org/zap"
)

type principalContextKey struct{}

func requestWithPrincipal(r *http.Request, principal principalmodel.Principal) *http.Request {
	ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
	ctx = requestcontext.WithWorkspaceID(ctx, principal.WorkspaceID)
	ctx = requestcontext.WithActorID(ctx, principal.UserID)
	return r.WithContext(ctx)
}

func principalFromContext(r *http.Request) (principalmodel.Principal, bool) {
	principal, ok := r.Context().Value(principalContextKey{}).(principalmodel.Principal)
	return principal, ok && principal.Known
}

func (s *HTTPRouter) decodeJSONBody(w http.ResponseWriter, r *http.Request, value any) bool {
	limit := s.maxJSONBodyBytes
	if limit <= 0 {
		limit = 2 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		s.writeJSONDecodeError(w, r, err)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		s.writeJSONDecodeError(w, r, err)
		return false
	}
	return true
}

func (s *HTTPRouter) writeJSONDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		if s.httpMetrics != nil {
			s.httpMetrics.ObserveBodyRejection("too_large")
		}
		writeError(w, r, http.StatusRequestEntityTooLarge, "backend.request_body_too_large")
		return
	}
	if s.httpMetrics != nil {
		s.httpMetrics.ObserveBodyRejection("invalid_json")
	}
	writeError(w, r, http.StatusBadRequest, "backend.invalid_json")
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func (s *HTTPRouter) principalFromRequest(r *http.Request) principalmodel.Principal {
	requestID := requestIDFromRequest(r)
	workspaceID := workspaceIDFromRequest(r)
	if builderTaskID := operationscontract.BuilderTaskID(r.Context()); builderTaskID != "" && bearerTokenFromRequest(r) == "" && apiKeyTokenFromRequest(r) == "" && strings.TrimSpace(r.Header.Get("X-User-ID")) == "" && strings.TrimSpace(r.Header.Get("X-Role")) == "" && strings.TrimSpace(r.Header.Get("X-User-Role")) == "" {
		principal := principalmodel.NewSystemPrincipal(
			"runtime-builder:"+builderTaskID,
			principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "runtime_direct_authoring"),
			businesssystemapplication.ActionBusinessSystemSnapshot,
			businesssystemapplication.ActionValidateRuntimeAuthoring,
			businesssystemapplication.ActionVerifyRuntimeAuthoringDelivery,
			schedulersdk.ActionSchedulerDefinitionsList,
			"runtime.automation.list_automation_rules",
			"runtime.automation.list_automation_executions",
		)
		principal.RequestID = requestID
		principal.WorkspaceID = workspaceID
		return principal
	}
	if principal, ok := principalFromContext(r); ok {
		principal.RequestID = requestID
		if workspaceID != "" && !strings.EqualFold(workspaceID, "default") {
			principal.WorkspaceID = workspaceID
		}
		return s.principalWithBusinessProfile(principal, r)
	}
	if token := apiKeyTokenFromRequest(r); token != "" {
		principal, _, err := s.integrationAuth.PrincipalFromIntegrationAPIKey(r.Context(), token, workspaceID, requestID)
		if err == nil {
			return s.principalWithBusinessProfile(principal, r)
		}
		return principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: workspaceID, Known: false}, RequestID: requestID}
	}
	if bearerTokenFromRequest(r) != "" {
		return principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: workspaceID, Known: false}, RequestID: requestID}
	}
	userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
	if s.allowDevAuthHeaders && userID != "" {
		if strings.TrimSpace(r.Header.Get("X-Role")) != "" || strings.TrimSpace(r.Header.Get("X-User-Role")) != "" {
			roleKey := valueOrDefault(strings.TrimSpace(r.Header.Get("X-Role")), strings.TrimSpace(r.Header.Get("X-User-Role")))
			principal := principalmodel.Principal{}
			var err error
			if s.identityAuthorization != nil {
				principal, err = resolveIdentitySDKPrincipal(r.Context(), s.identityAuthorization, userID, roleKey, requestID)
			}
			if err != nil || !principal.Known {
				principal = principalmodel.Principal{Principal: identitysdk.Principal{UserID: userID, Known: false}}
			}
			principal.RequestID = requestID
			principal.WorkspaceID = workspaceID
			return s.principalWithBusinessProfile(principal, r)
		}
		principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: userID, Known: false}}
		var err error
		if s.identityAuthorization != nil {
			principal, err = resolveIdentitySDKPrincipal(r.Context(), s.identityAuthorization, userID, "", requestID)
		}
		if err == nil && principal.Known {
			principal.RequestID = requestID
			principal.WorkspaceID = workspaceID
			return s.principalWithBusinessProfile(principal, r)
		}
		principal.RequestID = requestID
		principal.WorkspaceID = workspaceID
		return s.principalWithBusinessProfile(principal, r)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: userID, WorkspaceID: workspaceID, Known: false}}
	principal.RequestID = requestID
	return s.principalWithBusinessProfile(principal, r)
}

func (s *HTTPRouter) principalWithBusinessProfile(principal principalmodel.Principal, r *http.Request) principalmodel.Principal {
	if !principal.Known || s.businessPrincipal == nil {
		return principal
	}
	resolved, err := s.businessPrincipal.ResolveBusinessPrincipal(
		r.Context(),
		principal,
		r.Header.Get("X-Business-Profile-Key"),
		r.Header.Get("X-Business-Profile-ID"),
	)
	if err == nil {
		return resolved
	}
	s.appendSecurityAuditForPrincipal(r, principal, "business_profile_selection_denied", "Business profile selection denied", map[string]any{
		"binding_key": r.Header.Get("X-Business-Profile-Key"), "record_id": r.Header.Get("X-Business-Profile-ID"), "reason": err.Error(),
	})
	principal.Known = false
	principal.BusinessProfiles = nil
	principal.ActiveBusinessProfile = nil
	principal.BusinessClaims = nil
	return principal
}

func workspaceIDFromRequest(r *http.Request) string {
	workspaceID := explicitWorkspaceIDFromRequest(r)
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(requestcontext.WorkspaceID(r.Context()))
	}
	if workspaceID == "" {
		workspaceID = principalmodel.InstallationWorkspaceID
	}
	return workspaceID
}

func explicitWorkspaceIDFromRequest(r *http.Request) string {
	if workspaceID := strings.TrimSpace(r.Header.Get("X-Workspace-ID")); workspaceID != "" {
		return workspaceID
	}
	if workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id")); workspaceID != "" {
		return workspaceID
	}
	return strings.TrimSpace(r.PathValue("workspaceID"))
}

func authenticatedWorkspaceMatchesRequest(principal principalmodel.Principal, r *http.Request) bool {
	principalWorkspaceID := strings.TrimSpace(principal.WorkspaceID)
	if principalWorkspaceID == "" {
		return false
	}
	targetWorkspaceID := explicitWorkspaceIDFromRequest(r)
	return targetWorkspaceID == "" || targetWorkspaceID == principalWorkspaceID
}

func devAuthHeadersPresent(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("X-User-ID")) != ""
}

func bearerTokenFromRequest(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return ""
	}
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func apiKeyTokenFromRequest(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-API-Key")); value != "" {
		return value
	}
	token := bearerTokenFromRequest(r)
	if strings.HasPrefix(token, "vapi_") {
		return token
	}
	return ""
}

func (s *HTTPRouter) actorIDFromRequest(r *http.Request) string {
	if principal, ok := principalFromContext(r); ok {
		return principal.UserID
	}
	if token := apiKeyTokenFromRequest(r); token != "" {
		return "integration:api_key"
	}
	if requestIdentity, ok := identitysdk.RequestIdentityFromContext(r.Context()); ok {
		return requestIdentity.Principal.UserID
	}
	return strings.TrimSpace(r.Header.Get("X-User-ID"))
}

func (s *HTTPRouter) appendSecurityAudit(r *http.Request, event string, summary string, metadata map[string]any) {
	s.appendSecurityAuditForPrincipal(r, s.auditPrincipalFromRequest(r), event, summary, metadata)
}

func (s *HTTPRouter) appendSecurityAuditForPrincipal(r *http.Request, principal principalmodel.Principal, event string, summary string, metadata map[string]any) {
	if s.securityAudit == nil {
		return
	}
	metadata = cloneStringAnyMap(metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["path"] = r.URL.Path
	metadata["method"] = r.Method
	s.securityAudit.AppendWithMetadata(r.Context(), event, "auth", "", principal, summary, nil, nil, metadata)
}

func (s *HTTPRouter) auditPrincipalFromRequest(r *http.Request) principalmodel.Principal {
	requestID := requestIDFromRequest(r)
	if principal, ok := principalFromContext(r); ok {
		principal.RequestID = requestID
		return principal
	}
	if token := apiKeyTokenFromRequest(r); token != "" {
		principal, _, err := s.integrationAuth.PrincipalFromIntegrationAPIKey(r.Context(), token, workspaceIDFromRequest(r), requestID)
		if err == nil {
			return principal
		}
	}
	if s.allowDevAuthHeaders && devAuthHeadersPresent(r) {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		roleKey := valueOrDefault(strings.TrimSpace(r.Header.Get("X-Role")), strings.TrimSpace(r.Header.Get("X-User-Role")))
		principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: userID, Known: false}}
		if s.identityAuthorization != nil {
			var err error
			principal, err = resolveIdentitySDKPrincipal(r.Context(), s.identityAuthorization, userID, roleKey, requestID)
			if err != nil {
				principal = principalmodel.Principal{Principal: identitysdk.Principal{UserID: userID, Known: false}}
			}
		}
		principal.RequestID = requestID
		principal.WorkspaceID = workspaceIDFromRequest(r)
		return principal
	}
	return principalmodel.Principal{Principal: identitysdk.Principal{UserID: s.actorIDFromRequest(r), WorkspaceID: workspaceIDFromRequest(r), Known: false}, RequestID: requestID}
}

func resolveIdentitySDKPrincipal(ctx context.Context, resolver identitysdk.PrincipalResolver, userID, roleKey, requestID string) (principalmodel.Principal, error) {
	resolution, err := resolver.Resolve(ctx, identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(userID), RoleKey: roleKey})
	if err != nil {
		return principalmodel.Principal{}, err
	}
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	return principalmodel.NewPrincipalFromIdentity(resolution.Principal, requestID), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		zap.L().Error("write HTTP response failed", logging.StableErrorFields(err)...)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code string, params ...string) {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "backend.request_failed"
	}
	paramMap := responseParams(params...)
	writeErrorWithParams(w, r, status, code, paramMap)
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		writeErrorWithParams(w, r, 499, "backend.request_cancelled", nil)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeErrorWithParams(w, r, http.StatusGatewayTimeout, "backend.deadline_exceeded", nil)
		return
	}
	status := serviceErrorHTTPStatus(r, err)
	if status >= http.StatusInternalServerError {
		logging.FromContext(r.Context()).Error("http service error", logging.StableErrorFields(err)...)
	}
	writeErrorWithParams(w, r, status, publicServiceErrorCode(err), apperror.ParamsOf(err))
}

func publicServiceErrorCode(err error) string {
	code := apperror.CodeOf(err)
	if apperror.KindOf(err) == apperror.KindForbidden && code == "backend.permission.denied" {
		return "auth.permission_denied"
	}
	return code
}

func writeErrorWithParams(w http.ResponseWriter, r *http.Request, status int, code string, params map[string]string) {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "backend.request_failed"
	}
	params = apperror.SanitizeParams(params)
	contract := capabilitycontract.RuntimeAuthoringErrorContract(code, params)
	response := map[string]any{
		"error": code, "code": code, "message": code, "message_key": code, "params": params,
		"request_id":       requestIDFromRequest(r),
		"contract_version": contract.ContractVersion,
	}
	if contract.FieldPath != "" {
		response["field_path"] = contract.FieldPath
	}
	if contract.CapabilityKey != "" {
		response["capability_key"] = contract.CapabilityKey
	}
	if runtimeAuthoringRequestTrusted(r) {
		semantics := runtimeAuthoringSemantics(status, code)
		response["error_class"] = semantics.Class
		response["repair_action"] = semantics.RepairAction
		response["retryable"] = semantics.Retryable
	}
	writeJSON(w, status, response)
}
