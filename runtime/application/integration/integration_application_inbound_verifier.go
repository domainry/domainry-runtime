package integration

import integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
)

func (s *IntegrationApplicationService) verifyRegisteredInboundWebhook(ctx context.Context, request integrationcontract.InboundWebhookRequest) (integrationcontract.VerifiedInboundWebhook, error) {
	adapter, ok := s.AdapterForConnection(request.Connection)
	if !ok {
		return integrationcontract.VerifiedInboundWebhook{}, badRequest("backend.integration.webhook.provider_unsupported")
	}
	verifier, ok := adapter.(integrationcontract.WebhookVerifier)
	if !ok {
		return integrationcontract.VerifiedInboundWebhook{}, badRequest("backend.integration.webhook.provider_does_not_accept_events")
	}
	verified, err := verifier.VerifyWebhook(ctx, request)
	if err == nil {
		return verified, nil
	}
	code, params := webhookErrorDetails(err)
	values := make([]string, 0, len(params)*2)
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values = append(values, key, params[key])
	}
	if strings.Contains(code, "signature") || strings.Contains(code, "timestamp") || strings.Contains(code, "token_invalid") || strings.Contains(code, "client_state_invalid") {
		return integrationcontract.VerifiedInboundWebhook{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: code, Params: params, Err: err}
	}
	return integrationcontract.VerifiedInboundWebhook{}, badRequest(code, values...)
}

type codedWebhookError interface {
	ErrorCode() string
	ErrorParams() map[string]string
}

func webhookErrorDetails(err error) (string, map[string]string) {
	var coded codedWebhookError
	if errors.As(err, &coded) {
		return valueOrDefault(strings.TrimSpace(coded.ErrorCode()), "backend.integration.webhook.invalid"), coded.ErrorParams()
	}
	code := strings.TrimSpace(err.Error())
	if index := strings.IndexByte(code, ':'); index > 0 {
		code = strings.TrimSpace(code[:index])
	}
	return valueOrDefault(code, "backend.integration.webhook.invalid"), nil
}
