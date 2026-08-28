package integrationcontract

import (
	"context"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type Adapter interface {
	Call(context.Context, CallRequest) (CallResult, error)
}

type ConfigValidator interface {
	ValidateConfig(integrationmodel.IntegrationConnection) error
}

type ConnectionTester interface {
	TestConnection(context.Context, CallRequest) (CallResult, error)
}

type SchemaProvider interface {
	ProviderSchema() integrationmodel.ConnectorProviderSchema
}

type OperationIdentity struct {
	Key            string
	Mode           string
	ContractSHA256 string
	Effect         string
}

// OperationIdentityProvider exposes the frozen public Provider operation
// identity to Runtime without leaking the public SDK descriptor into the
// internal application contract.
type OperationIdentityProvider interface {
	OperationIdentity(string) (OperationIdentity, bool)
}

type CallRequest struct {
	ConnectorKey string
	Connection   integrationmodel.IntegrationConnection
	Operation    string
	Method       string
	Request      map[string]any
	RequestRef   string
	Headers      map[string]string
	Secrets      map[string]string
	Delivery     bool
	Timeout      time.Duration
	Principal    principalmodel.Principal
}

type CallResult struct {
	Response       map[string]any
	ResponseRef    string
	SecretUpdates  map[string]string
	ResourceHealth *integrationmodel.IntegrationProviderResourceHealth
}

type ReconciliationOutcome string

const (
	ReconciliationSucceeded ReconciliationOutcome = "succeeded"
	ReconciliationFailed    ReconciliationOutcome = "failed"
	ReconciliationPending   ReconciliationOutcome = "pending"
	ReconciliationNotFound  ReconciliationOutcome = "not_found"
	ReconciliationUnknown   ReconciliationOutcome = "unknown"
)

type ReconcileRequest struct {
	ConnectorKey   string
	Connection     integrationmodel.IntegrationConnection
	Operation      string
	ContractSHA256 string
	Request        map[string]any
	RequestRef     string
	ResponseRef    string
	Secrets        map[string]string
	Timeout        time.Duration
	Principal      principalmodel.Principal
}

type ReconcileResult struct {
	Outcome       ReconciliationOutcome
	Response      map[string]any
	ResponseRef   string
	SecretUpdates map[string]string
	FailureCode   string
	RetryAfter    time.Duration
}

type Reconciler interface {
	Reconcile(context.Context, ReconcileRequest) (ReconcileResult, error)
}
