package runtime

import (
	"strings"
	"testing"

	notificationcontract "github.com/domainry/domainry-notification-sdk/contract"
	hostsurfacemodel "github.com/domainry/domainry-runtime/runtime/domain/hostsurface/model"
)

func TestValidateNotificationAudienceResolverReferencesRejectsUnimplementedAndInvalidKeys(t *testing.T) {
	keys := hostsurfacemodel.NotificationAudienceResolverKeys()
	tests := []struct {
		name   string
		events []notificationcontract.NotificationEventType
		rules  []notificationcontract.NotificationRule
		keys   []string
		want   string
	}{
		{name: "supported event", events: []notificationcontract.NotificationEventType{{AudienceResolvers: []string{hostsurfacemodel.NotificationAudienceResolverWorkflowTaskAssignee}}}, keys: keys},
		{name: "supported rule", rules: []notificationcontract.NotificationRule{{AudienceResolvers: []string{hostsurfacemodel.NotificationAudienceResolverWorkflowTaskAssignee}}}, keys: keys},
		{name: "unknown event", events: []notificationcontract.NotificationEventType{{AudienceResolvers: []string{"project_owner"}}}, keys: keys, want: "notification_event_types[0]"},
		{name: "unknown rule", rules: []notificationcontract.NotificationRule{{AudienceResolvers: []string{"project_owner"}}}, keys: keys, want: "notification_rules[0]"},
		{name: "blank reference", events: []notificationcontract.NotificationEventType{{AudienceResolvers: []string{" "}}}, keys: keys, want: "blank"},
		{name: "duplicate reference", rules: []notificationcontract.NotificationRule{{AudienceResolvers: []string{hostsurfacemodel.NotificationAudienceResolverWorkflowTaskAssignee, hostsurfacemodel.NotificationAudienceResolverWorkflowTaskAssignee}}}, keys: keys, want: "duplicated"},
		{name: "invalid inventory", keys: []string{"resolver", " resolver "}, want: "inventory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateNotificationAudienceResolverReferences(test.events, test.rules, test.keys)
			if test.want == "" && err != nil {
				t.Fatal(err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("error=%v want substring %q", err, test.want)
			}
		})
	}
}
