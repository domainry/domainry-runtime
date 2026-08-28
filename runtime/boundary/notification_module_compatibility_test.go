package boundary_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	notificationsql "github.com/domainry/domainry-notification/sqlstore"
)

// TestNotificationModuleSchemaOwnershipMatchesPlane proves that the source
// module owns the complete durable boundary and Runtime does not recreate any
// of those tables in its technical schema metadata.
func TestNotificationModuleSchemaOwnershipMatchesPlane(t *testing.T) {
	want := []notificationsql.TableOwnership{
		{Name: "notification_alert_groups", Scope: notificationsql.WorkspaceData},
		{Name: "notification_channel_plans", Scope: notificationsql.WorkspaceData},
		{Name: "notification_delivery_policy", Scope: notificationsql.SystemData},
		{Name: "notification_delivery_reservations", Scope: notificationsql.WorkspaceData},
		{Name: "notification_event_failures", Scope: notificationsql.WorkspaceData},
		{Name: "notification_events", Scope: notificationsql.WorkspaceData},
		{Name: "notification_inbox_delegations", Scope: notificationsql.WorkspaceData},
		{Name: "notification_inbox_items", Scope: notificationsql.WorkspaceData},
		{Name: "notification_inbox_saved_views", Scope: notificationsql.WorkspaceData},
		{Name: "notification_migration_controls", Scope: notificationsql.WorkspaceData},
		{Name: "notification_recipient_preferences", Scope: notificationsql.WorkspaceData},
		{Name: "notification_retention_archive", Scope: notificationsql.WorkspaceData},
		{Name: "notification_template_publication_locks", Scope: notificationsql.SystemData},
		{Name: "notification_template_publication_requests", Scope: notificationsql.SystemData},
		{Name: "notification_template_records", Scope: notificationsql.SystemData},
		{Name: "notification_template_versions", Scope: notificationsql.SystemData},
	}
	got := notificationsql.SchemaOwnership()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notification module schema ownership drifted\ngot:  %+v\nwant: %+v", got, want)
	}

	root := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	metadata, err := os.ReadFile(filepath.Join(root, "runtime", "infrastructure", "persistence", "database", "schema", "metadata.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range got {
		if containsTableIdentifier(metadata, table.Name) {
			t.Errorf("Runtime metadata schema must not contain module-owned table %q", table.Name)
		}
	}
}

func containsTableIdentifier(source []byte, table string) bool {
	needle := []byte(`TableIdentifier("` + table + `")`)
	for index := 0; index+len(needle) <= len(source); index++ {
		if string(source[index:index+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}
