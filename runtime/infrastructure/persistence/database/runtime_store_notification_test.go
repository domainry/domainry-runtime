package database

import (
	"context"
	"testing"

	"github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
)

type notificationTransactionsStub struct{}

func (notificationTransactionsStub) CompileIntent(contract.NotificationIntent) (contract.NotificationEvent, error) {
	return contract.NotificationEvent{}, nil
}
func (notificationTransactionsStub) InsertEvent(context.Context, modulehost.Executor, contract.NotificationEvent) error {
	return nil
}

func TestRuntimeStoreBindsOneNotificationTransactionPublisher(t *testing.T) {
	store := &RuntimeStore{}
	publisher := notificationTransactionsStub{}
	if err := store.BindNotificationTransactions(publisher); err != nil {
		t.Fatal(err)
	}
	if store.NotificationTransactions() == nil {
		t.Fatal("Notification transaction publisher was not retained")
	}
	if err := store.BindNotificationTransactions(publisher); err == nil {
		t.Fatal("Notification transaction publisher was rebound")
	}
}

func TestRuntimeStoreBindsExactlyOneNotificationPublicationTopology(t *testing.T) {
	scope := NotificationSaaSPublicationScope{TenantID: "tenant-a", WorkspaceID: "workspace-a", ApplicationKey: "runtime-a"}
	store := &RuntimeStore{}
	if err := store.BindNotificationSaaSPublications(scope); err != nil {
		t.Fatal(err)
	}
	got, ok := store.NotificationSaaSPublications()
	if !ok || got != scope {
		t.Fatalf("scope=%+v ok=%v", got, ok)
	}
	if err := store.BindNotificationTransactions(notificationTransactionsStub{}); err == nil {
		t.Fatal("Module transaction publisher was accepted beside SaaS outbox")
	}
}
