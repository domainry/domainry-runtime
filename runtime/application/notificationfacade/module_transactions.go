package notificationfacade

import (
	"fmt"

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
