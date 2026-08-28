package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	sourcenotification "github.com/domainry/domainry-notification"
	sourcedelivery "github.com/domainry/domainry-notification/delivery"
	sourceinbox "github.com/domainry/domainry-notification/inbox"
	sourcetemplate "github.com/domainry/domainry-notification/template"
	notificationapplication "github.com/domainry/domainry-runtime/runtime/application/notification"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	notificationservice "github.com/domainry/domainry-runtime/runtime/domain/notification/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type notificationHTTPClock struct{}

func (notificationHTTPClock) Now() time.Time { return time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) }

type notificationHTTPDirectory struct{}

func (notificationHTTPDirectory) FindRecipient(_ context.Context, _ sourcenotification.WorkspaceID, id sourcenotification.UserID) (sourcenotification.Recipient, bool, error) {
	return sourcenotification.Recipient{ID: id, Email: id.String()}, true, nil
}

type notificationHTTPModuleStore struct{ repo *notificationHTTPRepository }

func notificationHTTPConvert[To any](from any) To {
	raw, _ := json.Marshal(from)
	var to To
	_ = json.Unmarshal(raw, &to)
	return to
}
func (s notificationHTTPModuleStore) SyncPublished(context.Context, []sourcetemplate.Template) error {
	return s.repo.err
}
func (s notificationHTTPModuleStore) List(context.Context) ([]sourcetemplate.Record, error) {
	return []sourcetemplate.Record{notificationHTTPConvert[sourcetemplate.Record](s.repo.record)}, s.repo.err
}
func (s notificationHTTPModuleStore) Get(_ context.Context, key string) (sourcetemplate.Record, bool, error) {
	return notificationHTTPConvert[sourcetemplate.Record](s.repo.record), s.repo.record.Key == key, s.repo.err
}
func (s notificationHTTPModuleStore) ListVersions(context.Context, string) ([]sourcetemplate.Version, error) {
	return []sourcetemplate.Version{notificationHTTPConvert[sourcetemplate.Version](s.repo.version)}, s.repo.err
}
func (s notificationHTTPModuleStore) GetVersion(_ context.Context, key string, version int) (sourcetemplate.Version, bool, error) {
	return notificationHTTPConvert[sourcetemplate.Version](s.repo.version), s.repo.version.TemplateKey == key && s.repo.version.Version == version, s.repo.err
}
func (s notificationHTTPModuleStore) SaveDraft(_ context.Context, value sourcetemplate.Template, _, actor string) (sourcetemplate.Record, error) {
	record := sourcetemplate.Record{Key: value.Key, Draft: &value, Status: "draft", UpdatedBy: actor}
	s.repo.record = notificationHTTPConvert[notificationmodel.NotificationTemplateRecord](record)
	return record, s.repo.err
}
func (s notificationHTTPModuleStore) Publish(_ context.Context, value sourcetemplate.Template, _, actor string) (sourcetemplate.Record, error) {
	record := sourcetemplate.Record{Key: value.Key, Published: &value, PublishedVersion: value.Version, Status: "active", UpdatedBy: actor}
	s.repo.record = notificationHTTPConvert[notificationmodel.NotificationTemplateRecord](record)
	return record, s.repo.err
}
func (s notificationHTTPModuleStore) Disable(_ context.Context, key, _, actor string) (sourcetemplate.Record, error) {
	record := notificationHTTPConvert[sourcetemplate.Record](s.repo.record)
	record.Key, record.Status, record.UpdatedBy = key, "disabled", actor
	s.repo.record = notificationHTTPConvert[notificationmodel.NotificationTemplateRecord](record)
	return record, s.repo.err
}
func (s notificationHTTPModuleStore) ListPublicationRequests(context.Context, string) ([]sourcetemplate.PublicationRequest, error) {
	return notificationHTTPConvert[[]sourcetemplate.PublicationRequest](s.repo.publications), s.repo.err
}
func (s notificationHTTPModuleStore) GetPublicationRequest(_ context.Context, id string) (sourcetemplate.PublicationRequest, bool, error) {
	for _, v := range s.repo.publications {
		if v.ID == id {
			return notificationHTTPConvert[sourcetemplate.PublicationRequest](v), true, s.repo.err
		}
	}
	return sourcetemplate.PublicationRequest{}, false, s.repo.err
}
func (s notificationHTTPModuleStore) CreatePublicationRequest(_ context.Context, v sourcetemplate.PublicationRequest) error {
	s.repo.lastCreated = notificationHTTPConvert[notificationmodel.NotificationPublicationRequest](v)
	return s.repo.err
}
func (s notificationHTTPModuleStore) TransitionPublicationRequest(_ context.Context, id string, expected sourcetemplate.PublicationStatus, t sourcetemplate.PublicationTransition) (sourcetemplate.PublicationRequest, error) {
	v, found, err := s.GetPublicationRequest(context.Background(), id)
	if !found {
		return v, sourcetemplate.ErrPublicationNotFound
	}
	v.Status = t.Status
	v.ReviewedBy = t.ReviewedBy
	v.Failure = t.Failure
	return v, err
}
func (s notificationHTTPModuleStore) ListDuePublicationRequests(context.Context, string, string, int) ([]sourcetemplate.PublicationRequest, error) {
	return nil, s.repo.err
}
func (s notificationHTTPModuleStore) ClaimPublicationRequest(context.Context, string, string, string, string) (sourcetemplate.PublicationRequest, bool, error) {
	return sourcetemplate.PublicationRequest{}, false, s.repo.err
}
func (s notificationHTTPModuleStore) HasOpenPublicationRequest(context.Context, string) (bool, error) {
	return false, s.repo.err
}
func (s notificationHTTPModuleStore) GetPolicy(context.Context) (sourcedelivery.Policy, error) {
	return notificationHTTPConvert[sourcedelivery.Policy](s.repo.policy), s.repo.err
}
func (s notificationHTTPModuleStore) SavePolicy(_ context.Context, v sourcedelivery.Policy) (sourcedelivery.Policy, error) {
	s.repo.policy = notificationHTTPConvert[notificationmodel.NotificationDeliveryPolicy](v)
	return v, s.repo.err
}
func (s notificationHTTPModuleStore) ListRecipientPreferences(_ context.Context, workspaceID sourcenotification.WorkspaceID) ([]sourcedelivery.RecipientPreference, error) {
	s.repo.lastWorkspaceID = workspaceID.String()
	return notificationHTTPConvert[[]sourcedelivery.RecipientPreference](s.repo.preferences), s.repo.err
}
func (s notificationHTTPModuleStore) GetRecipientPreference(context.Context, sourcenotification.WorkspaceID, sourcenotification.UserID) (sourcedelivery.RecipientPreference, bool, error) {
	return notificationHTTPConvert[sourcedelivery.RecipientPreference](s.repo.policyPreference()), s.repo.preferences != nil, s.repo.err
}
func (s notificationHTTPModuleStore) SaveRecipientPreference(_ context.Context, workspaceID sourcenotification.WorkspaceID, v sourcedelivery.RecipientPreference) (sourcedelivery.RecipientPreference, error) {
	s.repo.lastWorkspaceID = workspaceID.String()
	s.repo.preferences = append(s.repo.preferences, notificationHTTPConvert[notificationmodel.NotificationRecipientPreference](v))
	return v, s.repo.err
}
func (s notificationHTTPModuleStore) ReserveBatch(context.Context, sourcenotification.WorkspaceID, []sourcedelivery.Reservation, int, int) error {
	return s.repo.err
}
func (s notificationHTTPModuleStore) ListItems(_ context.Context, query sourceinbox.Query) ([]sourceinbox.Item, bool, error) {
	values, more, err := s.repo.ListInboxItems(context.Background(), notificationHTTPConvert[notificationmodel.NotificationInboxQuery](query))
	return notificationHTTPConvert[[]sourceinbox.Item](values), more, err
}
func (s notificationHTTPModuleStore) GetItem(_ context.Context, query sourceinbox.Query, id string) (sourceinbox.Item, bool, error) {
	value, found, err := s.repo.GetInboxItem(context.Background(), notificationHTTPConvert[notificationmodel.NotificationInboxQuery](query), id)
	return notificationHTTPConvert[sourceinbox.Item](value), found, err
}
func (s notificationHTTPModuleStore) CountFacets(_ context.Context, query sourceinbox.Query) (sourceinbox.Facets, error) {
	value, err := s.repo.CountInboxFacets(context.Background(), notificationHTTPConvert[notificationmodel.NotificationInboxQuery](query))
	return notificationHTTPConvert[sourceinbox.Facets](value), err
}
func (s notificationHTTPModuleStore) SetRead(_ context.Context, query sourceinbox.Query, id, readAt, updatedAt string) (sourceinbox.Item, bool, error) {
	value, found, err := s.repo.SetInboxItemRead(context.Background(), notificationHTTPConvert[notificationmodel.NotificationInboxQuery](query), id, readAt, updatedAt)
	return notificationHTTPConvert[sourceinbox.Item](value), found, err
}
func (s notificationHTTPModuleStore) SetArchived(_ context.Context, query sourceinbox.Query, id, archivedAt, updatedAt string) (sourceinbox.Item, bool, error) {
	value, found, err := s.repo.SetInboxItemArchived(context.Background(), notificationHTTPConvert[notificationmodel.NotificationInboxQuery](query), id, archivedAt, updatedAt)
	return notificationHTTPConvert[sourceinbox.Item](value), found, err
}
func (s notificationHTTPModuleStore) AcknowledgeAlert(_ context.Context, query sourceinbox.Query, id string, user sourcenotification.UserID, at string) (sourceinbox.Item, bool, error) {
	value, found, err := s.repo.AcknowledgeInboxAlert(context.Background(), notificationHTTPConvert[notificationmodel.NotificationInboxQuery](query), id, user.String(), at)
	return notificationHTTPConvert[sourceinbox.Item](value), found, err
}
func (s notificationHTTPModuleStore) MarkAllRead(_ context.Context, query sourceinbox.Query, at string) (int, error) {
	return s.repo.MarkAllInboxItemsRead(context.Background(), notificationHTTPConvert[notificationmodel.NotificationInboxQuery](query), at)
}
func (s notificationHTTPModuleStore) ListSavedViews(_ context.Context, w sourcenotification.WorkspaceID, u sourcenotification.UserID, surface sourcenotification.Surface) ([]sourceinbox.SavedView, error) {
	values, err := s.repo.ListInboxSavedViews(context.Background(), w.String(), u.String(), string(surface))
	return notificationHTTPConvert[[]sourceinbox.SavedView](values), err
}
func (s notificationHTTPModuleStore) SaveSavedView(_ context.Context, w sourcenotification.WorkspaceID, u sourcenotification.UserID, surface sourcenotification.Surface, value sourceinbox.SavedView) (sourceinbox.SavedView, error) {
	stored, err := s.repo.SaveInboxSavedView(context.Background(), w.String(), u.String(), string(surface), notificationHTTPConvert[notificationmodel.NotificationInboxSavedView](value))
	return notificationHTTPConvert[sourceinbox.SavedView](stored), err
}
func (s notificationHTTPModuleStore) DeleteSavedView(_ context.Context, w sourcenotification.WorkspaceID, u sourcenotification.UserID, surface sourcenotification.Surface, key string) (bool, error) {
	return s.repo.DeleteInboxSavedView(context.Background(), w.String(), u.String(), string(surface), key)
}
func (s notificationHTTPModuleStore) ListDelegations(_ context.Context, w sourcenotification.WorkspaceID, u sourcenotification.UserID, surface sourcenotification.Surface) ([]sourceinbox.Delegation, error) {
	values, err := s.repo.ListInboxDelegations(context.Background(), w.String(), u.String(), string(surface))
	return notificationHTTPConvert[[]sourceinbox.Delegation](values), err
}
func (s notificationHTTPModuleStore) SaveDelegation(_ context.Context, value sourceinbox.Delegation) (sourceinbox.Delegation, error) {
	stored, err := s.repo.SaveInboxDelegation(context.Background(), notificationHTTPConvert[notificationmodel.NotificationInboxDelegation](value))
	return notificationHTTPConvert[sourceinbox.Delegation](stored), err
}
func (s notificationHTTPModuleStore) DeleteDelegation(_ context.Context, w sourcenotification.WorkspaceID, u sourcenotification.UserID, id string) (bool, error) {
	return s.repo.DeleteInboxDelegation(context.Background(), w.String(), u.String(), id)
}
func (s notificationHTTPModuleStore) ListActiveDelegatedOwnerIDs(_ context.Context, w sourcenotification.WorkspaceID, u sourcenotification.UserID, surface sourcenotification.Surface, now string) ([]sourcenotification.UserID, error) {
	values, err := s.repo.ListActiveDelegatedOwnerIDs(context.Background(), w.String(), u.String(), string(surface), now)
	result := make([]sourcenotification.UserID, len(values))
	for i, v := range values {
		result[i] = sourcenotification.UserID(v)
	}
	return result, err
}
func (s notificationHTTPModuleStore) GovernanceMetrics(_ context.Context, w sourcenotification.WorkspaceID, since string) (sourceinbox.GovernanceMetrics, error) {
	value, err := s.repo.InboxGovernanceMetrics(context.Background(), w.String(), since)
	return notificationHTTPConvert[sourceinbox.GovernanceMetrics](value), err
}

