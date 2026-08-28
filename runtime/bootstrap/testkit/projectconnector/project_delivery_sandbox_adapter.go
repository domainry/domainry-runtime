// Package sandbox is a project-owned Connector Provider example. It uses only
// the public connector contract and has no Runtime-internal authority.
package projectconnector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/domainry/domainry-connector-sdk"
)

const (
	ProjectDeliverySandboxConnectorKey = "project_delivery"
	ProjectDeliverySandboxProviderKey  = "sandbox"

	ProjectDeliverySandboxLookupOperationKey = "lookup"
	ProjectDeliverySandboxNotifyOperationKey = "notify"
	ProjectDeliverySandboxExportOperationKey = "export"

	ProjectDeliverySandboxLookupContractSHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
	ProjectDeliverySandboxNotifyContractSHA256 = "2222222222222222222222222222222222222222222222222222222222222222"
	ProjectDeliverySandboxExportContractSHA256 = "3333333333333333333333333333333333333333333333333333333333333333"
)

type ProjectDeliverySandboxLookupInput struct {
	RecordID string `json:"record_id"`
}

type ProjectDeliverySandboxLookupOutput struct {
	RecordID string `json:"record_id"`
	Name     string `json:"name"`
}

type ProjectDeliverySandboxNotifyInput struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
}

type ProjectDeliverySandboxExportInput struct {
	Scope string `json:"scope"`
}

// ProjectDeliverySandboxExecutionSnapshot contains non-sensitive execution
// evidence for acceptance tests and diagnostics. Secret values are never retained.
type ProjectDeliverySandboxExecutionSnapshot struct {
	SynchronousCalls       int
	NotificationAttempts   int
	NotificationsDelivered int
	LongOperationsStarted  int
	Reconciliations        int
	WebhooksVerified       int
	UsedRotatedAccessToken bool
	LastNotifyRequestRef   string
	LastExportRequestRef   string
}

type projectDeliverySandboxAdapterState struct {
	mu                       sync.Mutex
	snapshot                 ProjectDeliverySandboxExecutionSnapshot
	retryableFailuresPending int
}

// ProjectDeliverySandboxAdapter is project-owned. The embedded public Adapter
// owns strict typed dispatch while the outer type publishes optional capabilities.
type ProjectDeliverySandboxAdapter struct {
	connector.Adapter
	state *projectDeliverySandboxAdapterState
}

