package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/deliverygateway"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
)

type gatewayTokenVerifier struct{}

func (gatewayTokenVerifier) Verify(context.Context, identitysdk.VerifyTokenRequest) (identitysdk.VerifiedToken, error) {
	return identitysdk.VerifiedToken{Audience: "runtime", SubjectID: "notification-service", TenantID: "tenant", WorkspaceID: "workspace", AuthorizationRevision: "revision"}, nil
}

type gatewayAuthorization struct{}

func (gatewayAuthorization) ResolveAccess(context.Context, identitysdk.AccessBundleRequest) (identitysdk.AccessBundle, error) {
	return identitysdk.AccessBundle{}, nil
}
func (gatewayAuthorization) Reauthorize(context.Context, identitysdk.DecisionRequest) (identitysdk.AccessDecision, error) {
	return identitysdk.AccessDecision{Allowed: true}, nil
}

type gatewayIdentityBinding struct{ identitysdk.Binding }

func (gatewayIdentityBinding) Tokens() identitysdk.TokenVerifier { return gatewayTokenVerifier{} }
func (gatewayIdentityBinding) Authorization() identitysdk.Authorization {
	return gatewayAuthorization{}
}

type gatewayDeliveryRepository struct {
	integrationrepository.IntegrationDeliveryRepository
	message integrationmodel.IntegrationOutboxMessage
}

func (r *gatewayDeliveryRepository) InsertOutbox(_ context.Context, _ string, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	value.ID = "message"
	r.message = value
	return value, nil
}

func TestNotificationDeliveryGatewayRequiresIdentityServiceScope(t *testing.T) {
	repository := &gatewayDeliveryRepository{}
	service := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{DeliveryRepository: repository})
	handler := NewIntegrationsHandler(IntegrationsDependencies{
		RuntimeExecution: service, Identity: gatewayIdentityBinding{}, IdentityAudience: "runtime", ProductName: "Product",
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusInternalServerError) },
	})
	requestBody := deliverygateway.Request{RequestID: "request", WorkspaceID: "workspace", PlanID: "plan", EventID: "event", Channel: "email", ConnectorKey: "smtp", Operation: "send", DedupeKey: "dedupe", CreatedAt: "now", Rendered: contract.RenderedNotification{Channel: "email", Recipients: []string{"user@example.com"}}}
	encoded, _ := json.Marshal(requestBody)

	missing := httptest.NewRequest(http.MethodPost, deliverygateway.AcceptPath, strings.NewReader(string(encoded)))
	missingResult := httptest.NewRecorder()
	handler.acceptNotificationDelivery(missingResult, missing)
	if missingResult.Code != http.StatusUnauthorized {
		t.Fatalf("missing credential status=%d", missingResult.Code)
	}

	accepted := httptest.NewRequest(http.MethodPost, deliverygateway.AcceptPath, strings.NewReader(string(encoded)))
	accepted.Header.Set("X-Domainry-Service-Credential", "credential")
	accepted.Header.Set("X-Domainry-Tenant-ID", "tenant")
	accepted.Header.Set("X-Domainry-Workspace-ID", "workspace")
	accepted.Header.Set("X-Domainry-Application-Key", "application")
	acceptedResult := httptest.NewRecorder()
	handler.acceptNotificationDelivery(acceptedResult, accepted)
	if acceptedResult.Code != http.StatusOK || repository.message.RequestRef != "request" {
		t.Fatalf("status=%d message=%+v body=%s", acceptedResult.Code, repository.message, acceptedResult.Body.String())
	}
}
