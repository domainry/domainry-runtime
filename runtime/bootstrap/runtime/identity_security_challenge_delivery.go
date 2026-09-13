package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodulehost "github.com/domainry/domainry-identity-sdk/modulehost"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
)

type runtimeIdentitySecurityChallengeDelivery struct {
	operations integrationsdk.Operations
}

func bindIdentitySecurityChallengeDelivery(binding identitysdk.Binding, operations integrationsdk.Operations) error {
	binder, ok := binding.(identitysdk.SecurityChallengeDeliveryBinder)
	if !ok {
		return nil
	}
	if operations == nil {
		return fmt.Errorf("Integration Operations are required for Identity security challenge delivery")
	}
	return binder.BindSecurityChallengeDelivery(runtimeIdentitySecurityChallengeDelivery{operations: operations})
}

func (delivery runtimeIdentitySecurityChallengeDelivery) DeliverSecurityChallenge(ctx context.Context, request identitymodulehost.SecurityChallengeDeliveryRequest) (identitymodulehost.SecurityChallengeDeliveryReceipt, error) {
	if delivery.operations == nil {
		return identitymodulehost.SecurityChallengeDeliveryReceipt{}, fmt.Errorf("Integration Operations are unavailable")
	}
	connectorKey, operation, payload, err := identitySecurityChallengeOperation(request.Channel, request.Destination, request.Message)
	if err != nil {
		return identitymodulehost.SecurityChallengeDeliveryReceipt{}, err
	}
	result, err := delivery.operations.Call(ctx, integrationsdk.ProviderCallRequest{
		RequestID:       strings.TrimSpace(request.ChallengeID),
		WorkspaceID:     strings.TrimSpace(request.WorkspaceID),
		ConnectorKey:    connectorKey,
		ConnectionKey:   strings.TrimSpace(request.ConnectionKey),
		Operation:       operation,
		Payload:         payload,
		PersistenceMode: integrationsdk.ProviderCallPersistenceSensitive,
		// Identity owns the user-facing destination mask. Integration evidence
		// needs only this non-personal routing label, including anonymous OTP.
		MaskedDestination: connectorKey + "/" + operation,
		ActorID:           "identity-security-challenge",
		RoleKey:           "system",
	})
	if err != nil {
		return identitymodulehost.SecurityChallengeDeliveryReceipt{}, err
	}
	responseRef := strings.TrimSpace(result.Invocation.ResponseRef)
	if responseRef == "" {
		responseRef = strings.TrimSpace(result.Invocation.ID)
	}
	return identitymodulehost.SecurityChallengeDeliveryReceipt{Status: result.Invocation.Status, ResponseRef: responseRef}, nil
}

func identitySecurityChallengeOperation(channel, destination, message string) (string, string, json.RawMessage, error) {
	channel = strings.ToLower(strings.TrimSpace(channel))
	destination, message = strings.TrimSpace(destination), strings.TrimSpace(message)
	if destination == "" || message == "" {
		return "", "", nil, fmt.Errorf("Identity security challenge destination and message are required")
	}
	var connectorKey, operation string
	var value map[string]string
	switch channel {
	case "sms", "twilio", "telephony":
		connectorKey, operation = "telephony", "send_sms"
		value = map[string]string{"to": destination, "body": message}
	case "whatsapp", "meta", "meta_cloud_api":
		connectorKey, operation = "whatsapp", "send_message"
		value = map[string]string{"recipient": destination, "message": message}
	default:
		return "", "", nil, fmt.Errorf("Identity security challenge channel %q is unsupported", channel)
	}
	payload, err := json.Marshal(value)
	return connectorKey, operation, payload, err
}

var _ identitymodulehost.SecurityChallengeDelivery = runtimeIdentitySecurityChallengeDelivery{}