// NewProjectDeliverySandboxAdapter is the canonical project composition
// constructor discovered by the project Connector generator.
func NewProjectDeliverySandboxAdapter(_ connector.Transport) *ProjectDeliverySandboxAdapter {
	state := &projectDeliverySandboxAdapterState{retryableFailuresPending: 1}
	lookup := projectDeliverySandboxMustBindCall(connector.CallOperation[ProjectDeliverySandboxLookupInput, ProjectDeliverySandboxLookupOutput]{
		ConnectorKey: ProjectDeliverySandboxConnectorKey, ProviderKey: ProjectDeliverySandboxProviderKey,
		Key: ProjectDeliverySandboxLookupOperationKey, ContractSHA256: ProjectDeliverySandboxLookupContractSHA256,
		Reliability: projectDeliverySandboxReadReliability(),
	}, state.projectDeliverySandboxLookup)
	notify := projectDeliverySandboxMustBindDelivery(connector.BindEnqueueDelivery(connector.EnqueueOperation[ProjectDeliverySandboxNotifyInput]{
		ConnectorKey: ProjectDeliverySandboxConnectorKey, ProviderKey: ProjectDeliverySandboxProviderKey,
		Key: ProjectDeliverySandboxNotifyOperationKey, ContractSHA256: ProjectDeliverySandboxNotifyContractSHA256,
		Reliability: projectDeliverySandboxWriteReliability(connector.ReconciliationNone),
	}, state.projectDeliverySandboxNotify))
	export := projectDeliverySandboxMustBindDelivery(connector.BindStartOperationDelivery(connector.StartOperation[ProjectDeliverySandboxExportInput]{
		ConnectorKey: ProjectDeliverySandboxConnectorKey, ProviderKey: ProjectDeliverySandboxProviderKey,
		Key: ProjectDeliverySandboxExportOperationKey, ContractSHA256: ProjectDeliverySandboxExportContractSHA256,
		Reliability: projectDeliverySandboxWriteReliability(connector.ReconciliationProviderLookup),
	}, state.projectDeliverySandboxStartExport))
	provider, err := connector.NewProvider(connector.ProviderSchema{
		ConnectorKey: ProjectDeliverySandboxConnectorKey, ProviderKey: ProjectDeliverySandboxProviderKey, ProviderRevision: "project-v1",
		ConfigFields: []connector.ConfigField{{
			Key: "endpoint", Name: "Endpoint", Type: connector.ConfigFieldText, Required: true,
			Validation: connector.ConfigValidation{MinLength: 1},
		}},
		SecretFields: []connector.SecretField{
			{
				Key: "access_token", Name: "Access token", Required: true,
				CredentialKind: connector.SecretCredentialBearerToken, MaterialFormat: connector.SecretMaterialOpaque,
				RotationPolicy: connector.SecretRotationOAuthRefresh, ExpiryPolicy: connector.SecretExpiryOptional,
				TestRequirement: connector.SecretTestWhenBound,
			},
			{
				Key: "webhook_secret", Name: "Webhook secret", Required: true,
				CredentialKind: connector.SecretCredentialSigningSecret, MaterialFormat: connector.SecretMaterialOpaque,
				RotationPolicy: connector.SecretRotationManual, ExpiryPolicy: connector.SecretExpiryNone,
				TestRequirement: connector.SecretTestWhenBound,
			},
		},
	}, lookup, notify, export)
	if err != nil {
		panic(fmt.Sprintf("build source-owned Connector: %v", err))
	}
	return &ProjectDeliverySandboxAdapter{Adapter: provider, state: state}
}

func (a *ProjectDeliverySandboxAdapter) ExecutionSnapshot() ProjectDeliverySandboxExecutionSnapshot {
	if a == nil || a.state == nil {
		return ProjectDeliverySandboxExecutionSnapshot{}
	}
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	return a.state.snapshot
}

func (a *ProjectDeliverySandboxAdapter) VerifyWebhook(_ context.Context, request connector.VerifyWebhookRequest) (connector.VerifiedWebhook, error) {
	signatures := request.Headers["X-Project-Signature"]
	if request.Secrets["webhook_secret"] == "" || len(signatures) != 1 || signatures[0] != request.Secrets["webhook_secret"] {
		return connector.VerifiedWebhook{}, connector.PermanentError("project.webhook_signature_invalid", errors.New("signature mismatch"))
	}
	var payload struct {
		EventID  string `json:"event_id"`
		RecordID string `json:"record_id"`
	}
	if err := json.Unmarshal(request.Body, &payload); err != nil || strings.TrimSpace(payload.EventID) == "" {
		return connector.VerifiedWebhook{}, connector.PermanentError("project.webhook_payload_invalid", err)
	}
	a.state.mu.Lock()
	a.state.snapshot.WebhooksVerified++
	a.state.mu.Unlock()
	body, _ := json.Marshal(map[string]string{"record_id": payload.RecordID})
	return connector.VerifiedWebhook{EventType: "export.completed", ExternalID: payload.EventID, Payload: body}, nil
}

func (a *ProjectDeliverySandboxAdapter) Reconcile(_ context.Context, request connector.ReconcileRequest) (connector.ReconcileResult, error) {
	if request.OperationKey != ProjectDeliverySandboxExportOperationKey || request.Secrets["access_token"] == "" {
		return connector.ReconcileResult{}, connector.PermanentError("project.reconciliation_invalid", errors.New("missing export identity or credential"))
	}
	a.state.mu.Lock()
	a.state.snapshot.Reconciliations++
	a.state.mu.Unlock()
	return connector.ReconcileResult{
		Outcome: connector.ReconciliationSucceeded,
		Result:  &connector.CallResult{ResponseRef: "external-export:completed"},
	}, nil
}

