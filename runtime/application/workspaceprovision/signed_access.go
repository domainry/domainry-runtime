package workspaceprovision

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	signature "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

type signedProvisionKey struct{}
type signedProvisionAccess struct{ clientID, requestID string }

// VerifySignedProvision reuses the released v2 request signature contract.
// The callback verifier fixes its caller to Scheduler; this adapter instead
// checks the host-configured provisioning caller, using the same SignRequest
// canonicalization and clock window. It grants only this existing use case.
func VerifySignedProvision(ctx context.Context, body []byte, request signature.SignedRequest, supplied signature.Signature, clientID string, secret []byte, now time.Time) (context.Context, error) {
	invalid := signature.ErrCallbackSignatureInvalid
	if clientID == "" || strings.TrimSpace(clientID) != clientID || strings.ContainsAny(clientID, "\r\n") || len(secret) < 32 ||
		request.Method != "POST" || request.Path != "/workspaces" || strings.ContainsAny(request.RuntimeID+request.IdempotencyKey, "\r\n") ||
		supplied.Version != signature.CallbackSignatureContractVersion || supplied.ClientID != clientID {
		return ctx, invalid
	}
	seconds, err := strconv.ParseInt(supplied.Timestamp, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != supplied.Timestamp {
		return ctx, invalid
	}
	if delta := now.Sub(time.Unix(seconds, 0)); delta < -signature.MaxClockSkew || delta > signature.MaxClockSkew {
		return ctx, signature.ErrCallbackSignatureStale
	}
	want, err := signature.SignRequest(body, request, clientID, supplied.Timestamp, secret)
	if err != nil {
		return ctx, invalid
	}
	wantBytes, _ := hex.DecodeString(want)
	gotBytes, err := hex.DecodeString(supplied.Value)
	if err != nil || !hmac.Equal(wantBytes, gotBytes) {
		return ctx, invalid
	}
	var input struct {
		RequestID string `json:"request_id"`
	}
	if json.Unmarshal(body, &input) != nil || input.RequestID != request.IdempotencyKey {
		return ctx, invalid
	}
	return context.WithValue(ctx, signedProvisionKey{}, signedProvisionAccess{clientID, input.RequestID}), nil
}

// SignedProvisionClient returns only verified, use-case-specific authority.
// No Identity user, role or reusable administrator permission is synthesized.
func SignedProvisionClient(ctx context.Context, requestID string) string {
	access, ok := ctx.Value(signedProvisionKey{}).(signedProvisionAccess)
	if !ok || (requestID != "" && requestID != access.requestID) {
		return ""
	}
	return access.clientID
}
