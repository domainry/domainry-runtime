package notification

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	sourcenotification "github.com/domainry/domainry-notification"
	sourcedelivery "github.com/domainry/domainry-notification/delivery"
	sourceinbox "github.com/domainry/domainry-notification/inbox"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type moduleTestClock struct{ now time.Time }

func (c moduleTestClock) Now() time.Time { return c.now }

type moduleTestStore struct {
	item         sourceinbox.Item
	query        sourceinbox.Query
	event        sourceinbox.Event
	materialized bool
	policy       sourcedelivery.Policy
	preference   sourcedelivery.RecipientPreference
	reserved     int
}

func (s *moduleTestStore) ListItems(_ context.Context, query sourceinbox.Query) ([]sourceinbox.Item, bool, error) {
	s.query = query
	return []sourceinbox.Item{s.item}, false, nil
}
func (s *moduleTestStore) GetItem(_ context.Context, query sourceinbox.Query, _ string) (sourceinbox.Item, bool, error) {
	s.query = query
	return s.item, true, nil
}
func (*moduleTestStore) CountFacets(context.Context, sourceinbox.Query) (sourceinbox.Facets, error) {
	return sourceinbox.Facets{}, nil
}
func (s *moduleTestStore) SetRead(_ context.Context, query sourceinbox.Query, _ string, readAt, updatedAt string) (sourceinbox.Item, bool, error) {
	s.query, s.item.ReadAt, s.item.UpdatedAt = query, readAt, updatedAt
	return s.item, true, nil
}
func (s *moduleTestStore) SetArchived(context.Context, sourceinbox.Query, string, string, string) (sourceinbox.Item, bool, error) {
	return s.item, true, nil
}
func (s *moduleTestStore) AcknowledgeAlert(context.Context, sourceinbox.Query, string, sourcenotification.UserID, string) (sourceinbox.Item, bool, error) {
	return s.item, true, nil
}
func (*moduleTestStore) MarkAllRead(context.Context, sourceinbox.Query, string) (int, error) {
	return 1, nil
}
func (s *moduleTestStore) Enqueue(_ context.Context, event sourceinbox.Event) (sourceinbox.Event, bool, error) {
	s.event = event
	return event, true, nil
}
func (s *moduleTestStore) ListDue(context.Context, string, int) ([]sourceinbox.Event, error) {
	return []sourceinbox.Event{s.event}, nil
}
func (s *moduleTestStore) Claim(_ context.Context, _ sourcenotification.WorkspaceID, _, owner, _, leaseExpiresAt string) (sourceinbox.Event, bool, error) {
	value := s.event
	value.Status, value.LeaseOwner, value.LeaseExpiresAt = sourceinbox.EventProcessing, owner, leaseExpiresAt
	value.FencingToken++
	return value, value.ID != "", nil
}
func (s *moduleTestStore) Materialize(_ context.Context, event sourceinbox.Event, items []sourceinbox.Item) error {
	s.event, s.materialized = event, len(items) == 1
	return nil
}
func (*moduleTestStore) Retry(context.Context, sourceinbox.Event, string, string, string, string) error {
	return nil
}
func (*moduleTestStore) Fail(context.Context, sourceinbox.Event, string, string, string) error {
	return nil
}
func (s *moduleTestStore) GetPolicy(context.Context) (sourcedelivery.Policy, error) {
	return s.policy, nil
}
func (s *moduleTestStore) SavePolicy(_ context.Context, value sourcedelivery.Policy) (sourcedelivery.Policy, error) {
	s.policy = value
	return value, nil
}
func (s *moduleTestStore) ListRecipientPreferences(context.Context, sourcenotification.WorkspaceID) ([]sourcedelivery.RecipientPreference, error) {
	return []sourcedelivery.RecipientPreference{s.preference}, nil
}
func (s *moduleTestStore) GetRecipientPreference(context.Context, sourcenotification.WorkspaceID, sourcenotification.UserID) (sourcedelivery.RecipientPreference, bool, error) {
	return s.preference, s.preference.RecipientKey != "", nil
}
func (s *moduleTestStore) SaveRecipientPreference(_ context.Context, _ sourcenotification.WorkspaceID, value sourcedelivery.RecipientPreference) (sourcedelivery.RecipientPreference, error) {
	s.preference = value
	return value, nil
}
func (s *moduleTestStore) ReserveBatch(_ context.Context, _ sourcenotification.WorkspaceID, values []sourcedelivery.Reservation, _, _ int) error {
	s.reserved += len(values)
	return nil
}

