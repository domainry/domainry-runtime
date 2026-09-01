package record_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/lifecyclesdkfixture"
)

func TestRecordSubjectLifecycleExportsRecordsAndFilesThenErasesDeclaredFields(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "record-subject.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	object := definitionmodel.ObjectSchema{Key: "employee_document", Fields: []definitionmodel.FieldSchema{
		{Key: "employee", Type: "user"},
		{Key: "owner", Type: "user"},
		{Key: "email", Type: "email", Config: map[string]any{"lifecycle_erase": "anonymize"}},
		{Key: "attachment", Type: "text", Config: map[string]any{"lifecycle_subject_file": true, "lifecycle_erase": "delete"}},
	}}
	manifest := manifestmodel.ManifestSchema{TemplateID: "record-subject", Version: "1", Name: "Record Subject", Objects: []definitionmodel.ObjectSchema{object}}
	if err := appschemapersistence.NewApplicationSchemaStore(store).SyncManifest(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test record subject schema"), manifest); err != nil {
		t.Fatal(err)
	}
	repository := recordpersistence.NewRecordStore(store)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repository.InsertRecord(t.Context(), "workspace-a", object, recordmodel.Record{ID: "document-1", CreatedAt: now, UpdatedAt: now, Data: map[string]any{"employee": "user-1", "owner": "user-1", "email": "person@example.com", "attachment": "/uploads/evidence.txt"}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.InsertRecord(t.Context(), "workspace-b", object, recordmodel.Record{ID: "document-1", CreatedAt: now, UpdatedAt: now, Data: map[string]any{"employee": "user-1", "owner": "user-1", "email": "other@example.com", "attachment": nil}}); err != nil {
		t.Fatal(err)
	}
	uploadRoot := t.TempDir()
	digest := sha256.Sum256([]byte("workspace-a"))
	workspaceDirectory := filepath.Join(uploadRoot, "workspace-"+hex.EncodeToString(digest[:16]))
	if err := os.MkdirAll(workspaceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(workspaceDirectory, "evidence.txt")
	if err := os.WriteFile(filePath, []byte("employee evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	lifecycleBinding, err := lifecyclesdkfixture.Open(t.Context(), store, "record-subject-test")
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := lifecycleBinding.SubjectArtifacts(uploadRoot)
	if err != nil {
		t.Fatal(err)
	}
	service := recordapplication.NewRecordSubjectLifecycleApplicationService(repository, []definitionmodel.ObjectSchema{object}, artifacts)

	raw, err := service.ExportSubject(t.Context(), "workspace-a", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Objects []struct {
			Records []recordmodel.Record `json:"records"`
		} `json:"objects"`
		Files []struct {
			Content []byte `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &exported); err != nil {
		t.Fatal(err)
	}
	if len(exported.Objects) != 1 || len(exported.Objects[0].Records) != 1 || len(exported.Files) != 1 || string(exported.Files[0].Content) != "employee evidence" {
		t.Fatalf("exported=%#v", exported)
	}

	evidence, err := service.EraseSubject(t.Context(), "workspace-a", "user-1", nil)
	if err != nil || !strings.Contains(string(evidence), "updated_records") {
		t.Fatalf("evidence=%s err=%v", evidence, err)
	}
	record, found, err := repository.GetRecord(t.Context(), "workspace-a", object, "document-1")
	if err != nil || !found {
		t.Fatalf("record found=%v err=%v", found, err)
	}
	if email := strings.TrimSpace(record.Data["email"].(string)); email == "person@example.com" || !strings.HasSuffix(email, "@invalid.local") || record.Data["attachment"] != nil {
		t.Fatalf("record after erase=%#v", record.Data)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("subject file remains: %v", err)
	}
	other, found, err := repository.GetRecord(t.Context(), "workspace-b", object, "document-1")
	if err != nil || !found || other.Data["email"] != "other@example.com" {
		t.Fatalf("cross-workspace record changed: %#v err=%v", other, err)
	}
}
