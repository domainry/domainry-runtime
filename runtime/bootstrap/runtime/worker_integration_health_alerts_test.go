package runtime

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationcontract "github.com/domainry/domainry-notification-sdk/contract"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
)

type integrationHealthAlertReaderStub struct {
	groups map[string]notificationcontract.NotificationAlertGroup
}

func (s *integrationHealthAlertReaderStub) GetAlertGroup(_ context.Context, _, recipient, groupKey string) (notificationcontract.NotificationAlertGroup, bool, error) {
	value, found := s.groups[recipient+"\x00"+groupKey]
	return value, found, nil
}

type integrationHealthAlertPublisherStub struct {
	intents []notificationcontract.NotificationIntent
}

func (s *integrationHealthAlertPublisherStub) PublishIntent(_ context.Context, intent notificationcontract.NotificationIntent) (notificationcontract.NotificationEvent, bool, error) {
	s.intents = append(s.intents, intent)
	return notificationcontract.NotificationEvent{ID: intent.ID}, true, nil
}

func TestPublishIntegrationHealthAlertTransitionsPublishesOnlyStateChanges(t *testing.T) {
	condition := deploymentapplication.IntegrationHealthCondition{
		Key: "dead_letter", MetricName: "dead-letter tasks", Unit: "tasks", Current: 2, Limit: 0, Firing: true, ObservedAt: "2026-09-29T02:10:00Z",
	}
	reader := &integrationHealthAlertReaderStub{groups: map[string]notificationcontract.NotificationAlertGroup{}}
	publisher := &integrationHealthAlertPublisherStub{}
	published, err := publishIntegrationHealthAlertTransitions(t.Context(), "workspace-1", []string{"admin-1"}, []deploymentapplication.IntegrationHealthCondition{condition}, reader, publisher)
	if err != nil || published != 1 || len(publisher.intents) != 1 {
		t.Fatalf("published=%d intents=%+v err=%v", published, publisher.intents, err)
	}
	firing := publisher.intents[0]
	if firing.EventType != "integration.health.degraded" || firing.AlertState != notificationcontract.NotificationAlertFiring || firing.GroupKey != integrationHealthAlertGroupPrefix+condition.Key || firing.SubjectID != condition.Key {
		t.Fatalf("firing intent=%+v", firing)
	}
	if err := firing.Validate(); err != nil {
		t.Fatalf("firing intent invalid: %v", err)
	}

	reader.groups["admin-1\x00"+firing.GroupKey] = notificationcontract.NotificationAlertGroup{State: notificationcontract.NotificationAlertAcknowledged, LastEventID: firing.ID}
	publisher.intents = nil
	if published, err = publishIntegrationHealthAlertTransitions(t.Context(), "workspace-1", []string{"admin-1"}, []deploymentapplication.IntegrationHealthCondition{condition}, reader, publisher); err != nil || published != 0 {
		t.Fatalf("acknowledged alert republished: published=%d err=%v", published, err)
	}

	condition.Firing, condition.Current = false, 0
	if published, err = publishIntegrationHealthAlertTransitions(t.Context(), "workspace-1", []string{"admin-1"}, []deploymentapplication.IntegrationHealthCondition{condition}, reader, publisher); err != nil || published != 1 {
		t.Fatalf("recovery published=%d err=%v", published, err)
	}
	recovered := publisher.intents[0]
	if recovered.EventType != "integration.health.recovered" || recovered.AlertState != notificationcontract.NotificationAlertResolved || recovered.ID == firing.ID {
		t.Fatalf("recovery intent=%+v", recovered)
	}
}

func TestIntegrationHealthTransitionIntentIsStableForRetry(t *testing.T) {
	condition := deploymentapplication.IntegrationHealthCondition{Key: "ready_due", MetricName: "ready due tasks", Unit: "tasks", Current: 11, Limit: 10, Firing: true, ObservedAt: "2026-09-29T02:10:00Z"}
	first := integrationHealthTransitionIntent("workspace-1", "admin-1", condition, integrationHealthAlertGroupPrefix+condition.Key, "none")
	second := integrationHealthTransitionIntent("workspace-1", "admin-1", condition, integrationHealthAlertGroupPrefix+condition.Key, "none")
	if first.ID != second.ID || first.SourceEventID != second.SourceEventID || first.DedupeKey != second.DedupeKey {
		t.Fatalf("retry identity changed: first=%+v second=%+v", first, second)
	}
}

type integrationHealthIdentityProjectionStub struct {
	users       []identitysdk.User
	roles       []identitysdk.Role
	assignments []identitysdk.UserRoleAssignment
}

func (s integrationHealthIdentityProjectionStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}
func (s integrationHealthIdentityProjectionStub) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{}, false, nil
}
func (s integrationHealthIdentityProjectionStub) ListUsers(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.User, error) {
	return s.users, nil
}
func (s integrationHealthIdentityProjectionStub) ListRoles(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.Role, error) {
	return s.roles, nil
}
func (s integrationHealthIdentityProjectionStub) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return s.assignments, nil
}

func TestInstallationAdministratorRecipientsUseOnlyActiveRoleAssignmentsAndUsers(t *testing.T) {
	expired := "2026-09-29T02:00:00Z"
	projection := integrationHealthIdentityProjectionStub{
		roles: []identitysdk.Role{{ID: "role-admin", Key: "tenant_admin", Status: "active"}, {ID: "role-workspace", Key: "workspace_admin", Status: "active"}},
		users: []identitysdk.User{{ID: "admin-b", Status: "active"}, {ID: "admin-a", Status: "active"}, {ID: "disabled", Status: "disabled"}, {ID: "ordinary", Status: "active"}, {ID: "expired", Status: "active"}},
		assignments: []identitysdk.UserRoleAssignment{
			{UserID: "admin-b", RoleID: "role-admin", Status: "active"}, {UserID: "admin-a", RoleID: "role-admin", Status: "active"},
			{UserID: "disabled", RoleID: "role-admin", Status: "active"}, {UserID: "ordinary", RoleID: "role-workspace", Status: "active"},
			{UserID: "expired", RoleID: "role-admin", Status: "active", ExpiresAt: &expired},
		},
	}
	recipients, err := installationAdministratorRecipients(t.Context(), projection, "workspace-1", time.Date(2026, 9, 29, 2, 10, 0, 0, time.UTC))
	if err != nil || len(recipients) != 2 || recipients[0] != "admin-a" || recipients[1] != "admin-b" {
		t.Fatalf("recipients=%v err=%v", recipients, err)
	}
}
