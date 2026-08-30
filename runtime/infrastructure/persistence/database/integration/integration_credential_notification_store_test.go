package integration

import (
	"database/sql/driver"
	"errors"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestIntegrationCredentialExpiryCandidateQueryIsSystemScopedAndMaterialFree(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	config := NewIntegrationConfigStore(store)
	for _, secret := range []integrationmodel.IntegrationSecret{
		{Key: "due", WorkspaceID: "workspace-a", Kind: "api_key", Status: "active", CreatedBy: "owner", ExpiresAt: "2026-08-01T00:00:00Z", ValueRef: "material:due", Fingerprint: "must-not-be-used-by-notification"},
		{Key: "far", WorkspaceID: "workspace-b", Kind: "api_key", Status: "active", CreatedBy: "owner", ExpiresAt: "2027-08-01T00:00:00Z"},
		{Key: "disabled", WorkspaceID: "workspace-a", Kind: "api_key", Status: "disabled", CreatedBy: "owner", ExpiresAt: "2026-08-01T00:00:00Z"},
	} {
		if _, err := config.UpsertSecret(t.Context(), secret.WorkspaceID, secret); err != nil {
			t.Fatal(err)
		}
	}
	repository := NewIntegrationCredentialExpiryStore(store)
	if _, err := repository.ListIntegrationCredentialExpiryCandidates(t.Context(), principalmodel.SystemScope{}, "2026-08-27T00:00:00Z", 10); err == nil {
		t.Fatal("expected system scope rejection")
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test credential expiry query")
	values, err := repository.ListIntegrationCredentialExpiryCandidates(t.Context(), scope, "2026-08-27T00:00:00Z", 10)
	if err != nil || len(values) != 1 || values[0].Key != "due" {
		t.Fatalf("values=%+v err=%v", values, err)
	}
	if values[0].ValueRef == "" || values[0].Fingerprint == "" {
		t.Fatal("persistence contract unexpectedly mutated stored secret metadata")
	}
	// The source returns metadata to Integration ownership; the application
	// intent tests prove these fields never cross into Notification variables.
}

func TestIntegrationCredentialExpiryCandidateQueryFailureAndLimitEdges(t *testing.T) {
	wantErr := errors.New("credential query failed")
	columns := []string{"secret_key", "workspace_id", "kind", "status", "description", "value_ref", "fingerprint", "created_by", "created_at", "updated_at", "disabled_at", "expires_at", "rotated_at", "revoked_at", "last_tested_at", "last_test_status", "last_test_error"}
	for index, step := range []integrationSQLQueryStep{
		{err: wantErr},
		{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}},
		{columns: columns, nextErr: wantErr},
		{columns: columns},
		{columns: columns},
	} {
		base := openStoreForGeneratedListTest(t)
		db := openIntegrationScriptedDB(&integrationSQLState{querySteps: []integrationSQLQueryStep{step}})
		t.Cleanup(func() { _ = db.Close(); _ = base.Close() })
		repository := NewIntegrationCredentialExpiryStore(base)
		repository.db = db
		limit := 10
		if index == 3 {
			limit = 0
		}
		if index == 4 {
			limit = 501
		}
		values, err := repository.ListIntegrationCredentialExpiryCandidates(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test query edge"), "2026-08-27T00:00:00Z", limit)
		if index < 3 && err == nil {
			t.Fatalf("step %d returned values=%+v without error", index, values)
		}
		if index >= 3 && (err != nil || len(values) != 0) {
			t.Fatalf("step %d values=%+v err=%v", index, values, err)
		}
	}
}
