package integration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/secrets"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type failingIntegrationKeyProvider struct{ err error }

func (p failingIntegrationKeyProvider) Active(context.Context, string) (secrets.Key, error) {
	return secrets.Key{}, p.err
}
func (p failingIntegrationKeyProvider) ByID(context.Context, string, string) (secrets.Key, error) {
	return secrets.Key{}, p.err
}

type integrationLifecycleStoreSeam struct {
	lifecycleSQLStore
	db *sql.DB
}

func (s integrationLifecycleStoreSeam) DB() *sql.DB { return s.db }

func TestIntegrationSecretMaterialLegacyAndInputEdges(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)
	for _, test := range []struct{ workspace, key, value string }{{"", "key", "value"}, {"workspace-primary", "", "value"}, {"workspace-primary", "key", ""}} {
		if err := repository.PutSecretMaterial(t.Context(), test.workspace, test.key, test.value); err == nil {
			t.Fatalf("invalid material accepted: %#v", test)
		}
	}
	if err := repository.PutSecretMaterial(t.Context(), "workspace-primary", "existing", "first"); err != nil {
		t.Fatal(err)
	}
	if err := repository.PutSecretMaterial(t.Context(), "workspace-primary", "existing", "second"); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := repository.PutSecretMaterial(cancelled, "workspace-primary", "key", "value"); !errors.Is(err, context.Canceled) {
		t.Fatalf("put cancellation=%v", err)
	}
	if _, err := repository.ResolveSecretMaterial(t.Context(), "", "key"); err == nil {
		t.Fatal("invalid resolve workspace accepted")
	}
	aead, err := repository.secretAEAD()
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	raw := aead.Seal(nonce, nonce, []byte("legacy-secret"), []byte("workspace-primary\x00legacy"))
	encoded := "v1:" + base64.RawStdEncoding.EncodeToString(raw)
	plain, err := repository.decryptSecretMaterial(t.Context(), "workspace-primary", "legacy", encoded)
	if err != nil || plain != "legacy-secret" {
		t.Fatalf("plain=%q err=%v", plain, err)
	}
	for _, invalid := range []string{"v0:value", "v1:not-base64!", "v1:" + base64.RawStdEncoding.EncodeToString([]byte("short")), encoded[:len(encoded)-2] + "aa"} {
		if _, err := repository.decryptSecretMaterial(t.Context(), "workspace-primary", "legacy", invalid); err == nil {
			t.Fatalf("invalid ciphertext accepted: %q", invalid)
		}
	}
	if _, err := repository.decryptSecretMaterial(t.Context(), "workspace-primary", "other-key", encoded); err == nil {
		t.Fatal("wrong AAD accepted")
	}
	if _, err := repository.decryptSecretMaterial(t.Context(), "workspace-primary", "legacy", "v2:missing:key:payload"); err == nil {
		t.Fatal("invalid v2 envelope accepted")
	}
}

func TestIntegrationSecretMaterialPropagatesKeyProviderFailure(t *testing.T) {
	wantErr := errors.New("key provider unavailable")
	store, err := database.OpenContextWithKeyProvider(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "secrets.db")}, failingIntegrationKeyProvider{err: wantErr})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repository := NewIntegrationConfigStore(store)
	if err := repository.PutSecretMaterial(t.Context(), "workspace-primary", "token", "value"); !errors.Is(err, wantErr) {
		t.Fatalf("put key-provider error=%v", err)
	}
}

func TestIntegrationSubjectLifecyclePreviewAndCancellation(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	handler := NewIntegrationSubjectLifecycleStore(store)
	if handler.Owner(t.Context()) != "integration" {
		t.Fatal("owner mismatch")
	}
	preview, err := handler.PreviewSubject(t.Context(), "workspace-primary", "missing")
	if err != nil || !strings.Contains(string(preview), `"external_identity_mappings":0`) {
		t.Fatalf("preview=%s err=%v", preview, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := handler.PreviewSubject(cancelled, "workspace-primary", "user"); !errors.Is(err, context.Canceled) {
		t.Fatalf("preview cancel=%v", err)
	}
	if _, err := handler.ExportSubject(cancelled, "workspace-primary", "user"); !errors.Is(err, context.Canceled) {
		t.Fatalf("export cancel=%v", err)
	}
	if _, err := handler.EraseSubject(cancelled, "workspace-primary", "user", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("erase cancel=%v", err)
	}
	if _, err := handler.RequestExternalErasure(cancelled, lifecyclemodel.SubjectRequest{WorkspaceID: "workspace-primary", ResolvedIdentity: "user"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancel=%v", err)
	}
}

func TestIntegrationSubjectLifecycleSQLFailures(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	wantErr := errors.New("subject SQL failure")
	for index, test := range []struct {
		state *integrationSQLState
		call  func(*IntegrationSubjectLifecycleStore) error
	}{
		{state: &integrationSQLState{querySteps: []integrationSQLQueryStep{{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}}}}, call: func(s *IntegrationSubjectLifecycleStore) error {
			_, err := s.ExportSubject(t.Context(), "workspace-primary", "user")
			return err
		}},
		{state: &integrationSQLState{querySteps: []integrationSQLQueryStep{{columns: []string{"bad"}, nextErr: wantErr}}}, call: func(s *IntegrationSubjectLifecycleStore) error {
			_, err := s.ExportSubject(t.Context(), "workspace-primary", "user")
			return err
		}},
		{state: &integrationSQLState{execSteps: []integrationSQLExecStep{{rowsErr: wantErr}}}, call: func(s *IntegrationSubjectLifecycleStore) error {
			_, err := s.EraseSubject(t.Context(), "workspace-primary", "user", nil)
			return err
		}},
		{state: &integrationSQLState{querySteps: []integrationSQLQueryStep{{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}}}}, call: func(s *IntegrationSubjectLifecycleStore) error {
			_, err := s.RequestExternalErasure(t.Context(), lifecyclemodel.SubjectRequest{WorkspaceID: "workspace-primary", ResolvedIdentity: "user"})
			return err
		}},
	} {
		db := openIntegrationScriptedDB(test.state)
		handler := NewIntegrationSubjectLifecycleStore(integrationLifecycleStoreSeam{lifecycleSQLStore: store, db: db})
		if err := test.call(handler); err == nil {
			t.Fatalf("subject SQL failure %d ignored", index)
		}
		_ = db.Close()
	}
}
