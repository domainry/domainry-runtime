package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

type runtimeAuthoringErrorSemantics struct {
	Class        string
	RepairAction string
	Retryable    bool
}

func serviceErrorHTTPStatus(r *http.Request, err error) int {
	status := http.StatusInternalServerError
	switch apperror.KindOf(err) {
	case apperror.KindBadRequest:
		status = http.StatusBadRequest
	case apperror.KindForbidden:
		status = http.StatusForbidden
	case apperror.KindNotFound:
		status = http.StatusNotFound
	case apperror.KindConflict:
		status = http.StatusConflict
	case apperror.KindRateLimited:
		status = http.StatusTooManyRequests
	case apperror.KindUnavailable:
		status = http.StatusServiceUnavailable
	}
	if !runtimeAuthoringRequestTrusted(r) {
		return status
	}
	code := apperror.CodeOf(err)
	switch apperror.KindOf(err) {
	case apperror.KindBadRequest:
		if !runtimeAuthoringProtocolError(code) {
			return http.StatusUnprocessableEntity
		}
	case apperror.KindUnavailable:
		if !runtimeAuthoringPlatformUnavailable(code) {
			return http.StatusFailedDependency
		}
	}
	return status
}

func runtimeAuthoringRequestTrusted(r *http.Request) bool {
	return r != nil && strings.TrimSpace(requestcontext.RuntimeAuthoringBuilderTaskID(r.Context())) != ""
}

func runtimeAuthoringProtocolError(code string) bool {
	switch strings.TrimSpace(code) {
	case "backend.authoring.request_identity_required",
		"backend.authoring.expected_schema_hash_required",
		"backend.idempotency.key_required",
		"backend.metadata.expected_schema_hash_required",
		"backend.metadata.expected_schema_hash_mismatch",
		"backend.invalid_json",
		"backend.request_body_too_large":
		return true
	default:
		return false
	}
}

func runtimeAuthoringPlatformUnavailable(code string) bool {
	switch strings.TrimSpace(code) {
	case "backend.authoring.owner_contract_unavailable",
		"backend.authoring.success_projection_unavailable",
		"backend.authoring.resource_projection_unavailable",
		"backend.authoring.snapshot_projection_unavailable":
		return true
	default:
		return false
	}
}

type capturedHTTPResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *capturedHTTPResponse) Header() http.Header {
	return r.header
}

func (r *capturedHTTPResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *capturedHTTPResponse) Write(payload []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(payload)
}

func (s *HTTPRouter) surfaceOpenAPIHandler(group SurfaceRouteGroup, full http.Handler) http.Handler {
	base := full
	if group == SurfaceRouteGroupPublic {
		raw := http.NewServeMux()
		s.openAPIHTTP.RegisterRoutes(raw)
		base = raw
	}
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		capture := &capturedHTTPResponse{header: make(http.Header)}
		base.ServeHTTP(capture, request)
		if capture.status != http.StatusOK {
			copyHTTPResponse(w, capture)
			return
		}
		var document map[string]any
		if err := json.Unmarshal(capture.body.Bytes(), &document); err != nil {
			writeError(w, request, http.StatusServiceUnavailable, "openapi.surface_projection_failed")
			return
		}
		if err := projectOpenAPIForSurfaceGroup(document, group); err != nil {
			writeError(w, request, http.StatusServiceUnavailable, "openapi.surface_projection_failed")
			return
		}
		// The document was decoded from JSON and projection only removes map
		// entries, so every remaining value is still JSON-encodable.
		payload, _ := json.Marshal(document)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	})
}

func projectOpenAPIForSurfaceGroup(document map[string]any, group SurfaceRouteGroup) error {
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return errors.New("OpenAPI paths are missing")
	}
	targets := routeGroupSurfaces(group)
	if len(targets) == 0 {
		return errors.New("unknown Surface route group")
	}
	for path, rawPathItem := range paths {
		pathItem, ok := rawPathItem.(map[string]any)
		if !ok {
			delete(paths, path)
			continue
		}
		for method := range pathItem {
			upperMethod := strings.ToUpper(method)
			switch upperMethod {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				continue
			}
			surfaces, classified := runtimeSurfaceRoutePolicies[upperMethod+" "+path]
			if !classified || !routeVisibleOnGroup(surfaces, targets) {
				delete(pathItem, method)
			}
		}
		hasOperation := false
		for method := range pathItem {
			switch strings.ToUpper(method) {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				hasOperation = true
			}
		}
		if !hasOperation {
			delete(paths, path)
		}
	}
	return nil
}

func copyHTTPResponse(w http.ResponseWriter, capture *capturedHTTPResponse) {
	for key, values := range capture.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := capture.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(capture.body.Bytes())
}

func runtimeAuthoringSemantics(status int, code string) runtimeAuthoringErrorSemantics {
	switch {
	case status == http.StatusBadRequest:
		return runtimeAuthoringErrorSemantics{Class: "protocol", RepairAction: "correct_request_protocol"}
	case status == http.StatusUnprocessableEntity:
		return runtimeAuthoringErrorSemantics{Class: "repairable", RepairAction: "repair_capability_payload", Retryable: true}
	case status == http.StatusConflict && runtimeAuthoringDriftCode(code):
		return runtimeAuthoringErrorSemantics{Class: "drift", RepairAction: "refresh_contract_and_snapshot", Retryable: true}
	case status == http.StatusConflict:
		return runtimeAuthoringErrorSemantics{Class: "conflict", RepairAction: "inspect_conflict"}
	case status == http.StatusFailedDependency:
		return runtimeAuthoringErrorSemantics{Class: "dependency", RepairAction: "resolve_runtime_dependency"}
	case status == http.StatusForbidden:
		return runtimeAuthoringErrorSemantics{Class: "permission", RepairAction: "resolve_authorization"}
	case status == http.StatusTooManyRequests || status >= http.StatusInternalServerError:
		return runtimeAuthoringErrorSemantics{Class: "transient", RepairAction: "retry_with_backoff", Retryable: true}
	default:
		return runtimeAuthoringErrorSemantics{Class: "request", RepairAction: "inspect_capability_diagnostic"}
	}
}

func runtimeAuthoringDriftCode(code string) bool {
	lowered := strings.ToLower(strings.TrimSpace(code))
	for _, marker := range []string{"schema", "snapshot", "contract", "version", "resource_hash"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}
