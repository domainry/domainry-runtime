package integration

import (
	"strings"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func TestIntegrationSubjectLifecycleExportsQueuesProviderEraseAndAnonymizes(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	columns := "id, identity_key, workspace_id, provider, external_subject, external_subject_type, external_name, actor_id, role_key, status, created_by, created_at, updated_at"
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO integration_external_identities ("+columns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", "mapping-1", "map-1", "workspace-a", "slack", "U123", "user", "Alice", "user-1", "member", "active", "admin", now, now); err != nil {
		t.Fatal(err)
	}
	handler := NewIntegrationSubjectLifecycleStore(store)
	payload, err := handler.ExportSubject(t.Context(), "workspace-a", "user-1")
	if err != nil || !strings.Contains(string(payload), "U123") {
		t.Fatalf("payload=%s err=%v", payload, err)
	}
	external, err := handler.RequestExternalErasure(t.Context(), lifecyclemodel.SubjectRequest{ID: "erase-1", WorkspaceID: "workspace-a", ResolvedIdentity: "user-1"})
	if err != nil || len(external) != 1 || external[0].ConnectorKey != "slack" || external[0].ProviderRef != "U123" {
		t.Fatalf("external=%#v err=%v", external, err)
	}
	retried, err := handler.RequestExternalErasure(t.Context(), lifecyclemodel.SubjectRequest{ID: "erase-1", WorkspaceID: "workspace-a", ResolvedIdentity: "user-1"})
	if err != nil || len(retried) != 1 || retried[0].ID != external[0].ID {
		t.Fatalf("retried=%#v external=%#v err=%v", retried, external, err)
	}
	if _, err := handler.EraseSubject(t.Context(), "workspace-a", "user-1", nil); err != nil {
		t.Fatal(err)
	}
	var subject, name, status string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT external_subject, external_name, status FROM integration_external_identities WHERE workspace_id = ? AND id = ?", "workspace-a", "mapping-1").Scan(&subject, &name, &status); err != nil {
		t.Fatal(err)
	}
	if subject == "U123" || name != "" || status != "erased" {
		t.Fatalf("subject=%q name=%q status=%q", subject, name, status)
	}
}
