package integration

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestAccountingJournalOutboxDerivesBatchEntryDedupKey(t *testing.T) {
	repository := &deliveryCommandRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{
		DeliveryRepository: repository,
		ConnectorExists:    func(key string) bool { return key == "accounting" },
	})
	principal := integrationManagementPrincipal(PermissionInvoke)
	journal, err := service.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{
		ConnectorKey: "accounting", ConnectionKey: "accounting-primary", Operation: "create_journal_entry", RequestRef: "request-journal",
		Payload: map[string]any{"batch_key": "batch-1", "entry_key": "entry-1"},
	}, principal)
	if err != nil || journal.DedupKey != "batch:7:batch-1:entry:7:entry-1" {
		t.Fatalf("journal outbox=%#v err=%v", journal, err)
	}
}
