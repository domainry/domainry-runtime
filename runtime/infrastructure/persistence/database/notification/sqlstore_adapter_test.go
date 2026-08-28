package notification

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	sourcenotification "github.com/domainry/domainry-notification"
	sourceinbox "github.com/domainry/domainry-notification/inbox"
	notificationsql "github.com/domainry/domainry-notification/sqlstore"
	notificationtemplate "github.com/domainry/domainry-notification/template"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	_ "modernc.org/sqlite"
)

type moduleStoreTestClock struct{ now time.Time }

func (c moduleStoreTestClock) Now() time.Time { return c.now }

func TestSQLStoreAdapterUsesPlaneSchemaAndDatabase(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "notification-module.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	clock := moduleStoreTestClock{now: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)}
	store, err := NewSQLStoreAdapter(runtimeStore, clock)
	if err != nil {
		t.Fatal(err)
	}
	template := notificationtemplate.Template{
		Key: "test.module", Name: "Module template", Channel: "in_app", Status: "draft", Version: 1, DefaultLocale: "en-US",
		Locales: map[string]notificationtemplate.Content{"en-US": {Title: "Title", Text: "Body"}},
	}
	record, err := store.SaveDraft(t.Context(), template, "", "actor-1")
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := store.Get(t.Context(), template.Key)
	if err != nil || !found || stored.Draft == nil || stored.Draft.Key != template.Key {
		t.Fatalf("saved=%+v stored=%+v found=%v err=%v", record, stored, found, err)
	}
	if stored.CreatedAt != "2026-08-24T00:00:00.000000000Z" {
		t.Fatalf("created_at=%q", stored.CreatedAt)
	}

	event := sourceinbox.Event{
		ID: "event-1", WorkspaceID: "workspace-1", Source: "workflow", SourceEventID: "task-1:opened", EventType: "workflow.task.opened",
		Category: "approval", Severity: "info", Surface: "business_workspace", RecipientUserIDs: []sourcenotification.UserID{"user-1"},
		ActionState: sourceinbox.ActionNone, OccurredAt: "2026-08-24T00:00:00.000000000Z", Snapshot: sourceinbox.Snapshot{Title: "Task", Body: "Task opened"},
		Status: sourceinbox.EventQueued, CreatedAt: "2026-08-24T00:00:00.000000000Z", UpdatedAt: "2026-08-24T00:00:00.000000000Z",
	}
	if _, created, err := store.Enqueue(t.Context(), event); err != nil || !created {
		t.Fatalf("enqueue created=%v err=%v", created, err)
	}
	due, err := store.ListDue(t.Context(), "2026-08-24T00:00:00.000000000Z", 25)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%+v err=%v", due, err)
	}
	claimed, found, err := store.Claim(t.Context(), "workspace-1", "event-1", "worker-1", "2026-08-24T00:00:00.000000000Z", "2026-08-24T00:01:00.000000000Z")
	if err != nil || !found {
		t.Fatalf("claimed=%+v found=%v err=%v", claimed, found, err)
	}
	item := sourceinbox.Item{
		ID: "item-1", WorkspaceID: "workspace-1", RecipientUserID: "user-1", Surface: "business_workspace", EventID: "event-1",
		EventType: event.EventType, Source: event.Source, Category: event.Category, Severity: event.Severity, Title: "Task", Body: "Task opened",
		ActionState: sourceinbox.ActionNone, OccurrenceCount: 1, FirstOccurredAt: event.OccurredAt, LastOccurredAt: event.OccurredAt,
		CreatedAt: event.CreatedAt, UpdatedAt: event.UpdatedAt,
	}
	if err := store.Materialize(t.Context(), claimed, []sourceinbox.Item{item}); err != nil {
		t.Fatal(err)
	}
	storedItem, found, err := store.GetItem(t.Context(), sourceinbox.Query{WorkspaceID: "workspace-1", ViewerUserID: "user-1", RecipientUserIDs: []sourcenotification.UserID{"user-1"}, Surface: "business_workspace", Scope: sourceinbox.ScopeMine, Mailbox: sourceinbox.MailboxInbox, Limit: 1}, "item-1")
	if err != nil || !found || storedItem.EventID != "event-1" {
		t.Fatalf("item=%+v found=%v err=%v", storedItem, found, err)
	}
}

func TestModuleSchemaMatchesPlaneNotificationTables(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "plane-schema.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	moduleDB, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "module-schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = moduleDB.Close() })
	migrations, err := notificationsql.SchemaMigrations(notificationsql.SQLite, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		for _, statement := range migration.Statements {
			if _, err := moduleDB.ExecContext(t.Context(), statement); err != nil {
				t.Fatalf("apply module migration %d: %v", migration.Version, err)
			}
		}
	}

	for _, table := range notificationsql.OwnedTables() {
		plane := sqliteTableContract(t, runtimeStore.DB(), table)
		module := sqliteTableContract(t, moduleDB, table)
		if strings.Join(plane, "\n") != strings.Join(module, "\n") {
			t.Errorf("notification schema mismatch for %s\nPlane:\n%s\nModule:\n%s", table, strings.Join(plane, "\n"), strings.Join(module, "\n"))
		}
	}
}

func sqliteTableContract(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `PRAGMA table_info("`+table+`")`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	contract := []string{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		// SQLite reports legacy `TEXT PRIMARY KEY` as nullable unless NOT NULL
		// was explicit. The module writes every identity and its fresh schema is
		// intentionally stricter, so compare primary-key semantics separately
		// from this SQLite-only metadata detail.
		if primaryKey != 0 {
			notNull = 1
		}
		contract = append(contract, fmt.Sprintf("column:%03d:%s:%s:notnull=%d:pk=%d", cid, name, strings.ToUpper(kind), notNull, primaryKey))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	indexRows, err := db.QueryContext(t.Context(), `PRAGMA index_list("`+table+`")`)
	if err != nil {
		t.Fatal(err)
	}
	type indexContract struct {
		name   string
		unique int
	}
	indexes := []indexContract{}
	for indexRows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := indexRows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		if origin != "pk" {
			indexes = append(indexes, indexContract{name: name, unique: unique})
		}
	}
	if err := indexRows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		columnRows, err := db.QueryContext(t.Context(), `PRAGMA index_info("`+index.name+`")`)
		if err != nil {
			t.Fatal(err)
		}
		columns := []string{}
		for columnRows.Next() {
			var sequence, cid int
			var name string
			if err := columnRows.Scan(&sequence, &cid, &name); err != nil {
				t.Fatal(err)
			}
			columns = append(columns, name)
		}
		if err := columnRows.Close(); err != nil {
			t.Fatal(err)
		}
		contract = append(contract, fmt.Sprintf("index:unique=%d:%s", index.unique, strings.Join(columns, ",")))
	}
	sort.Strings(contract)
	return contract
}
