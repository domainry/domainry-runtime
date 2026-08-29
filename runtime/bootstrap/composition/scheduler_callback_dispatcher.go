package composition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/modulehost"
)

// SchedulerCallbackDispatcher is the Runtime-owned execution boundary shared
// by in-process Module dispatch and authenticated Scheduler SaaS callbacks. It
// never owns or mutates Scheduler run lifecycle state.
type SchedulerCallbackDispatcher struct {
	scheduler    *schedulerapplication.SchedulerApplicationService
	integrations *integrationapplication.IntegrationApplicationService
}

func NewSchedulerCallbackDispatcher(scheduler *schedulerapplication.SchedulerApplicationService, integrations *integrationapplication.IntegrationApplicationService) *SchedulerCallbackDispatcher {
	return &SchedulerCallbackDispatcher{scheduler: scheduler, integrations: integrations}
}

func (d *SchedulerCallbackDispatcher) Dispatch(ctx context.Context, trigger schedulersdk.Trigger) (schedulersdk.DownstreamReceipt, error) {
	if trigger.Target.Type == "http" {
		return d.dispatchHTTPCallback(ctx, trigger)
	}
	definition, err := d.publishedDefinition(ctx, trigger.DefinitionKey)
	if err != nil {
		return schedulersdk.DownstreamReceipt{}, err
	}
	receiptID, err := d.scheduler.DispatchOwnedTrigger(ctx, definition, trigger.RunID, trigger.ScheduledFor, 25, schedulerSDKSystemPrincipal("scheduler.command"))
	if err != nil {
		return schedulersdk.DownstreamReceipt{}, err
	}
	return schedulersdk.DownstreamReceipt{ID: receiptID, Owner: trigger.Target.Owner, Status: "accepted"}, nil
}

func (d *SchedulerCallbackDispatcher) publishedDefinition(ctx context.Context, key string) (recordmodel.Record, error) {
	if d == nil || d.scheduler == nil {
		return recordmodel.Record{}, fmt.Errorf("Runtime Scheduler definition source is unavailable")
	}
	records, err := d.scheduler.PublishedDefinitions(ctx, schedulerSDKSystemPrincipal("scheduler.definition.read"))
	if err != nil {
		return recordmodel.Record{}, err
	}
	for _, record := range records {
		if record.ID == strings.TrimSpace(key) {
			return record, nil
		}
	}
	return recordmodel.Record{}, fmt.Errorf("Scheduler definition %q is not published", key)
}

func (d *SchedulerCallbackDispatcher) dispatchHTTPCallback(ctx context.Context, trigger schedulersdk.Trigger) (schedulersdk.DownstreamReceipt, error) {
	if d == nil || d.integrations == nil {
		return schedulersdk.DownstreamReceipt{}, fmt.Errorf("Runtime Integration application is unavailable")
	}
	principal := schedulerSDKWorkspacePrincipal("integration.invoke")
	connection, err := d.integrations.GetIntegrationConnection(ctx, trigger.Target.ConnectionKey, principal)
	if err != nil {
		return schedulersdk.DownstreamReceipt{}, err
	}
	payload := map[string]any{}
	if len(trigger.Target.Payload) > 0 {
		if err := json.Unmarshal(trigger.Target.Payload, &payload); err != nil {
			return schedulersdk.DownstreamReceipt{}, fmt.Errorf("decode Scheduler HTTP payload: %w", err)
		}
	}
	result, err := d.integrations.ExecuteIntegrationSyncCall(ctx, integrationapplication.SyncCallRequest{ConnectorKey: connection.ConnectorKey, ConnectionKey: connection.Key, Operation: trigger.Target.Operation, Request: payload, RequestRef: trigger.IdempotencyKey}, principal)
	if err != nil {
		return schedulersdk.DownstreamReceipt{}, err
	}
	receiptID := strings.TrimSpace(result.ActionInvocation.ID)
	if receiptID == "" {
		receiptID = trigger.RunID
	}
	return schedulersdk.DownstreamReceipt{ID: receiptID, Owner: connection.ConnectorKey, Status: "accepted"}, nil
}

var _ modulehost.Dispatcher = (*SchedulerCallbackDispatcher)(nil)