type notificationHTTPRepository struct {
	record          notificationmodel.NotificationTemplateRecord
	version         notificationmodel.NotificationTemplateVersion
	publications    []notificationmodel.NotificationPublicationRequest
	lastCreated     notificationmodel.NotificationPublicationRequest
	policy          notificationmodel.NotificationDeliveryPolicy
	preferences     []notificationmodel.NotificationRecipientPreference
	metrics         notificationmodel.NotificationDeliveryMetrics
	inboxMetrics    notificationmodel.NotificationInboxGovernanceMetrics
	inboxItem       notificationmodel.NotificationInboxItem
	delegations     []notificationmodel.NotificationInboxDelegation
	delegatedOwners []string
	lastScope       principalmodel.SystemScope
	lastWorkspaceID string
	lastSince       string
	err             error
	inboxListErr    error
	inboxFacetsErr  error
	inboxItems      []notificationmodel.NotificationInboxItem
	emptyInbox      bool
	inboxListErrors []error
	inboxListCalls  int
}

func (r *notificationHTTPRepository) GetRecipientPreference(context.Context, string, string) (notificationmodel.NotificationRecipientPreference, bool, error) {
	return r.policyPreference(), r.preferences != nil, r.err
}
func (r *notificationHTTPRepository) policyPreference() notificationmodel.NotificationRecipientPreference {
	if len(r.preferences) == 0 {
		return notificationmodel.NotificationRecipientPreference{}
	}
	return r.preferences[0]
}
func (r *notificationHTTPRepository) ListInboxItems(context.Context, notificationmodel.NotificationInboxQuery) ([]notificationmodel.NotificationInboxItem, bool, error) {
	call := r.inboxListCalls
	r.inboxListCalls++
	if call < len(r.inboxListErrors) && r.inboxListErrors[call] != nil {
		return nil, false, r.inboxListErrors[call]
	}
	if r.inboxListErr != nil {
		return nil, false, r.inboxListErr
	}
	if r.emptyInbox {
		return nil, false, r.err
	}
	if call < len(r.inboxItems) {
		return []notificationmodel.NotificationInboxItem{r.inboxItems[call]}, false, r.err
	}
	return []notificationmodel.NotificationInboxItem{r.inboxItem}, false, r.err
}
func (r *notificationHTTPRepository) GetInboxItem(context.Context, notificationmodel.NotificationInboxQuery, string) (notificationmodel.NotificationInboxItem, bool, error) {
	return r.inboxItem, r.inboxItem.ID != "", r.err
}
func (r *notificationHTTPRepository) CountInboxFacets(context.Context, notificationmodel.NotificationInboxQuery) (notificationmodel.NotificationInboxFacets, error) {
	if r.inboxFacetsErr != nil {
		return notificationmodel.NotificationInboxFacets{}, r.inboxFacetsErr
	}
	return notificationmodel.NotificationInboxFacets{Unread: 3}, r.err
}
func (r *notificationHTTPRepository) SetInboxItemRead(context.Context, notificationmodel.NotificationInboxQuery, string, string, string) (notificationmodel.NotificationInboxItem, bool, error) {
	return r.inboxItem, true, r.err
}
func (r *notificationHTTPRepository) SetInboxItemArchived(context.Context, notificationmodel.NotificationInboxQuery, string, string, string) (notificationmodel.NotificationInboxItem, bool, error) {
	return r.inboxItem, true, r.err
}
func (r *notificationHTTPRepository) AcknowledgeInboxAlert(context.Context, notificationmodel.NotificationInboxQuery, string, string, string) (notificationmodel.NotificationInboxItem, bool, error) {
	return r.inboxItem, true, r.err
}
func (r *notificationHTTPRepository) MarkAllInboxItemsRead(context.Context, notificationmodel.NotificationInboxQuery, string) (int, error) {
	return 2, r.err
}
func (r *notificationHTTPRepository) ListInboxSavedViews(context.Context, string, string, string) ([]notificationmodel.NotificationInboxSavedView, error) {
	return []notificationmodel.NotificationInboxSavedView{{Key: "saved", Name: "Saved"}}, r.err
}
func (r *notificationHTTPRepository) SaveInboxSavedView(_ context.Context, _, _, _ string, value notificationmodel.NotificationInboxSavedView) (notificationmodel.NotificationInboxSavedView, error) {
	return value, r.err
}
func (r *notificationHTTPRepository) DeleteInboxSavedView(context.Context, string, string, string, string) (bool, error) {
	return true, r.err
}
func (r *notificationHTTPRepository) ListInboxDelegations(context.Context, string, string, string) ([]notificationmodel.NotificationInboxDelegation, error) {
	return append([]notificationmodel.NotificationInboxDelegation(nil), r.delegations...), r.err
}
func (r *notificationHTTPRepository) SaveInboxDelegation(_ context.Context, value notificationmodel.NotificationInboxDelegation) (notificationmodel.NotificationInboxDelegation, error) {
	return value, r.err
}
func (r *notificationHTTPRepository) DeleteInboxDelegation(context.Context, string, string, string) (bool, error) {
	return true, r.err
}
func (r *notificationHTTPRepository) ListActiveDelegatedOwnerIDs(context.Context, string, string, string, string) ([]string, error) {
	return append([]string(nil), r.delegatedOwners...), r.err
}

