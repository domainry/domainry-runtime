package notificationfacade

import (
	"fmt"
	"strings"

	notificationsdk "github.com/domainry/domainry-notification-sdk"
	runtimemodel "github.com/domainry/domainry-notification-sdk/contract"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// ModuleCompiler preserves the existing compile-before-commit workflow while
// moving compilation ownership behind the Notification Module Binding.
type ModuleCompiler struct {
	transactions modulehost.TransactionalPublisher
}

// SaaSCompiler preserves the exact producer intent until the business-owned
// transaction inserts it into Runtime's durable publication outbox. Rendering
// and Notification-domain event compilation remain owned by Notification SaaS.
type SaaSCompiler struct{}

func (SaaSCompiler) CompileInboxIntent(value runtimemodel.NotificationIntent, scope principalmodel.SystemScope) (runtimemodel.NotificationEvent, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return runtimemodel.NotificationEvent{}, err
	}
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.WorkspaceID) == "" || strings.TrimSpace(value.SourceEventID) == "" || strings.TrimSpace(value.EventType) == "" || strings.TrimSpace(value.Surface) == "" || strings.TrimSpace(value.OccurredAt) == "" {
		return runtimemodel.NotificationEvent{}, fmt.Errorf("Notification SaaS publication intent is missing a required identity field")
	}
	intent := value
	return runtimemodel.NotificationEvent{
		ID: value.ID, WorkspaceID: value.WorkspaceID, SourceEventID: value.SourceEventID,
		EventType: value.EventType, Severity: value.Severity, Surface: value.Surface,
		RecipientUserIDs:     append([]string(nil), value.RecipientUserIDs...),
		AudienceResolverKeys: append([]string(nil), value.AudienceResolverKeys...),
		SubjectType:          value.SubjectType, SubjectID: value.SubjectID, SubjectVersion: value.SubjectVersion,
		GroupKey: value.GroupKey, DedupeKey: value.DedupeKey, ActionState: value.ActionState,
		AlertState: value.AlertState, ExpiresAt: value.ExpiresAt, OccurredAt: value.OccurredAt,
		CorrelationID: value.CorrelationID, TraceID: value.TraceID, PublicationIntent: &intent,
	}, nil
}

func NewModuleCompiler(binding notificationsdk.Binding) (*ModuleCompiler, error) {
	transactional, ok := binding.(modulehost.TransactionalBinding)
	if !ok || transactional.ModuleTransactions() == nil {
		return nil, fmt.Errorf("Notification Module Binding returned no transaction capability")
	}
	return &ModuleCompiler{transactions: transactional.ModuleTransactions()}, nil
}

func (c *ModuleCompiler) Transactions() modulehost.TransactionalPublisher {
	if c == nil {
		return nil
	}
	return c.transactions
}

func (c *ModuleCompiler) CompileInboxIntent(value runtimemodel.NotificationIntent, scope principalmodel.SystemScope) (runtimemodel.NotificationEvent, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return runtimemodel.NotificationEvent{}, err
	}
	if c == nil || c.transactions == nil {
		return runtimemodel.NotificationEvent{}, fmt.Errorf("Notification Module transaction compiler is unavailable")
	}
	input, err := convert[sdkcontract.NotificationIntent](value)
	if err != nil {
		return runtimemodel.NotificationEvent{}, err
	}
	event, err := c.transactions.CompileIntent(input)
	if err != nil {
		return runtimemodel.NotificationEvent{}, mapError(err)
	}
	converted, err := convert[runtimemodel.NotificationEvent](event)
	if err != nil {
		return runtimemodel.NotificationEvent{}, err
	}
	intent := value
	converted.PublicationIntent = &intent
	return converted, nil
}