func TestNotificationModuleDoesNotRequireLegacyInboxService(t *testing.T) {
	configuration, err := sourceinbox.NewConfiguration([]sourcenotification.Surface{sourcenotification.Surface(surfacemodel.ProductSurfaceBusinessWorkspace)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := sourceinbox.NewValidator(configuration)
	if err != nil {
		t.Fatal(err)
	}
	store := &moduleTestStore{item: sourceinbox.Item{
		ID: "item-1", WorkspaceID: "workspace-1", RecipientUserID: "user-1", Surface: "business_workspace", Title: "Title", ActionState: sourceinbox.ActionOpen,
		Actions: []sourceinbox.ActionRef{{Key: "workflow.task.open", Kind: "route", Label: "Open", ResourceType: "workflow_task", ResourceID: "task-1"}},
	}, policy: sourcedelivery.Policy{Enabled: true, QuietStart: "22:00", QuietEnd: "07:00", Timezone: "UTC", MaxPerRecipientPerHour: 10}}
	manager, err := sourceinbox.NewMailboxManager(sourceinbox.MailboxManagerDependencies{
		Validator: validator, Mailboxes: store, Clock: moduleTestClock{now: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatal(err)
	}
	eventType := sourceinbox.EventType{
		Key: "workflow.task.opened", Source: "workflow", Category: "approval", DefaultSeverity: "info", Surfaces: []sourcenotification.Surface{"business_workspace"},
		MandatoryInApp: true, TemplateKey: "workflow.task.opened", DefaultLocale: "en-US", Version: 1, Status: "published",
		Locales: map[string]sourceinbox.Content{"en-US": {Title: "Task", Body: "Open task", ActionLabels: map[string]string{"workflow.task.open": "Open"}}},
		Actions: []sourceinbox.ActionDescriptor{{Key: "workflow.task.open", Kind: "route", ResourceType: "workflow_task", SurfaceRoutes: map[string]string{"business_workspace": "workflow.task.detail"}}},
	}
	catalog, err := sourceinbox.NewCatalog(validator, []sourceinbox.EventType{eventType}, nil)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := sourceinbox.NewActionResolver(manager, catalog, moduleTestClock{now: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	clock := moduleTestClock{now: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)}
	compiler, err := sourceinbox.NewCompiler(catalog, validator, clock)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := sourceinbox.NewPublisher(compiler, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := sourceinbox.NewProcessor(sourceinbox.ProcessorDependencies{Events: store, Clock: clock, WorkerID: "worker-1"})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := sourcedelivery.NewPolicyManager(sourcedelivery.PolicyManagerDependencies{Store: store, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	application := NewNotificationApplicationServiceWithModule(nil, nil, NotificationModuleDependencies{
		Mailbox: manager, Actions: actions, Compiler: compiler, Publisher: publisher, Processor: processor, Policy: policy,
	})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1"}}
	page, err := application.ListInbox(t.Context(), notificationmodel.NotificationInboxQuery{}, "", surfacemodel.ProductSurfaceBusinessWorkspace, principal)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "item-1" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if store.query.WorkspaceID != "workspace-1" || store.query.RecipientUserID != "user-1" {
		t.Fatalf("source query=%+v", store.query)
	}
	item, err := application.SetInboxRead(t.Context(), "item-1", true, surfacemodel.ProductSurfaceBusinessWorkspace, principal)
	if err != nil || item.ReadAt == "" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	resolved, err := application.ResolveInboxAction(t.Context(), "item-1", "workflow.task.open", notificationmodel.NotificationInboxQuery{}, surfacemodel.ProductSurfaceBusinessWorkspace, principal)
	if err != nil || resolved.RouteKey != "workflow.task.detail" || resolved.RouteParams["resource_id"] != "task-1" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "notification module inbox test")
	intent := notificationmodel.NotificationIntent{
		ID: "event-1", WorkspaceID: "workspace-1", SourceEventID: "task-1:opened", EventType: "workflow.task.opened",
		Surface: "business_workspace", RecipientUserIDs: []string{"user-1"}, SubjectType: "workflow_task", SubjectID: "task-1", OccurredAt: "2026-08-24T00:00:00Z",
	}
	compiled, err := application.CompileInboxIntent(intent, scope)
	if err != nil || compiled.EventType != "workflow.task.opened" || len(compiled.Snapshot.Actions) != 1 {
		t.Fatalf("compiled=%+v err=%v", compiled, err)
	}
	stored, created, err := application.PublishInboxIntent(t.Context(), intent, scope)
	if err != nil || !created || stored.ID != "event-1" {
		t.Fatalf("stored=%+v created=%v err=%v", stored, created, err)
	}
	processed, err := application.ProcessInboxEvent(t.Context(), workerplatform.DurableTaskLocator{QueueKind: "notification_inbox", WorkspaceID: "workspace-1", TaskID: "event-1"}, scope)
	if err != nil || !processed || !store.materialized {
		t.Fatalf("processed=%v materialized=%v err=%v", processed, store.materialized, err)
	}
	preference, err := application.SaveMyNotificationPreference(t.Context(), notificationmodel.NotificationRecipientPreference{EnabledChannels: map[string]bool{"email": true}}, surfacemodel.ProductSurfaceBusinessWorkspace, principal)
	if err != nil || preference.RecipientKey != "user-1" {
		t.Fatalf("preference=%+v err=%v", preference, err)
	}
	decision, err := application.EvaluateDelivery(t.Context(), notificationmodel.NotificationDeliveryEvaluationRequest{
		WorkspaceID: "workspace-1", TemplateKey: "workflow.failed", Channel: "email", Recipients: []string{"user-1"}, DedupeKey: "dedupe-1",
	})
	if err != nil || decision.DeliverAfter != "" || store.reserved != 1 {
		t.Fatalf("decision=%+v reserved=%d err=%v", decision, store.reserved, err)
	}
}
