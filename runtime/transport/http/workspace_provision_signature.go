package http

import (
	"bytes"
	"io"
	"net/http"
	"time"

	workspaceprovision "github.com/domainry/domainry-runtime/runtime/application/workspaceprovision"
	signature "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

func provisioningSignaturePresented(r *http.Request) bool {
	for _, key := range []string{signature.SignatureHeader, signature.SignatureVersionHeader, signature.TimestampHeader, signature.ClientIDHeader} {
		if len(r.Header.Values(key)) != 0 {
			return true
		}
	}
	return false
}

func (s *HTTPRouter) authenticateProvisioningSignature(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	reject := func() (*http.Request, bool) {
		s.appendSecurityAudit(r, "workspace.provision_signature_denied", "Workspace provisioning signature rejected", nil)
		writeError(w, r, http.StatusUnauthorized, "workspace.provision_signature_invalid")
		return r, false
	}
	for _, key := range []string{signature.SignatureHeader, signature.SignatureVersionHeader, signature.TimestampHeader, signature.ClientIDHeader, signature.RuntimeIDHeader, "Idempotency-Key"} {
		if len(r.Header.Values(key)) != 1 {
			return reject()
		}
	}
	for _, key := range []string{"Authorization", "X-API-Key", "X-Workspace-ID", "X-User-ID", "X-Role", "X-User-Role"} {
		if len(r.Header.Values(key)) != 0 {
			return reject()
		}
	}
	if r.URL.EscapedPath() != "/workspaces" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.Header.Get("Authorization") != "" ||
		s.runtimeInstanceID == "" || r.Header.Get(signature.RuntimeIDHeader) != s.runtimeInstanceID {
		return reject()
	}
	limit := s.maxJSONBodyBytes
	if limit <= 0 {
		limit = 2 << 20
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		writeError(w, r, http.StatusRequestEntityTooLarge, "request.body_too_large")
		return r, false
	}
	ctx, err := workspaceprovision.VerifySignedProvision(r.Context(), body, signature.SignedRequest{
		Method: r.Method, Path: r.URL.EscapedPath(), RuntimeID: s.runtimeInstanceID, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	}, signature.Signature{
		Version: r.Header.Get(signature.SignatureVersionHeader), ClientID: r.Header.Get(signature.ClientIDHeader),
		Timestamp: r.Header.Get(signature.TimestampHeader), Value: r.Header.Get(signature.SignatureHeader),
	}, s.workspaceProvisionClientID, s.workspaceProvisionSigningSecret, time.Now())
	if err != nil {
		return reject()
	}
	r = r.WithContext(ctx)
	r.Body = io.NopCloser(bytes.NewReader(body))
	return r, true
}