func (r *notificationHTTPRepository) InboxGovernanceMetrics(_ context.Context, workspaceID, since string) (notificationmodel.NotificationInboxGovernanceMetrics, error) {
	r.lastWorkspaceID, r.lastSince = workspaceID, since
	return r.inboxMetrics, r.err
}

func (r *notificationHTTPRepository) List(context.Context, principalmodel.SystemScope) ([]notificationmodel.NotificationTemplateRecord, error) {
	return []notificationmodel.NotificationTemplateRecord{r.record}, r.err
}
func (r *notificationHTTPRepository) Get(_ context.Context, _ principalmodel.SystemScope, key string) (notificationmodel.NotificationTemplateRecord, bool, error) {
	return r.record, r.record.Key == key, r.err
}
func (r *notificationHTTPRepository) ListVersions(context.Context, principalmodel.SystemScope, string) ([]notificationmodel.NotificationTemplateVersion, error) {
	return []notificationmodel.NotificationTemplateVersion{r.version}, r.err
}
func (r *notificationHTTPRepository) GetVersion(_ context.Context, _ principalmodel.SystemScope, key string, version int) (notificationmodel.NotificationTemplateVersion, bool, error) {
	return r.version, r.version.TemplateKey == key && r.version.Version == version, r.err
}
func (r *notificationHTTPRepository) ListPublicationRequests(context.Context, principalmodel.SystemScope, string) ([]notificationmodel.NotificationPublicationRequest, error) {
	return append([]notificationmodel.NotificationPublicationRequest(nil), r.publications...), r.err
}
func (r *notificationHTTPRepository) GetPublicationRequest(_ context.Context, _ principalmodel.SystemScope, id string) (notificationmodel.NotificationPublicationRequest, bool, error) {
	for _, publication := range r.publications {
		if publication.ID == id {
			return publication, true, r.err
		}
	}
	return notificationmodel.NotificationPublicationRequest{}, false, r.err
}
func (r *notificationHTTPRepository) CreatePublicationRequest(_ context.Context, _ principalmodel.SystemScope, value notificationmodel.NotificationPublicationRequest) error {
	r.lastCreated = value
	return r.err
}
func (r *notificationHTTPRepository) TransitionPublicationRequest(_ context.Context, _ principalmodel.SystemScope, id, _ string, transition notificationmodel.NotificationPublicationTransition) (notificationmodel.NotificationPublicationRequest, error) {
	value, _, err := r.GetPublicationRequest(context.Background(), principalmodel.SystemScope{}, id)
	value.Status, value.ReviewedBy, value.Failure = transition.Status, transition.ReviewedBy, transition.Failure
	return value, err
}
func (r *notificationHTTPRepository) HasOpenPublicationRequest(context.Context, principalmodel.SystemScope, string) (bool, error) {
	return false, r.err
}
func (r *notificationHTTPRepository) SaveDraft(_ context.Context, _ principalmodel.SystemScope, template notificationmodel.NotificationTemplate, _, actor string) (notificationmodel.NotificationTemplateRecord, error) {
	value := notificationmodel.NotificationTemplateRecord{Key: template.Key, Draft: &template, Status: "draft", UpdatedBy: actor}
	return value, r.err
}
func (r *notificationHTTPRepository) Disable(_ context.Context, _ principalmodel.SystemScope, key, _, actor string) (notificationmodel.NotificationTemplateRecord, error) {
	return notificationmodel.NotificationTemplateRecord{Key: key, Status: "disabled", UpdatedBy: actor}, r.err
}
func (r *notificationHTTPRepository) DeliveryMetrics(_ context.Context, workspaceID, since string) (notificationmodel.NotificationDeliveryMetrics, error) {
	r.lastWorkspaceID, r.lastSince = workspaceID, since
	return r.metrics, r.err
}
func (r *notificationHTTPRepository) GetDeliveryPolicy(_ context.Context, scope principalmodel.SystemScope) (notificationmodel.NotificationDeliveryPolicy, error) {
	r.lastScope = scope
	return r.policy, r.err
}
func (r *notificationHTTPRepository) SaveDeliveryPolicy(_ context.Context, scope principalmodel.SystemScope, value notificationmodel.NotificationDeliveryPolicy) (notificationmodel.NotificationDeliveryPolicy, error) {
	r.lastScope, r.policy = scope, value
	return value, r.err
}
func (r *notificationHTTPRepository) ListRecipientPreferences(_ context.Context, workspaceID string) ([]notificationmodel.NotificationRecipientPreference, error) {
	r.lastWorkspaceID = workspaceID
	return append([]notificationmodel.NotificationRecipientPreference(nil), r.preferences...), r.err
}
func (r *notificationHTTPRepository) SaveRecipientPreference(_ context.Context, workspaceID string, value notificationmodel.NotificationRecipientPreference) (notificationmodel.NotificationRecipientPreference, error) {
	r.lastWorkspaceID = workspaceID
	r.preferences = append(r.preferences, value)
	return value, r.err
}