func (s *projectDeliverySandboxAdapterState) projectDeliverySandboxLookup(_ context.Context, request connector.TypedRequest[ProjectDeliverySandboxLookupInput]) (connector.TypedResult[ProjectDeliverySandboxLookupOutput], error) {
	if request.Secrets["access_token"] != "old-access-token" {
		return connector.TypedResult[ProjectDeliverySandboxLookupOutput]{}, connector.PermanentError("project.access_token_invalid", errors.New("unexpected credential"))
	}
	s.mu.Lock()
	s.snapshot.SynchronousCalls++
	s.mu.Unlock()
	return connector.TypedResult[ProjectDeliverySandboxLookupOutput]{
		Output:        ProjectDeliverySandboxLookupOutput{RecordID: request.Input.RecordID, Name: "Project record"},
		ResponseRef:   "project-lookup:" + request.Input.RecordID,
		SecretUpdates: map[string]string{"access_token": "rotated-access-token"},
	}, nil
}

func (s *projectDeliverySandboxAdapterState) projectDeliverySandboxNotify(_ context.Context, request connector.TypedRequest[ProjectDeliverySandboxNotifyInput]) (connector.DeliveryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.NotificationAttempts++
	s.snapshot.LastNotifyRequestRef = request.RequestRef
	if request.Delivery && request.Secrets["access_token"] == "rotated-access-token" {
		s.snapshot.UsedRotatedAccessToken = true
	}
	if s.retryableFailuresPending > 0 {
		s.retryableFailuresPending--
		return connector.DeliveryResult{}, connector.RetryableError("project.delivery_temporarily_unavailable", errors.New("retry later"))
	}
	s.snapshot.NotificationsDelivered++
	return connector.DeliveryResult{ResponseRef: "project-notification:" + request.Input.Recipient}, nil
}

func (s *projectDeliverySandboxAdapterState) projectDeliverySandboxStartExport(_ context.Context, request connector.TypedRequest[ProjectDeliverySandboxExportInput]) (connector.DeliveryResult, error) {
	if !request.Delivery || request.Secrets["access_token"] != "rotated-access-token" {
		return connector.DeliveryResult{}, connector.PermanentError("project.export_delivery_invalid", errors.New("export must run after commit with the current credential"))
	}
	s.mu.Lock()
	s.snapshot.LongOperationsStarted++
	s.snapshot.LastExportRequestRef = request.RequestRef
	s.mu.Unlock()
	return connector.DeliveryResult{ResponseRef: "external-export:" + request.Input.Scope}, nil
}

func projectDeliverySandboxReadReliability() connector.ReliabilityContract {
	return connector.ReliabilityContract{
		Effect: connector.EffectRead, Idempotency: connector.IdempotencyContract{Strategy: connector.IdempotencyNatural},
		Reconciliation: connector.ReconciliationNone, Compensation: connector.CompensationContract{Mode: connector.CompensationNone},
	}
}

func projectDeliverySandboxWriteReliability(reconciliation connector.ReconciliationMode) connector.ReliabilityContract {
	return connector.ReliabilityContract{
		Effect:         connector.EffectWrite,
		Idempotency:    connector.IdempotencyContract{Strategy: connector.IdempotencyProviderKey, KeyRetentionSeconds: 86400},
		Reconciliation: reconciliation, Compensation: connector.CompensationContract{Mode: connector.CompensationNone},
	}
}

func projectDeliverySandboxMustBindCall(operation connector.CallOperation[ProjectDeliverySandboxLookupInput, ProjectDeliverySandboxLookupOutput], handler connector.CallHandler[ProjectDeliverySandboxLookupInput, ProjectDeliverySandboxLookupOutput]) connector.BoundOperation {
	bound, err := connector.BindCall(operation, handler)
	if err != nil {
		panic(fmt.Sprintf("bind project lookup operation: %v", err))
	}
	return bound
}

func projectDeliverySandboxMustBindDelivery(bound connector.BoundOperation, err error) connector.BoundOperation {
	if err != nil {
		panic(fmt.Sprintf("bind project delivery operation: %v", err))
	}
	return bound
}
