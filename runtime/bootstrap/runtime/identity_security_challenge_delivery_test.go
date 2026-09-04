package runtime

import (
	"context"
	"encoding/json"
	"testing"

	identitymodulehost "github.com/domainry/domainry-identity-sdk/modulehost"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
)

type securityChallengeOperationsProbe struct {
	integrationsdk.Operations
	request integrationsdk.ProviderCallRequest
}

func (probe *securityChallengeOperationsProbe) Call(_ context.Context, request integrationsdk.ProviderCallRequest) (integrationsdk.ProviderCallResult, error) {
	probe.request = request
	return integrationsdk.ProviderCallResult{Invocation: integrationsdk.Invocation{ID: "invocation-1", Status: "succeeded"}}, nil
}

func TestIdentitySecurityChallengeDeliveryUsesSensitiveIntegrationOperation(t *testing.T) {
	probe := &securityChallengeOperationsProbe{}
	receipt, err := (runtimeIdentitySecurityChallengeDelivery{operations: probe}).DeliverSecurityChallenge(t.Context(), identitymodulehost.SecurityChallengeDeliveryRequest{
		ChallengeID: "challenge-1", WorkspaceID: "workspace-a", ConnectionKey: "twilio-primary", Channel: "sms",
		Destination: "+8613800000000", MaskedDestination: "+8*********0000", Message: "code 123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ResponseRef != "invocation-1" || probe.request.ConnectorKey != "telephony" || probe.request.Operation != "send_sms" || probe.request.PersistenceMode != integrationsdk.ProviderCallPersistenceSensitive || probe.request.MaskedDestination != "+8*********0000" {
		t.Fatalf("receipt=%#v request=%#v", receipt, probe.request)
	}
	var payload map[string]string
	if json.Unmarshal(probe.request.Payload, &payload) != nil || payload["to"] != "+8613800000000" || payload["body"] != "code 123456" {
		t.Fatalf("payload=%s", probe.request.Payload)
	}
}

func TestIdentitySecurityChallengeDeliveryMapsWhatsAppAndRejectsUnknownChannels(t *testing.T) {
	connectorKey, operation, payload, err := identitySecurityChallengeOperation("whatsapp", "+8613800000000", "code 123456")
	if err != nil || connectorKey != "whatsapp" || operation != "send_message" || !json.Valid(payload) {
		t.Fatalf("connector=%q operation=%q payload=%s err=%v", connectorKey, operation, payload, err)
	}
	if _, _, _, err := identitySecurityChallengeOperation("email", "person@example.com", "code"); err == nil {
		t.Fatal("unsupported channel was accepted")
	}
}