type notificationHTTPResponse struct {
	status int
	code   string
	value  any
	err    error
}

func notificationHTTPTemplate() notificationmodel.NotificationTemplate {
	return notificationmodel.NotificationTemplate{
		Key: "order-ready", Name: "Order ready", Channel: "email", Status: "draft", Version: 1, DefaultLocale: "en-US",
		Locales: map[string]notificationmodel.NotificationTemplateContent{"en-US": {Subject: "Order ready", Text: "Your order is ready"}},
	}
}

func newNotificationHTTPHandler(repo *notificationHTTPRepository) (*NotificationsHandler, *notificationHTTPResponse, *principalmodel.Principal) {
	template := notificationHTTPTemplate()
	moduleStore := notificationHTTPModuleStore{repo: repo}
	catalog, err := notificationservice.NewNotificationEventCatalog([]notificationmodel.NotificationEventType{{
		Key: "test.event", Source: "test", Category: "system", DefaultSeverity: "info", Surfaces: []string{"business_workspace"}, MandatoryInApp: true,
		TemplateKey: "builtin.test.event", DefaultLocale: "en-US", Locales: map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": {Title: "Test", Body: "Test body", ActionLabels: map[string]string{"test.open": "Open"}}}, Version: 1, Status: "published",
		Actions: []notificationmodel.NotificationInboxActionDescriptor{{Key: "test.open", Kind: "route", ResourceType: "test", SurfaceRoutes: map[string]string{"business_workspace": "business.test"}}},
	}})
	if err != nil {
		panic(err)
	}
	inboxConfiguration, err := sourceinbox.NewConfiguration([]sourcenotification.Surface{"business_workspace", "consumer_portal"}, nil)
	if err != nil {
		panic(err)
	}
	inboxValidator, err := sourceinbox.NewValidator(inboxConfiguration)
	if err != nil {
		panic(err)
	}
	moduleCatalog, err := sourceinbox.NewCatalog(inboxValidator, []sourceinbox.EventType{{Key: "test.event", Source: "test", Category: "system", DefaultSeverity: "info", Surfaces: []sourcenotification.Surface{"business_workspace"}, MandatoryInApp: true, TemplateKey: "builtin.test.event", DefaultLocale: "en-US", Locales: map[string]sourceinbox.Content{"en-US": {Title: "Test", Body: "Test body", ActionLabels: map[string]string{"test.open": "Open"}}}, Version: 1, Status: "published", Actions: []sourceinbox.ActionDescriptor{{Key: "test.open", Kind: "route", ResourceType: "test", SurfaceRoutes: map[string]string{"business_workspace": "business.test"}}}}}, nil)
	if err != nil {
		panic(err)
	}
	mailboxManager, err := sourceinbox.NewMailboxManager(sourceinbox.MailboxManagerDependencies{Validator: inboxValidator, Mailboxes: moduleStore, SavedViews: moduleStore, Delegations: moduleStore, Metrics: moduleStore, Clock: notificationHTTPClock{}})
	if err != nil {
		panic(err)
	}
	actionResolver, err := sourceinbox.NewActionResolver(mailboxManager, moduleCatalog, notificationHTTPClock{})
	if err != nil {
		panic(err)
	}
	_, templateManager, publicationProcessor, err := notificationapplication.NewTemplateModule("en-US", nil, notificationapplication.TemplateModuleDependencies{Store: moduleStore, Clock: notificationHTTPClock{}, WorkerID: "http-test", Directory: notificationHTTPDirectory{}})
	if err != nil {
		panic(err)
	}
	policyManager, err := sourcedelivery.NewPolicyManager(sourcedelivery.PolicyManagerDependencies{Store: moduleStore, Clock: notificationHTTPClock{}})
	if err != nil {
		panic(err)
	}
	application := notificationapplication.NewNotificationApplicationServiceWithModule(nil, catalog, notificationapplication.NotificationModuleDependencies{Mailbox: mailboxManager, Actions: actionResolver, Templates: templateManager, Publications: publicationProcessor, Policy: policyManager, DeliveryMetrics: repo, Capabilities: notificationcontract.NotificationProviderCapabilities()})
	response := &notificationHTTPResponse{}
	principal := accessfixture.AttachPointer(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "reviewer", WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Permissions: []string{
		notificationcontract.PermissionTemplateRead, notificationcontract.PermissionTemplateManage,
		notificationcontract.PermissionTemplatePublish, notificationcontract.PermissionTemplateApprove, notificationcontract.PermissionTemplateTest,
		notificationcontract.PermissionPolicyRead, notificationcontract.PermissionPolicyManage,
	}},
	)
	if repo.record.Key == "" {
		repo.record = notificationmodel.NotificationTemplateRecord{Key: template.Key, Draft: &template, Status: "draft", UpdatedAt: "revision-1"}
	}
	if repo.version.TemplateKey == "" {
		repo.version = notificationmodel.NotificationTemplateVersion{TemplateKey: template.Key, Version: 1, Template: template}
	}
	return NewNotificationsHandler(NotificationsDependencies{
		Management: application, Delivery: application, Inbox: application,
		Principal: func(*http.Request) principalmodel.Principal { return *principal },
		WriteJSON: func(_ http.ResponseWriter, status int, value any) { response.status, response.value = status, value },
		WriteError: func(_ http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			response.status, response.code = status, code
		},
		WriteServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) {
			response.status, response.err = http.StatusInternalServerError, err
		},
		DecodeJSON: func(_ http.ResponseWriter, request *http.Request, value any) bool {
			if err := json.NewDecoder(request.Body).Decode(value); err != nil {
				response.status, response.err = http.StatusBadRequest, err
				return false
			}
			return true
		},
		Authenticated: func(next http.HandlerFunc) http.HandlerFunc { return next },
	}), response, principal
}

func notificationHTTPPublications() []notificationmodel.NotificationPublicationRequest {
	return []notificationmodel.NotificationPublicationRequest{
		{ID: "approve-1", TemplateKey: "order-ready", Status: string(sourcetemplate.PublicationPending), RequestedBy: "author", ScheduledFor: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)},
		{ID: "reject-1", TemplateKey: "order-ready", Status: string(sourcetemplate.PublicationPending), RequestedBy: "author"},
		{ID: "cancel-1", TemplateKey: "order-ready", Status: string(sourcetemplate.PublicationPending), RequestedBy: "author"},
	}
}
