package audit

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestAuditInsertEncodingAndQueryVariants(t *testing.T) {
	store := openAuditEdgeStore(t)
	repository := NewAuditStore(store)
	base := auditmodel.AuditEvent{ID: "event", WorkspaceID: "default", Event: "updated", ObjectKey: "customer", RecordID: "record", ActorID: "actor", RoleKey: "role", CreatedAt: "2026-07-20T00:00:00Z"}
	for _, stage := range []string{"before", "after", "metadata"} {
		event := base
		switch stage {
		case "before":
			event.Before = map[string]any{"bad": make(chan int)}
		case "after":
			event.After = map[string]any{"bad": make(chan int)}
		case "metadata":
			event.Metadata = map[string]any{"bad": make(chan int)}
		}
		if err := repository.InsertAuditEvent(t.Context(), "default", event); err == nil || !strings.Contains(err.Error(), "encode audit "+stage) {
			t.Fatalf("stage=%s error=%v", stage, err)
		}
	}
	if err := repository.InsertAuditEvent(t.Context(), "", base); err == nil {
		t.Fatal("empty workspace insert accepted")
	}

	for index := 0; index < 3; index++ {
		event := base
		event.ID = "event-" + string(rune('a'+index))
		event.Metadata = map[string]any{"request_id": `req%_\`}
		event.CreatedAt = time.Date(2026, 7, 20, 0, index, 0, 0, time.UTC).Format(time.RFC3339)
		if err := repository.InsertAuditEvent(t.Context(), "default", event); err != nil {
			t.Fatal(err)
		}
	}
	queries := []auditmodel.AuditEventQuery{
		{},
		{ObjectKey: " customer ", RecordID: " record ", Event: " updated ", ActorID: " actor ", RoleKey: " role ", CreatedFrom: "2026-07-19", CreatedTo: "2026-07-21", RequestID: `req%_\`, Limit: 2000},
	}
	for index, query := range queries {
		events, err := repository.ListAuditEvents(t.Context(), "default", query)
		if err != nil || index == 0 && len(events) == 0 {
			t.Fatalf("query=%#v events=%#v err=%v", query, events, err)
		}
	}
	if _, err := repository.ListAuditEvents(t.Context(), "", auditmodel.AuditEventQuery{}); err == nil {
		t.Fatal("empty workspace query accepted")
	}
	if _, err := repository.ListAuditEventsForSystem(t.Context(), principalmodel.SystemScope{}, auditmodel.AuditEventQuery{}); err == nil {
		t.Fatal("invalid system scope accepted")
	}
	systemEvents, err := repository.ListAuditEventsForSystem(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test"), auditmodel.AuditEventQuery{Limit: 1})
	if err != nil || len(systemEvents) != 1 {
		t.Fatalf("system events=%#v err=%v", systemEvents, err)
	}

	if _, err := store.DB().ExecContext(t.Context(), `UPDATE _audit_events SET metadata_json='{', before_json='{', after_json='{' WHERE id='event-a'`); err != nil {
		t.Fatal(err)
	}
	if events, err := repository.ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{RecordID: "record"}); err != nil || len(events) != 3 {
		t.Fatalf("invalid JSON events=%#v err=%v", events, err)
	}
}

func TestAuditOptionsAllFieldsFiltersAndLimits(t *testing.T) {
	store := openAuditEdgeStore(t)
	repository := NewAuditStore(store)
	event := auditmodel.AuditEvent{ID: "event", WorkspaceID: "default", Event: "updated", ObjectKey: "customer", RecordID: "record", ActorID: "actor", RoleKey: "role", CreatedAt: "2026-07-20T00:00:00Z"}
	if err := repository.InsertAuditEvent(t.Context(), "default", event); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"record_id", "actor_id", "role_key", "event"} {
		options, err := repository.ListAuditOptions(t.Context(), "default", auditmodel.AuditOptionQuery{Field: field, ObjectKey: " customer ", CreatedFrom: "2026-07-19", CreatedTo: "2026-07-21", Query: " %_\\ ", Limit: 100})
		if err != nil {
			t.Fatalf("field=%s error=%v", field, err)
		}
		_ = options
	}
	if _, err := repository.ListAuditOptions(t.Context(), "", auditmodel.AuditOptionQuery{Field: "event"}); err == nil {
		t.Fatal("empty workspace options accepted")
	}
	if _, err := repository.ListAuditOptions(t.Context(), "default", auditmodel.AuditOptionQuery{Field: "unsupported"}); err == nil {
		t.Fatal("unsupported field accepted")
	}
	if options, err := repository.ListAuditOptions(t.Context(), "default", auditmodel.AuditOptionQuery{Field: "event", Limit: 0}); err != nil || len(options) != 1 || options[0].Label != options[0].Value {
		t.Fatalf("default options=%#v err=%v", options, err)
	}
}

func TestAuditSubjectLifecycleContractAndCancellation(t *testing.T) {
	store := openAuditEdgeStore(t)
	repository := NewAuditStore(store)
	for _, event := range []auditmodel.AuditEvent{
		{ID: "one", WorkspaceID: "default", Event: "created", ActorID: "subject", CreatedAt: "2026-07-20T00:00:00Z"},
		{ID: "two", WorkspaceID: "default", Event: "updated", ActorID: "subject", CreatedAt: "2026-07-20T00:01:00Z"},
	} {
		if err := repository.InsertAuditEvent(t.Context(), "default", event); err != nil {
			t.Fatal(err)
		}
	}
	lifecycle := NewAuditSubjectLifecycleStore(store)
	if lifecycle.Owner(t.Context()) != "audit" {
		t.Fatal("unexpected lifecycle owner")
	}
	preview, err := lifecycle.PreviewSubject(t.Context(), "default", "subject")
	if err != nil || !json.Valid(preview) || !strings.Contains(string(preview), "2") {
		t.Fatalf("preview=%s err=%v", preview, err)
	}
	exported, err := lifecycle.ExportSubject(t.Context(), "default", "subject")
	if err != nil || !json.Valid(exported) || !strings.Contains(string(exported), "one") {
		t.Fatalf("export=%s err=%v", exported, err)
	}
	erased, err := lifecycle.EraseSubject(t.Context(), "default", "subject", nil)
	if err != nil || !json.Valid(erased) || !strings.Contains(string(erased), "2") {
		t.Fatalf("erase=%s err=%v", erased, err)
	}
	var remaining int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _audit_events WHERE actor_id='subject'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
	var retained, distinctAnonymous int
	var anonymous string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*), COUNT(DISTINCT actor_id), MIN(actor_id) FROM _audit_events WHERE id IN ('one','two')`).Scan(&retained, &distinctAnonymous, &anonymous); err != nil ||
		retained != 2 || distinctAnonymous != 1 || !strings.HasPrefix(anonymous, "erased-") {
		t.Fatalf("retained=%d distinct=%d anonymous=%q err=%v", retained, distinctAnonymous, anonymous, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := lifecycle.PreviewSubject(cancelled, "default", "subject"); !errors.Is(err, context.Canceled) {
		t.Fatalf("preview cancellation=%v", err)
	}
	if _, err := lifecycle.ExportSubject(cancelled, "default", "subject"); !errors.Is(err, context.Canceled) {
		t.Fatalf("export cancellation=%v", err)
	}
	if _, err := lifecycle.EraseSubject(cancelled, "default", "subject", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("erase cancellation=%v", err)
	}
	if auditLifecycleColumns(store) != "" || !strings.Contains(auditLifecycleColumns(store, "id", "event"), ", ") {
		t.Fatal("lifecycle columns malformed")
	}
}

func openAuditEdgeStore(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
